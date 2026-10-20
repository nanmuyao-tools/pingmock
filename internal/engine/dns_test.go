package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/nanmuyao/pingmock/internal/rule"
)

func setDNS(t *testing.T, rs *RuleSet, name, text string) {
	t.Helper()
	sc, err := rule.ParseDNSScenario(text)
	if err != nil {
		t.Fatal(err)
	}
	rs.SetDNS(name, sc.Scenario)
}

const (
	qA    = 1
	qAAAA = 28
)

func TestDNSRuleOutcomes(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 1)
	setDNS(t, rs, "API.example.com.", "delay=200ms")
	setDNS(t, rs, "gone.example", "nxdomain")
	setDNS(t, rs, "broken.example", "delay=5ms,servfail")
	setDNS(t, rs, "slow.example", "timeout")
	for _, c := range []struct {
		name  string
		out   rule.Outcome
		delay time.Duration
	}{
		{"api.example.com", rule.Reply, 200 * time.Millisecond},
		{"gone.example.", rule.NXDomain, 0},
		{"Broken.Example", rule.ServFail, 5 * time.Millisecond},
		{"slow.example", rule.Drop, 0},
	} {
		d, ok := rs.DecideDNS(c.name, qA)
		if !ok || d.Outcome != c.out || d.Delay != c.delay || d.Phase != 1 || d.Rule != "dns "+dnsName(c.name) {
			t.Errorf("%s: %+v %v, want %v after %v", c.name, d, ok, c.out, c.delay)
		}
	}
	if _, ok := rs.DecideDNS("other.example", qA); ok {
		t.Error("a name without a rule was decided")
	}
	if !rs.HasDNS("gone.example.") || rs.HasDNS("other.example") {
		t.Error("HasDNS")
	}
}

// Phases count queries per name and type: a lookup's A and AAAA queries
// each start the name's scenario, in whichever order they arrive.
func TestDNSPhasesPerType(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 1)
	setDNS(t, rs, "api.example.com", "servfail for 2 requests; 10ms")
	var got []string
	for _, q := range []uint16{qAAAA, qA, qA, qAAAA, qA, qAAAA} {
		d, _ := rs.DecideDNS("api.example.com", q)
		got = append(got, fmt.Sprintf("%s/%d", d.Outcome, d.Phase))
	}
	if want := "[servfail/1 servfail/1 servfail/1 servfail/1 reply/2 reply/2]"; fmt.Sprint(got) != want {
		t.Errorf("got %v, want %s", got, want)
	}
	st := rs.Snapshot()["dns_stats"].(map[string]DNSTargetState)
	if a := st["api.example.com/A"]; a.Requests != 3 || a.ServFail != 2 || a.Answered != 1 || a.Phase != 2 {
		t.Errorf("A stats: %+v", a)
	}
	if aaaa := st["api.example.com/AAAA"]; aaaa.Requests != 3 || aaaa.ServFail != 2 || aaaa.Rule != "dns api.example.com" {
		t.Errorf("AAAA stats: %+v", aaaa)
	}
	rules := rs.Snapshot()["dns"].([]map[string]any)
	if len(rules) != 1 || rules[0]["target"] != "api.example.com" ||
		rules[0]["rule"].(rule.DNSScenario).String() != "delay=0s,servfail for 2 requests; delay=10ms" {
		t.Errorf("rules: %v", rules)
	}
}

// Each name and type has its own random stream, apart from pings and TCP:
// the same seed gives the same sequence whatever else is asked.
func TestDNSRandomnessIsSeparate(t *testing.T) {
	run := func(noise bool) string {
		rs, _ := newTestRuleSet(t, "0ms", 7)
		setDNS(t, rs, "api.example.com", "delay=20ms,jitter=10ms,loss=40%")
		setDNS(t, rs, "other.example", "loss=50%")
		setTCP(t, rs, "10.99.0.1:80", "loss=50%")
		var out []string
		for i := 0; i < 20; i++ {
			if noise {
				rs.DecideDNS("other.example", qA)
				rs.DecideDNS("api.example.com", qAAAA)
				rs.DecideProbe(syn("10.99.0.1", 80, uint16(40000+i), 1))
				rs.Decide(addr("10.99.0.1"))
			}
			d, _ := rs.DecideDNS("api.example.com", qA)
			out = append(out, fmt.Sprint(d.Outcome, d.Delay))
		}
		return fmt.Sprint(out)
	}
	if a, b := run(false), run(true); a != b {
		t.Errorf("other traffic changed the sequence:\n%s\n%s", a, b)
	}
}

func TestDNSResetAndStats(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 3)
	setDNS(t, rs, "api.example.com", "nxdomain for 1 request; loss=50%")
	rs.SaveBaseline()
	run := func() string {
		var out []string
		for i := 0; i < 10; i++ {
			d, _ := rs.DecideDNS("api.example.com", qA)
			out = append(out, d.Outcome.String())
		}
		return fmt.Sprint(out)
	}
	first := run()
	setDNS(t, rs, "api.example.com", "servfail")
	setDNS(t, rs, "new.example", "nxdomain")
	rs.ResetStats()
	if st := rs.Snapshot()["dns_stats"].(map[string]DNSTargetState)["api.example.com/A"]; st.Requests != 0 {
		t.Errorf("ResetStats left %+v", st)
	}
	if !rs.DeleteDNS("new.example") || rs.DeleteDNS("new.example") {
		t.Error("DeleteDNS")
	}
	rs.Reset()
	if again := run(); again != first {
		t.Errorf("after Reset:\n%s\nwant\n%s", again, first)
	}
}
