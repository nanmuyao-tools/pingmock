package main

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Spec describes how the mock answers pings to a target.
type Spec struct {
	Delay  time.Duration // base round-trip delay added before replying
	Jitter time.Duration // reply delay is uniform in [Delay-Jitter, Delay+Jitter]
	Loss   float64       // probability in [0,1] that a request gets no reply
}

// ParseSpec parses either a bare duration ("120ms") or a comma-separated
// list of key=value pairs: "delay=120ms,jitter=10ms,loss=5%".
// The keyword "timeout" is shorthand for loss=100%.
func ParseSpec(s string) (Spec, error) {
	var sp Spec
	s = strings.TrimSpace(s)
	if s == "" {
		return sp, fmt.Errorf("empty spec")
	}
	if s == "timeout" {
		sp.Loss = 1
		return sp, nil
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		key, val, found := strings.Cut(part, "=")
		if !found {
			key, val = "delay", part
		}
		var err error
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "delay":
			sp.Delay, err = parseDuration(val)
		case "jitter":
			sp.Jitter, err = parseDuration(val)
		case "loss":
			sp.Loss, err = parseLoss(val)
		default:
			err = fmt.Errorf("unknown key %q", key)
		}
		if err != nil {
			return Spec{}, fmt.Errorf("invalid spec %q: %w", s, err)
		}
	}
	return sp, nil
}

// parseDuration accepts Go durations ("1.5s", "120ms") and plain numbers,
// which are taken as milliseconds.
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		s = strconv.FormatFloat(f, 'f', -1, 64) + "ms"
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, fmt.Errorf("negative duration %s", s)
	}
	return d, nil
}

// parseLoss accepts "5%" or a fraction such as "0.05".
func parseLoss(s string) (float64, error) {
	s = strings.TrimSpace(s)
	pct := strings.HasSuffix(s, "%")
	f, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil {
		return 0, err
	}
	if pct {
		f /= 100
	}
	if f < 0 || f > 1 {
		return 0, fmt.Errorf("loss %s out of range", s)
	}
	return f, nil
}

func (sp Spec) String() string {
	return fmt.Sprintf("delay=%s,jitter=%s,loss=%g%%", sp.Delay, sp.Jitter, sp.Loss*100)
}

// sample decides whether to drop a request and, if not, how long to wait.
func (sp Spec) sample() (time.Duration, bool) {
	if sp.Loss > 0 && rand.Float64() < sp.Loss {
		return 0, false
	}
	d := sp.Delay
	if sp.Jitter > 0 {
		d += time.Duration(rand.Int64N(int64(2*sp.Jitter)+1)) - sp.Jitter
	}
	if d < 0 {
		d = 0
	}
	return d, true
}

// specJSON is the API representation of a Spec.
type specJSON struct {
	Delay  string  `json:"delay"`
	Jitter string  `json:"jitter"`
	Loss   float64 `json:"loss"`
}

func (sp Spec) MarshalJSON() ([]byte, error) {
	return json.Marshal(specJSON{sp.Delay.String(), sp.Jitter.String(), sp.Loss})
}

func (sp *Spec) UnmarshalJSON(b []byte) error {
	var j specJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	var out Spec
	var err error
	if j.Delay != "" {
		if out.Delay, err = parseDuration(j.Delay); err != nil {
			return err
		}
	}
	if j.Jitter != "" {
		if out.Jitter, err = parseDuration(j.Jitter); err != nil {
			return err
		}
	}
	if j.Loss < 0 || j.Loss > 1 {
		return fmt.Errorf("loss %g out of range [0,1]", j.Loss)
	}
	out.Loss = j.Loss
	*sp = out
	return nil
}

// ParsePrefix accepts a single IPv4 address or a CIDR prefix.
func ParsePrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		if !p.Addr().Is4() {
			return netip.Prefix{}, fmt.Errorf("%s: only IPv4 is supported", s)
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	if !a.Is4() {
		return netip.Prefix{}, fmt.Errorf("%s: only IPv4 is supported", s)
	}
	return netip.PrefixFrom(a, 32), nil
}

// Stats counts what happened to requests for one target address.
type Stats struct {
	Requests uint64 `json:"requests"`
	Replied  uint64 `json:"replied"`
	Dropped  uint64 `json:"dropped"`
}

// RuleSet maps targets to specs. The most specific matching prefix wins;
// targets without a matching rule use the default spec.
type RuleSet struct {
	mu    sync.RWMutex
	def   Spec
	rules map[netip.Prefix]Spec
	stats map[netip.Addr]*Stats
}

func NewRuleSet(def Spec) *RuleSet {
	return &RuleSet{def: def, rules: map[netip.Prefix]Spec{}, stats: map[netip.Addr]*Stats{}}
}

func (rs *RuleSet) Lookup(a netip.Addr) Spec {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	best, bestBits, found := Spec{}, -1, false
	for p, sp := range rs.rules {
		if p.Contains(a) && p.Bits() > bestBits {
			best, bestBits, found = sp, p.Bits(), true
		}
	}
	if !found {
		return rs.def
	}
	return best
}

func (rs *RuleSet) Set(p netip.Prefix, sp Spec) {
	rs.mu.Lock()
	rs.rules[p] = sp
	rs.mu.Unlock()
}

func (rs *RuleSet) Delete(p netip.Prefix) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	_, ok := rs.rules[p]
	delete(rs.rules, p)
	return ok
}

func (rs *RuleSet) SetDefault(sp Spec) {
	rs.mu.Lock()
	rs.def = sp
	rs.mu.Unlock()
}

// record updates the counters for a target address.
func (rs *RuleSet) record(a netip.Addr, replied bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	st := rs.stats[a]
	if st == nil {
		st = &Stats{}
		rs.stats[a] = st
	}
	st.Requests++
	if replied {
		st.Replied++
	} else {
		st.Dropped++
	}
}

// Snapshot returns a copy of the current state for the API.
func (rs *RuleSet) Snapshot() map[string]any {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	keys := make([]netip.Prefix, 0, len(rs.rules))
	for p := range rs.rules {
		keys = append(keys, p)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	rules := make([]map[string]any, 0, len(keys))
	for _, p := range keys {
		rules = append(rules, map[string]any{"target": p.String(), "spec": rs.rules[p]})
	}
	stats := map[string]Stats{}
	for a, st := range rs.stats {
		stats[a.String()] = *st
	}
	return map[string]any{"default": rs.def, "rules": rules, "stats": stats}
}

func (rs *RuleSet) ResetStats() {
	rs.mu.Lock()
	rs.stats = map[netip.Addr]*Stats{}
	rs.mu.Unlock()
}
