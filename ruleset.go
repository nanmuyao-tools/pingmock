package main

import (
	"hash/fnv"
	"math/rand/v2"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
)

// ParsePrefix accepts a single IPv4 address or a CIDR prefix.
func ParsePrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		if !p.Addr().Is4() {
			return netip.Prefix{}, errIPv4Only(s)
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	if !a.Is4() {
		return netip.Prefix{}, errIPv4Only(s)
	}
	return netip.PrefixFrom(a, 32), nil
}

type errIPv4Only string

func (e errIPv4Only) Error() string { return string(e) + ": only IPv4 is supported" }

// Stats counts what happened to requests for one target address.
type Stats struct {
	Requests uint64 `json:"requests"`
	Replied  uint64 `json:"replied"`
	Dropped  uint64 `json:"dropped"`
}

// entry is a scenario installed for a prefix (or as the default). Every
// installation gets a new id, so targets restart the scenario from its
// first phase whenever the rule that applies to them changes.
type entry struct {
	sc    Scenario
	id    uint64
	setAt time.Time
}

// target is the per-address state: where it is in its scenario and the
// random source used for its loss and jitter.
type target struct {
	entryID    uint64
	phase      int
	phaseStart time.Time
	phaseReqs  int
	rng        *rand.Rand
	stats      Stats
}

// Decision is the outcome for one echo request.
type Decision struct {
	Reply bool
	Delay time.Duration
	Rule  string // matching prefix, or "default"
	Phase int    // zero-based index of the phase that applied
}

// RuleSet maps targets to scenarios. The most specific matching prefix
// wins; targets without a matching rule use the default.
type RuleSet struct {
	mu      sync.Mutex
	seed    uint64
	nextID  uint64
	now     func() time.Time
	def     *entry
	rules   map[netip.Prefix]*entry
	targets map[netip.Addr]*target
}

// NewRuleSet creates a rule set. Each target draws its randomness from a
// stream derived from seed and its address, so its sequence of drops and
// delays does not depend on traffic to other targets.
func NewRuleSet(def Scenario, seed uint64) *RuleSet {
	rs := &RuleSet{
		seed:    seed,
		now:     time.Now,
		rules:   map[netip.Prefix]*entry{},
		targets: map[netip.Addr]*target{},
	}
	rs.def = rs.newEntry(def)
	return rs
}

func (rs *RuleSet) Seed() uint64 { return rs.seed }

func (rs *RuleSet) newEntry(sc Scenario) *entry {
	rs.nextID++
	return &entry{sc: sc, id: rs.nextID, setAt: rs.now()}
}

func (rs *RuleSet) Set(p netip.Prefix, sc Scenario) {
	rs.mu.Lock()
	rs.rules[p] = rs.newEntry(sc)
	rs.mu.Unlock()
}

func (rs *RuleSet) Delete(p netip.Prefix) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	_, ok := rs.rules[p]
	delete(rs.rules, p)
	return ok
}

func (rs *RuleSet) SetDefault(sc Scenario) {
	rs.mu.Lock()
	rs.def = rs.newEntry(sc)
	rs.mu.Unlock()
}

// match returns the entry for a, and its label. Callers hold rs.mu.
func (rs *RuleSet) match(a netip.Addr) (*entry, string) {
	var best *entry
	var bestPrefix netip.Prefix
	for p, e := range rs.rules {
		if p.Contains(a) && (best == nil || p.Bits() > bestPrefix.Bits()) {
			best, bestPrefix = e, p
		}
	}
	if best == nil {
		return rs.def, "default"
	}
	return best, bestPrefix.String()
}

func (rs *RuleSet) rngFor(a netip.Addr) *rand.Rand {
	h := fnv.New64a()
	b := a.As4()
	h.Write(b[:])
	return rand.New(rand.NewPCG(rs.seed, h.Sum64()))
}

// Decide determines the outcome of an echo request to dst and updates the
// target's scenario position and counters.
func (rs *RuleSet) Decide(dst netip.Addr) Decision {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	now := rs.now()
	e, label := rs.match(dst)
	t := rs.targets[dst]
	if t == nil {
		t = &target{}
		rs.targets[dst] = t
	}
	if t.entryID != e.id {
		t.entryID, t.phase, t.phaseStart, t.phaseReqs = e.id, 0, e.setAt, 0
		t.rng = rs.rngFor(dst)
	}
	advance(t, e.sc, now)
	delay, reply := e.sc.Phases[t.phase].Spec.decide(t.phaseReqs, t.rng)
	t.phaseReqs++
	t.stats.Requests++
	if reply {
		t.stats.Replied++
	} else {
		t.stats.Dropped++
	}
	return Decision{Reply: reply, Delay: delay, Rule: label, Phase: t.phase}
}

// advance moves t past every phase of sc that has ended by now.
func advance(t *target, sc Scenario, now time.Time) {
	last := len(sc.Phases) - 1
	for {
		p := sc.Phases[t.phase]
		switch {
		case p.For > 0 && now.Sub(t.phaseStart) >= p.For:
			t.phaseStart = t.phaseStart.Add(p.For)
		case p.Count > 0 && t.phaseReqs >= p.Count:
			t.phaseStart = now
		default:
			return
		}
		t.phaseReqs = 0
		if t.phase < last {
			t.phase++
			continue
		}
		if !sc.Repeat {
			return // the last phase stays in effect
		}
		t.phase = 0
		// Skip whole idle cycles at once instead of looping through them.
		if c := sc.cycle(); c > 0 {
			if n := now.Sub(t.phaseStart) / c; n > 0 {
				t.phaseStart = t.phaseStart.Add(n * c)
			}
		}
	}
}

// TargetState is the API view of one target.
type TargetState struct {
	Stats
	Rule  string `json:"rule"`
	Phase int    `json:"phase"` // 1-based
}

// Snapshot returns a copy of the current state for the API.
func (rs *RuleSet) Snapshot() map[string]any {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	keys := make([]netip.Prefix, 0, len(rs.rules))
	for p := range rs.rules {
		keys = append(keys, p)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	rules := make([]map[string]any, 0, len(keys))
	for _, p := range keys {
		rules = append(rules, map[string]any{"target": p.String(), "rule": rs.rules[p].sc})
	}
	targets := map[string]TargetState{}
	for a, t := range rs.targets {
		e, label := rs.match(a)
		st := TargetState{Stats: t.stats, Rule: label, Phase: 1}
		if t.entryID == e.id {
			st.Phase = t.phase + 1
		}
		targets[a.String()] = st
	}
	return map[string]any{"seed": rs.seed, "default": rs.def.sc, "rules": rules, "stats": targets}
}

// ResetStats zeroes the counters without touching scenario positions.
func (rs *RuleSet) ResetStats() {
	rs.mu.Lock()
	for _, t := range rs.targets {
		t.stats = Stats{}
	}
	rs.mu.Unlock()
}
