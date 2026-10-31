package engine

import (
	"encoding/binary"
	"hash/fnv"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"

	"github.com/nanmuyao-tools/ghostwire/internal/rule"
)

// DNSStats counts what happened to the queries for one name and type.
// Every query counts, including the ones a resolver sends again after a
// SERVFAIL or a timeout.
type DNSStats struct {
	Requests uint64 `json:"requests"`
	Answered uint64 `json:"answered"`
	NXDomain uint64 `json:"nxdomain"`
	ServFail uint64 `json:"servfail"`
	Dropped  uint64 `json:"dropped"`
}

// dnsKey is what DNS state is kept for: a name and a query type, since a
// lookup asks for A and AAAA at once and the two arrive in either order.
type dnsKey struct {
	name  string
	qtype uint16
}

// String is the key in the API: "api.example.com/AAAA".
func (k dnsKey) String() string { return k.name + "/" + QType(k.qtype) }

// QType names a DNS query type: A, AAAA, or its number.
func QType(t uint16) string {
	switch t {
	case 1:
		return "A"
	case 12:
		return "PTR"
	case 28:
		return "AAAA"
	case 65:
		return "HTTPS"
	}
	return "TYPE" + strconv.Itoa(int(t))
}

type dnsTarget struct {
	position
	stats DNSStats
}

// dnsState holds the DNS rules, by name, and the state of each name and
// query type that got a query.
type dnsState struct {
	rules   map[string]*entry
	targets map[dnsKey]*dnsTarget
}

func newDNSState() dnsState {
	return dnsState{rules: map[string]*entry{}, targets: map[dnsKey]*dnsTarget{}}
}

// dnsName is the form names are compared in: lower case, no trailing dot.
func dnsName(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
}

// SetDNS installs the DNS rule for name, replacing any rule for it.
func (rs *RuleSet) SetDNS(name string, sc rule.Scenario) {
	rs.mu.Lock()
	rs.dns.rules[dnsName(name)] = rs.newEntry(sc)
	rs.mu.Unlock()
}

// DeleteDNS removes a DNS rule; it reports whether there was one.
func (rs *RuleSet) DeleteDNS(name string) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	_, ok := rs.dns.rules[dnsName(name)]
	delete(rs.dns.rules, dnsName(name))
	return ok
}

// HasDNS reports whether name has a DNS rule.
func (rs *RuleSet) HasDNS(name string) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	_, ok := rs.dns.rules[dnsName(name)]
	return ok
}

// dnsRNG returns the random stream of a name and query type, derived from
// a hash that starts with "dns", apart from every other stream.
func (rs *RuleSet) dnsRNG(k dnsKey) *rand.Rand {
	h := fnv.New64a()
	h.Write([]byte("dns"))
	h.Write([]byte(k.name))
	var t [2]byte
	binary.BigEndian.PutUint16(t[:], k.qtype)
	h.Write(t[:])
	return rand.New(rand.NewPCG(rs.seed, h.Sum64()))
}

// DecideDNS decides how to answer a query for name of type qtype, if a
// DNS rule applies to the name: rule.Reply (answer as without the rule,
// after the delay), rule.NXDomain, rule.ServFail or rule.Drop. Each
// query counts as a request of the name and type's own phase.
func (rs *RuleSet) DecideDNS(name string, qtype uint16) (Decision, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	k := dnsKey{dnsName(name), qtype}
	e, ok := rs.dns.rules[k.name]
	if !ok {
		return Decision{}, false
	}
	t := rs.dns.targets[k]
	if t == nil {
		t = &dnsTarget{}
		rs.dns.targets[k] = t
	}
	if t.entryID != e.id {
		t.position = position{entryID: e.id, phaseStart: e.setAt, rng: rs.dnsRNG(k)}
	}
	advance(&t.position, e.sc, rs.now())
	out, _, delay := e.sc.Phases[t.phase].Spec.Decide(t.phaseReqs, t.rng)
	t.phaseReqs++
	t.stats.Requests++
	switch out {
	case rule.Reply:
		t.stats.Answered++
	case rule.NXDomain:
		t.stats.NXDomain++
	case rule.ServFail:
		t.stats.ServFail++
	case rule.Drop:
		t.stats.Dropped++
	}
	return Decision{Outcome: out, Delay: delay, Rule: "dns " + k.name, Phase: t.phase + 1}, true
}

// DNSTargetState is the API view of one name and query type.
type DNSTargetState struct {
	DNSStats
	Rule  string `json:"rule"`
	Phase int    `json:"phase"` // 1-based
}

// dnsSnapshot returns the DNS rules and the state of every name and type
// that got a query. Callers hold rs.mu.
func (rs *RuleSet) dnsSnapshot() ([]map[string]any, map[string]DNSTargetState) {
	names := make([]string, 0, len(rs.dns.rules))
	for n := range rs.dns.rules {
		names = append(names, n)
	}
	sort.Strings(names)
	rules := make([]map[string]any, 0, len(names))
	for _, n := range names {
		rules = append(rules, map[string]any{"target": n, "rule": rule.DNSScenario{Scenario: rs.dns.rules[n].sc}})
	}
	stats := map[string]DNSTargetState{}
	for k, t := range rs.dns.targets {
		st := DNSTargetState{DNSStats: t.stats, Phase: 1}
		if e, ok := rs.dns.rules[k.name]; ok {
			st.Rule = "dns " + k.name
			if t.entryID == e.id {
				st.Phase = t.phase + 1
			}
		}
		stats[k.String()] = st
	}
	return rules, stats
}
