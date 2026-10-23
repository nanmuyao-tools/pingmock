package rule

import (
	"encoding/json"
	"math/rand/v2"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestParsePath(t *testing.T) {
	p, err := ParsePath("192.168.1.1@2ms > 10.0.0.1@delay=15ms,jitter=3ms,loss=10%,cut=5%,ttl=250 > * > fd99::1:1 > 72.14.0.1@25")
	if err != nil {
		t.Fatal(err)
	}
	want := Path{
		{Addr: netip.MustParseAddr("192.168.1.1"), Delay: 2 * time.Millisecond},
		{Addr: netip.MustParseAddr("10.0.0.1"), Delay: 15 * time.Millisecond, Jitter: 3 * time.Millisecond, Loss: 0.1, Cut: 0.05, TTL: 250},
		{},
		{Addr: netip.MustParseAddr("fd99::1:1")},
		{Addr: netip.MustParseAddr("72.14.0.1"), Delay: 25 * time.Millisecond},
	}
	if len(p) != len(want) {
		t.Fatalf("%d hops, want %d: %v", len(p), len(want), p)
	}
	for i := range want {
		if p[i] != want[i] {
			t.Errorf("hop %d = %+v, want %+v", i+1, p[i], want[i])
		}
	}
	if s := p.String(); s != "192.168.1.1@delay=2ms > 10.0.0.1@delay=15ms,jitter=3ms,loss=10%,cut=5%,ttl=250 > * > fd99::1:1@delay=0s > 72.14.0.1@delay=25ms" {
		t.Errorf("String() = %s", s)
	}
	again, err := ParsePath(p.String())
	if err != nil || again.String() != p.String() {
		t.Errorf("round trip: %v, %v", again, err)
	}
	if addrs := p.Addrs(); len(addrs) != 4 || addrs[2] != want[3].Addr {
		t.Errorf("Addrs() = %v", addrs)
	}
	for _, bad := range []string{"", " > ", "10.0.0.1@", "10.0.0.1@pattern=.x", "10.0.0.1@unreachable", "10.0.0.1@loss=200%", "10.0.0.1@cut=-1", "nope@2ms", "10.0.0.1@ttl=0", "fe80::1%eth0@1ms", "10.0.0.1/24@1ms", "10.0.0.1 @ 2ms > >"} {
		if _, err := ParsePath(bad); err == nil {
			t.Errorf("ParsePath(%q): expected error", bad)
		}
	}
}

func TestHopDecisions(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	if ok, _ := (Hop{}).DecideHop(rng); ok {
		t.Error("a silent hop answered")
	}
	h := Hop{Addr: netip.MustParseAddr("10.0.0.1"), Delay: 10 * time.Millisecond, Jitter: 2 * time.Millisecond}
	for range 50 {
		ok, d := h.DecideHop(rng)
		if !ok || d < 8*time.Millisecond || d > 12*time.Millisecond {
			t.Errorf("answered %v after %v, want always, 8..12ms", ok, d)
		}
	}
	lossy := Hop{Addr: h.Addr, Loss: 0.5}
	answered := 0
	for range 1000 {
		if ok, _ := lossy.DecideHop(rng); ok {
			answered++
		}
	}
	if answered < 400 || answered > 600 {
		t.Errorf("loss=50%%: %d of 1000 answered", answered)
	}
	cutter := Hop{Addr: h.Addr, Cut: 0.25}
	cuts := 0
	for range 1000 {
		if cutter.Cuts(rng) {
			cuts++
		}
	}
	if cuts < 180 || cuts > 320 {
		t.Errorf("cut=25%%: %d of 1000 cut", cuts)
	}
	if h.Cuts(rng) {
		t.Error("a hop without cut cut a packet")
	}
	if h.EffectiveTTL() != DefaultTTL || (Hop{TTL: 7}).EffectiveTTL() != 7 {
		t.Error("EffectiveTTL")
	}
}

func TestPathJSON(t *testing.T) {
	p, _ := ParsePath("10.0.0.1@2ms > *")
	b, err := json.Marshal(p)
	var text string
	if err != nil || json.Unmarshal(b, &text) != nil || text != "10.0.0.1@delay=2ms > *" {
		t.Errorf("marshal = %s, %v", b, err)
	}
	var out Path
	if err := json.Unmarshal(b, &out); err != nil || out.String() != p.String() {
		t.Errorf("unmarshal = %v, %v", out, err)
	}
	if err := json.Unmarshal([]byte(`{"hops":1}`), &out); err == nil {
		t.Error("an object was accepted as a path")
	}
	if err := json.Unmarshal([]byte(`"10.0.0.1@nope"`), &out); err == nil {
		t.Error("a bad path was accepted")
	}
}

// The JSON of a path keeps ">" as it is, so that GET /rules reads as the
// text form does.
func TestPathJSONKeepsArrows(t *testing.T) {
	p, err := ParsePath("192.168.1.1@2ms > * > 10.0.0.1@20ms")
	if err != nil {
		t.Fatal(err)
	}
	// As the API writes it, with an encoder that does not escape HTML;
	// json.Marshal would escape what MarshalJSON returns again.
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]Path{"path": p}); err != nil {
		t.Fatal(err)
	}
	escaped := "\\" + "u003e"
	if got := buf.String(); strings.Contains(got, escaped) || !strings.Contains(got, " > ") {
		t.Errorf("JSON %s", got)
	}
	var back map[string]Path
	if err := json.Unmarshal([]byte(buf.String()), &back); err != nil || back["path"].String() != p.String() {
		t.Errorf("round trip: %v %v", back, err)
	}
}
