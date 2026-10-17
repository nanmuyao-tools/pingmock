package main

import (
	"encoding/binary"
	"net/netip"
)

// echoRequest describes an ICMPv4 echo request read from the TUN device.
type echoRequest struct {
	src, dst netip.Addr
	id, seq  uint16
}

// parseEchoRequest reports whether pkt is an IPv4 ICMP echo request
// and returns its addressing information.
func parseEchoRequest(pkt []byte) (echoRequest, bool) {
	if len(pkt) < 20 || pkt[0]>>4 != 4 {
		return echoRequest{}, false
	}
	ihl := int(pkt[0]&0x0f) * 4
	total := int(binary.BigEndian.Uint16(pkt[2:4]))
	if ihl < 20 || total < ihl+8 || total > len(pkt) {
		return echoRequest{}, false
	}
	// Ignore fragments: a fragmented echo request cannot be answered here.
	if binary.BigEndian.Uint16(pkt[6:8])&0x3fff != 0 {
		return echoRequest{}, false
	}
	if pkt[9] != 1 { // protocol ICMP
		return echoRequest{}, false
	}
	icmp := pkt[ihl:total]
	if icmp[0] != 8 || icmp[1] != 0 { // type 8 code 0: echo request
		return echoRequest{}, false
	}
	return echoRequest{
		src: netip.AddrFrom4([4]byte(pkt[12:16])),
		dst: netip.AddrFrom4([4]byte(pkt[16:20])),
		id:  binary.BigEndian.Uint16(icmp[4:6]),
		seq: binary.BigEndian.Uint16(icmp[6:8]),
	}, true
}

// buildEchoReply turns a valid echo request into an echo reply. The payload
// is echoed unchanged, so tools that embed a send timestamp in it (like
// iputils ping) compute the RTT from their own clock.
func buildEchoReply(req []byte) []byte {
	ihl := int(req[0]&0x0f) * 4
	total := int(binary.BigEndian.Uint16(req[2:4]))
	reply := make([]byte, total)
	copy(reply, req[:total])

	// IPv4 header: swap addresses, reset TTL, recompute checksum.
	copy(reply[12:16], req[16:20])
	copy(reply[16:20], req[12:16])
	reply[8] = 64
	reply[10], reply[11] = 0, 0
	binary.BigEndian.PutUint16(reply[10:12], checksum(reply[:ihl]))

	// ICMP: type 0 (echo reply), recompute checksum.
	icmp := reply[ihl:]
	icmp[0] = 0
	icmp[2], icmp[3] = 0, 0
	binary.BigEndian.PutUint16(icmp[2:4], checksum(icmp))
	return reply
}

// checksum computes the Internet checksum (RFC 1071).
func checksum(b []byte) uint16 {
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
