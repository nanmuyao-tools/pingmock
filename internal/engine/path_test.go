package engine

import (
	"net/netip"
	"testing"
	"time"

	"github.com/nanmuyao/pingmock/internal/icmp"
	"github.com/nanmuyao/pingmock/internal/rule"
)

func mustPath(t *testing.T, s string) rule.Path {
	t.Helper()
	p, err := rule.ParsePath(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDecideProbeWalksThePath(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 1)
	target := addr("8.8.8.8")
	rs.Set(netip.MustParsePrefix("8.8.8.8/32"), mustScenario(t, "30ms"))
	rs.SetPath(target, mustPath(t, "192.168.1.1@2ms > 10.0.0.1@15ms,ttl=250 > * > 72.14.0.1@25ms"))

	// TTL 1 to 4: each hop in turn; the third never answers.
	want := []struct {
		out  rule.Outcome
		from string
		d    time.Duration
		ttl  uint8
	}{
		{rule.TimeExceeded, "192.168.1.1", 2 * time.Millisecond, 64},
		{rule.TimeExceeded, "10.0.0.1", 15 * time.Millisecond, 250},
		{rule.Drop, "invalid IP", 0, 0},
		{rule.TimeExceeded, "72.14.0.1", 25 * time.Millisecond, 64},
	}
	for i, w := range want {
		d := rs.DecideProbe(icmp.Probe{Dst: target, TTL: uint8(i + 1), Kind: icmp.UDP})
		if d.Outcome != w.out || d.From.String() != w.from || d.Delay != w.d || d.Hop != i+1 || (w.out == rule.TimeExceeded && d.TTL != w.ttl) {
			t.Errorf("ttl %d: %+v", i+1, d)
		}
	}
	// Past the last hop: the destination answers by its rule, as the
	// probe's kind requires.
	for kind, out := range map[icmp.Kind]rule.Outcome{icmp.UDP: rule.PortUnreachable, icmp.TCP: rule.SynAck, icmp.Echo: rule.Reply} {
		d := rs.DecideProbe(icmp.Probe{Dst: target, TTL: 5, Kind: kind})
		if d.Outcome != out || d.From != target || d.Delay != 30*time.Millisecond || d.Hop != 0 || d.Rule != "8.8.8.8/32" {
			t.Errorf("%v at ttl 5: %+v", kind, d)
		}
	}
	// A ping with the usual TTL reaches the destination.
	if d := rs.DecideProbe(icmp.Probe{Dst: target, TTL: 64, Kind: icmp.Echo}); d.Outcome != rule.Reply || d.From != target {
		t.Errorf("ping: %+v", d)
	}
	st := rs.Snapshot()["stats"].(map[string]TargetState)[target.String()]
	// 4 probes answered by hops or lost there, 4 answered by the destination.
	if st.Requests != 8 || st.Hops != 3 || st.Dropped != 1 || st.Replied != 4 || st.Unreachable != 0 {
		t.Errorf("stats = %+v", st)
	}
	// A rule that drops or rejects applies to UDP and TCP probes too.
	rs.Set(netip.MustParsePrefix("8.8.8.8/32"), mustScenario(t, "unreachable=admin"))
	if d := rs.DecideProbe(icmp.Probe{Dst: target, TTL: 9, Kind: icmp.UDP}); d.Outcome != rule.Unreachable || d.Code != rule.UnreachAdmin {
		t.Errorf("unreachable destination: %+v", d)
	}
	rs.Set(netip.MustParsePrefix("8.8.8.8/32"), mustScenario(t, "timeout"))
	if d := rs.DecideProbe(icmp.Probe{Dst: target, TTL: 9, Kind: icmp.TCP}); d.Outcome != rule.Drop {
		t.Errorf("silent destination: %+v", d)
	}
}

func TestDecideProbeWithoutPath(t *testing.T) {
	rs, _ := newTestRuleSet(t, "5ms", 1)
	target := addr("10.99.0.1")
	// No path: the TTL is ignored and only echo requests are answered,
	// exactly as before paths existed.
	if d := rs.DecideProbe(icmp.Probe{Dst: target, TTL: 1, Kind: icmp.Echo}); d.Outcome != rule.Reply || d.Delay != 5*time.Millisecond || d.Hop != 0 || d.From != target {
		t.Errorf("echo with ttl 1: %+v", d)
	}
	for _, kind := range []icmp.Kind{icmp.UDP, icmp.TCP} {
		if d := rs.DecideProbe(icmp.Probe{Dst: target, TTL: 64, Kind: kind}); d.Outcome != rule.Ignored || d.Rule != "none" {
			t.Errorf("%v without a path: %+v", kind, d)
		}
	}
	if st := rs.Snapshot()["stats"].(map[string]TargetState)[target.String()]; st.Requests != 1 {
		t.Errorf("ignored probes were counted: %+v", st)
	}
}

func TestPathCutLosesPacketsBeyondTheHop(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 3)
	target := addr("8.8.8.8")
	rs.SetPath(target, mustPath(t, "10.0.0.1@1ms,cut=100% > 10.0.0.2@2ms"))
	// The first hop still answers probes that end there...
	if d := rs.DecideProbe(icmp.Probe{Dst: target, TTL: 1, Kind: icmp.UDP}); d.Outcome != rule.TimeExceeded {
		t.Errorf("ttl 1: %+v", d)
	}
	// ...but loses everything going past it: the second hop's probes, and
	// pings to the destination.
	if d := rs.DecideProbe(icmp.Probe{Dst: target, TTL: 2, Kind: icmp.UDP}); d.Outcome != rule.Drop || d.Hop != 1 || d.From.String() != "10.0.0.1" {
		t.Errorf("ttl 2: %+v", d)
	}
	if d := rs.DecideProbe(icmp.Probe{Dst: target, TTL: 64, Kind: icmp.Echo}); d.Outcome != rule.Drop || d.Hop != 1 {
		t.Errorf("ping: %+v", d)
	}
	// Loss on a hop is local to it: the probe to the next hop gets through.
	rs.SetPath(target, mustPath(t, "10.0.0.1@1ms,loss=100% > 10.0.0.2@2ms"))
	if d := rs.DecideProbe(icmp.Probe{Dst: target, TTL: 1, Kind: icmp.UDP}); d.Outcome != rule.Drop || d.Hop != 1 {
		t.Errorf("lossy hop, ttl 1: %+v", d)
	}
	if d := rs.DecideProbe(icmp.Probe{Dst: target, TTL: 2, Kind: icmp.UDP}); d.Outcome != rule.TimeExceeded || d.Hop != 2 {
		t.Errorf("lossy hop, ttl 2: %+v", d)
	}
}

