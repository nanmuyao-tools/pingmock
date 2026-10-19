package icmp

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"testing"
)

// buildProbe builds a UDP datagram or a TCP SYN from src to dst with the
// given TTL, as traceroute sends them.
func buildProbe(kind Kind, src, dst netip.Addr, ttl uint8, sport, dport uint16, seq uint32) []byte {
	var seg []byte
	proto := byte(protoUDP)
	if kind == UDP {
		seg = make([]byte, 8+12)
		binary.BigEndian.PutUint16(seg[0:], sport)
		binary.BigEndian.PutUint16(seg[2:], dport)
		binary.BigEndian.PutUint16(seg[4:], uint16(len(seg)))
	} else {
		proto = protoTCP
		seg = make([]byte, 20)
		binary.BigEndian.PutUint16(seg[0:], sport)
		binary.BigEndian.PutUint16(seg[2:], dport)
		binary.BigEndian.PutUint32(seg[4:], seq)
		seg[12] = 5 << 4
		seg[13] = 0x02 // SYN
	}
	if src.Is6() {
		pkt := make([]byte, ipv6Header+len(seg))
		pkt[0] = 0x60
		binary.BigEndian.PutUint16(pkt[4:6], uint16(len(seg)))
		pkt[6] = proto
		pkt[7] = ttl
		s, d := src.As16(), dst.As16()
		copy(pkt[8:24], s[:])
		copy(pkt[24:40], d[:])
		copy(pkt[ipv6Header:], seg)
		return pkt
	}
	pkt := make([]byte, ipv4Header+len(seg))
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	pkt[8] = ttl
	pkt[9] = proto
	s, d := src.As4(), dst.As4()
	copy(pkt[12:16], s[:])
	copy(pkt[16:20], d[:])
	binary.BigEndian.PutUint16(pkt[10:12], Checksum(pkt[:ipv4Header]))
	copy(pkt[ipv4Header:], seg)
	return pkt
}

func TestParseProbe(t *testing.T) {
	c4, t4 := netip.MustParseAddr("10.99.0.254"), netip.MustParseAddr("10.99.0.1")
	echo := BuildEchoRequest(c4, t4, 7, 42, []byte("x"))
	echo[8] = 3 // traceroute -I's third probe
	binary.BigEndian.PutUint16(echo[10:12], 0)
	binary.BigEndian.PutUint16(echo[10:12], Checksum(echo[:20]))
	cases := []struct {
		name string
		pkt  []byte
		want Probe
	}{
		{"echo v4", echo, Probe{Kind: Echo, Src: c4, Dst: t4, TTL: 3, ID: 7, Seq: 42}},
		{"echo v6", BuildEchoRequest(client6, target6, 9, 77, nil), Probe{Kind: Echo, Src: client6, Dst: target6, TTL: 64, ID: 9, Seq: 77}},
		{"udp v4", buildProbe(UDP, c4, t4, 1, 40000, 33434, 0), Probe{Kind: UDP, Src: c4, Dst: t4, TTL: 1, SrcPort: 40000, DstPort: 33434}},
		{"udp v6", buildProbe(UDP, client6, target6, 2, 40001, 33435, 0), Probe{Kind: UDP, Src: client6, Dst: target6, TTL: 2, SrcPort: 40001, DstPort: 33435}},
		{"tcp v4", buildProbe(TCP, c4, t4, 5, 50000, 80, 123456), Probe{Kind: TCP, Src: c4, Dst: t4, TTL: 5, SrcPort: 50000, DstPort: 80, TCPSeq: 123456}},
		{"tcp v6", buildProbe(TCP, client6, target6, 6, 50001, 443, 7), Probe{Kind: TCP, Src: client6, Dst: target6, TTL: 6, SrcPort: 50001, DstPort: 443, TCPSeq: 7}},
	}
	for _, c := range cases {
		got, ok := ParseProbe(c.pkt)
		if !ok || got != c.want {
			t.Errorf("%s: ParseProbe = %+v, %v; want %+v", c.name, got, ok, c.want)
		}
	}
	// What is not a probe: a SYN-ACK, a plain ACK, an echo reply, another
	// protocol, a fragment.
	synack := buildProbe(TCP, c4, t4, 64, 1, 2, 0)
	synack[20+13] = 0x12
	ack := buildProbe(TCP, c4, t4, 64, 1, 2, 0)
	ack[20+13] = 0x10
	reply := BuildEchoReply(BuildEchoRequest(c4, t4, 1, 1, nil), 64)
	gre := buildProbe(UDP, c4, t4, 64, 1, 2, 0)
	gre[9] = 47
	frag := buildProbe(UDP, c4, t4, 64, 1, 2, 0)
	frag[6] = 0x20
	for name, pkt := range map[string][]byte{"syn-ack": synack, "ack": ack, "echo reply": reply, "gre": gre, "fragment": frag, "short": {0x45, 0}} {
		if p, ok := ParseProbe(pkt); ok {
			t.Errorf("%s: parsed as a probe: %+v", name, p)
		}
	}
}

