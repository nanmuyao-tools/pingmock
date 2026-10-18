package tun

import (
	"fmt"
	"net/netip"
	"strings"
)

// existingRoutes describes what already routes exactly the prefix p other
// than the device itself, one entry per route, such as "dev vlan20" or
// "type unreachable". Empty means nothing does.
type existingRoutes func(p netip.Prefix) ([]string, error)

// planRoutes returns the prefixes to route through the device so that
// traffic to p reaches it, without replacing a route the host already has
// for exactly the same prefix, such as the route of a network the host is
// on. That would cut the host off from the network, and since replacing a
// route deletes the old one, it would stay cut off after pingmock exits.
//
// The plan is p itself if nothing else routes it. Otherwise it is p's two
// halves, each planned the same way: being more specific, they take the
// traffic while the host's route stays. A single address that has a route
// of its own cannot be split, so it is an error. kept describes the routes
// left alone.
func planRoutes(p netip.Prefix, existing existingRoutes) (plan []netip.Prefix, kept []string, err error) {
	p = p.Masked()
	others, err := existing(p)
	if err != nil {
		return nil, nil, err
	}
	if len(others) == 0 {
		return []netip.Prefix{p}, nil, nil
	}
	if p.Bits() == p.Addr().BitLen() {
		return nil, nil, fmt.Errorf("cannot mock %s: the host already routes that address (%s), and pingmock would have to replace that route; use pingmock exec, which leaves the host's routes alone",
			p.Addr(), strings.Join(others, ", "))
	}
	kept = append(kept, fmt.Sprintf("route for %s (%s)", p, strings.Join(others, ", ")))
	lo, hi := halves(p)
	for _, h := range []netip.Prefix{lo, hi} {
		hp, hk, err := planRoutes(h, existing)
		if err != nil {
			return nil, nil, err
		}
		plan = append(plan, hp...)
		kept = append(kept, hk...)
	}
	return plan, kept, nil
}

// halves splits the masked prefix p, shorter than a single address, into
// its lower and upper halves.
func halves(p netip.Prefix) (lo, hi netip.Prefix) {
	bits := p.Bits() + 1
	lo = netip.PrefixFrom(p.Addr(), bits)
	b := p.Addr().AsSlice()
	b[(bits-1)/8] |= 0x80 >> ((bits - 1) % 8)
	upper, _ := netip.AddrFromSlice(b)
	return lo, netip.PrefixFrom(upper, bits)
}
