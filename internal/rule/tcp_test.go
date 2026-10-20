package rule

import (
	"encoding/json"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

func TestParseTCPScenario(t *testing.T) {
	cases := []struct{ in, want string }{
		{"20ms", "delay=20ms"},
		{"refuse", "delay=0s,refuse"},
		{"delay=50ms,refuse", "delay=50ms,refuse"},
		{"refuse for 3 requests; 20ms", "delay=0s,refuse for 3 requests; delay=20ms"},
		{"timeout for 2 connections; 10ms", "timeout for 2 requests; delay=10ms"},
		{"timeout for 1 ping; 10ms", "timeout for 1 request; delay=10ms"},
		{"delay=5ms,pattern=.r.x", "delay=5ms,pattern=.r.x"},
		{"unreachable=admin", "delay=0s,unreachable=admin"},
		{"delay=1ms,ttl=128", "delay=1ms,ttl=128"},
		{"10ms,after=close", "delay=10ms,after=close"},
		{"after=RESET", "delay=0s,after=reset"},
		{"after=hang", "delay=0s"},
		{"delay=5ms,pattern=.r,after=reset", "delay=5ms,pattern=.r,after=reset"},
		{"loss=50%,after=close", "delay=0s,loss=50%,after=close"},
		{"refuse for 1 request; after=close", "delay=0s,refuse for 1 request; delay=0s,after=close"},
	}
	for _, c := range cases {
		got, err := ParseTCPScenario(c.in)
		if err != nil {
			t.Errorf("ParseTCPScenario(%q): %v", c.in, err)
			continue
		}
		if got.String() != c.want {
			t.Errorf("ParseTCPScenario(%q) = %q, want %q", c.in, got, c.want)
		}
		again, err := ParseTCPScenario(got.String())
		if err != nil || again.String() != got.String() {
			t.Errorf("round trip of %q: %q, %v", got, again, err)
		}
	}
	for _, bad := range []string{
		"refuse=yes", "refuse,unreachable", "refuse,pattern=.r", "pattern=.q", "refuse for 0 requests",
		"after=shut", "after", "after=close:1s", "refuse,after=close", "unreachable,after=reset",
		"loss=100%,after=close", "pattern=xr,after=reset",
	} {
		if _, err := ParseTCPScenario(bad); err == nil {
			t.Errorf("ParseTCPScenario(%q) succeeded", bad)
		}
	}
}

// Ping rules must not accept what only TCP rules have, through any of the
// ways a ping rule or the default comes in: text (flags, the API) and
// JSON (the configuration file, the API).
func TestPingRulesRejectTCPSettings(t *testing.T) {
	for _, text := range []string{"refuse", "delay=10ms,refuse", "pattern=.r", "10ms for 1 ping; refuse", "after=close", "10ms for 1 ping; after=reset"} {
		if _, err := ParseScenario(text); err == nil || !strings.Contains(err.Error(), "TCP") {
			t.Errorf("ParseScenario(%q) = %v, want an error naming TCP rules", text, err)
		}
		if !strings.Contains(text, ";") {
			if _, err := ParseSpec(text); err == nil {
				t.Errorf("ParseSpec(%q) succeeded", text)
			}
		}
		b, _ := json.Marshal(text)
		var sc Scenario
		if err := json.Unmarshal(b, &sc); err == nil {
			t.Errorf("JSON string %s accepted as a ping rule", b)
		}
	}
	for _, obj := range []string{
		`{"refuse": true}`,
		`{"delay": "10ms", "pattern": ".r"}`,
		`{"after": "close"}`,
		`{"phases": [{"spec": {"refuse": true}, "count": 1}, {"spec": "10ms"}]}`,
		`{"phases": [{"spec": "refuse", "count": 1}, {"spec": "10ms"}]}`,
	} {
		var sc Scenario
		if err := json.Unmarshal([]byte(obj), &sc); err == nil {
			t.Errorf("JSON %s accepted as a ping rule: %v", obj, sc)
		}
		var sp Spec
		if !strings.Contains(obj, "phases") {
			if err := json.Unmarshal([]byte(obj), &sp); err == nil {
				t.Errorf("JSON %s accepted as a ping spec", obj)
			}
		}
	}
}

func TestTCPScenarioJSON(t *testing.T) {
	for in, want := range map[string]string{
		`"refuse for 3 requests; 20ms"`:  "delay=0s,refuse for 3 requests; delay=20ms",
		`{"refuse": true}`:               "delay=0s,refuse",
		`{"delay": 50, "pattern": ".r"}`: "delay=50ms,pattern=.r",
		`{"phases": [{"spec": {"refuse": true}, "count": 2}, {"spec": "10ms"}]}`: "delay=0s,refuse for 2 requests; delay=10ms",
		`30`:                                 "delay=30ms",
		`{"delay": "5ms", "after": "reset"}`: "delay=5ms,after=reset",
		`{"after": "hang"}`:                  "delay=0s",
	} {
		var sc TCPScenario
		if err := json.Unmarshal([]byte(in), &sc); err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if sc.String() != want {
			t.Errorf("%s = %q, want %q", in, sc, want)
		}
		out, _ := json.Marshal(sc)
		if string(out) != `"`+want+`"` {
			t.Errorf("%s marshals to %s", in, out)
		}
	}
	// A spec with refuse marshals it, and one without leaves it out, so the
	// JSON of ping specs is unchanged.
	if b, _ := json.Marshal(Spec{Delay: time.Millisecond}); strings.Contains(string(b), "refuse") {
		t.Errorf("ping spec JSON %s mentions refuse", b)
	}
	if b, _ := json.Marshal(Spec{Refuse: true}); !strings.Contains(string(b), `"refuse":true`) {
		t.Errorf("refusing spec JSON %s", b)
	}
	if b, _ := json.Marshal(Spec{After: AfterClose}); !strings.Contains(string(b), `"after":"close"`) {
		t.Errorf("closing spec JSON %s", b)
	}
	if b, _ := json.Marshal(Spec{}); strings.Contains(string(b), "after") {
		t.Errorf("ping spec JSON %s mentions after", b)
	}
	// A delay before the action is reserved for later, and says so.
	if _, err := ParseTCPScenario("after=close:1s"); err == nil || !strings.Contains(err.Error(), "not supported yet") {
		t.Errorf("after=close:1s: %v", err)
	}
}

func TestDecideRefuse(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	sp, _ := ParseTCPSpec("delay=30ms,refuse")
	if out, _, d := sp.Decide(0, rng); out != Refused || d != 30*time.Millisecond {
		t.Errorf("refuse: %v after %v", out, d)
	}
	sp, _ = ParseTCPSpec("pattern=.rx")
	var got []string
	for i := 0; i < 6; i++ {
		out, _, _ := sp.Decide(i, rng)
		got = append(got, out.String())
	}
	if want := "reply refused drop reply refused drop"; strings.Join(got, " ") != want {
		t.Errorf("pattern .rx = %v, want %s", got, want)
	}
}

func TestParseTCPTarget(t *testing.T) {
	cases := []struct{ in, want string }{
		{"10.99.0.1", "10.99.0.1/32"},
		{"10.99.0.1:443", "10.99.0.1/32:443"},
		{"10.99.0.0/24:6379", "10.99.0.0/24:6379"},
		{"10.99.0.7/24", "10.99.0.0/24"},
		{"[fd99::1]", "[fd99::1/128]"},
		{"[fd99::1]:443", "[fd99::1/128]:443"},
		{"[fd99::/64]:443", "[fd99::/64]:443"},
		{"[::ffff:10.99.0.1]:80", "10.99.0.1/32:80"},
		{"DB.Example.com:5432", "db.example.com:5432"},
		{"db.example.com", "db.example.com"},
		{" 10.99.0.1:65535 ", "10.99.0.1/32:65535"},
	}
	for _, c := range cases {
		got, err := ParseTCPTarget(c.in)
		if err != nil {
			t.Errorf("ParseTCPTarget(%q): %v", c.in, err)
			continue
		}
		if got.String() != c.want {
			t.Errorf("ParseTCPTarget(%q) = %q, want %q", c.in, got, c.want)
		}
		again, err := ParseTCPTarget(got.String())
		if err != nil || again != got {
			t.Errorf("round trip of %q: %+v, %v", got, again, err)
		}
	}
	for _, bad := range []string{
		"fd99::1:443", "fd99::1", "10.99.0.1:0", "10.99.0.1:65536", "10.99.0.1:", "10.99.0.1:http",
		"[fd99::1", "[fd99::1]443", "[fd99::1]:", "[db.example.com]:80", "10.99.0.300:80", "",
	} {
		if got, err := ParseTCPTarget(bad); err == nil {
			t.Errorf("ParseTCPTarget(%q) = %v, want an error", bad, got)
		}
	}
	if _, err := ParseTCPTarget("fd99::1:443"); err == nil || !strings.Contains(err.Error(), "[fd99::1]:443") {
		t.Errorf("unbracketed IPv6: %v, want the bracketed form suggested", err)
	}
}
