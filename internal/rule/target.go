package rule

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Target is what a rule applies to: an address or prefix, or a name that
// ghostwire exec resolves to a mocked address.
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

// TCPTarget is what a TCP rule applies to: an address or prefix, or under
// ghostwire exec a name, and a port, or every port.
type TCPTarget struct {
	Prefix netip.Prefix // valid unless Name is set
	Name   string       // canonical: lower case, no trailing dot
	Port   uint16       // 0 means every port
}

// ParseTCPTarget accepts ADDR[:PORT], PREFIX[:PORT] or NAME[:PORT]. IPv6
// addresses and prefixes are written in brackets, with or without a port:
// [fd99::1]:443, [fd99::/64]:443, [fd99::1]. Without the brackets
// fd99::1:443 would itself be an address, so IPv6 without them is refused.
func ParseTCPTarget(s string) (TCPTarget, error) {
	s = strings.TrimSpace(s)
	host, portText := s, ""
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return TCPTarget{}, fmt.Errorf("%q: missing ]", s)
		}
		host, portText = s[1:end], s[end+1:]
		if portText != "" {
			if portText[0] != ':' {
				return TCPTarget{}, fmt.Errorf("%q: want [ADDR] or [ADDR]:PORT", s)
			}
			portText = portText[1:]
			if portText == "" {
				return TCPTarget{}, fmt.Errorf("%q: missing port after :", s)
			}
		}
		p, err := ParsePrefix(host)
		if err != nil {
			return TCPTarget{}, err
		}
		port, err := parsePort(portText)
		if err != nil {
			return TCPTarget{}, fmt.Errorf("%q: %w", s, err)
		}
		return TCPTarget{Prefix: p, Port: port}, nil
	}
	switch strings.Count(s, ":") {
	case 0:
	case 1:
		var ok bool
		host, portText, ok = strings.Cut(s, ":")
		if !ok || portText == "" {
			return TCPTarget{}, fmt.Errorf("%q: missing port after :", s)
		}
	default:
		return TCPTarget{}, fmt.Errorf("%q: write an IPv6 target in brackets, such as [fd99::1]:443, or [fd99::1] for every port", s)
	}
	port, err := parsePort(portText)
	if err != nil {
		return TCPTarget{}, fmt.Errorf("%q: %w", s, err)
	}
	t, err := ParseTarget(host)
	if err != nil {
		return TCPTarget{}, err
	}
	return TCPTarget{Prefix: t.Prefix, Name: t.Name, Port: port}, nil
}

// parsePort accepts a port from 1 to 65535, or "" for every port.
func parsePort(s string) (uint16, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("invalid port %q: want a number from 1 to 65535", s)
	}
	return uint16(n), nil
}

// String writes the target in the form ParseTCPTarget accepts, with the
// prefix length, as ping rules are shown: 10.99.0.1/32:443, [fd99::/64]:443.
func (t TCPTarget) String() string {
	host := t.Name
	if host == "" {
		host = t.Prefix.String()
		if t.Prefix.Addr().Is6() {
			host = "[" + host + "]"
		}
	}
	if t.Port == 0 {
		return host
	}
	return host + ":" + strconv.Itoa(int(t.Port))
}
