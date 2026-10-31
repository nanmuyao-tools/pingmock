package tun

import (
	"fmt"
	"net/netip"
	"sort"
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
// route deletes the old one, it would stay cut off after ghostwire exits.
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
		return nil, nil, fmt.Errorf("cannot mock %s: the host already routes that address (%s), and ghostwire would have to replace that route; use ghostwire exec, which leaves the host's routes alone",
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

// unneeded returns, of the targets routed (each with the routes planRoutes
// made for it), those not in want, and the routes to remove for them: all
// of theirs but those a wanted target also uses, as the same half of a
// split network can be. Both are sorted.
func unneeded(routed map[netip.Prefix][]netip.Prefix, want []netip.Prefix) (targets, routes []netip.Prefix) {
	keep := map[netip.Prefix]bool{}
	for _, p := range want {
		keep[p.Masked()] = true
	}
	inUse := map[netip.Prefix]bool{}
	for p, plan := range routed {
		if keep[p] {
			for _, q := range plan {
				inUse[q] = true
			}
		}
	}
	for p, plan := range routed {
		if keep[p] {
			continue
		}
		targets = append(targets, p)
		for _, q := range plan {
			if !inUse[q] {
				routes = append(routes, q)
				inUse[q] = true // removed once
			}
		}
	}
	less := func(s []netip.Prefix) func(i, j int) bool {
		return func(i, j int) bool { return s[i].String() < s[j].String() }
	}
	sort.Slice(targets, less(targets))
	sort.Slice(routes, less(routes))
	return targets, routes
}
