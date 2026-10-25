package tun

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
)

// routesOf is a host's routing table: each prefix and what routes it.
func routesOf(table map[string]string) existingRoutes {
	return func(p netip.Prefix) ([]string, error) {
		if dev, ok := table[p.String()]; ok {
			return []string{dev}, nil
		}
		return nil, nil
	}
}

func TestPlanRoutes(t *testing.T) {
	cases := []struct {
		name   string
		target string
		table  map[string]string
		plan   string
		kept   string
	}{
		{"nothing routes it", "192.168.20.0/24", nil, "192.168.20.0/24", ""},
		{"a single address", "8.8.8.8/32", map[string]string{"0.0.0.0/0": "dev eth0"}, "8.8.8.8/32", ""},
		{"a network the host is on", "192.168.20.0/24", map[string]string{"192.168.20.0/24": "dev vlan20"},
			"192.168.20.0/25 192.168.20.128/25", "route for 192.168.20.0/24 (dev vlan20)"},
		{"a route of a half too", "192.168.20.0/24", map[string]string{"192.168.20.0/24": "dev vlan20", "192.168.20.0/25": "dev vlan21"},
			"192.168.20.0/26 192.168.20.64/26 192.168.20.128/25", "route for 192.168.20.0/24 (dev vlan20); route for 192.168.20.0/25 (dev vlan21)"},
		{"a route without a device", "172.31.0.0/16", map[string]string{"172.31.0.0/16": "type unreachable"},
			"172.31.0.0/17 172.31.128.0/17", "route for 172.31.0.0/16 (type unreachable)"},
		{"the default route", "0.0.0.0/0", map[string]string{"0.0.0.0/0": "dev eth0"},
			"0.0.0.0/1 128.0.0.0/1", "route for 0.0.0.0/0 (dev eth0)"},
		{"not masked", "192.168.20.77/24", map[string]string{"192.168.20.0/24": "dev vlan20"},
			"192.168.20.0/25 192.168.20.128/25", "route for 192.168.20.0/24 (dev vlan20)"},
		{"IPv6", "fd20::/64", map[string]string{"fd20::/64": "dev vlan20"},
			"fd20::/65 fd20::8000:0:0:0/65", "route for fd20::/64 (dev vlan20)"},
		{"IPv6, a /127", "fd20::/127", map[string]string{"fd20::/127": "dev vlan20"},
			"fd20::/128 fd20::1/128", "route for fd20::/127 (dev vlan20)"},
	}
	for _, c := range cases {
		plan, kept, err := planRoutes(netip.MustParsePrefix(c.target), routesOf(c.table))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := fmt.Sprint(plan); got != "["+c.plan+"]" {
			t.Errorf("%s: plan %s, want [%s]", c.name, got, c.plan)
		}
		if got := strings.Join(kept, "; "); got != c.kept {
			t.Errorf("%s: kept %q, want %q", c.name, got, c.kept)
		}
	}
}

func TestPlanRoutesAddressRoutedAlready(t *testing.T) {
	for _, c := range []struct{ target, route string }{
		{"192.168.20.99/32", "192.168.20.99/32"},
		{"192.168.20.98/31", "192.168.20.98/31"}, // splits, then each half...
		{"fd20::5/128", "fd20::5/128"},
	} {
		table := map[string]string{c.route: "dev vlan20"}
		if c.target == "192.168.20.98/31" {
			table["192.168.20.99/32"] = "dev vlan21" // ...one of which is routed too
		}
		_, _, err := planRoutes(netip.MustParsePrefix(c.target), routesOf(table))
		if err == nil || !strings.Contains(err.Error(), "use pingmock exec") {
			t.Errorf("%s: error %v, want one that suggests pingmock exec", c.target, err)
		}
	}
}

func TestPlanRoutesLookupError(t *testing.T) {
	boom := errors.New("ip failed")
	_, _, err := planRoutes(netip.MustParsePrefix("10.0.0.0/8"), func(netip.Prefix) ([]string, error) { return nil, boom })
	if !errors.Is(err, boom) {
		t.Errorf("error %v, want %v", err, boom)
	}
}

func TestHalves(t *testing.T) {
	for in, want := range map[string]string{
		"10.0.0.0/8":    "10.0.0.0/9 10.128.0.0/9",
		"10.0.0.0/31":   "10.0.0.0/32 10.0.0.1/32",
		"0.0.0.0/0":     "0.0.0.0/1 128.0.0.0/1",
		"fd00::/8":      "fd00::/9 fd80::/9",
		"2001:db8::/32": "2001:db8::/33 2001:db8:8000::/33",
	} {
		lo, hi := halves(netip.MustParsePrefix(in))
		if got := lo.String() + " " + hi.String(); got != want {
			t.Errorf("halves(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestUnneeded(t *testing.T) {
	pfx := func(ss ...string) []netip.Prefix {
		var out []netip.Prefix
		for _, s := range ss {
			out = append(out, netip.MustParsePrefix(s))
		}
		return out
	}
	join := func(ps []netip.Prefix) string {
		var s []string
		for _, p := range ps {
			s = append(s, p.String())
		}
		return strings.Join(s, " ")
	}
	routed := map[netip.Prefix][]netip.Prefix{
		netip.MustParsePrefix("8.8.8.8/32"): pfx("8.8.8.8/32"),
		netip.MustParsePrefix("1.1.1.1/32"): pfx("1.1.1.1/32"),
		// A network the host is on, split in halves, and a rule for one
		// of the halves, which shares its route.
		netip.MustParsePrefix("192.168.20.0/24"): pfx("192.168.20.0/25", "192.168.20.128/25"),
		netip.MustParsePrefix("192.168.20.0/25"): pfx("192.168.20.0/25"),
	}
	cases := []struct{ want, targets, routes string }{
		{"8.8.8.8/32 1.1.1.1/32 192.168.20.0/24 192.168.20.0/25", "", ""},
		{"8.8.8.8/32 192.168.20.0/24 192.168.20.0/25", "1.1.1.1/32", "1.1.1.1/32"},
		// The /24 goes, but the half the /25 still needs stays.
		{"8.8.8.8/32 1.1.1.1/32 192.168.20.0/25", "192.168.20.0/24", "192.168.20.128/25"},
		{"192.168.20.0/24", "1.1.1.1/32 192.168.20.0/25 8.8.8.8/32", "1.1.1.1/32 8.8.8.8/32"},
		{"", "1.1.1.1/32 192.168.20.0/24 192.168.20.0/25 8.8.8.8/32", "1.1.1.1/32 192.168.20.0/25 192.168.20.128/25 8.8.8.8/32"},
		// Unmasked prefixes are compared masked.
		{"8.8.8.8/32 1.1.1.1/32 192.168.20.7/24 192.168.20.0/25", "", ""},
	}
	for _, c := range cases {
		want := pfx(strings.Fields(c.want)...)
		targets, routes := unneeded(routed, want)
		if join(targets) != c.targets || join(routes) != c.routes {
			t.Errorf("want %s: targets %q routes %q, want %q and %q", c.want, join(targets), join(routes), c.targets, c.routes)
		}
	}
}
