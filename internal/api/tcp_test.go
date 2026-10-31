package api

import (
	"net/http"
	"net/netip"
	"testing"

	"github.com/nanmuyao-tools/ghostwire/internal/engine"
	"github.com/nanmuyao-tools/ghostwire/internal/icmp"
)

func TestTCPRulesAPI(t *testing.T) {
	f := newAPIFixture(t)
	var set struct{ Target, Rule string }
	if code := f.do("PUT", "/tcp/10.99.0.1:5432", "refuse for 2 requests; 20ms", &set); code != http.StatusOK {
		t.Fatalf("PUT text: %d", code)
	}
	if set.Target != "10.99.0.1/32:5432" || set.Rule != "delay=0s,refuse for 2 requests; delay=20ms" {
		t.Errorf("PUT answered %+v", set)
	}
	if code := f.do("PUT", "/tcp/[fd99::1]:443", `{"delay": "50ms", "pattern": ".r"}`, &set); code != http.StatusOK || set.Target != "[fd99::1/128]:443" {
		t.Fatalf("PUT JSON for IPv6: %d %+v", code, set)
	}
	if code := f.do("PUT", "/tcp/10.99.0.0/24", "timeout", nil); code != http.StatusOK {
		t.Fatalf("PUT a prefix for every port: %d", code)
	}
	want := []netip.Prefix{netip.MustParsePrefix("10.99.0.1/32"), netip.MustParsePrefix("fd99::1/128"), netip.MustParsePrefix("10.99.0.0/24")}
	if len(f.routed) != 3 || f.routed[0] != want[0] || f.routed[1] != want[1] || f.routed[2] != want[2] {
		t.Errorf("routed %v, want %v", f.routed, want)
	}

	for _, c := range []struct{ path, body string }{
		{"/tcp/fd99::1:443", "10ms"},              // IPv6 without brackets
		{"/tcp/10.99.0.1:0", "10ms"},              // no such port
		{"/tcp/db.example.com:5432", "10ms"},      // names are resolved at startup only
		{"/tcp/10.99.0.1:443", "pattern=.q"},      // not a TCP rule
		{"/tcp/10.99.0.1:443", `{"refuse": "x"}`}, // bad JSON
	} {
		if code := f.do("PUT", c.path, c.body, nil); code != http.StatusBadRequest {
			t.Errorf("PUT %s %q: %d, want 400", c.path, c.body, code)
		}
	}
	// Ping rules still refuse what only TCP rules have, as text and JSON.
	for _, body := range []string{"refuse", `{"refuse": true}`, "pattern=.r"} {
		if code := f.do("PUT", "/rules/10.99.0.1", body, nil); code != http.StatusBadRequest {
			t.Errorf("PUT /rules %q: %d, want 400", body, code)
		}
		if code := f.do("PUT", "/default", body, nil); code != http.StatusBadRequest {
			t.Errorf("PUT /default %q: %d, want 400", body, code)
		}
	}

	f.rs.DecideProbe(icmp.Probe{Kind: icmp.TCP, Src: netip.MustParseAddr("10.99.0.254"), Dst: netip.MustParseAddr("10.99.0.1"),
		TTL: 64, SrcPort: 40000, DstPort: 5432, TCPSeq: 1})
	var snap struct {
		TCP []struct {
			Target, Rule string
		} `json:"tcp"`
		TCPStats map[string]engine.TCPTargetState `json:"tcp_stats"`
		Stats    map[string]any                   `json:"stats"`
	}
	f.do("GET", "/rules", "", &snap)
	if len(snap.TCP) != 3 || snap.TCP[0].Target != "10.99.0.0/24" || snap.TCP[1].Target != "10.99.0.1/32:5432" {
		t.Errorf("tcp in snapshot = %+v", snap.TCP)
	}
	if st := snap.TCPStats["10.99.0.1:5432"]; st.Requests != 1 || st.Refused != 1 || st.Rule != "tcp 10.99.0.1/32:5432" || st.Phase != 1 {
		t.Errorf("tcp_stats = %+v", snap.TCPStats)
	}
	if len(snap.Stats) != 0 {
		t.Errorf("TCP attempts showed up in the ping stats: %v", snap.Stats)
	}

	if code := f.do("DELETE", "/tcp/10.99.0.1:5432", "", nil); code != http.StatusNoContent {
		t.Errorf("DELETE: %d", code)
	}
	if code := f.do("DELETE", "/tcp/10.99.0.1:5432", "", nil); code != http.StatusNotFound {
		t.Errorf("DELETE again: %d", code)
	}
	if code := f.do("DELETE", "/tcp/[fd99::1]:443", "", nil); code != http.StatusNoContent {
		t.Errorf("DELETE IPv6: %d", code)
	}
	// POST /reset forgets rules set through the API.
	f.do("POST", "/reset", "", nil)
	f.do("GET", "/rules", "", &snap)
	if len(snap.TCP) != 0 {
		t.Errorf("after reset: %+v", snap.TCP)
	}
}
