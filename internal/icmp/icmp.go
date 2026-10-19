// Package icmp parses ICMP and ICMPv6 echo requests and builds the replies
// and destination unreachable messages sent back through the TUN device.
package icmp

import (
	"encoding/binary"
	"net/netip"

	"github.com/nanmuyao/pingmock/internal/rule"
)

// EchoRequest describes an echo request read from the TUN device.
type EchoRequest struct {
	Src, Dst netip.Addr
	ID, Seq  uint16
}

const (
	protoICMP   = 1
	protoICMPv6 = 58

	ipv4Header = 20 // without options, as pingmock sends them
	ipv6Header = 40

	icmpEchoReply    = 0
	icmpUnreachable  = 3
	icmpEchoRequest  = 8
	icmp6Unreachable = 1
	icmp6EchoRequest = 128
	icmp6EchoReply   = 129

	// RFC 1812 asks routers to quote as much of the original datagram as
	// fits in 576 bytes; RFC 4443 uses the IPv6 minimum MTU of 1280.
	maxError4 = 576
	maxError6 = 1280
)

// ParseEchoRequest reports whether pkt is an IPv4 ICMP or IPv6 ICMPv6 echo
// request and returns its addressing information.
func ParseEchoRequest(pkt []byte) (EchoRequest, bool) {
	if len(pkt) < 1 {
		return EchoRequest{}, false
	}
	switch pkt[0] >> 4 {
	case 4:
		return parse4(pkt)
	case 6:
		return parse6(pkt)
	}
	return EchoRequest{}, false
}

func parse4(pkt []byte) (EchoRequest, bool) {
	if len(pkt) < ipv4Header {
		return EchoRequest{}, false
	}
	ihl := int(pkt[0]&0x0f) * 4
	total := int(binary.BigEndian.Uint16(pkt[2:4]))
	if ihl < ipv4Header || total < ihl+8 || total > len(pkt) {
		return EchoRequest{}, false
	}
	// Ignore fragments: a fragmented echo request cannot be answered here.
	if binary.BigEndian.Uint16(pkt[6:8])&0x3fff != 0 {
		return EchoRequest{}, false
	}
	if pkt[9] != protoICMP {
		return EchoRequest{}, false
	}
	msg := pkt[ihl:total]
	if msg[0] != icmpEchoRequest || msg[1] != 0 {
		return EchoRequest{}, false
	}
	return EchoRequest{
		Src: netip.AddrFrom4([4]byte(pkt[12:16])),
		Dst: netip.AddrFrom4([4]byte(pkt[16:20])),
		ID:  binary.BigEndian.Uint16(msg[4:6]),
		Seq: binary.BigEndian.Uint16(msg[6:8]),
	}, true
}

func parse6(pkt []byte) (EchoRequest, bool) {
	if len(pkt) < ipv6Header+8 {
		return EchoRequest{}, false
	}
	plen := int(binary.BigEndian.Uint16(pkt[4:6]))
	// Extension headers (and so fragments) are not supported: ping does
	// not use them for ordinary echo requests.
	if pkt[6] != protoICMPv6 || plen < 8 || ipv6Header+plen > len(pkt) {
		return EchoRequest{}, false
	}
	msg := pkt[ipv6Header : ipv6Header+plen]
	if msg[0] != icmp6EchoRequest || msg[1] != 0 {
		return EchoRequest{}, false
	}
	return EchoRequest{
		Src: netip.AddrFrom16([16]byte(pkt[8:24])),
		Dst: netip.AddrFrom16([16]byte(pkt[24:40])),
		ID:  binary.BigEndian.Uint16(msg[4:6]),
		Seq: binary.BigEndian.Uint16(msg[6:8]),
	}, true
}

// BuildEchoReply turns a valid echo request into an echo reply sent with
// the given IP TTL (IPv6 hop limit). The payload is echoed unchanged, so
// tools that embed a send timestamp in it (like iputils ping) compute the
// RTT from their own clock.
func BuildEchoReply(req []byte, ttl uint8) []byte {
	if req[0]>>4 == 6 {
		return reply6(req, ttl)
	}
	return reply4(req, ttl)
}

func reply4(req []byte, ttl uint8) []byte {
	ihl := int(req[0]&0x0f) * 4
	total := int(binary.BigEndian.Uint16(req[2:4]))
	reply := make([]byte, total)
	copy(reply, req[:total])

	// IPv4 header: swap addresses, set the TTL, recompute checksum.
	copy(reply[12:16], req[16:20])
	copy(reply[16:20], req[12:16])
	reply[8] = ttl
	reply[10], reply[11] = 0, 0
	binary.BigEndian.PutUint16(reply[10:12], Checksum(reply[:ihl]))

	msg := reply[ihl:]
	msg[0] = icmpEchoReply
	msg[2], msg[3] = 0, 0
	binary.BigEndian.PutUint16(msg[2:4], Checksum(msg))
	return reply
}

