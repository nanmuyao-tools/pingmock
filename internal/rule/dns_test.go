package rule

import (
	"encoding/json"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

func TestParseDNSScenario(t *testing.T) {
	cases := []struct{ in, want string }{
		{"nxdomain", "delay=0s,nxdomain"},
		{"SERVFAIL", "delay=0s,servfail"},
		{"delay=50ms,nxdomain", "delay=50ms,nxdomain"},
		{"servfail for 2 requests; 10ms", "delay=0s,servfail for 2 requests; delay=10ms"},
		{"timeout for 1 query; 5ms", "timeout for 1 request; delay=5ms"},
		{"nxdomain for 3 queries; delay=2s", "delay=0s,nxdomain for 3 requests; delay=2s"},
		{"delay=5ms,pattern=.ns.x", "delay=5ms,pattern=.ns.x"},
		{"delay=1s,jitter=100ms,loss=20%", "delay=1s,jitter=100ms,loss=20%"},
	}
	for _, c := range cases {
		got, err := ParseDNSScenario(c.in)
		if err != nil {
			t.Errorf("ParseDNSScenario(%q): %v", c.in, err)
			continue
		}
		if got.String() != c.want {
			t.Errorf("ParseDNSScenario(%q) = %q, want %q", c.in, got, c.want)
		}
		again, err := ParseDNSScenario(got.String())
		if err != nil || again.String() != got.String() {
			t.Errorf("round trip of %q: %q, %v", got, again, err)
		}
	}
	for bad, want := range map[string]string{
		"nxdomain=yes":         "takes no value",
		"nxdomain,servfail":    "cannot be combined",
		"nxdomain,pattern=.n":  "use n or s",
		"pattern=.u":           "may only contain",
		"refuse":               "TCP and UDP rules",
		"after=close":          "TCP rules",
		"unreachable":          "not for DNS",
		"ttl=5":                "TTL 0",
		"servfail for 0 query": "count",
	} {
		if _, err := ParseDNSScenario(bad); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseDNSScenario(%q) = %v, want an error containing %q", bad, err, want)
		}
	}
}

// Ping and TCP rules reject what only DNS rules have, as text and as JSON.
func TestOtherRulesRejectDNSSettings(t *testing.T) {
	for _, text := range []string{"nxdomain", "delay=1ms,servfail", "pattern=.n", "10ms for 1 ping; servfail"} {
		if _, err := ParseScenario(text); err == nil || !strings.Contains(err.Error(), "DNS") {
			t.Errorf("ParseScenario(%q) = %v, want an error naming DNS rules", text, err)
		}
		if _, err := ParseTCPScenario(text); err == nil || !strings.Contains(err.Error(), "DNS") {
			t.Errorf("ParseTCPScenario(%q) = %v, want an error naming DNS rules", text, err)
		}
	}
	for _, obj := range []string{`{"nxdomain": true}`, `{"delay": 1, "pattern": ".s"}`} {
		var sc Scenario
		if err := json.Unmarshal([]byte(obj), &sc); err == nil {
			t.Errorf("JSON %s accepted as a ping rule", obj)
		}
		var tc TCPScenario
		if err := json.Unmarshal([]byte(obj), &tc); err == nil {
			t.Errorf("JSON %s accepted as a TCP rule", obj)
		}
	}
}

func TestDNSScenarioJSON(t *testing.T) {
	for in, want := range map[string]string{
		`"servfail for 2 requests; 10ms"`:                             "delay=0s,servfail for 2 requests; delay=10ms",
		`{"nxdomain": true}`:                                          "delay=0s,nxdomain",
		`{"delay": "2s", "servfail": true}`:                           "delay=2s,servfail",
		`{"delay": 5, "pattern": ".n"}`:                               "delay=5ms,pattern=.n",
		`{"phases": [{"spec": "nxdomain", "count": 1}, {"spec": 1}]}`: "delay=0s,nxdomain for 1 request; delay=1ms",
	} {
		var sc DNSScenario
		if err := json.Unmarshal([]byte(in), &sc); err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if sc.String() != want {
			t.Errorf("%s = %q, want %q", in, sc, want)
		}
	}
	var sc DNSScenario
	if err := json.Unmarshal([]byte(`{"nxdomain": true, "servfail": true}`), &sc); err == nil {
		t.Error("nxdomain and servfail together accepted")
	}
	if b, _ := json.Marshal(Spec{DNSFail: DNSServFail}); !strings.Contains(string(b), `"servfail":true`) {
		t.Errorf("servfail spec JSON %s", b)
	}
	if b, _ := json.Marshal(Spec{Delay: time.Millisecond}); strings.Contains(string(b), "nxdomain") || strings.Contains(string(b), "servfail") {
		t.Errorf("ping spec JSON %s mentions DNS settings", b)
	}
}

func TestDecideDNS(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	sp, _ := ParseDNSSpec("delay=20ms,nxdomain")
	if out, _, d := sp.Decide(0, rng); out != NXDomain || d != 20*time.Millisecond {
		t.Errorf("nxdomain: %v after %v", out, d)
	}
	sp, _ = ParseDNSSpec("servfail")
	if out, _, _ := sp.Decide(0, rng); out != ServFail {
		t.Errorf("servfail: %v", out)
	}
	sp, _ = ParseDNSSpec("pattern=.nsx")
	var got []string
	for i := 0; i < 5; i++ {
		out, _, _ := sp.Decide(i, rng)
		got = append(got, out.String())
	}
	if strings.Join(got, " ") != "reply nxdomain servfail drop reply" {
		t.Errorf("pattern: %v", got)
	}
}
