package engine

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/nanmuyao/pingmock/internal/rule"
)

// fakeClock lets tests move time forward explicitly.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestRuleSet(t *testing.T, def string, seed uint64) (*RuleSet, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	return NewRuleSet(mustScenario(t, def), seed, WithClock(clock.now)), clock
}

func mustScenario(t *testing.T, s string) rule.Scenario {
	t.Helper()
	sc, err := rule.ParseScenario(s)
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

func TestMostSpecificRuleWins(t *testing.T) {
	rs, _ := newTestRuleSet(t, "1ms", 1)
	rs.Set(netip.MustParsePrefix("10.99.0.0/24"), mustScenario(t, "10ms"))
	rs.Set(netip.MustParsePrefix("10.99.0.128/25"), mustScenario(t, "20ms"))
	rs.Set(netip.MustParsePrefix("10.99.0.200/32"), mustScenario(t, "30ms"))
	cases := map[string]struct {
		delay time.Duration
		rule  string
	}{
		"10.99.0.5":   {10 * time.Millisecond, "10.99.0.0/24"},
		"10.99.0.150": {20 * time.Millisecond, "10.99.0.128/25"},
		"10.99.0.200": {30 * time.Millisecond, "10.99.0.200/32"},
		"192.0.2.1":   {time.Millisecond, "default"},
	}
	for ip, want := range cases {
		d := rs.Decide(addr(ip))
		if d.Delay != want.delay || d.Rule != want.rule {
			t.Errorf("%s: got %v via %s, want %v via %s", ip, d.Delay, d.Rule, want.delay, want.rule)
		}
	}
	rs.Delete(netip.MustParsePrefix("10.99.0.200/32"))
	if d := rs.Decide(addr("10.99.0.200")); d.Delay != 20*time.Millisecond {
		t.Errorf("after delete, delay = %v, want 20ms", d.Delay)
	}
}

// outcomes records the decisions for one target as a comparable string.
func outcomes(rs *RuleSet, ip string, n int) []Decision {
	out := make([]Decision, n)
	for i := range out {
		out[i] = rs.Decide(addr(ip))
	}
	return out
}

func TestSameSeedSameSequencePerTarget(t *testing.T) {
	const text = "delay=100ms,jitter=50ms,loss=30%"
	a, _ := newTestRuleSet(t, text, 42)
	b, _ := newTestRuleSet(t, text, 42)

	// Interleave requests differently: a sees A,B,A,B...; b sees all B then all A.
	var aA, aB []Decision
	for i := 0; i < 50; i++ {
		aA = append(aA, a.Decide(addr("10.99.0.1")))
		aB = append(aB, a.Decide(addr("10.99.0.2")))
	}
	bB := outcomes(b, "10.99.0.2", 50)
	bA := outcomes(b, "10.99.0.1", 50)
	for i := range aA {
		if aA[i] != bA[i] || aB[i] != bB[i] {
			t.Fatalf("request %d differs between runs with the same seed", i)
		}
	}

	c, _ := newTestRuleSet(t, text, 43)
	cA := outcomes(c, "10.99.0.1", 50)
	same := true
	for i := range aA {
		same = same && aA[i] == cA[i]
	}
	if same {
		t.Error("different seeds produced identical sequences")
	}
}

func TestPatternPerTarget(t *testing.T) {
	rs, _ := newTestRuleSet(t, "delay=1ms,pattern=..x", 1)
	want := []bool{true, true, false, true, true, false}
	for _, ip := range []string{"10.99.0.1", "10.99.0.2"} {
		for i, d := range outcomes(rs, ip, len(want)) {
			if (d.Outcome == rule.Reply) != want[i] {
				t.Errorf("%s request %d: %v, want reply=%v", ip, i, d.Outcome, want[i])
			}
		}
	}
}

func TestCountPhases(t *testing.T) {
	rs, _ := newTestRuleSet(t, "10ms for 2 pings; delay=20ms,pattern=.x for 2 pings; 30ms", 1)
	type step struct {
		out   rule.Outcome
		delay time.Duration
		phase int
	}
	ms := time.Millisecond
	want := []step{{rule.Reply, 10 * ms, 1}, {rule.Reply, 10 * ms, 1}, {rule.Reply, 20 * ms, 2}, {rule.Drop, 0, 2}, {rule.Reply, 30 * ms, 3}, {rule.Reply, 30 * ms, 3}}
	for i, d := range outcomes(rs, "10.99.0.1", len(want)) {
		if d.Outcome != want[i].out || d.Delay != want[i].delay || d.Phase != want[i].phase {
			t.Errorf("request %d: got %+v, want %+v", i, d, want[i])
		}
	}
}

func TestTimePhasesRepeat(t *testing.T) {
	rs, clock := newTestRuleSet(t, "10ms for 1s; 20ms for 1s; repeat", 1)
	ms := time.Millisecond
	steps := []struct {
		at   time.Duration // absolute offset from the rule being set
		want time.Duration
	}{
		{0, 10 * ms},
		{900 * ms, 10 * ms},
		{1500 * ms, 20 * ms},
		{2200 * ms, 10 * ms},
		{3999 * ms, 20 * ms},
		{1000*time.Second + 500*ms, 10 * ms}, // long idle gap: whole cycles are skipped
		{1001*time.Second + 500*ms, 20 * ms},
	}
	var elapsed time.Duration
	for _, s := range steps {
		clock.advance(s.at - elapsed)
		elapsed = s.at
		if d := rs.Decide(addr("10.99.0.1")); d.Delay != s.want {
			t.Errorf("at %v: delay %v, want %v", s.at, d.Delay, s.want)
		}
	}
}

func TestTimePhasesStartWhenRuleIsSet(t *testing.T) {
	rs, clock := newTestRuleSet(t, "0ms", 1)
	rs.Set(netip.MustParsePrefix("10.99.0.1/32"), mustScenario(t, "10ms for 1s; timeout"))
	clock.advance(2 * time.Second) // first request arrives after the first phase ended
	if d := rs.Decide(addr("10.99.0.1")); d.Outcome != rule.Drop || d.Phase != 2 {
		t.Errorf("got %+v, want a drop in phase 2", d)
	}
}

func TestRuleChangeRestartsScenario(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 7)
	p := netip.MustParsePrefix("10.99.0.1/32")
	text := "delay=50ms,jitter=40ms,loss=40% for 3 pings; 5ms"
	rs.Set(p, mustScenario(t, text))
	first := outcomes(rs, "10.99.0.1", 5)
	rs.Set(p, mustScenario(t, text))
	second := outcomes(rs, "10.99.0.1", 5)
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("request %d: %+v after reinstalling the rule, want %+v", i, second[i], first[i])
		}
	}
	if first[3].Phase != 2 {
		t.Errorf("4th request should be in phase 2, got %+v", first[3])
	}
}

