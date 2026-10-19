package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// describe renders a loaded configuration so two can be compared.
func describe(cfg *Config, rules []Rule) string {
	var b strings.Builder
	if cfg.Seed != nil {
		fmt.Fprintf(&b, "seed=%d ", *cfg.Seed)
	}
	if cfg.Default != nil {
		fmt.Fprintf(&b, "default=%s ", cfg.Default)
	}
	for _, r := range rules {
		fmt.Fprintf(&b, "%s=%s; ", r.Prefix, r.Scenario)
	}
	return b.String()
}

func TestLoad(t *testing.T) {
	path := writeFile(t, "c.yaml", `
# comments are allowed
seed: 7
api: ""          # disables the API
addr: 10.98.0.254/24
addr6: fd99::fe/64
history: 50
default: timeout
rules:
  10.99.0.1: 20            # a bare number is milliseconds
  10.99.0.2: 5ms for 2 pings; unreachable
  fd99::1: delay=10ms,pattern=..u   # IPv6 needs no quotes
  "::1": 3ms
  10.99.0.130/25: {delay: 80, jitter: 10, loss: 5%}
  10.99.0.3: &slow 300ms
  10.99.0.4: *slow
`)
	cfg, rules, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if *cfg.Seed != 7 || *cfg.API != "" || cfg.Addr != "10.98.0.254/24" || *cfg.Addr6 != "fd99::fe/64" ||
		*cfg.History != 50 || cfg.Default.String() != "timeout" {
		t.Errorf("settings = %+v", cfg)
	}
	got := map[string]string{}
	for _, r := range rules {
		got[r.Prefix.String()] = r.Scenario.String()
	}
	want := map[string]string{
		"10.99.0.1/32":   "delay=20ms",
		"10.99.0.2/32":   "delay=5ms for 2 pings; delay=0s,unreachable",
		"fd99::1/128":    "delay=10ms,pattern=..u",
		"::1/128":        "delay=3ms",
		"10.99.0.128/25": "delay=80ms,jitter=10ms,loss=5%", // masked
		"10.99.0.3/32":   "delay=300ms",
		"10.99.0.4/32":   "delay=300ms",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("rules:\n got %v\nwant %v", got, want)
	}
	// Rules come back sorted by their key as written.
	if rules[0].Prefix.String() != "10.99.0.1/32" || rules[len(rules)-1].Prefix.String() != "fd99::1/128" {
		t.Errorf("order: first %s, last %s", rules[0].Prefix, rules[len(rules)-1].Prefix)
	}
}

func TestLoadExample(t *testing.T) {
	cfg, rules, err := Load("../../examples/pingmock.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if *cfg.Seed != 42 || len(rules) != 7 {
		t.Errorf("example: seed %v, %d rules", cfg.Seed, len(rules))
	}
}

// JSON is a subset of YAML, so JSON configuration files written for
// earlier versions keep loading, to the same settings as in YAML.
func TestLoadJSON(t *testing.T) {
	legacy := `{
	"seed": 42,
	"default": "1ms",
	"rules": {
		"10.99.0.1": "20ms",
		"10.99.0.2": "delay=120ms,jitter=15ms,loss=2%",
		"10.99.0.3": "30ms for 10 pings; timeout for 5s; 30ms",
		"10.99.0.4": {
			"phases": [
				{"spec": "delay=40ms,pattern=....x", "for": "20s"},
				{"spec": {"delay": "400ms", "jitter": "100ms"}, "for": "10s"}
			],
			"repeat": true
		},
		"10.99.0.5": "delay=50ms,unreachable=admin",
		"10.99.0.6": {"delay": "25ms", "ttl": 128},
		"8.8.8.8": "timeout"
	}
}`
	j, jr, err := Load(writeFile(t, "pingmock.json", legacy))
	if err != nil {
		t.Fatal(err)
	}
	y, yr, err := Load("../../examples/pingmock.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if describe(j, jr) != describe(y, yr) {
		t.Errorf("the JSON configuration differs from the YAML example:\nJSON: %s\nYAML: %s", describe(j, jr), describe(y, yr))
	}
}

func TestLoadEmpty(t *testing.T) {
	for name, content := range map[string]string{"empty.yaml": "", "comments.yaml": "# nothing here\n", "empty.json": "{}"} {
		cfg, rules, err := Load(writeFile(t, name, content))
		if err != nil || len(rules) != 0 || cfg.Seed != nil {
			t.Errorf("%s: %+v %v %v", name, cfg, rules, err)
		}
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]string{
		"seed: 1\nrulez: {}\n":                                  "c.yaml:2: unknown field \"rulez\"",
		"rules:\n  10.99.0.1: 5ms\n  nope: 6ms\n":               "c.yaml:3: rule \"nope\"",
		"rules:\n  fe80::1%eth0: 5ms\n":                         "c.yaml:2: rule \"fe80::1%eth0\": fe80::1%eth0: zones are not supported",
		"rules:\n  10.99.0.1: 5ms\n  10.99.0.2: 5ms; 6ms\n":     "c.yaml:3: rule \"10.99.0.2\": invalid rule \"5ms; 6ms\"",
		"rules:\n  10.99.0.1: 5ms\n  10.99.0.1: 6ms\n":          "c.yaml:3: rule \"10.99.0.1\" is the same target 10.99.0.1/32 as on line 2",
		"rules:\n  10.0.0.1/24: 5ms\n  10.0.0.9/24: 6ms\n":      "c.yaml:3: rule \"10.0.0.9/24\" is the same target 10.0.0.0/24 as on line 2",
		"rules:\n  10.0.0.1: 5ms\n  \"::ffff:10.0.0.1\": 6ms\n": "c.yaml:3: rule \"::ffff:10.0.0.1\" is the same target 10.0.0.1/32 as on line 2",
		"rules:\n  10.99.0.1:\n":                                "c.yaml:2: rule \"10.99.0.1\": empty rule",
		"rules:\n  10.99.0.1: {delay: 5ms, dealy: 6ms}\n":       "c.yaml:2: rule \"10.99.0.1\": json: unknown field \"dealy\"",
		"default: 5ms; 6ms\n":                                   "c.yaml:1: default: ",
		"rules: [10.99.0.1]\n":                                  "c.yaml:1: rules must be a mapping",
		"- seed: 1\n":                                           "c.yaml:1: the configuration must be a mapping",
		"seed: 1\n---\nseed: 2\n":                               "only one YAML document",
		"seed: [1\n":                                            "c.yaml: yaml:",
		"seed: -1\n":                                            "cannot unmarshal",
		"addr: 10.99.0.1\n":                                     "invalid addr",
		"addr: fd99::fe/64\n":                                   "invalid addr",
		"addr6: 10.99.0.254/24\n":                               "invalid addr6",
		"history: -1\n":                                         "history must not be negative",
		"{\"rulez\": {}}":                                       "c.yaml:1: unknown field \"rulez\"", // JSON, too
	}
	for content, want := range cases {
		_, _, err := Load(writeFile(t, "c.yaml", content))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want it to contain %q", content, err, want)
		}
	}
	if _, _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Error("missing file: expected error")
	}
}
