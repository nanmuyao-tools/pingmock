package icmp

import (
	"encoding/binary"
	"net/netip"
)

// Kind is what a probe is: the packets traceroute, mtr and ping send.
type Kind int

const (
	Echo Kind = iota // ICMP or ICMPv6 echo request: ping, traceroute -I, mtr
	UDP              // a UDP datagram: traceroute's default probes
	TCP              // a TCP SYN: traceroute -T, mtr --tcp
)

func (k Kind) String() string {
	switch k {
	case Echo:
		return "echo"
	case UDP:
		return "udp"
	case TCP:
		return "tcp"
	}
	return "?"
}

// Probe describes a packet read from the TUN device that pingmock may
// answer: an echo request, or, for targets with a path, a UDP datagram or
// a TCP SYN. TTL is the IPv4 TTL or IPv6 hop limit as the sender set it,
// which is how traceroute asks each router in turn to answer.
type Probe struct {
	Kind     Kind
	Src, Dst netip.Addr
	TTL      uint8
	ID, Seq  uint16 // echo requests
	SrcPort  uint16 // UDP and TCP
	DstPort  uint16
	TCPSeq   uint32 // TCP: the SYN's sequence number, to acknowledge
}

const (
	protoTCP = 6
	protoUDP = 17

	icmpTimeExceeded  = 11
	icmp6TimeExceeded = 3
)

// ParseProbe reports whether pkt is a packet pingmock may answer, and
// describes it.
func ParseProbe(pkt []byte) (Probe, bool) {
	if len(pkt) < 1 {
		return Probe{}, false
	}
	switch pkt[0] >> 4 {
	case 4:
		return probe4(pkt)
	case 6:
		return probe6(pkt)
	}
	return Probe{}, false
}

func probe4(pkt []byte) (Probe, bool) {
	if len(pkt) < ipv4Header {
		return Probe{}, false
	}
	ihl := int(pkt[0]&0x0f) * 4
	total := int(binary.BigEndian.Uint16(pkt[2:4]))
	if ihl < ipv4Header || total < ihl+8 || total > len(pkt) {
		return Probe{}, false
	}
	if binary.BigEndian.Uint16(pkt[6:8])&0x3fff != 0 { // a fragment
		return Probe{}, false
	}
	p := Probe{
		Src: netip.AddrFrom4([4]byte(pkt[12:16])),
		Dst: netip.AddrFrom4([4]byte(pkt[16:20])),
		TTL: pkt[8],
	}
	return transport(p, pkt[9], pkt[ihl:total], icmpEchoRequest)
}

func probe6(pkt []byte) (Probe, bool) {
	if len(pkt) < ipv6Header+8 {
		return Probe{}, false
	}
	plen := int(binary.BigEndian.Uint16(pkt[4:6]))
	if plen < 8 || ipv6Header+plen > len(pkt) {
		return Probe{}, false
	}
	p := Probe{
		Src: netip.AddrFrom16([16]byte(pkt[8:24])),
		Dst: netip.AddrFrom16([16]byte(pkt[24:40])),
		TTL: pkt[7],
	}
	return transport(p, pkt[6], pkt[ipv6Header:ipv6Header+plen], icmp6EchoRequest)
}

// transport fills in the part of p that depends on the protocol, given
// the protocol number, the payload after the IP header and the type an
// echo request has in this IP version.
func transport(p Probe, proto byte, msg []byte, echoType byte) (Probe, bool) {
	switch {
	case (proto == protoICMP && echoType == icmpEchoRequest) || (proto == protoICMPv6 && echoType == icmp6EchoRequest):
		if msg[0] != echoType || msg[1] != 0 {
			return Probe{}, false
		}
		p.Kind = Echo
		p.ID = binary.BigEndian.Uint16(msg[4:6])
		p.Seq = binary.BigEndian.Uint16(msg[6:8])
	case proto == protoUDP:
		p.Kind = UDP
		p.SrcPort = binary.BigEndian.Uint16(msg[0:2])
		p.DstPort = binary.BigEndian.Uint16(msg[2:4])
	case proto == protoTCP:
		if len(msg) < 20 || msg[13]&0x12 != 0x02 { // not a SYN, or a SYN-ACK
			return Probe{}, false
		}
		p.Kind = TCP
		p.SrcPort = binary.BigEndian.Uint16(msg[0:2])
		p.DstPort = binary.BigEndian.Uint16(msg[2:4])
		p.TCPSeq = binary.BigEndian.Uint32(msg[4:8])
	default:
		return Probe{}, false
	}
	return p, true
}

// BuildTimeExceeded answers a probe as the router at from would when the
// probe's TTL ran out there: ICMP time exceeded in transit (RFC 792 type
// 11, RFC 4443 type 3), quoting the start of the probe.
func BuildTimeExceeded(req []byte, from netip.Addr, ttl uint8) []byte {
	if req[0]>>4 == 6 {
		return icmpError6(req, from, icmp6TimeExceeded, 0, ttl)
	}
	return icmpError4(req, from, icmpTimeExceeded, 0, ttl)
}

// BuildPortUnreachable answers a UDP probe as its destination would when
// nothing listens on the port: ICMP destination unreachable, port
// unreachable (RFC 792 code 3, RFC 4443 code 4). traceroute takes it as
// having reached the destination.
func BuildPortUnreachable(req []byte, ttl uint8) []byte {
	if req[0]>>4 == 6 {
		return icmpError6(req, netip.AddrFrom16([16]byte(req[24:40])), icmp6Unreachable, 4, ttl)
	}
	return icmpError4(req, netip.AddrFrom4([4]byte(req[16:20])), icmpUnreachable, 3, ttl)
}

