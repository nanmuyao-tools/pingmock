package engine

import (
	"encoding/binary"
	"hash/fnv"
	"math/rand/v2"
	"net/netip"
	"sort"

	"github.com/nanmuyao-tools/pingmock/internal/icmp"
	"github.com/nanmuyao-tools/pingmock/internal/rule"
)

// UDPStats counts the datagrams sent to one address and port.
type UDPStats struct {
	Requests    uint64 `json:"requests"`
	Refused     uint64 `json:"refused"`
	Dropped     uint64 `json:"dropped"`
	Unreachable uint64 `json:"unreachable"`
}

type udpTarget struct {
	position
	stats UDPStats
}

// udpState holds the UDP rules, keyed as TCP rules are, and the state of
// each address and port that got a datagram.
type udpState struct {
	rules   map[tcpRuleKey]*entry
	targets map[netip.AddrPort]*udpTarget
}

func newUDPState() udpState {
	return udpState{rules: map[tcpRuleKey]*entry{}, targets: map[netip.AddrPort]*udpTarget{}}
}

// SetUDP installs the UDP rule for prefix p and port, 0 meaning every
// port, replacing any rule for the same target.
func (rs *RuleSet) SetUDP(p netip.Prefix, port uint16, sc rule.Scenario) {
	rs.mu.Lock()
	rs.udp.rules[tcpRuleKey{p.Masked(), port}] = rs.newEntry(sc)
	rs.mu.Unlock()
}

// DeleteUDP removes a UDP rule; it reports whether there was one.
func (rs *RuleSet) DeleteUDP(p netip.Prefix, port uint16) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	k := tcpRuleKey{p.Masked(), port}
	_, ok := rs.udp.rules[k]
	delete(rs.udp.rules, k)
	return ok
}

// udpRNG returns the random stream of an address and port, derived from a
// hash that starts with "udp", apart from every other stream.
func (rs *RuleSet) udpRNG(ap netip.AddrPort) *rand.Rand {
	h := fnv.New64a()
	h.Write([]byte("udp"))
	b := ap.Addr().As16()
	h.Write(b[:])
	var port [2]byte
	binary.BigEndian.PutUint16(port[:], ap.Port())
	h.Write(port[:])
	return rand.New(rand.NewPCG(rs.seed, h.Sum64()))
}

// DecideUDP decides how to answer the UDP datagram p, if a UDP rule applies
// to its destination address and port: rule.Refused (ICMP port
// unreachable), rule.Unreachable or rule.Drop. Every datagram is a
// request.
func (rs *RuleSet) DecideUDP(p icmp.Probe) (Decision, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	e, key, ok := matchPort(rs.udp.rules, p.Dst, p.DstPort)
	if !ok {
		return Decision{}, false
	}
	ap := netip.AddrPortFrom(p.Dst, p.DstPort)
	t := rs.udp.targets[ap]
	if t == nil {
		t = &udpTarget{}
		rs.udp.targets[ap] = t
	}
	if t.entryID != e.id {
		t.position = position{entryID: e.id, phaseStart: e.setAt, rng: rs.udpRNG(ap)}
	}
	advance(&t.position, e.sc, rs.now())
	sp := e.sc.Phases[t.phase].Spec
	out, code, delay := sp.Decide(t.phaseReqs, t.rng)
	t.phaseReqs++
	t.stats.Requests++
	switch out {
	case rule.Refused:
		t.stats.Refused++
	case rule.Unreachable:
		t.stats.Unreachable++
	default: // a UDP rule sends no answer of its own
		out, delay = rule.Drop, 0
		t.stats.Dropped++
	}
	return Decision{Outcome: out, Code: code, Delay: delay, TTL: sp.EffectiveTTL(),
		Rule: "udp " + key.String(), Phase: t.phase + 1, From: p.Dst}, true
}

// UDPTargetState is the API view of one address and port.
type UDPTargetState struct {
	UDPStats
	Rule  string `json:"rule"`
	Phase int    `json:"phase"` // 1-based
}

// udpSnapshot returns the UDP rules and the state of every address and
// port that got a datagram. Callers hold rs.mu.
func (rs *RuleSet) udpSnapshot() ([]map[string]any, map[string]UDPTargetState) {
	keys := make([]tcpRuleKey, 0, len(rs.udp.rules))
	for k := range rs.udp.rules {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	rules := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		rules = append(rules, map[string]any{"target": k.String(), "rule": rule.UDPScenario{Scenario: rs.udp.rules[k].sc}})
	}
	stats := map[string]UDPTargetState{}
	for ap, t := range rs.udp.targets {
		st := UDPTargetState{UDPStats: t.stats, Phase: 1}
		if e, k, ok := matchPort(rs.udp.rules, ap.Addr(), ap.Port()); ok {
			st.Rule = "udp " + k.String()
			if t.entryID == e.id {
				st.Phase = t.phase + 1
			}
		}
		stats[ap.String()] = st
	}
	return rules, stats
}
