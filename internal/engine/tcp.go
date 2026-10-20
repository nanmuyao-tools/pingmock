package engine

import (
	"encoding/binary"
	"hash/fnv"
	"math/rand/v2"
	"net/netip"
	"sort"
	"time"

	"github.com/nanmuyao/pingmock/internal/icmp"
	"github.com/nanmuyao/pingmock/internal/rule"
)

// repeatWindow is how long a connection attempt is remembered, so that
// the SYNs a client's kernel sends again for it get the same answer. It
// covers Linux's default of 6 retries, which give up after about 127s.
const repeatWindow = 2 * time.Minute

// TCPStats counts what happened to connection attempts to one address
// and port. Repeated SYNs of an attempt are not counted again.
type TCPStats struct {
	Requests    uint64 `json:"requests"`
	Accepted    uint64 `json:"accepted"`
	Refused     uint64 `json:"refused"`
	Dropped     uint64 `json:"dropped"`
	Unreachable uint64 `json:"unreachable"`
}

// tcpRuleKey is what a TCP rule applies to: a prefix, and a port or 0 for
// every port.
type tcpRuleKey struct {
	prefix netip.Prefix
	port   uint16
}

func (k tcpRuleKey) String() string {
	return rule.TCPTarget{Prefix: k.prefix, Port: k.port}.String()
}

// tcpTarget is the state of one address and port.
type tcpTarget struct {
	position
	stats TCPStats
}

// attempt identifies a connection attempt: the SYN's addresses, ports and
// initial sequence number, which a retransmitted SYN repeats.
type attempt struct {
	src, dst         netip.Addr
	srcPort, dstPort uint16
	isn              uint32
}

type answered struct {
	d  Decision
	at time.Time
}

// tcpState holds the TCP rules and what they have done so far.
type tcpState struct {
	rules   map[tcpRuleKey]*entry
	targets map[netip.AddrPort]*tcpTarget
	seen    map[attempt]answered
	inserts int // since the last pruning of seen
}

func newTCPState() tcpState {
	return tcpState{
		rules:   map[tcpRuleKey]*entry{},
		targets: map[netip.AddrPort]*tcpTarget{},
		seen:    map[attempt]answered{},
	}
}

// SetTCP installs the TCP rule for prefix p and port, 0 meaning every
// port, replacing any rule for the same target.
func (rs *RuleSet) SetTCP(p netip.Prefix, port uint16, sc rule.Scenario) {
	rs.mu.Lock()
	rs.tcp.rules[tcpRuleKey{p.Masked(), port}] = rs.newEntry(sc)
	rs.mu.Unlock()
}

// DeleteTCP removes a TCP rule; it reports whether there was one.
func (rs *RuleSet) DeleteTCP(p netip.Prefix, port uint16) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	k := tcpRuleKey{p.Masked(), port}
	_, ok := rs.tcp.rules[k]
	delete(rs.tcp.rules, k)
	return ok
}

// matchTCP returns the TCP rule for a and port: the most specific prefix
// wins, then a rule for the port over one for every port. Callers hold
// rs.mu.
func (rs *RuleSet) matchTCP(a netip.Addr, port uint16) (*entry, tcpRuleKey, bool) {
	var best *entry
	var bestKey tcpRuleKey
	for k, e := range rs.tcp.rules {
		if !k.prefix.Contains(a) || (k.port != 0 && k.port != port) {
			continue
		}
		if best == nil || k.prefix.Bits() > bestKey.prefix.Bits() ||
			(k.prefix.Bits() == bestKey.prefix.Bits() && k.port != 0 && bestKey.port == 0) {
			best, bestKey = e, k
		}
	}
	return best, bestKey, best != nil
}

