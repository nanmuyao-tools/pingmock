package rule

import (
	"net/netip"
	"strings"
)

// ParsePrefix accepts a single IPv4 address or a CIDR prefix.
func ParsePrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		if !p.Addr().Is4() {
			return netip.Prefix{}, errIPv4Only(s)
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	if !a.Is4() {
		return netip.Prefix{}, errIPv4Only(s)
	}
	return netip.PrefixFrom(a, 32), nil
}

type errIPv4Only string

func (e errIPv4Only) Error() string { return string(e) + ": only IPv4 is supported" }
