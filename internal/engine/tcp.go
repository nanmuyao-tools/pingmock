package engine

import (
	"encoding/binary"
	"hash/fnv"
	"math/rand/v2"
	"net/netip"
	"sort"
	"time"

	"github.com/nanmuyao-tools/pingmock/internal/icmp"
	"github.com/nanmuyao-tools/pingmock/internal/rule"
)

// repeatWindow is how long a connection attempt is remembered, so that
// the SYNs a client's kernel sends again for it get the same answer. It
// covers Linux's default of 6 retries, which give up after about 127s.
const repeatWindow = 2 * time.Minute

// idleWindow is how long a connection accepted under after=close or
// after=reset is remembered after its last segment. A client that waits
// longer before sending gets nothing, as under after=hang.
const idleWindow = 10 * time.Minute

// TCPStats counts what happened to connection attempts to one address
// and port. Repeated SYNs of an attempt are not counted again.
type TCPStats struct {
	Requests    uint64 `json:"requests"`
	Accepted    uint64 `json:"accepted"`
	Refused     uint64 `json:"refused"`
	Dropped     uint64 `json:"dropped"`
	Unreachable uint64 `json:"unreachable"`
	// Accepted connections that pingmock then closed or reset (after=).
	Closed uint64 `json:"closed"`
	Reset  uint64 `json:"reset"`
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

// conn identifies a connection by its addresses and ports.
type conn struct {
	src, dst         netip.Addr
	srcPort, dstPort uint16
}

// Where an accepted connection with after=close or after=reset is.
const (
	connOpen    = iota // accepted, nothing done yet
	connClosing        // pingmock sent its FIN
	connDone           // closed or reset; later segments get the answers of a closed port
)

// connState is an accepted connection whose rule says what to do after
// the handshake. The rule's settings are taken with the SYN, so a phase
// that ends in between does not change them.
type connState struct {
	after rule.After
	stage int
	d     Decision // what the SYN got: rule and phase, for the history
	at    time.Time

	// For rules that exchange data (rule.Spec.Talks).
	talks       bool
	send, reply []byte
	wait        time.Duration
	limit       int
	snd         uint32    // pingmock's next sequence number
	rcv         uint32    // the next byte expected from the client
	got         int       // bytes the client sent so far
	started     bool      // the handshake's last ACK has come
	replied     bool      // reply has been sent
	ended       bool      // after= has closed or reset the connection
	pendingFrom uint32    // the first sequence number of data not yet written,
	pendingTill time.Time // until then, when wait= delays it
}

// tcpState holds the TCP rules and what they have done so far.
type tcpState struct {
	rules   map[tcpRuleKey]*entry
	targets map[netip.AddrPort]*tcpTarget
	seen    map[attempt]answered
	conns   map[conn]*connState
	ignored map[attempt]time.Time // SYNs to ports without a rule, for Repeat
	inserts int                   // since the last pruning of seen, conns and ignored
}

func newTCPState() tcpState {
	return tcpState{
		rules:   map[tcpRuleKey]*entry{},
		targets: map[netip.AddrPort]*tcpTarget{},
		seen:    map[attempt]answered{},
		conns:   map[conn]*connState{},
		ignored: map[attempt]time.Time{},
	}
}

// ignored is the decision for a TCP or UDP probe that no rule or path
// covers: no answer, as before pingmock, but an event in the history, so a
// test sees that its program tried. A SYN sent again is marked Repeat, as
// for ports with a rule. It touches no target's state or random sequence.
func (rs *RuleSet) ignored(p icmp.Probe) Decision {
	d := Decision{Outcome: rule.Ignored, Rule: "none"}
	if p.Kind != icmp.TCP {
		return d
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	now := rs.now()
	at := attempt{p.Src, p.Dst, p.SrcPort, p.DstPort, p.TCPSeq}
	if prev, ok := rs.tcp.ignored[at]; ok && now.Sub(prev) < repeatWindow {
		d.Repeat = true
		return d
	}
	rs.tcp.ignored[at] = now
	if rs.tcp.inserts++; rs.tcp.inserts >= 256 {
		rs.tcp.inserts = 0
		rs.pruneTCP(now)
	}
	return d
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
	return matchPort(rs.tcp.rules, a, port)
}

// matchPort returns the rule of rules for a and port, as matchTCP says.
func matchPort(rules map[tcpRuleKey]*entry, a netip.Addr, port uint16) (*entry, tcpRuleKey, bool) {
	var best *entry
	var bestKey tcpRuleKey
	for k, e := range rules {
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

	c := conn{p.Src, p.Dst, p.SrcPort, p.DstPort}
	if out == rule.SynAck && (sp.After != rule.AfterHang || sp.Talks()) {
		rs.tcp.conns[c] = &connState{after: sp.After, d: d, at: now, talks: sp.Talks(),
			send: sp.Send, reply: sp.Reply, wait: sp.Wait, limit: sp.Limit,
			snd: icmp.SynAckSeq + 1, rcv: p.TCPSeq + 1}
	} else {
		delete(rs.tcp.conns, c) // a new attempt from the same port
	}
	rs.tcp.seen[at] = answered{d, now}
	if rs.tcp.inserts++; rs.tcp.inserts >= 256 {
		rs.tcp.inserts = 0
		rs.pruneTCP(now)
	}
	return d, true
}

// pruneTCP forgets attempts too old to be sent again and idle connections;
// the caller holds rs.mu.
func (rs *RuleSet) pruneTCP(now time.Time) {
	for a, s := range rs.tcp.seen {
		if now.Sub(s.at) >= repeatWindow {
			delete(rs.tcp.seen, a)
		}
	}
	for a, at := range rs.tcp.ignored {
		if now.Sub(at) >= repeatWindow {
			delete(rs.tcp.ignored, a)
		}
	}
	for c, cs := range rs.tcp.conns {
		if now.Sub(cs.at) >= idleWindow {
			delete(rs.tcp.conns, c)
		}
	}
}

// TCPSegment is a segment pingmock sends on a connection, with data or
// without.
type TCPSegment struct {
	Flags    byte
	Seq, Ack uint32
	Data     []byte
}

// SegmentAnswer is what to send in answer to a segment of an accepted
// connection.
type SegmentAnswer struct {
	// Now are acknowledgments to write at once.
	Now []TCPSegment
	// Ordered are segments that use up sequence numbers (data, a FIN) or
	// must match them (a reset): they go out after Delay, and after
	// anything still waiting to go out on the connection, in order.
	Ordered []TCPSegment
	Delay   time.Duration
	TTL     uint8 // the TTL of the rule that accepted the connection
	// Action is set when the answer closes or resets the connection, once
	// per connection: Outcome is rule.Closed or rule.Reset, with the rule
	// and phase the SYN got. The answers that follow, such as the ACK of
	// the client's FIN, leave it nil and are not recorded.
	Action *Decision
	// Sent is how many bytes of send= or reply= Ordered carries, and
	// Accepted what the SYN got, for the history.
	Sent     int
	Accepted Decision
}

// DecideSegment decides how to answer a segment p, other than a SYN, of a
// connection accepted under after=close or after=reset, or under a rule
// that exchanges data. It reports false for every other segment, which
// gets no answer, as under after=hang without data: pingmock
// acknowledges nothing and sends nothing.
//
// Without data, after=close sends a FIN as soon as the client completes
// the handshake. after=reset waits for the client's first data or FIN: a
// reset on the handshake's final ACK would race with the client's
// connect, which then fails in some runs and succeeds in others. Data
// after pingmock's FIN gets a reset, as from a real server that closed
// its socket.
func (rs *RuleSet) DecideSegment(p icmp.Probe) (SegmentAnswer, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	cs := rs.tcp.conns[conn{p.Src, p.Dst, p.SrcPort, p.DstPort}]
	if cs == nil {
		return SegmentAnswer{}, false
	}
	if p.TCPFlags&icmp.FlagACK == 0 || p.TCPAck-(icmp.SynAckSeq+1) > cs.snd+1-(icmp.SynAckSeq+1) {
		return SegmentAnswer{}, false // not acknowledging what pingmock sent: another connection
	}
	now := rs.now()
	cs.at = now
	a := SegmentAnswer{TTL: cs.d.TTL, Accepted: cs.d}
	action := func(out rule.Outcome) *Decision {
		t := rs.tcp.targets[netip.AddrPortFrom(p.Dst, p.DstPort)]
		if t != nil && out == rule.Closed {
			t.stats.Closed++
		} else if t != nil {
			t.stats.Reset++
		}
		d := cs.d
		d.Outcome, d.Delay, d.Repeat = out, 0, false
		return &d
	}
	if cs.talks {
		rs.talk(cs, p, now, &a, action)
		return a, len(a.Now)+len(a.Ordered) > 0
	}

	const next = icmp.SynAckSeq + 1 // our sequence number after the SYN-ACK
	fin := p.TCPFlags&icmp.FlagFIN != 0
	end := p.TCPSeq + uint32(p.TCPData) // what the client sent up to
	if fin {
		end++
	}
	reset := TCPSegment{Flags: icmp.FlagRST | icmp.FlagACK, Seq: p.TCPAck, Ack: end}
	switch {
	case cs.stage == connOpen && cs.after == rule.AfterClose:
		cs.stage = connClosing
		cs.snd = next + 1
		a.Ordered, a.Action = []TCPSegment{{Flags: icmp.FlagFIN | icmp.FlagACK, Seq: next, Ack: end}}, action(rule.Closed)
	case cs.stage == connOpen && (p.TCPData > 0 || fin):
		cs.stage = connDone
		a.Ordered, a.Action = []TCPSegment{reset}, action(rule.Reset)
	case cs.stage == connOpen:
		return SegmentAnswer{}, false // the handshake's ACK: wait for data
	case p.TCPData > 0:
		cs.stage = connDone
		a.Ordered = []TCPSegment{reset}
	case fin && cs.after == rule.AfterClose:
		cs.stage = connDone
		a.Ordered = []TCPSegment{{Flags: icmp.FlagACK, Seq: next + 1, Ack: end}}
	default:
		return SegmentAnswer{}, false // the ACK of our FIN
	}
	return a, true
}

// talk answers a segment of a connection whose rule exchanges data:
// pingmock acknowledges what the client sends, sends send= once the
// handshake completes and reply= on the client's first data, after wait=,
// and ends the connection as after= says, at limit= bytes if there is a
// limit, once it has sent all it has to send otherwise. Callers hold
// rs.mu.
func (rs *RuleSet) talk(cs *connState, p icmp.Probe, now time.Time, a *SegmentAnswer, action func(rule.Outcome) *Decision) {
	// The sequence number of a bare acknowledgment: the start of data
	// still waiting to go out, which is what the client expects next.
	ackSeq := func() uint32 {
		if now.Before(cs.pendingTill) {
			return cs.pendingFrom
		}
		return cs.snd
	}
	ack := func() { a.Now = append(a.Now, TCPSegment{Flags: icmp.FlagACK, Seq: ackSeq(), Ack: cs.rcv}) }
	data := func(b []byte) {
		if cs.wait > 0 {
			ack() // at once, so that the client does not send it again while pingmock waits
			cs.pendingFrom, cs.pendingTill = cs.snd, now.Add(cs.wait)
			a.Delay = cs.wait
		}
		for len(b) > 0 {
			n := min(len(b), icmp.MaxSegment)
			flags := byte(icmp.FlagACK)
			if n == len(b) {
				flags |= icmp.FlagPSH
			}
			a.Ordered = append(a.Ordered, TCPSegment{Flags: flags, Seq: cs.snd, Ack: cs.rcv, Data: b[:n]})
			cs.snd += uint32(n)
			a.Sent += n
			b = b[n:]
		}
	}
	end := func(out rule.Outcome) {
		cs.ended = true
		if out == rule.Closed {
			a.Ordered = append(a.Ordered, TCPSegment{Flags: icmp.FlagFIN | icmp.FlagACK, Seq: cs.snd, Ack: cs.rcv})
			cs.snd++
			cs.stage = connClosing
		} else {
			a.Ordered = append(a.Ordered, TCPSegment{Flags: icmp.FlagRST | icmp.FlagACK, Seq: cs.snd, Ack: cs.rcv})
			cs.stage = connDone
		}
		a.Action = action(out)
	}
	fin := p.TCPFlags&icmp.FlagFIN != 0
	// Data the client sends again, up to what was acknowledged, as it does
	// when an acknowledgment is late: acknowledged again, whatever came since.
	old := p.TCPData > 0 && cs.rcv-(p.TCPSeq+uint32(p.TCPData)) < 1<<31
	switch cs.stage {
	case connDone: // reset, or both sides closed: a closed port
		if old && cs.after == rule.AfterClose {
			ack()
		} else if p.TCPData > 0 {
			a.Ordered = append(a.Ordered, TCPSegment{Flags: icmp.FlagRST | icmp.FlagACK, Seq: p.TCPAck, Ack: p.TCPSeq + uint32(p.TCPData)})
		} else if fin && cs.after == rule.AfterClose {
			ack() // the client's FIN again: our ACK was lost
		}
		return
	case connClosing: // pingmock sent its FIN
		switch {
		case old:
			ack()
		case p.TCPData > 0:
			cs.stage = connDone
			a.Ordered = append(a.Ordered, TCPSegment{Flags: icmp.FlagRST | icmp.FlagACK, Seq: p.TCPAck, Ack: p.TCPSeq + uint32(p.TCPData)})
		case fin:
			cs.rcv = p.TCPSeq + 1
			cs.stage = connDone
			ack()
		}
		return
	}

	// The handshake completes with the client's first segment after the
	// SYN, which may already carry data.
	if !cs.started {
		cs.started = true
		if len(cs.send) > 0 {
			data(cs.send)
		}
		if cs.after == rule.AfterClose && len(cs.reply) == 0 && cs.limit == 0 {
			end(rule.Closed)
			return
		}
	}
	if p.TCPData > 0 {
		if p.TCPSeq != cs.rcv { // sent again, or ahead of what came: say what is expected
			ack()
			return
		}
		n := p.TCPData
		if cs.limit > 0 && cs.got+n >= cs.limit {
			cs.rcv += uint32(cs.limit - cs.got)
			cs.got = cs.limit
			if cs.after == rule.AfterClose {
				end(rule.Closed)
			} else {
				end(rule.Reset)
			}
			return
		}
		cs.rcv += uint32(n)
		cs.got += n
		sentAll := len(cs.reply) == 0 || cs.replied
		switch {
		case len(cs.reply) > 0 && !cs.replied:
			cs.replied = true
			data(cs.reply)
			if cs.after == rule.AfterClose && cs.limit == 0 {
				end(rule.Closed)
			}
		case sentAll && cs.limit == 0 && cs.after == rule.AfterReset:
			end(rule.Reset)
		default:
			ack()
		}
	}
	if fin && cs.stage == connClosing { // pingmock's FIN just went out: acknowledge the client's after it
		cs.rcv++
		cs.stage = connDone
		a.Ordered = append(a.Ordered, TCPSegment{Flags: icmp.FlagACK, Seq: cs.snd, Ack: cs.rcv})
		return
	}
	if fin && !cs.ended {
		cs.rcv++
		switch {
		case cs.after == rule.AfterReset:
			end(rule.Reset)
		case cs.after == rule.AfterClose:
			end(rule.Closed)
			cs.stage = connDone // both sides have closed
		default:
			ack() // a half close: pingmock's side stays open, as under hang
		}
	}
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
