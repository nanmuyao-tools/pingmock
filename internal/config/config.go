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
}

// Rule is a parsed entry of Config.Rules.
type Rule struct {
	Prefix   netip.Prefix
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
	if name == "" || strings.ContainsAny(name, " \t#") || strings.HasPrefix(name, "-") || strings.HasPrefix(name, ".") {
		return Host{}, fmt.Errorf("invalid host name %q", name)
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return Host{}, fmt.Errorf("host name %q is an address; want a name such as api.example.com", name)
	}
	a, err := netip.ParseAddr(strings.TrimSpace(addr))
	if err != nil || a.Zone() != "" {
		return Host{}, fmt.Errorf("host %s: invalid address %q", name, addr)
	}
	return Host{Name: name, Addr: a}, nil
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
	seen := map[netip.Prefix]string{}
	for _, k := range keys {
		p, err := rule.ParsePrefix(k)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: rule %q: %w", path, k, err)
		}
		if other, dup := seen[p]; dup {
			return nil, nil, fmt.Errorf("%s: rules %q and %q are the same target %s", path, other, k, p)
		}
		seen[p] = k
		rules = append(rules, Rule{p, cfg.Rules[k]})
	}
	if _, err := cfg.SortedHosts(path); err != nil {
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
