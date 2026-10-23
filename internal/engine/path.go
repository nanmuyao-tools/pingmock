package engine

import (
	"net/netip"

	"github.com/nanmuyao/pingmock/internal/icmp"
	"github.com/nanmuyao/pingmock/internal/rule"
)

// SetPath gives target a route: traceroute and mtr then see its hops,
// and UDP and TCP probes to it get answered.
func (rs *RuleSet) SetPath(target netip.Addr, p rule.Path) {
	rs.mu.Lock()
	rs.paths[target] = p
	rs.mu.Unlock()
}

// DeletePath removes target's route; it reports whether there was one.
func (rs *RuleSet) DeletePath(target netip.Addr) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	_, ok := rs.paths[target]
	delete(rs.paths, target)
	return ok
}

// Path returns target's route, if it has one.
func (rs *RuleSet) Path(target netip.Addr) (rule.Path, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	p, ok := rs.paths[target]
	return p, ok
}

// HopAddrs returns the addresses of every hop of every path, for routing.
func (rs *RuleSet) HopAddrs() []netip.Addr {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	var out []netip.Addr
	for _, p := range rs.paths {
		out = append(out, p.Addrs()...)
	}
	return out
}

// DecideProbe determines the outcome of the probe p: for a target with a
// path, which hop answers (TimeExceeded from it), whether a lossy hop on
// the way loses the probe, and, once the probe reaches the destination,
// how it answers an echo request (its rule, as always), a UDP probe
// (PortUnreachable) or a TCP SYN (SynAck). A target without a path
// ignores the TTL and answers echo requests only. A TCP SYN that reaches
// its destination is answered by the TCP rule for its address and port
// if there is one, with or without a path.
func (rs *RuleSet) DecideProbe(p icmp.Probe) Decision {
	dst, ttl, kind := p.Dst, p.TTL, p.Kind
	rs.mu.Lock()
	path, hasPath := rs.paths[dst]
	rs.mu.Unlock()
	if !hasPath {
		if kind == icmp.TCP {
			if d, ok := rs.DecideTCP(p); ok {
				return d
			}
		}
		if kind == icmp.UDP {
			if d, ok := rs.DecideUDP(p); ok {
				return d
			}
		}
		if kind != icmp.Echo {
			return Decision{Outcome: rule.Drop, Rule: "none"}
		}
		d := rs.Decide(dst)
		d.From = dst
		return d
	}

	rs.mu.Lock()
	t := rs.targets[dst]
	if t == nil {
		t = &target{}
		rs.targets[dst] = t
	}
	if t.rng == nil {
		t.rng = rs.rngFor(dst)
	}
	// On the way: every hop before the one that answers may lose the
	// probe. The last hop's cut applies to probes going past it too.
	last := len(path) // hops the probe passes through before being answered
	if int(ttl) <= len(path) {
		last = int(ttl) - 1
	}
	for i := 0; i < last; i++ {
		if path[i].Cuts(t.rng) {
			t.stats.Requests++
			t.stats.Dropped++
			rs.mu.Unlock()
			return Decision{Outcome: rule.Drop, Rule: "path", Hop: i + 1, From: path[i].Addr}
		}
	}
	if int(ttl) <= len(path) {
		hop := path[ttl-1]
		answered, delay := hop.DecideHop(t.rng)
		t.stats.Requests++
		d := Decision{Rule: "path", Hop: int(ttl), From: hop.Addr, Delay: delay, TTL: hop.EffectiveTTL()}
		if answered {
			t.stats.Hops++
			d.Outcome = rule.TimeExceeded
		} else {
			t.stats.Dropped++
			d.Outcome = rule.Drop
		}
		rs.mu.Unlock()
		return d
	}
	rs.mu.Unlock()

	// The destination: a TCP or UDP rule for the port decides; otherwise
	// the target's rule decides, as for any target, and a UDP or TCP probe
	// gets the answer that means "reached" instead of an echo reply.
	if kind == icmp.TCP {
		if d, ok := rs.DecideTCP(p); ok {
			return d
		}
	}
	if kind == icmp.UDP {
		if d, ok := rs.DecideUDP(p); ok {
			return d
		}
	}
	d := rs.Decide(dst)
	d.From = dst
	if d.Outcome == rule.Reply {
		switch kind {
		case icmp.UDP:
			d.Outcome = rule.PortUnreachable
		case icmp.TCP:
			d.Outcome = rule.SynAck
		}
	}
	return d
}