func TestBuildTimeExceeded(t *testing.T) {
	c4, t4, hop := netip.MustParseAddr("10.99.0.254"), netip.MustParseAddr("10.99.0.1"), netip.MustParseAddr("192.168.1.1")
	for name, req := range map[string][]byte{
		"udp":  buildProbe(UDP, c4, t4, 1, 40000, 33434, 0),
		"echo": BuildEchoRequest(c4, t4, 1, 1, []byte("payload")),
	} {
		out := BuildTimeExceeded(req, hop, 61)
		if Checksum(out[:20]) != 0 || Checksum(out[20:]) != 0 {
			t.Errorf("%s: bad checksum", name)
		}
		if out[8] != 61 || out[9] != protoICMP || out[20] != 11 || out[21] != 0 {
			t.Errorf("%s: ttl %d proto %d type %d code %d, want 61/1/11/0", name, out[8], out[9], out[20], out[21])
		}
		if src := netip.AddrFrom4([4]byte(out[12:16])); src != hop {
			t.Errorf("%s: from %s, want the hop %s", name, src, hop)
		}
		if dst := netip.AddrFrom4([4]byte(out[16:20])); dst != c4 {
			t.Errorf("%s: to %s, want the sender %s", name, dst, c4)
		}
		if !bytes.Equal(out[28:], req) {
			t.Errorf("%s: the probe is not quoted in full", name)
		}
		if _, ok := ParseProbe(out); ok {
			t.Errorf("%s: the error parsed as a probe", name)
		}
	}
	hop6 := netip.MustParseAddr("fd99::1:1")
	req := buildProbe(UDP, client6, target6, 1, 40000, 33434, 0)
	out := BuildTimeExceeded(req, hop6, 64)
	if !valid6(out) || out[6] != protoICMPv6 || out[40] != 3 || out[41] != 0 {
		t.Errorf("v6: proto %d type %d code %d or bad checksum", out[6], out[40], out[41])
	}
	if src := netip.AddrFrom16([16]byte(out[8:24])); src != hop6 {
		t.Errorf("v6: from %s, want %s", src, hop6)
	}
	if !bytes.Equal(out[48:], req) {
		t.Error("v6: the probe is not quoted in full")
	}
	big := buildProbe(UDP, client6, target6, 1, 1, 2, 0)
	big = append(big, make([]byte, 1400)...)
	binary.BigEndian.PutUint16(big[4:6], uint16(len(big)-ipv6Header))
	if out := BuildTimeExceeded(big, hop6, 64); len(out) != maxError6 || !valid6(out) {
		t.Errorf("v6: large probe: length %d, want %d", len(out), maxError6)
	}
}

func TestBuildPortUnreachable(t *testing.T) {
	c4, t4 := netip.MustParseAddr("10.99.0.254"), netip.MustParseAddr("10.99.0.1")
	req := buildProbe(UDP, c4, t4, 64, 40000, 33434, 0)
	out := BuildPortUnreachable(req, 64)
	if Checksum(out[:20]) != 0 || Checksum(out[20:]) != 0 || out[20] != 3 || out[21] != 3 {
		t.Errorf("v4: type %d code %d or bad checksum", out[20], out[21])
	}
	if src := netip.AddrFrom4([4]byte(out[12:16])); src != t4 {
		t.Errorf("v4: from %s, want the destination %s", src, t4)
	}
	req = buildProbe(UDP, client6, target6, 64, 40000, 33434, 0)
	out = BuildPortUnreachable(req, 64)
	if !valid6(out) || out[40] != 1 || out[41] != 4 {
		t.Errorf("v6: type %d code %d or bad checksum", out[40], out[41])
	}
	if src := netip.AddrFrom16([16]byte(out[8:24])); src != target6 {
		t.Errorf("v6: from %s, want the destination %s", src, target6)
	}
}

func TestBuildSynAck(t *testing.T) {
	c4, t4 := netip.MustParseAddr("10.99.0.254"), netip.MustParseAddr("10.99.0.1")
	req := buildProbe(TCP, c4, t4, 64, 50000, 80, 1000)
	out := BuildSynAck(req, 128)
	if len(out) != 40 || Checksum(out[:20]) != 0 || out[8] != 128 || out[9] != protoTCP {
		t.Fatalf("v4: length %d ttl %d proto %d or bad IP checksum", len(out), out[8], out[9])
	}
	if tcpChecksum(out[12:16], out[16:20], out[20:]) != 0 {
		t.Error("v4: bad TCP checksum")
	}
	seg := out[20:]
	if binary.BigEndian.Uint16(seg[0:]) != 80 || binary.BigEndian.Uint16(seg[2:]) != 50000 || seg[13] != 0x12 || binary.BigEndian.Uint32(seg[8:]) != 1001 {
		t.Errorf("v4: ports %d->%d flags %#x ack %d", binary.BigEndian.Uint16(seg[0:]), binary.BigEndian.Uint16(seg[2:]), seg[13], binary.BigEndian.Uint32(seg[8:]))
	}
	if src := netip.AddrFrom4([4]byte(out[12:16])); src != t4 {
		t.Errorf("v4: from %s, want %s", src, t4)
	}
	req = buildProbe(TCP, client6, target6, 64, 50001, 443, 5)
	out = BuildSynAck(req, 64)
	if len(out) != 60 || out[6] != protoTCP || tcpChecksum(out[8:24], out[24:40], out[40:]) != 0 {
		t.Errorf("v6: length %d proto %d or bad TCP checksum", len(out), out[6])
	}
	if seg := out[40:]; binary.BigEndian.Uint32(seg[8:]) != 6 || seg[13] != 0x12 {
		t.Errorf("v6: ack %d flags %#x", binary.BigEndian.Uint32(seg[8:]), seg[13])
	}
	// The SYN-ACK must not itself look like a probe.
	if _, ok := ParseProbe(out); ok {
		t.Error("the SYN-ACK parsed as a probe")
	}
}