func TestPathsAreSeededAndReset(t *testing.T) {
	run := func(rs *RuleSet) string {
		out := ""
		for range 30 {
			if rs.DecideProbe(icmp.Probe{Dst: addr("8.8.8.8"), TTL: 1, Kind: icmp.UDP}).Outcome == rule.Drop {
				out += "x"
			} else {
				out += "."
			}
		}
		return out
	}
	rs1, _ := newTestRuleSet(t, "0ms", 11)
	rs1.SetPath(addr("8.8.8.8"), mustPath(t, "10.0.0.1@1ms,loss=50%"))
	rs2, _ := newTestRuleSet(t, "0ms", 11)
	rs2.SetPath(addr("8.8.8.8"), mustPath(t, "10.0.0.1@1ms,loss=50%"))
	first := run(rs1)
	if first != run(rs2) {
		t.Error("the same seed gave different hop losses")
	}
	rs1.SaveBaseline()
	rs1.SetPath(addr("8.8.8.8"), mustPath(t, "10.0.0.9@1ms"))
	rs1.DeletePath(addr("1.1.1.1"))
	rs1.Reset()
	if p, ok := rs1.Path(addr("8.8.8.8")); !ok || p.String() != "10.0.0.1@delay=1ms,loss=50%" {
		t.Errorf("path after reset = %v, %v", p, ok)
	}
	if again := run(rs1); again != first {
		t.Errorf("after reset %q, want %q", again, first)
	}
	if !rs1.DeletePath(addr("8.8.8.8")) || rs1.DeletePath(addr("8.8.8.8")) {
		t.Error("DeletePath")
	}
	if addrs := rs1.HopAddrs(); len(addrs) != 0 {
		t.Errorf("HopAddrs after delete = %v", addrs)
	}
	snap := rs1.Snapshot()
	if paths := snap["paths"].([]map[string]any); len(paths) != 0 {
		t.Errorf("paths in snapshot = %v", paths)
	}
}
