// Package engine keeps the rules installed for each target, decides how
// every echo request is answered, and records recent requests.
package engine

import (
	"hash/fnv"
	"math/rand/v2"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/nanmuyao/pingmock/internal/rule"
)

// Stats counts what happened to requests for one target address.
type Stats struct {
	Requests    uint64 `json:"requests"`
	Replied     uint64 `json:"replied"`
	Dropped     uint64 `json:"dropped"`
	Unreachable uint64 `json:"unreachable"`
}

// entry is a scenario installed for a prefix (or as the default). Every
// installation gets a new id, so targets restart the scenario from its
// first phase whenever the rule that applies to them changes.
type entry struct {
	sc    rule.Scenario
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
	Outcome rule.Outcome
	Code    rule.UnreachCode // set when Outcome is rule.Unreachable
	Delay   time.Duration    // before the reply or unreachable message
	TTL     uint8            // IP TTL or hop limit of the answer
	Rule    string           // matching prefix, or "default"
	Phase   int              // the phase that applied, counting from 1
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

	// baseline is what Reset restores: the default and the rules as of
	// SaveBaseline, or of NewRuleSet if it was never called.
	baseline struct {
		def   rule.Scenario
		rules map[netip.Prefix]rule.Scenario
	}
}

// Option configures a RuleSet.
type Option func(*RuleSet)

// WithClock replaces time.Now, for tests.
func WithClock(now func() time.Time) Option { return func(rs *RuleSet) { rs.now = now } }

// NewRuleSet creates a rule set. Each target draws its randomness from a
// stream derived from seed and its address, so its sequence of drops and
// delays does not depend on traffic to other targets.
func NewRuleSet(def rule.Scenario, seed uint64, opts ...Option) *RuleSet {
	rs := &RuleSet{
		seed:    seed,
		now:     time.Now,
		rules:   map[netip.Prefix]*entry{},
		targets: map[netip.Addr]*target{},
	}
	for _, o := range opts {
		o(rs)
	}
	rs.def = rs.newEntry(def)
	rs.SaveBaseline()
	return rs
}

// SaveBaseline makes the current default and rules what Reset restores.
// The command calls it once its startup rules are installed.
func (rs *RuleSet) SaveBaseline() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.baseline.def = rs.def.sc
	rs.baseline.rules = make(map[netip.Prefix]rule.Scenario, len(rs.rules))
	for p, e := range rs.rules {
		rs.baseline.rules[p] = e.sc
	}
}

// Reset puts the default and the rules back as they were at the baseline
// and forgets every target, so that scenario positions, counters and
// random sequences start over as they did at startup.
func (rs *RuleSet) Reset() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.def = rs.newEntry(rs.baseline.def)
	rs.rules = make(map[netip.Prefix]*entry, len(rs.baseline.rules))
	for p, sc := range rs.baseline.rules {
		rs.rules[p] = rs.newEntry(sc)
	}
	rs.targets = map[netip.Addr]*target{}
}

func (rs *RuleSet) Seed() uint64 { return rs.seed }

func (rs *RuleSet) newEntry(sc rule.Scenario) *entry {
	rs.nextID++
	return &entry{sc: sc, id: rs.nextID, setAt: rs.now()}
}

func (rs *RuleSet) Set(p netip.Prefix, sc rule.Scenario) {
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

func (rs *RuleSet) SetDefault(sc rule.Scenario) {
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

// rngFor returns the random stream of a target. IPv4 addresses hash their
// four bytes, as in earlier versions, so seeded runs stay reproducible.
func (rs *RuleSet) rngFor(a netip.Addr) *rand.Rand {
	h := fnv.New64a()
	if a.Is4() {
		b := a.As4()
		h.Write(b[:])
	} else {
		b := a.As16()
		h.Write(b[:])
	}
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
	sp := e.sc.Phases[t.phase].Spec
	out, code, delay := sp.Decide(t.phaseReqs, t.rng)
	t.phaseReqs++
	t.stats.Requests++
	switch out {
	case rule.Reply:
		t.stats.Replied++
	case rule.Drop:
		t.stats.Dropped++
	case rule.Unreachable:
		t.stats.Unreachable++
	}
	return Decision{Outcome: out, Code: code, Delay: delay, TTL: sp.EffectiveTTL(), Rule: label, Phase: t.phase + 1}
}

// advance moves t past every phase of sc that has ended by now.
func advance(t *target, sc rule.Scenario, now time.Time) {
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
		if c := sc.Cycle(); c > 0 {
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
