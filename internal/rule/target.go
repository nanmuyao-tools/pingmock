package rule

import (
	"fmt"
	"net/netip"
	"strings"
)

// Target is what a rule applies to: an address or prefix, or a name that
// pingmock exec resolves to a mocked address.
type Target struct {
	Prefix netip.Prefix // valid unless Name is set
	Name   string       // canonical: lower case, no trailing dot
}

func (t Target) String() string {
	if t.Name != "" {
		return t.Name
	}
	return t.Prefix.String()
}

// ParseTarget accepts what ParsePrefix accepts, or a host name.
func ParseTarget(s string) (Target, error) {
	s = strings.TrimSpace(s)
	if p, err := ParsePrefix(s); err == nil {
		return Target{Prefix: p}, nil
	} else if strings.ContainsAny(s, "/:") || strings.Trim(s, "0123456789.") == "" {
		return Target{}, err // meant as an address or prefix
	}
	if err := ValidName(s); err != nil {
		return Target{}, err
	}
	return Target{Name: strings.ToLower(strings.TrimSuffix(s, "."))}, nil
}

// ValidName checks a host name: labels of letters, digits and hyphens,
// joined by dots, as in RFC 1123, with underscores allowed as some
// internal names have them. A trailing dot is accepted.
func ValidName(s string) error {
	name := strings.TrimSuffix(s, ".")
	if name == "" || len(name) > 253 {
		return fmt.Errorf("invalid host name %q", s)
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid host name %q", s)
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return fmt.Errorf("invalid host name %q", s)
			}
		}
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return fmt.Errorf("%q is an address, not a host name", s)
	}
	return nil
}

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
