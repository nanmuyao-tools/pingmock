package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"sort"
)

// Config is the JSON configuration file loaded with -config. Every field
// is optional; flags given on the command line take precedence.
//
//	{
//	  "seed": 42,
//	  "default": "0ms",
//	  "rules": {
//	    "10.99.0.1": "50ms",
//	    "10.99.0.2": "20ms for 5 pings; timeout",
//	    "10.99.0.128/25": {"delay": "80ms", "jitter": "10ms"}
//	  }
//	}
type Config struct {
	Dev     string              `json:"dev"`
	Addr    string              `json:"addr"`
	API     *string             `json:"api"` // pointer so "" can disable the API
	Seed    *uint64             `json:"seed"`
	Default *Scenario           `json:"default"`
	Rules   map[string]Scenario `json:"rules"`
}

// configRule is a parsed entry of Config.Rules.
type configRule struct {
	prefix netip.Prefix
	sc     Scenario
}

// LoadConfig reads and validates a configuration file.
func LoadConfig(path string) (*Config, []configRule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
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
	rules := make([]configRule, 0, len(keys))
	seen := map[netip.Prefix]string{}
	for _, k := range keys {
		p, err := ParsePrefix(k)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: rule %q: %w", path, k, err)
		}
		if other, dup := seen[p]; dup {
			return nil, nil, fmt.Errorf("%s: rules %q and %q are the same target %s", path, other, k, p)
		}
		seen[p] = k
		rules = append(rules, configRule{p, cfg.Rules[k]})
	}
	if cfg.Addr != "" {
		if p, err := netip.ParsePrefix(cfg.Addr); err != nil || !p.Addr().Is4() {
			return nil, nil, fmt.Errorf("%s: invalid addr %q: need an IPv4 address with prefix, e.g. 10.99.0.254/24", path, cfg.Addr)
		}
	}
	return &cfg, rules, nil
}
