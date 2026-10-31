package engine

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/nanmuyao-tools/ghostwire/internal/icmp"
	"github.com/nanmuyao-tools/ghostwire/internal/rule"
)

func setUDP(t *testing.T, rs *RuleSet, target, text string) {
	t.Helper()
	tt, err := rule.ParseTCPTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := rule.ParseUDPScenario(text)
	if err != nil {
		t.Fatal(err)
	}
	rs.SetUDP(tt.Prefix, tt.Port, sc.Scenario)
}

func datagram(dst string, port uint16) icmp.Probe {
	return icmp.Probe{Kind: icmp.UDP, Src: addr("10.99.0.254"), Dst: addr(dst), TTL: 64, SrcPort: 40000, DstPort: port}
}

func TestUDPRules(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 1)
	setUDP(t, rs, "10.99.0.1:514", "delay=5ms,refuse")
	setUDP(t, rs, "10.99.0.2:123", "timeout for 2 requests; refuse")
	setUDP(t, rs, "10.99.0.3", "unreachable=admin")
	if d := rs.DecideProbe(datagram("10.99.0.1", 514)); d.Outcome != rule.Refused || d.Delay != 5e6 || d.Rule != "udp 10.99.0.1/32:514" {
		t.Errorf("refuse: %+v", d)
	}
	var got []string
	for i := 0; i < 3; i++ {
		d := rs.DecideProbe(datagram("10.99.0.2", 123))
		got = append(got, fmt.Sprintf("%s/%d", d.Outcome, d.Phase))
	}
	if fmt.Sprint(got) != "[drop/1 drop/1 refused/2]" {
		t.Errorf("phases: %v", got)
	}
	if d := rs.DecideProbe(datagram("10.99.0.3", 9)); d.Outcome != rule.Unreachable || d.Code != rule.UnreachAdmin {
		t.Errorf("every port: %+v", d)
	}
	// No UDP rule and no path: ignored, as before.
	if d := rs.DecideProbe(datagram("10.99.0.1", 515)); d.Rule != "none" {
		t.Errorf("no rule: %+v", d)
	}
	st := rs.Snapshot()["udp_stats"].(map[string]UDPTargetState)["10.99.0.2:123"]
	if st.Requests != 3 || st.Dropped != 2 || st.Refused != 1 || st.Phase != 2 {
		t.Errorf("stats %+v", st)
	}
	// TCP to the same address and port is a different matter.
	if d := rs.DecideProbe(syn("10.99.0.1", 514, 40000, 1)); d.Rule != "none" {
		t.Errorf("a UDP rule answered TCP: %+v", d)
	}
}

// With a path, the hops still answer the probes whose TTL runs out on the
// way, and the UDP rule decides at the destination.
func TestUDPRuleAndPath(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 1)
	p, _ := rule.ParsePath("192.168.1.1@2ms > 10.0.0.1@3ms")
	rs.SetPath(addr("10.99.0.5"), p)
	setUDP(t, rs, "10.99.0.5:33434", "unreachable")
	probe := datagram("10.99.0.5", 33434)
	probe.TTL = 1
	if d := rs.DecideProbe(probe); d.Outcome != rule.TimeExceeded || d.Hop != 1 {
		t.Errorf("hop: %+v", d)
	}
	probe.TTL = 5
	if d := rs.DecideProbe(probe); d.Outcome != rule.Unreachable || d.Rule != "udp 10.99.0.5/32:33434" {
		t.Errorf("destination: %+v", d)
	}
	probe.DstPort = 33435
	if d := rs.DecideProbe(probe); d.Outcome != rule.PortUnreachable {
		t.Errorf("destination, another port: %+v", d)
	}
}

func TestUDPRandomnessAndReset(t *testing.T) {
	run := func(noise bool) string {
		rs, _ := newTestRuleSet(t, "0ms", 7)
		setUDP(t, rs, "10.99.0.1:53", "loss=40%,refuse")
		setTCP(t, rs, "10.99.0.1:53", "loss=50%")
		var out []string
		for i := 0; i < 20; i++ {
			if noise {
				rs.DecideProbe(syn("10.99.0.1", 53, uint16(40000+i), 1))
				rs.Decide(addr("10.99.0.1"))
			}
			out = append(out, rs.DecideProbe(datagram("10.99.0.1", 53)).Outcome.String())
		}
		return fmt.Sprint(out)
	}
	if a, b := run(false), run(true); a != b {
		t.Errorf("other traffic changed the sequence:\n%s\n%s", a, b)
	}
	rs, _ := newTestRuleSet(t, "0ms", 1)
	setUDP(t, rs, "10.99.0.1:53", "refuse")
	rs.SaveBaseline()
	setUDP(t, rs, "10.99.0.2:53", "refuse")
	rs.DecideProbe(datagram("10.99.0.1", 53))
	rs.ResetStats()
	if st := rs.Snapshot()["udp_stats"].(map[string]UDPTargetState)["10.99.0.1:53"]; st.Requests != 0 {
		t.Errorf("ResetStats: %+v", st)
	}
	rs.Reset()
	if rules := rs.Snapshot()["udp"].([]map[string]any); len(rules) != 1 {
		t.Errorf("after Reset: %v", rules)
	}
	if !rs.DeleteUDP(netip.PrefixFrom(addr("10.99.0.1"), 32), 53) {
		t.Error("DeleteUDP")
	}
}