// tcpRNG returns the random stream of an address and port. It is derived
// from a hash that starts with "tcp", so it never coincides with the
// stream of pings to the same address.
func (rs *RuleSet) tcpRNG(ap netip.AddrPort) *rand.Rand {
	h := fnv.New64a()
	h.Write([]byte("tcp"))
	b := ap.Addr().As16()
	h.Write(b[:])
	var port [2]byte
	binary.BigEndian.PutUint16(port[:], ap.Port())
	h.Write(port[:])
	return rand.New(rand.NewPCG(rs.seed, h.Sum64()))
}

// DecideTCP decides how to answer the TCP SYN p, if a TCP rule applies
// to its destination address and port. A SYN the sender's kernel sends
// again for the same attempt gets the answer the first one got, without
// counting as a new request, so that "timeout for 2 requests" means two
// connection attempts, whatever the kernel's retry timer.
func (rs *RuleSet) DecideTCP(p icmp.Probe) (Decision, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	e, key, ok := rs.matchTCP(p.Dst, p.DstPort)
	if !ok {
		return Decision{}, false
	}
	now := rs.now()
	at := attempt{p.Src, p.Dst, p.SrcPort, p.DstPort, p.TCPSeq}
	if prev, ok := rs.tcp.seen[at]; ok && now.Sub(prev.at) < repeatWindow {
		d := prev.d
		d.Repeat = true
		return d, true
	}

	ap := netip.AddrPortFrom(p.Dst, p.DstPort)
	t := rs.tcp.targets[ap]
	if t == nil {
		t = &tcpTarget{}
		rs.tcp.targets[ap] = t
	}
	if t.entryID != e.id {
		t.position = position{entryID: e.id, phaseStart: e.setAt, rng: rs.tcpRNG(ap)}
	}
	advance(&t.position, e.sc, now)
	sp := e.sc.Phases[t.phase].Spec
	out, code, delay := sp.Decide(t.phaseReqs, t.rng)
	t.phaseReqs++
	t.stats.Requests++
	switch out {
	case rule.Reply:
		out = rule.SynAck
		t.stats.Accepted++
	case rule.Refused:
		t.stats.Refused++
	case rule.Drop:
		t.stats.Dropped++
	case rule.Unreachable:
		t.stats.Unreachable++
	}
	d := Decision{Outcome: out, Code: code, Delay: delay, TTL: sp.EffectiveTTL(),
		Rule: "tcp " + key.String(), Phase: t.phase + 1, From: p.Dst}

	rs.tcp.seen[at] = answered{d, now}
	if rs.tcp.inserts++; rs.tcp.inserts >= 256 {
		rs.tcp.inserts = 0
		for a, s := range rs.tcp.seen {
			if now.Sub(s.at) >= repeatWindow {
				delete(rs.tcp.seen, a)
			}
		}
	}
	return d, true
}

// TCPTargetState is the API view of one address and port.
type TCPTargetState struct {
	TCPStats
	Rule  string `json:"rule"`
	Phase int    `json:"phase"` // 1-based
}

// tcpSnapshot returns the TCP rules and the state of every address and
// port that got a connection attempt, keyed as "10.99.0.1:443" or
// "[fd99::1]:443". Callers hold rs.mu.
func (rs *RuleSet) tcpSnapshot() ([]map[string]any, map[string]TCPTargetState) {
	keys := make([]tcpRuleKey, 0, len(rs.tcp.rules))
	for k := range rs.tcp.rules {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	rules := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		rules = append(rules, map[string]any{"target": k.String(), "rule": rule.TCPScenario{Scenario: rs.tcp.rules[k].sc}})
	}
	stats := map[string]TCPTargetState{}
	for ap, t := range rs.tcp.targets {
		st := TCPTargetState{TCPStats: t.stats, Phase: 1}
		if e, k, ok := rs.matchTCP(ap.Addr(), ap.Port()); ok {
			st.Rule = "tcp " + k.String()
			if t.entryID == e.id {
				st.Phase = t.phase + 1
			}
		}
		stats[ap.String()] = st
	}
	return rules, stats
}
