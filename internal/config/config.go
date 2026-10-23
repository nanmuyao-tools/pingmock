// Package config loads the YAML configuration file given with -config.
// Since JSON is a subset of YAML, JSON files load as well.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strings"

	"github.com/nanmuyao/pingmock/internal/rule"
)

// Config is the configuration file loaded with -config. Every field is
// optional; flags given on the command line take precedence.
//
//	seed: 42
//	default: 0ms
//	rules:
//	  10.99.0.1: 50ms
//	  10.99.0.2: 20ms for 5 pings; timeout
//	  10.99.0.128/25: {delay: 80ms, jitter: 10ms}
//	  fd99::1: unreachable
type Config struct {
	Dev     string                   `json:"dev"`
	Addr    string                   `json:"addr"`
	Addr6   *string                  `json:"addr6"` // pointer so "" can disable IPv6
	API     *string                  `json:"api"`   // pointer so "" can disable the API
	Seed    *uint64                  `json:"seed"`
	History *int                     `json:"history"`
	Default *rule.Scenario           `json:"default"`
	Rules   map[string]rule.Scenario `json:"rules"`
	// Hosts maps names to addresses for pingmock exec, which resolves
	// them for the command through a private /etc/hosts:
	//
	//	hosts:
	//	  api.example.com: 10.99.0.1
	Hosts map[string]string `json:"hosts"`
	// Paths gives targets a route, for traceroute and mtr:
	//
	//	paths:
	//	  8.8.8.8: 192.168.1.1@2ms > 10.0.0.1@15ms > * > 72.14.0.1@25ms
	Paths map[string]rule.Path `json:"paths"`
	// TCP gives addresses and ports TCP rules:
	//
	//	tcp:
	//	  10.99.0.1:5432: timeout
	//	  "[fd99::1]:443": refuse
	TCP map[string]rule.TCPScenario `json:"tcp"`
	// UDP gives addresses and ports UDP rules:
	//
	//	udp:
	//	  10.99.0.1:514: refuse
	UDP map[string]rule.UDPScenario `json:"udp"`
	// DNS gives names DNS rules, for pingmock exec:
	//
	//	dns:
	//	  api.example.com: nxdomain
	//	  backup.example.com: servfail for 2 requests; 10ms
	DNS map[string]rule.DNSScenario `json:"dns"`
	// Outside gives the command of pingmock exec the host's network; see
	// Outside.
	Outside *Outside `json:"outside"`
}

// DNSRule is a parsed entry of Config.DNS.
type DNSRule struct {
	Name     string // lower case, without a trailing dot
	Scenario rule.Scenario
}

// SortedDNS returns the file's DNS rules, validated and sorted by name.
func (c *Config) SortedDNS(path string) ([]DNSRule, error) {
	keys := make([]string, 0, len(c.DNS))
	for k := range c.DNS {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]DNSRule, 0, len(keys))
	seen := map[string]string{}
	for _, k := range keys {
		name, err := ParseDNSName(k)
		if err != nil {
			return nil, fmt.Errorf("%s: dns %q: %w", path, k, err)
		}
		if other, dup := seen[name]; dup {
			return nil, fmt.Errorf("%s: dns %q and %q are the same name", path, other, k)
		}
		seen[name] = k
		out = append(out, DNSRule{name, c.DNS[k].Scenario})
	}
	return out, nil
}

// ParseDNSName checks the name of a DNS rule and returns it in the form
// names are compared in: lower case, without a trailing dot.
func ParseDNSName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n, t, ok := strings.Cut(name, "/"); ok && rule.ValidName(n) == nil && t != "" {
		return "", fmt.Errorf("%s: a rule for one query type is not supported yet; the rule for %s covers every type", name, n)
	}
	if err := rule.ValidName(name); err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimSuffix(name, ".")), nil
}

// TCPRule is a parsed entry of Config.TCP, or of Config.UDP.
type TCPRule struct {
	Target   rule.TCPTarget
	Scenario rule.Scenario
}

// SortedTCP returns the file's TCP rules, validated and sorted by target
// as written.
func (c *Config) SortedTCP(path string) ([]TCPRule, error) {
	keys := make([]string, 0, len(c.TCP))
	for k := range c.TCP {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]TCPRule, 0, len(keys))
	seen := map[string]string{}
	for _, k := range keys {
		t, err := rule.ParseTCPTarget(k)
		if err != nil {
			return nil, fmt.Errorf("%s: tcp %q: %w", path, k, err)
		}
		if other, dup := seen[t.String()]; dup {
			return nil, fmt.Errorf("%s: tcp %q and %q are the same target %s", path, other, k, t)
		}
		seen[t.String()] = k
		out = append(out, TCPRule{t, c.TCP[k].Scenario})
	}
	return out, nil
}

