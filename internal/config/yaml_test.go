package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := writeBytes(path, content); err != nil {
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

func TestYAMLAndJSONExamplesMatch(t *testing.T) {
	y, yr, err := Load("../../examples/pingmock.yaml")
	if err != nil {
		t.Fatal(err)
	}
	j, jr, err := Load("../../examples/pingmock.json")
	if err != nil {
		t.Fatal(err)
	}
	if describe(y, yr) != describe(j, jr) {
		t.Errorf("the YAML and JSON examples differ:\nYAML: %s\nJSON: %s", describe(y, yr), describe(j, jr))
	}
	if len(yr) != 6 {
		t.Errorf("%d rules in the example, want 6", len(yr))
	}
}

func TestLoadYAML(t *testing.T) {
	path := writeFile(t, "c.yml", `
# comments are allowed
seed: 7
api: ""          # disables the API
addr6: fd99::fe/64
history: 50
default: timeout
rules:
  10.99.0.1: 20            # a bare number is milliseconds
  10.99.0.2: 5ms for 2 pings; unreachable
  fd99::1: delay=10ms,pattern=..u   # IPv6 needs no quotes
  "::1": 3ms
  10.99.0.128/25: {delay: 80, jitter: 10, loss: 5%}
  10.99.0.3: &slow 300ms
  10.99.0.4: *slow
`)
	cfg, rules, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if *cfg.Seed != 7 || *cfg.API != "" || *cfg.Addr6 != "fd99::fe/64" || *cfg.History != 50 || cfg.Default.String() != "timeout" {
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
		"10.99.0.128/25": "delay=80ms,jitter=10ms,loss=5%",
		"10.99.0.3/32":   "delay=300ms",
		"10.99.0.4/32":   "delay=300ms",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("rules:\n got %v\nwant %v", got, want)
	}
}

func TestLoadYAMLEmpty(t *testing.T) {
	for name, content := range map[string]string{"empty.yaml": "", "comments.yaml": "# nothing here\n"} {
		cfg, rules, err := Load(writeFile(t, name, content))
		if err != nil || len(rules) != 0 || cfg.Seed != nil {
			t.Errorf("%s: %+v %v %v", name, cfg, rules, err)
		}
	}
}

func TestLoadYAMLErrors(t *testing.T) {
	cases := map[string]string{
		"seed: 1\nrulez: {}\n":                              "c.yaml:2: unknown field \"rulez\"",
		"rules:\n  10.99.0.1: 5ms\n  nope: 6ms\n":           "c.yaml:3: rule \"nope\"",
		"rules:\n  10.99.0.1: 5ms\n  10.99.0.2: 5ms; 6ms\n": "c.yaml:3: rule \"10.99.0.2\": ",
		"rules:\n  10.99.0.1: 5ms\n  10.99.0.1: 6ms\n":      "c.yaml:3: rule \"10.99.0.1\" is the same target 10.99.0.1/32 as on line 2",
		"rules:\n  10.0.0.1/24: 5ms\n  10.0.0.9/24: 6ms\n":  "c.yaml:3: rule \"10.0.0.9/24\" is the same target 10.0.0.0/24 as on line 2",
		"rules:\n  10.99.0.1:\n":                            "c.yaml:2: rule \"10.99.0.1\": empty rule",
		"default: 5ms; 6ms\n":                               "c.yaml:1: default: ",
		"rules: [10.99.0.1]\n":                              "c.yaml:1: rules must be a mapping",
		"- seed: 1\n":                                       "c.yaml:1: the configuration must be a mapping",
		"seed: 1\n---\nseed: 2\n":                           "only one YAML document",
		"seed: [1\n":                                        "c.yaml: yaml:",
		"seed: -1\n":                                        "cannot unmarshal",
		"rules:\n  10.99.0.1: {delay: 5ms, dealy: 6ms}\n":   "c.yaml:2: rule \"10.99.0.1\": json: unknown field \"dealy\"",
	}
	for content, want := range cases {
		_, _, err := Load(writeFile(t, "c.yaml", content))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want it to contain %q", content, err, want)
		}
	}
}

func TestJSONStillWorks(t *testing.T) {
	// Not .yaml or .yml: JSON, which also accepts bare numbers now.
	cfg, rules, err := Load(writeFile(t, "c.conf", `{"seed": 3, "rules": {"10.99.0.1": 15}}`))
	if err != nil || *cfg.Seed != 3 || len(rules) != 1 || rules[0].Scenario.String() != "delay=15ms" {
		t.Errorf("%+v %v %v", cfg, rules, err)
	}
	if _, _, err := Load(writeFile(t, "c.json", "seed: 3\n")); err == nil {
		t.Error("YAML in a .json file was accepted")
	}
}
