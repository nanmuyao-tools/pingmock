package rule

import (
	"encoding/json"
	"math/rand/v2"
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
	for _, bad := range []string{"", "abc", "delay=-5ms", "loss=150%", "speed=3", "pattern=", "pattern=.-x", "loss=5%,pattern=.x"} {
		if _, err := ParseSpec(bad); err == nil {
			t.Errorf("ParseSpec(%q): expected error", bad)
		}
	}
}

func TestSpecStringRoundTrip(t *testing.T) {
	for _, in := range []string{"0ms", "120ms", "delay=200ms,jitter=30ms", "delay=10ms,loss=5%", "timeout", "delay=5ms,pattern=..x"} {
		sp, err := ParseSpec(in)
		if err != nil {
			t.Fatal(err)
		}
		again, err := ParseSpec(sp.String())
		if err != nil || again != sp {
			t.Errorf("%q -> %q -> %+v (%v), want %+v", in, sp.String(), again, err, sp)
		}
	}
}

func TestDecidePattern(t *testing.T) {
	sp := Spec{Delay: time.Millisecond, Pattern: ".x."}
	want := []bool{true, false, true, true, false, true}
	for i, w := range want {
		if _, ok := sp.Decide(i, nil); ok != w {
			t.Errorf("request %d: reply=%v, want %v", i, ok, w)
		}
	}
}

func TestDecideJitterRange(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	sp := Spec{Delay: 100 * time.Millisecond, Jitter: 20 * time.Millisecond}
	for i := 0; i < 1000; i++ {
		d, ok := sp.Decide(i, rng)
		if !ok || d < 80*time.Millisecond || d > 120*time.Millisecond {
			t.Fatalf("decide = %v, %v; want within 80-120ms", d, ok)
		}
	}
	for i := 0; i < 1000; i++ {
		if d, _ := (Spec{Delay: time.Millisecond, Jitter: 50 * time.Millisecond}).Decide(i, rng); d < 0 {
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
		if _, ok := sp.Decide(i, rng); !ok {
			dropped++
		}
	}
	if rate := float64(dropped) / n; rate < 0.27 || rate > 0.33 {
		t.Errorf("drop rate %.3f, want about 0.30", rate)
	}
	if _, ok := (Spec{Loss: 1}).Decide(0, rng); ok {
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
	if err := json.Unmarshal(b, &out); err != nil || out != in {
		t.Errorf("round trip: got %+v (%v), want %+v (json %s)", out, err, in, b)
	}
	if err := json.Unmarshal([]byte(`"delay=7ms,loss=10%"`), &out); err != nil || out != (Spec{Delay: 7 * time.Millisecond, Loss: 0.1}) {
		t.Errorf("string form: got %+v (%v)", out, err)
	}
	for _, bad := range []string{`{"loss":2}`, `{"delay":"5ms","dealy":"6ms"}`, `{"loss":0.5,"pattern":".x"}`} {
		if err := json.Unmarshal([]byte(bad), &out); err == nil {
			t.Errorf("Unmarshal(%s): expected error", bad)
		}
	}
}
