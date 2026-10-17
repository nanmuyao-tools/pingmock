package rule

import (
	"fmt"
	"net/netip"
	"strings"
)

// ParsePrefix accepts an IPv4 or IPv6 address or CIDR prefix. A single
// address becomes a /32 or /128 prefix. IPv4-mapped IPv6 addresses
// (::ffff:10.0.0.1) are treated as the IPv4 addresses they contain.
func ParsePrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		if p.Addr().Is4In6() {
			if p.Bits() < 96 {
				return netip.Prefix{}, fmt.Errorf("%s: an IPv4-mapped prefix must be at least /96", s)
			}
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		if p.Addr().Zone() != "" {
			return netip.Prefix{}, fmt.Errorf("%s: zones are not supported", s)
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	if a.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("%s: zones are not supported", s)
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}
