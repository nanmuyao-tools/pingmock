package rule

import (
	"encoding/json"
	"math/rand/v2"
	"reflect"
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
		{"delay=50ms,pattern=..X", Spec{Delay: 50 * time.Millisecond, Pattern: "..x"}},
		{"unreachable", Spec{Unreachable: UnreachHost}},
		{"delay=20ms,unreachable=admin", Spec{Delay: 20 * time.Millisecond, Unreachable: UnreachAdmin}},
		{"unreachable=net,loss=10%", Spec{Unreachable: UnreachNet, Loss: 0.1}},
		{"pattern=.u.x", Spec{Pattern: ".u.x"}},
		{"delay=20ms,ttl=128", Spec{Delay: 20 * time.Millisecond, TTL: 128}},
		{"ttl=255,unreachable", Spec{TTL: 255, Unreachable: UnreachHost}},
	}
	for _, c := range cases {
		got, err := ParseSpec(c.in)
		if err != nil {
			t.Errorf("ParseSpec(%q): %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseSpec(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", "abc", "delay=-5ms", "loss=150%", "speed=3", "pattern=", "pattern=.-x", "loss=5%,pattern=.x", "unreachable=port", "unreachable=1", "ttl=0", "ttl=256", "ttl=-1", "ttl=64ms"} {
		if _, err := ParseSpec(bad); err == nil {
			t.Errorf("ParseSpec(%q): expected error", bad)
		}
	}
}

func TestSpecStringRoundTrip(t *testing.T) {
	for _, in := range []string{"0ms", "120ms", "delay=200ms,jitter=30ms", "delay=10ms,loss=5%", "timeout", "delay=5ms,pattern=..x",
		"unreachable", "delay=3ms,unreachable=net", "pattern=.u,unreachable=admin", "delay=5ms,ttl=128", "unreachable=net,ttl=1"} {
		sp, err := ParseSpec(in)
		if err != nil {
			t.Fatal(err)
		}
		again, err := ParseSpec(sp.String())
		if err != nil || !reflect.DeepEqual(again, sp) {
			t.Errorf("%q -> %q -> %+v (%v), want %+v", in, sp.String(), again, err, sp)
		}
	}
}

func TestDecidePattern(t *testing.T) {
	sp := Spec{Delay: time.Millisecond, Pattern: ".xu"}
	want := []Outcome{Reply, Drop, Unreachable, Reply, Drop, Unreachable}
	for i, w := range want {
		out, code, _ := sp.Decide(i, nil)
		if out != w {
			t.Errorf("request %d: %v, want %v", i, out, w)
		}
		if (out == Unreachable) != (code == UnreachHost) {
			t.Errorf("request %d: code %v with outcome %v", i, code, out)
		}
	}
	// With a pattern, unreachable= only selects the code used for 'u'.
	sp = Spec{Pattern: ".u", Unreachable: UnreachAdmin}
	if out, _, _ := sp.Decide(0, nil); out != Reply {
		t.Errorf("'.' gave %v, want reply", out)
	}
	if out, code, _ := sp.Decide(1, nil); out != Unreachable || code != UnreachAdmin {
		t.Errorf("'u' gave %v/%v, want unreachable/admin", out, code)
	}
}

func TestDecideUnreachable(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	sp := Spec{Delay: 30 * time.Millisecond, Unreachable: UnreachNet, Loss: 0.5}
	counts := map[Outcome]int{}
	for i := 0; i < 2000; i++ {
		out, code, d := sp.Decide(i, rng)
		counts[out]++
		if out == Unreachable && (code != UnreachNet || d != 30*time.Millisecond) {
			t.Fatalf("got %v/%v after %v", out, code, d)
		}
	}
	if counts[Reply] != 0 || counts[Drop] < 900 || counts[Unreachable] < 900 {
		t.Errorf("outcomes %v: want about half dropped and half unreachable, no replies", counts)
	}
}

func TestDecideJitterRange(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	sp := Spec{Delay: 100 * time.Millisecond, Jitter: 20 * time.Millisecond}
	for i := 0; i < 1000; i++ {
		out, _, d := sp.Decide(i, rng)
		if out != Reply || d < 80*time.Millisecond || d > 120*time.Millisecond {
			t.Fatalf("Decide = %v, %v; want a reply within 80-120ms", out, d)
		}
	}
	for i := 0; i < 1000; i++ {
		if _, _, d := (Spec{Delay: time.Millisecond, Jitter: 50 * time.Millisecond}).Decide(i, rng); d < 0 {
			t.Fatal("negative delay")
		}
	}
}

func TestDecideLossRate(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	sp := Spec{Loss: 0.3}
	dropped := 0
	const n = 20000
	for i := 0; i < n; i++ {
		if out, _, _ := sp.Decide(i, rng); out == Drop {
			dropped++
		}
	}
	if rate := float64(dropped) / n; rate < 0.27 || rate > 0.33 {
		t.Errorf("drop rate %.3f, want about 0.30", rate)
	}
	if out, _, _ := (Spec{Loss: 1}).Decide(0, rng); out != Drop {
		t.Error("loss=1 should always drop")
	}
}

func TestSpecJSON(t *testing.T) {
	in := Spec{Delay: 120 * time.Millisecond, Jitter: 5 * time.Millisecond, Pattern: ".x"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Spec
	if err := json.Unmarshal(b, &out); err != nil || !reflect.DeepEqual(out, in) {
		t.Errorf("round trip: got %+v (%v), want %+v (json %s)", out, err, in, b)
	}
	if err := json.Unmarshal([]byte(`"delay=7ms,loss=10%"`), &out); err != nil || out.String() != (Spec{Delay: 7 * time.Millisecond, Loss: 0.1}).String() {
		t.Errorf("string form: got %+v (%v)", out, err)
	}
	for in, want := range map[string]UnreachCode{`{"unreachable":""}`: UnreachHost, `{"unreachable":"admin"}`: UnreachAdmin} {
		var sp Spec
		if err := json.Unmarshal([]byte(in), &sp); err != nil || sp.Unreachable != want {
			t.Errorf("Unmarshal(%s) = %+v (%v)", in, sp, err)
		}
		b, _ := json.Marshal(sp)
		var again Spec
		if err := json.Unmarshal(b, &again); err != nil || !reflect.DeepEqual(again, sp) {
			t.Errorf("round trip of %s via %s: %+v (%v)", in, b, again, err)
		}
	}
	numeric := map[string]Spec{
		`20`:                           {Delay: 20 * time.Millisecond},
		`1.5`:                          {Delay: 1500 * time.Microsecond},
		`{"delay":50,"jitter":5}`:      {Delay: 50 * time.Millisecond, Jitter: 5 * time.Millisecond},
		`{"delay":"50ms","loss":"5%"}`: {Delay: 50 * time.Millisecond, Loss: 0.05},
		`{"loss":0.25}`:                {Loss: 0.25},
		`{"delay":"1s","loss":"0.1"}`:  {Delay: time.Second, Loss: 0.1},
	}
	for in, want := range numeric {
		var sp Spec
		if err := json.Unmarshal([]byte(in), &sp); err != nil || !reflect.DeepEqual(sp, want) {
			t.Errorf("Unmarshal(%s) = %+v (%v), want %+v", in, sp, err, want)
		}
	}
	for _, bad := range []string{`{"loss":2}`, `{"delay":"5ms","dealy":"6ms"}`, `{"loss":0.5,"pattern":".x"}`, `{"unreachable":"port"}`,
		`-5`, `{"delay":-5}`, `{"loss":"150%"}`, `{"delay":true}`} {
		if err := json.Unmarshal([]byte(bad), &out); err == nil {
			t.Errorf("Unmarshal(%s): expected error", bad)
		}
	}
}

func TestSpecJSONTTL(t *testing.T) {
	for _, in := range []string{`{"delay":"5ms","ttl":128}`, `{"delay":"5ms","ttl":"128"}`, `"delay=5ms,ttl=128"`} {
		var sp Spec
		if err := json.Unmarshal([]byte(in), &sp); err != nil || sp.String() != (Spec{Delay: 5 * time.Millisecond, TTL: 128}).String() {
			t.Errorf("%s: %+v, %v", in, sp, err)
		}
	}
	b, _ := json.Marshal(Spec{Delay: 5 * time.Millisecond, TTL: 128})
	if string(b) != `{"delay":"5ms","ttl":"128"}` {
		t.Errorf("marshal = %s", b)
	}
	if b, _ := json.Marshal(Spec{Delay: 5 * time.Millisecond}); string(b) != `{"delay":"5ms"}` {
		t.Errorf("default TTL must not be marshalled: %s", b)
	}
	for _, bad := range []string{`{"ttl":0}`, `{"ttl":300}`, `{"ttl":"64ms"}`} {
		var sp Spec
		if err := json.Unmarshal([]byte(bad), &sp); err == nil {
			t.Errorf("%s: expected error", bad)
		}
	}
}
