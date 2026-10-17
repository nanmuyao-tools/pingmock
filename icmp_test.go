package main

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

// makeEchoRequest builds an IPv4 ICMP echo request with valid checksums.
func makeEchoRequest(src, dst string, id, seq uint16, payload []byte) []byte {
	pkt := make([]byte, 20+8+len(payload))
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = 1
	s, d := netip.MustParseAddr(src).As4(), netip.MustParseAddr(dst).As4()
	copy(pkt[12:16], s[:])
	copy(pkt[16:20], d[:])
	binary.BigEndian.PutUint16(pkt[10:12], checksum(pkt[:20]))
	icmp := pkt[20:]
	icmp[0] = 8
	binary.BigEndian.PutUint16(icmp[4:6], id)
	binary.BigEndian.PutUint16(icmp[6:8], seq)
	copy(icmp[8:], payload)
	binary.BigEndian.PutUint16(icmp[2:4], checksum(icmp))
	return pkt
}

func TestParseEchoRequest(t *testing.T) {
	pkt := makeEchoRequest("10.99.0.254", "10.99.0.1", 7, 42, []byte("abc"))
	req, ok := parseEchoRequest(pkt)
	if !ok {
		t.Fatal("valid echo request not recognized")
	}
	if req.src.String() != "10.99.0.254" || req.dst.String() != "10.99.0.1" || req.id != 7 || req.seq != 42 {
		t.Fatalf("unexpected fields: %+v", req)
	}
}

func TestParseEchoRequestRejects(t *testing.T) {
	good := makeEchoRequest("10.0.0.1", "10.0.0.2", 1, 1, nil)
	cases := map[string]func([]byte) []byte{
		"too short":  func(b []byte) []byte { return b[:10] },
		"ipv6":       func(b []byte) []byte { b[0] = 0x65; return b },
		"udp":        func(b []byte) []byte { b[9] = 17; return b },
		"echo reply": func(b []byte) []byte { b[20] = 0; return b },
		"fragment":   func(b []byte) []byte { b[6] = 0x20; return b }, // MF flag
		"truncated":  func(b []byte) []byte { return b[:len(b)-1] },
	}
	for name, mutate := range cases {
		pkt := mutate(append([]byte(nil), good...))
		if _, ok := parseEchoRequest(pkt); ok {
			t.Errorf("%s: accepted invalid packet", name)
		}
	}
}

func TestBuildEchoReply(t *testing.T) {
	payload := []byte("timestamp-and-padding")
	req := makeEchoRequest("10.99.0.254", "10.99.0.1", 7, 42, payload)
	reply := buildEchoReply(req)

	if got, want := netip.AddrFrom4([4]byte(reply[12:16])).String(), "10.99.0.1"; got != want {
		t.Errorf("reply src = %s, want %s", got, want)
	}
	if got, want := netip.AddrFrom4([4]byte(reply[16:20])).String(), "10.99.0.254"; got != want {
		t.Errorf("reply dst = %s, want %s", got, want)
	}
	if checksum(reply[:20]) != 0 {
		t.Error("bad IPv4 header checksum")
	}
	icmp := reply[20:]
	if icmp[0] != 0 || icmp[1] != 0 {
		t.Errorf("type/code = %d/%d, want 0/0", icmp[0], icmp[1])
	}
	if checksum(icmp) != 0 {
		t.Error("bad ICMP checksum")
	}
	if binary.BigEndian.Uint16(icmp[4:6]) != 7 || binary.BigEndian.Uint16(icmp[6:8]) != 42 {
		t.Error("id/seq not preserved")
	}
	if string(icmp[8:]) != string(payload) {
		t.Error("payload not echoed")
	}
	if req[20] != 8 {
		t.Error("request was modified in place")
	}
}

func TestChecksumOddLength(t *testing.T) {
	b := []byte{0x45, 0x00, 0x01}
	sum := checksum(b)
	// Appending the checksum must make the total verify to zero.
	full := append([]byte{0x45, 0x00, 0x01, 0x00}, byte(sum>>8), byte(sum))
	if checksum(full) != 0 {
		t.Errorf("odd-length checksum does not verify")
	}
}