func TestStatsAndSnapshot(t *testing.T) {
	rs, _ := newTestRuleSet(t, "delay=1ms,pattern=.x", 9)
	outcomes(rs, "10.99.0.1", 4)
	snap := rs.Snapshot()
	st := snap["stats"].(map[string]TargetState)["10.99.0.1"]
	if st.Requests != 4 || st.Replied != 2 || st.Dropped != 2 || st.Rule != "default" {
		t.Errorf("stats = %+v", st)
	}
	if snap["seed"].(uint64) != 9 {
		t.Errorf("seed = %v", snap["seed"])
	}
	rs.ResetStats()
	st = rs.Snapshot()["stats"].(map[string]TargetState)["10.99.0.1"]
	if st.Requests != 0 {
		t.Errorf("after reset: %+v", st)
	}
	// Resetting counters must not restart the pattern: the 5th request is a reply.
	if d := rs.Decide(addr("10.99.0.1")); d.Outcome != rule.Reply {
		t.Error("ResetStats restarted the scenario")
	}
}

func TestUnreachable(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 1)
	rs.Set(netip.MustParsePrefix("10.99.0.1/32"), mustScenario(t, "delay=15ms,unreachable=admin"))
	rs.Set(netip.MustParsePrefix("10.99.0.2/32"), mustScenario(t, "delay=5ms,pattern=.ux"))
	if d := rs.Decide(addr("10.99.0.1")); d.Outcome != rule.Unreachable || d.Code != rule.UnreachAdmin || d.Delay != 15*time.Millisecond {
		t.Errorf("unreachable=admin: %+v", d)
	}
	want := []struct {
		out  rule.Outcome
		code rule.UnreachCode
	}{{rule.Reply, rule.UnreachNone}, {rule.Unreachable, rule.UnreachHost}, {rule.Drop, rule.UnreachNone}}
	for i, d := range outcomes(rs, "10.99.0.2", 3) {
		if d.Outcome != want[i].out || d.Code != want[i].code {
			t.Errorf("pattern .ux request %d: %+v, want %v/%v", i, d, want[i].out, want[i].code)
		}
	}
	st := rs.Snapshot()["stats"].(map[string]TargetState)["10.99.0.2"]
	if st.Replied != 1 || st.Unreachable != 1 || st.Dropped != 1 {
		t.Errorf("stats = %+v", st)
	}
}

// Seeded sequences are part of the behavior users rely on: the same seed
// and rule must give the same results across versions. These values were
// recorded with v0.2.0.
func TestSeededSequenceIsStable(t *testing.T) {
	rs, _ := newTestRuleSet(t, "delay=100ms,jitter=50ms,loss=50%", 1234)
	var got []string
	for _, d := range outcomes(rs, "10.99.0.5", 8) {
		got = append(got, d.Outcome.String()+"/"+d.Delay.String())
	}
	const want = "reply/80.783202ms drop/0s reply/105.683618ms drop/0s drop/0s reply/133.752918ms reply/122.480243ms drop/0s"
	if s := strings.Join(got, " "); s != want {
		t.Errorf("seeded sequence changed:\n got %s\nwant %s", s, want)
	}
}

