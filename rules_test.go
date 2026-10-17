package main

import (
	"encoding/json"
	"net/netip"
	"testing"
	"time"
)

func TestParseSpec(t *testing.T) {
	cases := []struct {
		in   string
		want Spec
	}{
		{"120ms", Spec{Delay: 120 * time.Millisecond}},
		{"120", Spec{Delay: 120 * time.Millisecond}},
		{"1.5s", Spec{Delay: 1500 * time.Millisecond}},
		{"delay=200ms,jitter=30ms", Spec{Delay: 200 * time.Millisecond, Jitter: 30 * time.Millisecond}},
		{"delay=10, loss=5%", Spec{Delay: 10 * time.Millisecond, Loss: 0.05}},
		{"loss=0.25", Spec{Loss: 0.25}},
		{"timeout", Spec{Loss: 1}},
	}
	for _, c := range cases {
		got, err := ParseSpec(c.in)
		if err != nil {
			t.Errorf("ParseSpec(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseSpec(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", "abc", "delay=-5ms", "loss=150%", "speed=3"} {
		if _, err := ParseSpec(bad); err == nil {
			t.Errorf("ParseSpec(%q): expected error", bad)
		}
	}
}

func TestSample(t *testing.T) {
	sp := Spec{Delay: 100 * time.Millisecond, Jitter: 20 * time.Millisecond}
	for i := 0; i < 1000; i++ {
		d, ok := sp.sample()
		if !ok || d < 80*time.Millisecond || d > 120*time.Millisecond {
			t.Fatalf("sample = %v, %v; want within 80-120ms", d, ok)
		}
	}
	if _, ok := (Spec{Loss: 1}).sample(); ok {
		t.Error("loss=1 should always drop")
	}
	// Jitter larger than delay must never produce a negative delay.
	for i := 0; i < 1000; i++ {
		if d, _ := (Spec{Delay: time.Millisecond, Jitter: 50 * time.Millisecond}).sample(); d < 0 {
			t.Fatal("negative delay")
		}
	}
}

func TestLossRate(t *testing.T) {
	sp := Spec{Loss: 0.3}
	dropped := 0
	const n = 20000
	for i := 0; i < n; i++ {
		if _, ok := sp.sample(); !ok {
			dropped++
		}
	}
	if rate := float64(dropped) / n; rate < 0.27 || rate > 0.33 {
		t.Errorf("drop rate %.3f, want about 0.30", rate)
	}
}

func TestLookupMostSpecificWins(t *testing.T) {
	rs := NewRuleSet(Spec{Delay: time.Millisecond})
	rs.Set(netip.MustParsePrefix("10.99.0.0/24"), Spec{Delay: 10 * time.Millisecond})
	rs.Set(netip.MustParsePrefix("10.99.0.128/25"), Spec{Delay: 20 * time.Millisecond})
	rs.Set(netip.MustParsePrefix("10.99.0.200/32"), Spec{Delay: 30 * time.Millisecond})
	cases := map[string]time.Duration{
		"10.99.0.5":   10 * time.Millisecond,
		"10.99.0.150": 20 * time.Millisecond,
		"10.99.0.200": 30 * time.Millisecond,
		"192.0.2.1":   time.Millisecond,
	}
	for ip, want := range cases {
		if got := rs.Lookup(netip.MustParseAddr(ip)).Delay; got != want {
			t.Errorf("Lookup(%s) delay = %v, want %v", ip, got, want)
		}
	}
	rs.Delete(netip.MustParsePrefix("10.99.0.200/32"))
	if got := rs.Lookup(netip.MustParseAddr("10.99.0.200")).Delay; got != 20*time.Millisecond {
		t.Errorf("after delete, delay = %v, want 20ms", got)
	}
}

func TestParsePrefix(t *testing.T) {
	if p, err := ParsePrefix("8.8.8.8"); err != nil || p.String() != "8.8.8.8/32" {
		t.Errorf("single address: %v %v", p, err)
	}
	if p, err := ParsePrefix("10.99.0.130/25"); err != nil || p.String() != "10.99.0.128/25" {
		t.Errorf("CIDR should be masked: %v %v", p, err)
	}
	for _, bad := range []string{"::1", "2001:db8::/32", "nope"} {
		if _, err := ParsePrefix(bad); err == nil {
			t.Errorf("ParsePrefix(%q): expected error", bad)
		}
	}
}

func TestSpecJSONRoundTrip(t *testing.T) {
	in := Spec{Delay: 120 * time.Millisecond, Jitter: 5 * time.Millisecond, Loss: 0.1}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Spec
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Errorf("round trip: got %+v, want %+v (json %s)", out, in, b)
	}
	if err := json.Unmarshal([]byte(`{"loss":2}`), &out); err == nil {
		t.Error("expected error for loss > 1")
	}
}
