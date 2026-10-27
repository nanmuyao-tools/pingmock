package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/nanmuyao-tools/pingmock/internal/rule"
)

// knownFields are the top-level keys of a configuration file.
var knownFields = map[string]bool{
	"dev": true, "addr": true, "addr6": true, "api": true,
	"seed": true, "history": true, "default": true, "rules": true, "hosts": true, "paths": true,
	"tcp": true, "dns": true, "udp": true, "outside": true,
}

// yamlToJSON converts a YAML configuration into the equivalent JSON, which
// the rule types decode and validate. Mistakes that are easy to make are
// reported first, with their line number.
func yamlToJSON(data []byte, path string) ([]byte, error) {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return []byte("{}"), nil // an empty file
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: only one YAML document is allowed", path)
	}
	doc := &root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		doc = doc.Content[0]
	}
	if doc.Kind == yaml.ScalarNode && doc.Tag == "!!null" {
		return []byte("{}"), nil // only comments
	}
	if doc.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s:%d: the configuration must be a mapping of settings", path, doc.Line)
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key, val := doc.Content[i], doc.Content[i+1]
		switch {
		case !knownFields[key.Value]:
			return nil, fmt.Errorf("%s:%d: unknown field %q", path, key.Line, key.Value)
		case key.Value == "default":
			if err := checkRule(val); err != nil {
				return nil, fmt.Errorf("%s:%d: default: %w", path, val.Line, err)
			}
		case key.Value == "rules":
			if err := checkRules(val, path); err != nil {
				return nil, err
			}
		case key.Value == "hosts":
			if err := checkHosts(val, path); err != nil {
				return nil, err
			}
		case key.Value == "paths":
			if err := checkPaths(val, path); err != nil {
				return nil, err
			}
		case key.Value == "tcp":
			if err := checkPortRules(val, path, "tcp", func(b []byte) error { var sc rule.TCPScenario; return json.Unmarshal(b, &sc) }); err != nil {
				return nil, err
			}
		case key.Value == "udp":
			if err := checkPortRules(val, path, "udp", func(b []byte) error { var sc rule.UDPScenario; return json.Unmarshal(b, &sc) }); err != nil {
				return nil, err
			}
		}
	}
	var v any
	if err := doc.Decode(&v); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return b, nil
}

// checkRules validates the rules mapping, reporting the line of the first
// bad target or rule.
func checkRules(n *yaml.Node, path string) error {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%s:%d: rules must be a mapping of targets to rules", path, n.Line)
	}
	seen := map[string]int{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		t, err := rule.ParseTarget(key.Value)
		if err != nil {
			return fmt.Errorf("%s:%d: rule %q: %w", path, key.Line, key.Value, err)
		}
		if line, dup := seen[t.String()]; dup {
			return fmt.Errorf("%s:%d: rule %q is the same target %s as on line %d", path, key.Line, key.Value, t, line)
		}
		seen[t.String()] = key.Line
		if err := checkRule(val); err != nil {
			return fmt.Errorf("%s:%d: rule %q: %w", path, val.Line, key.Value, err)
		}
	}
	return nil
}

// checkPortRules validates the tcp or udp mapping, reporting the line of
// the first bad target or rule; decode decodes one rule's JSON.
func checkPortRules(n *yaml.Node, path, section string, decode func([]byte) error) error {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%s:%d: %s must be a mapping of targets to rules, such as 10.99.0.1:5432: timeout", path, n.Line, section)
	}
	seen := map[string]int{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		t, err := rule.ParseTCPTarget(key.Value)
		if err != nil {
			return fmt.Errorf("%s:%d: %s %q: %w", path, key.Line, section, key.Value, err)
		}
		if line, dup := seen[t.String()]; dup {
			return fmt.Errorf("%s:%d: %s %q is the same target %s as on line %d", path, key.Line, section, key.Value, t, line)
		}
		seen[t.String()] = key.Line
		if err := checkPortRule(val, decode); err != nil {
			return fmt.Errorf("%s:%d: %s %q: %w", path, val.Line, section, key.Value, err)
		}
	}
	return nil
}

// checkPortRule decodes one rule as the JSON decoding will.
func checkPortRule(n *yaml.Node, decode func([]byte) error) error {
	var v any
	if err := n.Decode(&v); err != nil {
		return err
	}
	if v == nil {
		return errors.New("empty rule")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return decode(b)
}

// checkHosts validates the hosts mapping, reporting the line of the first
// bad name or address.
func checkHosts(n *yaml.Node, path string) error {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%s:%d: hosts must be a mapping of names to addresses", path, n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		if val.Kind != yaml.ScalarNode {
			return fmt.Errorf("%s:%d: host %q: want an address", path, val.Line, key.Value)
		}
		if _, err := ParseHost(key.Value, val.Value); err != nil {
			return fmt.Errorf("%s:%d: hosts: %w", path, key.Line, err)
		}
	}
	return nil
}

// checkPaths validates the paths mapping, reporting the line of the first
// bad target or path. A value starting with "*" needs quotes in YAML,
// which is the mistake to explain.
func checkPaths(n *yaml.Node, path string) error {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%s:%d: paths must be a mapping of targets to paths", path, n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		if _, err := rule.ParseTarget(key.Value); err != nil {
			return fmt.Errorf("%s:%d: path %q: %w", path, key.Line, key.Value, err)
		}
		if val.Kind != yaml.ScalarNode || val.Tag == "!!null" {
			return fmt.Errorf("%s:%d: path %q: want a string such as \"192.168.1.1@2ms > * > 10.0.0.1@20ms\" (quote it when a hop is *)", path, val.Line, key.Value)
		}
		if _, err := rule.ParsePath(val.Value); err != nil {
			return fmt.Errorf("%s:%d: path %q: %w", path, val.Line, key.Value, err)
		}
	}
	return nil
}

// checkRule decodes one rule as the JSON decoding will.
func checkRule(n *yaml.Node) error {
	var v any
	if err := n.Decode(&v); err != nil {
		return err
	}
	if v == nil {
		return errors.New("empty rule")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var sc rule.Scenario
	return json.Unmarshal(b, &sc)
}