// icmpError4 builds an ICMP error message of the given type and code from
// the address from to the sender of req, quoting as much of req as fits.
func icmpError4(req []byte, from netip.Addr, typ, code, ttl byte) []byte {
	total := int(binary.BigEndian.Uint16(req[2:4]))
	quote := req[:min(total, maxError4-ipv4Header-8)]
	out := make([]byte, ipv4Header+8+len(quote))
	out[0] = 0x45
	binary.BigEndian.PutUint16(out[2:4], uint16(len(out)))
	out[8] = ttl
	out[9] = protoICMP
	src := from.As4()
	copy(out[12:16], src[:])
	copy(out[16:20], req[12:16]) // to the sender
	binary.BigEndian.PutUint16(out[10:12], Checksum(out[:ipv4Header]))

	msg := out[ipv4Header:]
	msg[0] = typ
	msg[1] = code
	copy(msg[8:], quote)
	binary.BigEndian.PutUint16(msg[2:4], Checksum(msg))
	return out
}

func icmpError6(req []byte, from netip.Addr, typ, code, ttl byte) []byte {
	total := ipv6Header + int(binary.BigEndian.Uint16(req[4:6]))
	quote := req[:min(total, maxError6-ipv6Header-8)]
	out := make([]byte, ipv6Header+8+len(quote))
	out[0] = 0x60
	binary.BigEndian.PutUint16(out[4:6], uint16(8+len(quote)))
	out[6] = protoICMPv6
	out[7] = ttl
	src := from.As16()
	copy(out[8:24], src[:])
	copy(out[24:40], req[8:24]) // to the sender

	msg := out[ipv6Header:]
	msg[0] = typ
	msg[1] = code
	copy(msg[8:], quote)
	binary.BigEndian.PutUint16(msg[2:4], checksum6(out[8:24], out[24:40], msg))
	return out
}

// BuildSynAck answers a TCP SYN as a host with the port open would: a
// SYN-ACK from the destination, acknowledging the SYN. traceroute -T and
// mtr --tcp take it as having reached the destination; the sender's
// kernel, which never opened the connection, answers it with a reset,
// which closes the matter.
func BuildSynAck(req []byte, ttl uint8) []byte {
	return buildTCPAnswer(req, ttl, 0x12, 0x70696e67, 65535) // SYN, ACK; our sequence number: "ping"
}

// BuildRST answers a TCP SYN as a host with the port closed would: a
// reset acknowledging the SYN (RFC 9293 3.10.7.1), so that the sender's
// connect fails at once with "connection refused".
func BuildRST(req []byte, ttl uint8) []byte {
	return buildTCPAnswer(req, ttl, 0x14, 0, 0) // RST, ACK
}

// buildTCPAnswer builds a TCP segment without data from the destination
// of the SYN req back to its sender, with the given flags, sequence
// number and window, acknowledging the SYN.
func buildTCPAnswer(req []byte, ttl uint8, flags byte, seq uint32, window uint16) []byte {
	var out, seg []byte
	var src, dst []byte
	if req[0]>>4 == 6 {
		out = make([]byte, ipv6Header+20)
		out[0] = 0x60
		binary.BigEndian.PutUint16(out[4:6], 20)
		out[6] = protoTCP
		out[7] = ttl
		copy(out[8:24], req[24:40])
		copy(out[24:40], req[8:24])
		src, dst, seg = out[8:24], out[24:40], out[ipv6Header:]
	} else {
		out = make([]byte, ipv4Header+20)
		out[0] = 0x45
		binary.BigEndian.PutUint16(out[2:4], uint16(len(out)))
		out[8] = ttl
		out[9] = protoTCP
		copy(out[12:16], req[16:20])
		copy(out[16:20], req[12:16])
		binary.BigEndian.PutUint16(out[10:12], Checksum(out[:ipv4Header]))
		src, dst, seg = out[12:16], out[16:20], out[ipv4Header:]
	}
	ihl := ipv6Header
	if req[0]>>4 == 4 {
		ihl = int(req[0]&0x0f) * 4
	}
	syn := req[ihl:]
	copy(seg[0:2], syn[2:4])                         // from the probed port
	copy(seg[2:4], syn[0:2])                         // to the sender's port
	binary.BigEndian.PutUint32(seg[4:8], seq)
	binary.BigEndian.PutUint32(seg[8:12], binary.BigEndian.Uint32(syn[4:8])+1)
	seg[12] = 5 << 4 // data offset: 20 bytes
	seg[13] = flags
	binary.BigEndian.PutUint16(seg[14:16], window)
	binary.BigEndian.PutUint16(seg[16:18], tcpChecksum(src, dst, seg))
	return out
}

// tcpChecksum covers the pseudo-header and the segment, for both IP
// versions (RFC 793 3.1, RFC 8200 8.1).
func tcpChecksum(src, dst, seg []byte) uint16 {
	s := sum(0, src)
	s = sum(s, dst)
	var pseudo [4]byte
	if len(src) == 4 {
		pseudo[1] = protoTCP
		binary.BigEndian.PutUint16(pseudo[2:], uint16(len(seg)))
		s = sum(s, pseudo[:])
	} else {
		var p6 [8]byte
		binary.BigEndian.PutUint32(p6[0:4], uint32(len(seg)))
		p6[7] = protoTCP
		s = sum(s, p6[:])
	}
	return ^fold(sum(s, seg))
}
