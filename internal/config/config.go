// Package config loads the YAML or JSON configuration file given with
// -config. Files ending in .yaml or .yml are YAML; any other is JSON.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"sort"

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
}

// Rule is a parsed entry of Config.Rules.
type Rule struct {
	Prefix   netip.Prefix
	Scenario rule.Scenario
}

// Load reads and validates a configuration file. The rules are returned
// sorted by their key as written in the file.
func Load(path string) (*Config, []Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if isYAML(path) {
		if data, err = yamlToJSON(data, path); err != nil {
			return nil, nil, err
		}
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
