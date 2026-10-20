package engine

import (
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/nanmuyao/pingmock/internal/icmp"
	"github.com/nanmuyao/pingmock/internal/rule"
)

func setTCP(t *testing.T, rs *RuleSet, target, text string) {
	t.Helper()
	tt, err := rule.ParseTCPTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := rule.ParseTCPScenario(text)
	if err != nil {
		t.Fatal(err)
	}
	rs.SetTCP(tt.Prefix, tt.Port, sc.Scenario)
}

// syn describes a connection attempt from a client port, with its ISN.
func syn(dst string, port, srcPort uint16, isn uint32) icmp.Probe {
	return icmp.Probe{Kind: icmp.TCP, Src: addr("10.99.0.254"), Dst: addr(dst), TTL: 64,
		SrcPort: srcPort, DstPort: port, TCPSeq: isn}
}

func TestTCPRuleOutcomes(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 1)
	setTCP(t, rs, "10.99.0.1:443", "delay=200ms")
	setTCP(t, rs, "10.99.0.2:443", "delay=5ms,refuse")
	setTCP(t, rs, "10.99.0.3:443", "timeout")
	setTCP(t, rs, "10.99.0.4:443", "unreachable=admin")
	cases := []struct {
		dst   string
		out   rule.Outcome
		delay time.Duration
	}{
		{"10.99.0.1", rule.SynAck, 200 * time.Millisecond},
		{"10.99.0.2", rule.Refused, 5 * time.Millisecond},
		{"10.99.0.3", rule.Drop, 0},
		{"10.99.0.4", rule.Unreachable, 0},
	}
	for i, c := range cases {
		d := rs.DecideProbe(syn(c.dst, 443, uint16(40000+i), 1))
		if d.Outcome != c.out || d.Delay != c.delay || d.Rule != "tcp "+c.dst+"/32:443" || d.From != addr(c.dst) {
			t.Errorf("%s: %+v, want %v after %v", c.dst, d, c.out, c.delay)
		}
	}
	// No TCP rule and no path: ignored, as before TCP rules existed.
	if d := rs.DecideProbe(syn("10.99.0.1", 80, 41000, 1)); d.Outcome != rule.Drop || d.Rule != "none" {
		t.Errorf("port without a rule: %+v", d)
	}
}

func TestTCPRulePrecedence(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 1)
	setTCP(t, rs, "10.99.0.0/24", "delay=1ms")
	setTCP(t, rs, "10.99.0.0/24:443", "delay=2ms")
	setTCP(t, rs, "10.99.0.7", "delay=3ms")
	setTCP(t, rs, "10.99.0.7:443", "delay=4ms")
	setTCP(t, rs, "10.99.0.8", "delay=5ms")
	for i, c := range []struct {
		dst  string
		port uint16
		want time.Duration
	}{
		{"10.99.0.1", 80, 1 * time.Millisecond},  // only the subnet
		{"10.99.0.1", 443, 2 * time.Millisecond}, // the subnet's port rule beats its every-port rule
		{"10.99.0.7", 80, 3 * time.Millisecond},  // the host beats the subnet
		{"10.99.0.7", 443, 4 * time.Millisecond}, // the host's port rule
		{"10.99.0.8", 443, 5 * time.Millisecond}, // the more specific prefix beats the subnet's port rule
	} {
		if d := rs.DecideProbe(syn(c.dst, c.port, uint16(40000+i), 1)); d.Delay != c.want {
			t.Errorf("%s:%d: %v (%s), want %v", c.dst, c.port, d.Delay, d.Rule, c.want)
		}
	}
	if !rs.DeleteTCP(netip.MustParsePrefix("10.99.0.7/32"), 443) || rs.DeleteTCP(netip.MustParsePrefix("10.99.0.7/32"), 443) {
		t.Error("DeleteTCP should report whether there was a rule")
	}
	if d := rs.DecideProbe(syn("10.99.0.7", 443, 42000, 1)); d.Delay != 3*time.Millisecond {
		t.Errorf("after deleting the port rule: %v", d.Delay)
	}
}

// A SYN the kernel sends again for the same attempt gets the same answer
// and is not a new request: "timeout for 1 requests" lets the second
// attempt through however many SYNs the first one sent.
func TestTCPRepeatedSYN(t *testing.T) {
	rs, clock := newTestRuleSet(t, "0ms", 1)
	setTCP(t, rs, "10.99.0.1:443", "timeout for 1 requests; 10ms")
	first := syn("10.99.0.1", 443, 40000, 777)
	for i := 0; i < 4; i++ {
		d := rs.DecideProbe(first)
		if d.Outcome != rule.Drop || d.Repeat != (i > 0) {
			t.Fatalf("SYN %d of the first attempt: %+v", i+1, d)
		}
		clock.advance(time.Second << i) // 1s, 2s, 4s, 8s, as Linux retries
	}
	if d := rs.DecideProbe(syn("10.99.0.1", 443, 40001, 888)); d.Outcome != rule.SynAck || d.Phase != 2 || d.Repeat {
		t.Errorf("second attempt: %+v", d)
	}
	st := rs.Snapshot()["tcp_stats"].(map[string]TCPTargetState)["10.99.0.1:443"]
	if st.Requests != 2 || st.Dropped != 1 || st.Accepted != 1 {
		t.Errorf("stats count SYNs sent again: %+v", st)
	}
	// After the window, the same addresses, ports and ISN are a new attempt.
	clock.advance(repeatWindow)
	if d := rs.DecideProbe(first); d.Repeat || d.Outcome != rule.SynAck {
		t.Errorf("after the window: %+v", d)
	}
}

