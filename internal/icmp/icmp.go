// Package icmp parses ICMP echo requests and builds the replies sent back
// through the TUN device.
package icmp

import (
	"encoding/binary"
	"net/netip"

	"github.com/nanmuyao/pingmock/internal/rule"
)

// EchoRequest describes an ICMPv4 echo request read from the TUN device.
type EchoRequest struct {
	Src, Dst netip.Addr
	ID, Seq  uint16
}

// ParseEchoRequest reports whether pkt is an IPv4 ICMP echo request
// and returns its addressing information.
func ParseEchoRequest(pkt []byte) (EchoRequest, bool) {
	if len(pkt) < 20 || pkt[0]>>4 != 4 {
		return EchoRequest{}, false
	}
	ihl := int(pkt[0]&0x0f) * 4
	total := int(binary.BigEndian.Uint16(pkt[2:4]))
	if ihl < 20 || total < ihl+8 || total > len(pkt) {
		return EchoRequest{}, false
	}
	// Ignore fragments: a fragmented echo request cannot be answered here.
	if binary.BigEndian.Uint16(pkt[6:8])&0x3fff != 0 {
		return EchoRequest{}, false
	}
	if pkt[9] != 1 { // protocol ICMP
		return EchoRequest{}, false
	}
	icmp := pkt[ihl:total]
	if icmp[0] != 8 || icmp[1] != 0 { // type 8 code 0: echo request
		return EchoRequest{}, false
	}
	return EchoRequest{
		Src: netip.AddrFrom4([4]byte(pkt[12:16])),
		Dst: netip.AddrFrom4([4]byte(pkt[16:20])),
		ID:  binary.BigEndian.Uint16(icmp[4:6]),
		Seq: binary.BigEndian.Uint16(icmp[6:8]),
	}, true
}

// BuildEchoReply turns a valid echo request into an echo reply. The payload
// is echoed unchanged, so tools that embed a send timestamp in it (like
// iputils ping) compute the RTT from their own clock.
func BuildEchoReply(req []byte) []byte {
	ihl := int(req[0]&0x0f) * 4
	total := int(binary.BigEndian.Uint16(req[2:4]))
	reply := make([]byte, total)
	copy(reply, req[:total])

	// IPv4 header: swap addresses, reset TTL, recompute checksum.
	copy(reply[12:16], req[16:20])
	copy(reply[16:20], req[12:16])
	reply[8] = 64
	reply[10], reply[11] = 0, 0
	binary.BigEndian.PutUint16(reply[10:12], Checksum(reply[:ihl]))

	// ICMP: type 0 (echo reply), recompute checksum.
	icmp := reply[ihl:]
	icmp[0] = 0
	icmp[2], icmp[3] = 0, 0
	binary.BigEndian.PutUint16(icmp[2:4], Checksum(icmp))
	return reply
}

// ICMPv4 destination unreachable codes (RFC 792, RFC 1812).
var unreachCodes4 = map[rule.UnreachCode]byte{
	rule.UnreachNet:   0,
	rule.UnreachHost:  1,
	rule.UnreachAdmin: 13, // communication administratively prohibited
}

// maxError4 is the largest ICMPv4 error message; RFC 1812 asks routers to
// quote as much of the original datagram as fits within 576 bytes.
const maxError4 = 576

// BuildUnreachable answers a valid echo request with an ICMP destination
// unreachable message. It appears to come from the target itself and
// quotes the start of the request, which is how ping matches the error to
// the request it sent.
func BuildUnreachable(req []byte, code rule.UnreachCode) []byte {
	total := int(binary.BigEndian.Uint16(req[2:4]))
	quote := req[:total]
	if len(quote) > maxError4-20-8 {
		quote = quote[:maxError4-20-8]
	}
	out := make([]byte, 20+8+len(quote))
	out[0] = 0x45
	binary.BigEndian.PutUint16(out[2:4], uint16(len(out)))
	out[8] = 64
	out[9] = 1                   // ICMP
	copy(out[12:16], req[16:20]) // from the target
	copy(out[16:20], req[12:16]) // to the client
	binary.BigEndian.PutUint16(out[10:12], Checksum(out[:20]))

	msg := out[20:]
	msg[0] = 3 // destination unreachable
	msg[1] = unreachCodes4[code]
	copy(msg[8:], quote)
	binary.BigEndian.PutUint16(msg[2:4], Checksum(msg))
	return out
}

// Checksum computes the Internet checksum (RFC 1071).
func Checksum(b []byte) uint16 {
	var sum uint32
	for len(b) >= 2 {
		sum += uint32(b[0])<<8 | uint32(b[1])
		b = b[2:]
	}
	if len(b) == 1 {
		sum += uint32(b[0]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

// BuildEchoRequest builds an IPv4 ICMP echo request with valid checksums,
// as a ping client would send it. It is used by tests.
func BuildEchoRequest(src, dst netip.Addr, id, seq uint16, payload []byte) []byte {
	pkt := make([]byte, 20+8+len(payload))
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = 1
	s, d := src.As4(), dst.As4()
	copy(pkt[12:16], s[:])
	copy(pkt[16:20], d[:])
	binary.BigEndian.PutUint16(pkt[10:12], Checksum(pkt[:20]))
	icmp := pkt[20:]
	icmp[0] = 8
	binary.BigEndian.PutUint16(icmp[4:6], id)
	binary.BigEndian.PutUint16(icmp[6:8], seq)
	copy(icmp[8:], payload)
	binary.BigEndian.PutUint16(icmp[2:4], Checksum(icmp))
	return pkt
}
