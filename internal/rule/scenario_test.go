package rule

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseScenario(t *testing.T) {
	ms := time.Millisecond
	cases := []struct {
		in   string
		want Scenario
	}{
		{"50ms", Single(Spec{Delay: 50 * ms})},
		{"50ms for 3 pings; timeout", Scenario{Phases: []Phase{
			{Spec: Spec{Delay: 50 * ms}, Count: 3},
			{Spec: Spec{Loss: 1}},
		}}},
		{"delay=500ms,jitter=50ms for 10s ; 20ms for 1 request; repeat", Scenario{Repeat: true, Phases: []Phase{
			{Spec: Spec{Delay: 500 * ms, Jitter: 50 * ms}, For: 10 * time.Second},
			{Spec: Spec{Delay: 20 * ms}, Count: 1},
		}}},
		{"timeout for 2s; 10ms", Scenario{Phases: []Phase{
			{Spec: Spec{Loss: 1}, For: 2 * time.Second},
			{Spec: Spec{Delay: 10 * ms}},
		}}},
	}
	for _, c := range cases {
		got, err := ParseScenario(c.in)
		if err != nil {
			t.Errorf("ParseScenario(%q): %v", c.in, err)
			continue
		}
		if got.String() != c.want.String() {
			t.Errorf("ParseScenario(%q) = %q, want %q", c.in, got, c.want)
		}
		again, err := ParseScenario(got.String())
		if err != nil || again.String() != got.String() {
			t.Errorf("round trip of %q failed: %q (%v)", got, again, err)
		}
	}
	bad := map[string]string{
		"":                           "empty",
		"50ms; timeout":              "needs a length",
		"50ms; repeat":               "needs a length", // repeating rule with an unbounded phase
		"50ms for 2s; 10ms; repeat":  "needs a length",
		"50ms for 10":                "invalid phase length",
		"50ms for 0 pings; timeout":  "invalid request count",
		"50ms for 3 hours; timeout":  "invalid phase length",
		"50ms for 3 pings;; timeout": "empty phase",
		"bogus for 2s; timeout":      "invalid spec",
	}
	for in, msg := range bad {
		_, err := ParseScenario(in)
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("ParseScenario(%q) error = %v, want it to mention %q", in, err, msg)
		}
	}
}

func TestScenarioJSON(t *testing.T) {
	cases := map[string]string{
		`"50ms for 3 pings; timeout"`:     "delay=50ms for 3 pings; timeout",
		`{"delay":"20ms","pattern":".x"}`: "delay=20ms,pattern=.x",
		`"10ms"`:                          "delay=10ms",
		`{"phases":[{"spec":"5ms","count":2},{"spec":{"delay":"9ms"},"for":"1s"}],"repeat":true}`: "delay=5ms for 2 pings; delay=9ms for 1s; repeat",
	}
	for in, want := range cases {
		var sc Scenario
		if err := json.Unmarshal([]byte(in), &sc); err != nil {
			t.Errorf("Unmarshal(%s): %v", in, err)
			continue
		}
		if sc.String() != want {
			t.Errorf("Unmarshal(%s) = %q, want %q", in, sc, want)
		}
		b, _ := json.Marshal(sc)
		if string(b) != `"`+want+`"` {
			t.Errorf("Marshal = %s, want %q", b, want)
		}
	}
	for _, bad := range []string{
		`{"phases":[]}`,
		`{"phases":[{"spec":"5ms"},{"spec":"6ms"}]}`,
		`{"phases":[{"spec":"5ms","for":"1s","count":2}]}`,
		`{"phases":[{"spec":"5ms","for":"1s"}],"repaet":true}`,
		`42`,
	} {
		var sc Scenario
		if err := json.Unmarshal([]byte(bad), &sc); err == nil {
			t.Errorf("Unmarshal(%s): expected error", bad)
		}
	}
}