func TestTCPPatternAndPhases(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 1)
	setTCP(t, rs, "10.99.0.1:443", "refuse for 2 requests; delay=1ms,pattern=.rx")
	var got []string
	for i := 0; i < 7; i++ {
		got = append(got, rs.DecideProbe(syn("10.99.0.1", 443, uint16(40000+i), 1)).Outcome.String())
	}
	want := "[refused refused syn-ack refused drop syn-ack refused]"
	if fmt.Sprint(got) != want {
		t.Errorf("got %v, want %s", got, want)
	}
}

// TCP rules have their own random streams: connection attempts do not
// change the seeded sequence of pings to the same address, and the same
// seed gives the same TCP outcomes.
func TestTCPRandomnessIsSeparate(t *testing.T) {
	pings := func(withTCP bool) string {
		rs, _ := newTestRuleSet(t, "0ms", 42)
		rs.Set(netip.MustParsePrefix("10.99.0.1/32"), mustScenario(t, "loss=50%"))
		setTCP(t, rs, "10.99.0.1", "loss=50%")
		var out []string
		for i := 0; i < 30; i++ {
			if withTCP {
				rs.DecideProbe(syn("10.99.0.1", 443, uint16(40000+i), 1))
			}
			out = append(out, rs.Decide(addr("10.99.0.1")).Outcome.String())
		}
		return fmt.Sprint(out)
	}
	if a, b := pings(false), pings(true); a != b {
		t.Errorf("pings changed by TCP traffic:\n%s\n%s", a, b)
	}
	tcp := func(seed uint64) string {
		rs, _ := newTestRuleSet(t, "0ms", seed)
		setTCP(t, rs, "10.99.0.1", "loss=50%")
		var out []string
		for i := 0; i < 30; i++ {
			out = append(out, rs.DecideProbe(syn("10.99.0.1", 443, uint16(40000+i), 1)).Outcome.String())
		}
		return fmt.Sprint(out)
	}
	if tcp(7) != tcp(7) {
		t.Error("the same seed gave different TCP outcomes")
	}
	if tcp(7) == tcp(8) {
		t.Error("different seeds gave the same TCP outcomes")
	}
}

func TestTCPRuleAndPath(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 1)
	target := addr("8.8.8.8")
	path, err := rule.ParsePath("10.0.0.1@2ms")
	if err != nil {
		t.Fatal(err)
	}
	rs.SetPath(target, path)
	rs.Set(netip.PrefixFrom(target, 32), mustScenario(t, "30ms"))
	setTCP(t, rs, "8.8.8.8:443", "refuse")
	p := syn("8.8.8.8", 443, 40000, 1)
	p.TTL = 1
	if d := rs.DecideProbe(p); d.Outcome != rule.TimeExceeded || d.Hop != 1 {
		t.Errorf("the hop still answers by TTL: %+v", d)
	}
	p.TTL, p.SrcPort = 64, 40001
	if d := rs.DecideProbe(p); d.Outcome != rule.Refused {
		t.Errorf("at the destination the TCP rule decides: %+v", d)
	}
	p.DstPort = 80
	if d := rs.DecideProbe(p); d.Outcome != rule.SynAck || d.Delay != 30*time.Millisecond {
		t.Errorf("another port keeps the path's behavior: %+v", d)
	}
}

func TestTCPResetAndStats(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 9)
	setTCP(t, rs, "10.99.0.1:443", "refuse for 1 requests; loss=50%")
	rs.SaveBaseline()
	run := func() string {
		var out []string
		for i := 0; i < 12; i++ {
			out = append(out, rs.DecideProbe(syn("10.99.0.1", 443, uint16(40000+i), 1)).Outcome.String())
		}
		return fmt.Sprint(out)
	}
	first := run()
	setTCP(t, rs, "10.99.0.1:443", "5ms")
	setTCP(t, rs, "10.99.0.2:22", "refuse")
	rs.ResetStats()
	if st := rs.Snapshot()["tcp_stats"].(map[string]TCPTargetState)["10.99.0.1:443"]; st.Requests != 0 {
		t.Errorf("ResetStats left %+v", st)
	}
	rs.Reset()
	if rules := rs.Snapshot()["tcp"].([]map[string]any); len(rules) != 1 || rules[0]["target"] != "10.99.0.1/32:443" {
		t.Errorf("after Reset: %v", rules)
	}
	// The same attempts again: the repeat cache was cleared, and the
	// scenario and stream start over.
	if again := run(); again != first {
		t.Errorf("after Reset:\n%s\nwant\n%s", again, first)
	}
}