func TestIPv6Targets(t *testing.T) {
	rs, _ := newTestRuleSet(t, "1ms", 3)
	rs.Set(netip.MustParsePrefix("fd99::/64"), mustScenario(t, "10ms"))
	rs.Set(netip.MustParsePrefix("fd99::5/128"), mustScenario(t, "delay=20ms,pattern=.x"))
	if d := rs.Decide(addr("fd99::1")); d.Delay != 10*time.Millisecond || d.Rule != "fd99::/64" {
		t.Errorf("fd99::1: %+v", d)
	}
	if d := rs.Decide(addr("fd99::5")); d.Delay != 20*time.Millisecond || d.Rule != "fd99::5/128" {
		t.Errorf("fd99::5: %+v", d)
	}
	if d := rs.Decide(addr("fd99::5")); d.Outcome != rule.Drop {
		t.Errorf("fd99::5 second request: %+v", d)
	}
	// An IPv6 prefix never matches an IPv4 address, and the other way round.
	rs.Set(netip.MustParsePrefix("0.0.0.0/0"), mustScenario(t, "30ms"))
	if d := rs.Decide(addr("2001:db8::1")); d.Rule != "default" {
		t.Errorf("IPv4 catch-all matched an IPv6 address: %+v", d)
	}
	if d := rs.Decide(addr("192.0.2.1")); d.Rule != "0.0.0.0/0" {
		t.Errorf("192.0.2.1: %+v", d)
	}
	// Seeded streams differ between targets, including IPv6 ones.
	a, _ := newTestRuleSet(t, "delay=50ms,jitter=50ms", 3)
	if x, y := a.Decide(addr("fd99::1")), a.Decide(addr("fd99::2")); x.Delay == y.Delay {
		t.Errorf("fd99::1 and fd99::2 got the same random delay %v", x.Delay)
	}
}

func TestResetRestoresBaseline(t *testing.T) {
	rs, _ := newTestRuleSet(t, "1ms", 7)
	rs.Set(netip.MustParsePrefix("10.99.0.1/32"), mustScenario(t, "delay=5ms,pattern=.x"))
	rs.SaveBaseline()

	// A test changes everything: rules, default, and the targets' state.
	rs.Set(netip.MustParsePrefix("10.99.0.1/32"), mustScenario(t, "timeout"))
	rs.Set(netip.MustParsePrefix("10.99.0.2/32"), mustScenario(t, "unreachable"))
	rs.SetDefault(mustScenario(t, "timeout"))
	before := rs.Decide(addr("10.99.0.1")) // pattern position 1
	rs.Decide(addr("10.99.0.9"))
	if before.Outcome != rule.Drop {
		t.Fatalf("changed rule not applied: %+v", before)
	}

	rs.Reset()
	snap := rs.Snapshot()
	if snap["default"].(rule.Scenario).String() != "delay=1ms" {
		t.Errorf("default after reset = %v", snap["default"])
	}
	if rules := snap["rules"].([]map[string]any); len(rules) != 1 || rules[0]["target"] != "10.99.0.1/32" || rules[0]["rule"].(rule.Scenario).String() != "delay=5ms,pattern=.x" {
		t.Errorf("rules after reset = %v", rules)
	}
	if stats := snap["stats"].(map[string]TargetState); len(stats) != 0 {
		t.Errorf("stats after reset = %v", stats)
	}
	// Targets start their scenarios over: the pattern begins at ".", and
	// the default rule applies to 10.99.0.2 again.
	if d := rs.Decide(addr("10.99.0.1")); d.Outcome != rule.Reply || d.Phase != 1 {
		t.Errorf("first decision after reset = %+v", d)
	}
	if d := rs.Decide(addr("10.99.0.2")); d.Rule != "default" || d.Delay != time.Millisecond {
		t.Errorf("deleted rule still applies: %+v", d)
	}
}

func TestResetRepeatsSeededSequence(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 3)
	rs.Set(netip.MustParsePrefix("10.99.0.0/24"), mustScenario(t, "loss=50%"))
	rs.SaveBaseline()
	run := func() string {
		var b strings.Builder
		for range 20 {
			if rs.Decide(addr("10.99.0.1")).Outcome == rule.Drop {
				b.WriteByte('x')
			} else {
				b.WriteByte('.')
			}
		}
		return b.String()
	}
	first := run()
	if strings.Count(first, "x") == 0 || strings.Count(first, ".") == 0 {
		t.Fatalf("sequence %q has no variety", first)
	}
	if again := run(); again == first {
		t.Fatalf("the sequence repeated without a reset: %q", first)
	}
	rs.Reset()
	if again := run(); again != first {
		t.Errorf("after reset %q, before %q", again, first)
	}
}