// SortedUDP returns the file's UDP rules, validated and sorted by target
// as written, as SortedTCP does.
func (c *Config) SortedUDP(path string) ([]TCPRule, error) {
	keys := make([]string, 0, len(c.UDP))
	for k := range c.UDP {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]TCPRule, 0, len(keys))
	seen := map[string]string{}
	for _, k := range keys {
		t, err := rule.ParseTCPTarget(k)
		if err != nil {
			return nil, fmt.Errorf("%s: udp %q: %w", path, k, err)
		}
		if other, dup := seen[t.String()]; dup {
			return nil, fmt.Errorf("%s: udp %q and %q are the same target %s", path, other, k, t)
		}
		seen[t.String()] = k
		out = append(out, TCPRule{t, c.UDP[k].Scenario})
	}
	return out, nil
}

// PathRule is a parsed entry of Config.Paths.
type PathRule struct {
	Target rule.Target
	Path   rule.Path
}

// SortedPaths returns the file's paths, validated and sorted by target.
func (c *Config) SortedPaths(path string) ([]PathRule, error) {
	keys := make([]string, 0, len(c.Paths))
	for k := range c.Paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]PathRule, 0, len(keys))
	seen := map[string]string{}
	for _, k := range keys {
		t, err := rule.ParseTarget(k)
		if err != nil || (t.Name == "" && !t.Prefix.IsSingleIP()) {
			return nil, fmt.Errorf("%s: path %q: want a single address or a name", path, k)
		}
		if other, dup := seen[t.String()]; dup {
			return nil, fmt.Errorf("%s: paths %q and %q are the same target %s", path, other, k, t)
		}
		seen[t.String()] = k
		out = append(out, PathRule{t, c.Paths[k]})
	}
	return out, nil
}

// Rule is a parsed entry of Config.Rules.
type Rule struct {
	Target   rule.Target
	Scenario rule.Scenario
}

// Host is a name that pingmock exec resolves to an address.
type Host struct {
	Name string
	Addr netip.Addr
}

// ParseHost validates a host entry, from the file or from -host NAME=ADDR.
func ParseHost(name, addr string) (Host, error) {
	name = strings.TrimSpace(name)
	if err := rule.ValidName(name); err != nil {
		return Host{}, err
	}
	a, err := netip.ParseAddr(strings.TrimSpace(addr))
	if err != nil || a.Zone() != "" {
		return Host{}, fmt.Errorf("host %s: invalid address %q", name, addr)
	}
	return Host{Name: strings.ToLower(strings.TrimSuffix(name, ".")), Addr: a.Unmap()}, nil
}

// SortedHosts returns the file's hosts, validated and sorted by name.
func (c *Config) SortedHosts(path string) ([]Host, error) {
	names := make([]string, 0, len(c.Hosts))
	for n := range c.Hosts {
		names = append(names, n)
	}
	sort.Strings(names)
	hosts := make([]Host, 0, len(names))
	for _, n := range names {
		h, err := ParseHost(n, c.Hosts[n])
		if err != nil {
			return nil, fmt.Errorf("%s: hosts: %w", path, err)
		}
		hosts = append(hosts, h)
	}
	return hosts, nil
}

// Load reads and validates a configuration file. The rules are returned
// sorted by their key as written in the file.
func Load(path string) (*Config, []Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	// YAML is converted to JSON, which the types in package rule decode
	// and validate (the HTTP API sends them JSON too).
	if data, err = yamlToJSON(data, path); err != nil {
		return nil, nil, err
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	keys := make([]string, 0, len(cfg.Rules))
	for k := range cfg.Rules {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rules := make([]Rule, 0, len(keys))
	seen := map[string]string{}
	for _, k := range keys {
		t, err := rule.ParseTarget(k)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: rule %q: %w", path, k, err)
		}
		if other, dup := seen[t.String()]; dup {
			return nil, nil, fmt.Errorf("%s: rules %q and %q are the same target %s", path, other, k, t)
		}
		seen[t.String()] = k
		rules = append(rules, Rule{t, cfg.Rules[k]})
	}
	if _, err := cfg.SortedHosts(path); err != nil {
		return nil, nil, err
	}
	if _, err := cfg.SortedPaths(path); err != nil {
		return nil, nil, err
	}
	if _, err := cfg.SortedTCP(path); err != nil {
		return nil, nil, err
	}
	if _, err := cfg.SortedUDP(path); err != nil {
		return nil, nil, err
	}
	if _, err := cfg.SortedDNS(path); err != nil {
		return nil, nil, err
	}
	if cfg.History != nil && *cfg.History < 0 {
		return nil, nil, fmt.Errorf("%s: history must not be negative", path)
	}
	if cfg.Addr6 != nil && *cfg.Addr6 != "" {
		if p, err := netip.ParsePrefix(*cfg.Addr6); err != nil || !p.Addr().Is6() || p.Addr().Is4In6() {
			return nil, nil, fmt.Errorf("%s: invalid addr6 %q: need an IPv6 address with prefix, e.g. fd99::fe/64", path, *cfg.Addr6)
		}
	}
	if cfg.Addr != "" {
		if p, err := netip.ParsePrefix(cfg.Addr); err != nil || !p.Addr().Is4() {
			return nil, nil, fmt.Errorf("%s: invalid addr %q: need an IPv4 address with prefix, e.g. 10.99.0.254/24", path, cfg.Addr)
		}
	}
	return &cfg, rules, nil
}