func reply6(req []byte, ttl uint8) []byte {
	total := ipv6Header + int(binary.BigEndian.Uint16(req[4:6]))
	reply := make([]byte, total)
	copy(reply, req[:total])
	copy(reply[8:24], req[24:40])
	copy(reply[24:40], req[8:24])
	reply[7] = ttl // hop limit

	msg := reply[ipv6Header:]
	msg[0] = icmp6EchoReply
	msg[2], msg[3] = 0, 0
	binary.BigEndian.PutUint16(msg[2:4], checksum6(reply[8:24], reply[24:40], msg))
	return reply
}

// ICMPv4 (RFC 792, RFC 1812) and ICMPv6 (RFC 4443) unreachable codes.
var (
	unreachCodes4 = map[rule.UnreachCode]byte{
		rule.UnreachNet:   0,
		rule.UnreachHost:  1,
		rule.UnreachAdmin: 13, // communication administratively prohibited
	}
	unreachCodes6 = map[rule.UnreachCode]byte{
		rule.UnreachNet:   0, // no route to destination
		rule.UnreachAdmin: 1, // communication administratively prohibited
		rule.UnreachHost:  3, // address unreachable
	}
)

// BuildUnreachable answers a valid echo request with a destination
// unreachable message sent with the given IP TTL (IPv6 hop limit). It
// appears to come from the target itself and quotes the start of the
// request, which is how ping matches the error to the request it sent.
func BuildUnreachable(req []byte, code rule.UnreachCode, ttl uint8) []byte {
	if req[0]>>4 == 6 {
		return unreachable6(req, code, ttl)
	}
	return unreachable4(req, code, ttl)
}

func unreachable4(req []byte, code rule.UnreachCode, ttl uint8) []byte {
	return icmpError4(req, netip.AddrFrom4([4]byte(req[16:20])), icmpUnreachable, unreachCodes4[code], ttl)
}

func unreachable6(req []byte, code rule.UnreachCode, ttl uint8) []byte {
	return icmpError6(req, netip.AddrFrom16([16]byte(req[24:40])), icmp6Unreachable, unreachCodes6[code], ttl)
}

// Checksum computes the Internet checksum (RFC 1071).
func Checksum(b []byte) uint16 {
	return ^fold(sum(0, b))
}

// checksum6 computes the ICMPv6 checksum, which covers a pseudo-header made
// of the addresses, the message length and the next header (RFC 8200 8.1).
func checksum6(src, dst, msg []byte) uint16 {
	var pseudo [8]byte
	binary.BigEndian.PutUint32(pseudo[0:4], uint32(len(msg)))
	pseudo[7] = protoICMPv6
	s := sum(0, src)
	s = sum(s, dst)
	s = sum(s, pseudo[:])
	return ^fold(sum(s, msg))
}

// sum adds b to a running one's-complement sum. Every chunk except the last
// must have an even length.
func sum(acc uint32, b []byte) uint32 {
	for len(b) >= 2 {
		acc += uint32(b[0])<<8 | uint32(b[1])
		b = b[2:]
	}
	if len(b) == 1 {
		acc += uint32(b[0]) << 8
	}
	return acc
}

func fold(s uint32) uint16 {
	for s>>16 != 0 {
		s = s&0xffff + s>>16
	}
	return uint16(s)
}

// BuildEchoRequest builds an echo request with valid checksums, as a ping
// client would send it: ICMP for IPv4 addresses, ICMPv6 for IPv6 ones.
// It is used by tests.
func BuildEchoRequest(src, dst netip.Addr, id, seq uint16, payload []byte) []byte {
	if src.Is6() {
		pkt := make([]byte, ipv6Header+8+len(payload))
		pkt[0] = 0x60
		binary.BigEndian.PutUint16(pkt[4:6], uint16(8+len(payload)))
		pkt[6] = protoICMPv6
		pkt[7] = 64
		s, d := src.As16(), dst.As16()
		copy(pkt[8:24], s[:])
		copy(pkt[24:40], d[:])
		msg := pkt[ipv6Header:]
		msg[0] = icmp6EchoRequest
		binary.BigEndian.PutUint16(msg[4:6], id)
		binary.BigEndian.PutUint16(msg[6:8], seq)
		copy(msg[8:], payload)
		binary.BigEndian.PutUint16(msg[2:4], checksum6(pkt[8:24], pkt[24:40], msg))
		return pkt
	}
	pkt := make([]byte, ipv4Header+8+len(payload))
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = protoICMP
	s, d := src.As4(), dst.As4()
	copy(pkt[12:16], s[:])
	copy(pkt[16:20], d[:])
	binary.BigEndian.PutUint16(pkt[10:12], Checksum(pkt[:ipv4Header]))
	msg := pkt[ipv4Header:]
	msg[0] = icmpEchoRequest
	binary.BigEndian.PutUint16(msg[4:6], id)
	binary.BigEndian.PutUint16(msg[6:8], seq)
	copy(msg[8:], payload)
	binary.BigEndian.PutUint16(msg[2:4], Checksum(msg))
	return pkt
}
