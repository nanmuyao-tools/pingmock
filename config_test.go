package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pingmock.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfig(t *testing.T) {
	path := writeConfig(t, `{
		"seed": 42,
		"api": "",
		"default": "timeout",
		"rules": {
			"10.99.0.130/25": {"delay": "80ms"},
			"10.99.0.1": "20ms for 3 pings; 5ms"
		}
	}`)
	cfg, rules, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if *cfg.Seed != 42 || cfg.API == nil || *cfg.API != "" || cfg.Default.String() != "timeout" {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if len(rules) != 2 {
		t.Fatalf("got %d rules", len(rules))
	}
	// Sorted by the key as written; prefixes are masked.
	if rules[0].prefix.String() != "10.99.0.1/32" || rules[0].sc.String() != "delay=20ms for 3 pings; delay=5ms" {
		t.Errorf("rule 0 = %v %v", rules[0].prefix, rules[0].sc)
	}
	if rules[1].prefix.String() != "10.99.0.128/25" || rules[1].sc.String() != "delay=80ms" {
		t.Errorf("rule 1 = %v %v", rules[1].prefix, rules[1].sc)
	}
}

func TestLoadConfigExample(t *testing.T) {
	if _, rules, err := LoadConfig("examples/pingmock.json"); err != nil || len(rules) == 0 {
		t.Fatalf("example config: %d rules, %v", len(rules), err)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	cases := map[string]string{
		`{"rulez": {}}`:                                           "unknown field",
		`{"rules": {"::1": "5ms"}}`:                               "only IPv4",
		`{"rules": {"10.99.0.1": "bogus"}}`:                       "invalid spec",
		`{"rules": {"10.99.0.1": "5ms; 6ms"}}`:                    "needs a length",
		`{"rules": {"10.0.0.1/24": "5ms", "10.0.0.7/24": "6ms"}}`: "same target",
		`{"addr": "10.99.0.1"}`:                                   "invalid addr",
		`{"seed": -1}`:                                            "cannot unmarshal",
		`{`:                                                       "unexpected EOF",
	}
	for content, want := range cases {
		_, _, err := LoadConfig(writeConfig(t, content))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("config %s: error %v, want it to mention %q", content, err, want)
		}
	}
	if _, _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("missing file: expected error")
	}
}
