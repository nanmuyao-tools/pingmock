package icmp

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"testing"

	"github.com/nanmuyao-tools/pingmock/internal/rule"
)

func TestParseEchoRequest(t *testing.T) {
	pkt := BuildEchoRequest(netip.MustParseAddr("10.99.0.254"), netip.MustParseAddr("10.99.0.1"), 7, 42, []byte("abc"))
	req, ok := ParseEchoRequest(pkt)
	if !ok {
		t.Fatal("valid echo request not recognized")
	}
	if req.Src.String() != "10.99.0.254" || req.Dst.String() != "10.99.0.1" || req.ID != 7 || req.Seq != 42 {
		t.Fatalf("unexpected fields: %+v", req)
	}
}

func TestParseEchoRequestRejects(t *testing.T) {
	good := BuildEchoRequest(netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.0.0.2"), 1, 1, nil)
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
		if _, ok := ParseEchoRequest(pkt); ok {
			t.Errorf("%s: accepted invalid packet", name)
		}
	}
}

func TestBuildEchoReply(t *testing.T) {
	payload := []byte("timestamp-and-padding")
	req := BuildEchoRequest(netip.MustParseAddr("10.99.0.254"), netip.MustParseAddr("10.99.0.1"), 7, 42, payload)
	reply := BuildEchoReply(req, 64)

	if got, want := netip.AddrFrom4([4]byte(reply[12:16])).String(), "10.99.0.1"; got != want {
		t.Errorf("reply src = %s, want %s", got, want)
	}
	if got, want := netip.AddrFrom4([4]byte(reply[16:20])).String(), "10.99.0.254"; got != want {
		t.Errorf("reply dst = %s, want %s", got, want)
	}
	if Checksum(reply[:20]) != 0 {
		t.Error("bad IPv4 header checksum")
	}
	icmp := reply[20:]
	if icmp[0] != 0 || icmp[1] != 0 {
		t.Errorf("type/code = %d/%d, want 0/0", icmp[0], icmp[1])
	}
	if Checksum(icmp) != 0 {
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
	sum := Checksum(b)
	// Appending the checksum must make the total verify to zero.
	full := append([]byte{0x45, 0x00, 0x01, 0x00}, byte(sum>>8), byte(sum))
	if Checksum(full) != 0 {
		t.Errorf("odd-length checksum does not verify")
	}
}

func TestBuildUnreachable(t *testing.T) {
	client, target := netip.MustParseAddr("10.99.0.254"), netip.MustParseAddr("10.99.0.3")
	req := BuildEchoRequest(client, target, 7, 42, []byte("payload"))
	for code, want := range map[rule.UnreachCode]byte{rule.UnreachHost: 1, rule.UnreachNet: 0, rule.UnreachAdmin: 13} {
		out := BuildUnreachable(req, code, 64)
		if len(out) != 20+8+len(req) || int(binary.BigEndian.Uint16(out[2:4])) != len(out) {
			t.Fatalf("%v: length %d, header says %d, want %d", code, len(out), binary.BigEndian.Uint16(out[2:4]), 20+8+len(req))
		}
		if Checksum(out[:20]) != 0 || Checksum(out[20:]) != 0 {
			t.Errorf("%v: bad checksum", code)
		}
		if out[9] != 1 || out[20] != 3 || out[21] != want {
			t.Errorf("%v: protocol %d type %d code %d, want 1/3/%d", code, out[9], out[20], out[21], want)
		}
		if src := netip.AddrFrom4([4]byte(out[12:16])); src != target {
			t.Errorf("%v: from %s, want the target %s", code, src, target)
		}
		if dst := netip.AddrFrom4([4]byte(out[16:20])); dst != client {
			t.Errorf("%v: to %s, want the client %s", code, dst, client)
		}
		if !bytes.Equal(out[28:], req) {
			t.Errorf("%v: the original request is not quoted in full", code)
		}
		if _, ok := ParseEchoRequest(out); ok {
			t.Errorf("%v: parsed as an echo request", code)
		}
	}
}

func TestBuildUnreachableTruncatesLargeRequests(t *testing.T) {
	req := BuildEchoRequest(netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.0.0.2"), 1, 1, make([]byte, 1400))
	out := BuildUnreachable(req, rule.UnreachHost, 64)
	if len(out) != 576 {
		t.Errorf("length %d, want 576", len(out))
	}
	if Checksum(out[20:]) != 0 {
		t.Error("bad ICMP checksum")
	}
	if !bytes.Equal(out[28:], req[:548]) {
		t.Error("quote is not the start of the request")
	}
}

var (
	client6 = netip.MustParseAddr("fd99::fe")
	target6 = netip.MustParseAddr("fd99::3")
)

// valid6 reports whether the ICMPv6 checksum of an IPv6 packet verifies.
func valid6(pkt []byte) bool {
	return checksum6(pkt[8:24], pkt[24:40], pkt[ipv6Header:]) == 0
}

func TestParseEchoRequest6(t *testing.T) {
	pkt := BuildEchoRequest(client6, target6, 9, 77, []byte("abc"))
	if !valid6(pkt) {
		t.Fatal("BuildEchoRequest produced a bad ICMPv6 checksum")
	}
	req, ok := ParseEchoRequest(pkt)
	if !ok || req.Src != client6 || req.Dst != target6 || req.ID != 9 || req.Seq != 77 {
		t.Fatalf("ParseEchoRequest = %+v, %v", req, ok)
	}
	cases := map[string]func([]byte) []byte{
		"too short":       func(b []byte) []byte { return b[:30] },
		"not ICMPv6":      func(b []byte) []byte { b[6] = 17; return b },
		"echo reply":      func(b []byte) []byte { b[ipv6Header] = icmp6EchoReply; return b },
		"payload too big": func(b []byte) []byte { binary.BigEndian.PutUint16(b[4:6], 500); return b },
		"extension":       func(b []byte) []byte { b[6] = 0; return b }, // hop-by-hop options
	}
	for name, mutate := range cases {
		if _, ok := ParseEchoRequest(mutate(append([]byte(nil), pkt...))); ok {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestBuildEchoReply6(t *testing.T) {
	req := BuildEchoRequest(client6, target6, 9, 77, []byte("timestamp"))
	reply := BuildEchoReply(req, 64)
	if len(reply) != len(req) || reply[0]>>4 != 6 || reply[7] != 64 {
		t.Fatalf("bad header: %x", reply[:8])
	}
	if src, dst := netip.AddrFrom16([16]byte(reply[8:24])), netip.AddrFrom16([16]byte(reply[24:40])); src != target6 || dst != client6 {
		t.Errorf("reply %s -> %s, want %s -> %s", src, dst, target6, client6)
	}
	msg := reply[ipv6Header:]
	if msg[0] != icmp6EchoReply || msg[1] != 0 || !valid6(reply) {
		t.Errorf("type %d code %d valid %v", msg[0], msg[1], valid6(reply))
	}
	if binary.BigEndian.Uint16(msg[4:6]) != 9 || binary.BigEndian.Uint16(msg[6:8]) != 77 || string(msg[8:]) != "timestamp" {
		t.Error("id, seq or payload not preserved")
	}
	if req[ipv6Header] != icmp6EchoRequest {
		t.Error("request was modified in place")
	}
}

func TestBuildUnreachable6(t *testing.T) {
	req := BuildEchoRequest(client6, target6, 9, 77, []byte("payload"))
	for code, want := range map[rule.UnreachCode]byte{rule.UnreachNet: 0, rule.UnreachAdmin: 1, rule.UnreachHost: 3} {
		out := BuildUnreachable(req, code, 64)
		if len(out) != ipv6Header+8+len(req) || int(binary.BigEndian.Uint16(out[4:6])) != 8+len(req) {
			t.Fatalf("%v: length %d", code, len(out))
		}
		if out[6] != protoICMPv6 || out[ipv6Header] != icmp6Unreachable || out[ipv6Header+1] != want || !valid6(out) {
			t.Errorf("%v: next header %d type %d code %d valid %v", code, out[6], out[ipv6Header], out[ipv6Header+1], valid6(out))
		}
		if src := netip.AddrFrom16([16]byte(out[8:24])); src != target6 {
			t.Errorf("%v: from %s, want %s", code, src, target6)
		}
		if !bytes.Equal(out[ipv6Header+8:], req) {
			t.Errorf("%v: request not quoted", code)
		}
	}
	big := BuildEchoRequest(client6, target6, 1, 1, make([]byte, 1400))
	if out := BuildUnreachable(big, rule.UnreachHost, 64); len(out) != maxError6 || !valid6(out) {
		t.Errorf("large request: length %d (want %d), valid %v", len(out), maxError6, valid6(out))
	}
}

// TestChecksum6KnownValue checks checksum6 against a value computed by an
// independent implementation of the RFC 8200 pseudo-header checksum.
func TestChecksum6KnownValue(t *testing.T) {
	pkt := BuildEchoRequest(netip.MustParseAddr("fd99::fe"), netip.MustParseAddr("fd99::1"), 0x1234, 1, []byte("pingmock"))
	if got := binary.BigEndian.Uint16(pkt[ipv6Header+2 : ipv6Header+4]); got != 0xc1a1 {
		t.Errorf("checksum %#04x, want 0xc1a1", got)
	}
}

func TestAnswersCarryTheTTL(t *testing.T) {
	req4 := BuildEchoRequest(netip.MustParseAddr("10.99.0.254"), netip.MustParseAddr("10.99.0.1"), 1, 1, []byte("x"))
	req6 := BuildEchoRequest(netip.MustParseAddr("fd99::fe"), netip.MustParseAddr("fd99::1"), 1, 1, []byte("x"))
	for _, ttl := range []uint8{1, 64, 128, 255} {
		if got := BuildEchoReply(req4, ttl)[8]; got != ttl {
			t.Errorf("IPv4 reply TTL = %d, want %d", got, ttl)
		}
		if got := BuildUnreachable(req4, rule.UnreachHost, ttl)[8]; got != ttl {
			t.Errorf("IPv4 unreachable TTL = %d, want %d", got, ttl)
		}
		if got := BuildEchoReply(req6, ttl)[7]; got != ttl {
			t.Errorf("IPv6 reply hop limit = %d, want %d", got, ttl)
		}
		if got := BuildUnreachable(req6, rule.UnreachHost, ttl)[7]; got != ttl {
			t.Errorf("IPv6 unreachable hop limit = %d, want %d", got, ttl)
		}
	}
	// The header checksum covers the TTL, so it must still be right.
	if reply := BuildEchoReply(req4, 128); Checksum(reply[:20]) != 0 {
		t.Error("IPv4 header checksum wrong after setting the TTL")
	}
}
