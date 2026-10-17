package icmp

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"testing"

	"github.com/nanmuyao/pingmock/internal/rule"
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
	reply := BuildEchoReply(req)

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
		out := BuildUnreachable(req, code)
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
	out := BuildUnreachable(req, rule.UnreachHost)
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
