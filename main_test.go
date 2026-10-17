package main

import (
	"strings"
	"testing"
)

func TestParseRuleFlag(t *testing.T) {
	cases := map[string]struct{ prefix, rule string }{
		"10.99.0.1=50ms":                         {"10.99.0.1/32", "delay=50ms"},
		"10.99.0.1=delay=50ms,jitter=5ms":        {"10.99.0.1/32", "delay=50ms,jitter=5ms"},
		"10.99.0.0/24=20ms for 3 pings; timeout": {"10.99.0.0/24", "delay=20ms for 3 pings; timeout"},
		" 8.8.8.8 =timeout":                      {"8.8.8.8/32", "timeout"},
	}
	for in, want := range cases {
		p, sc, err := parseRuleFlag(in)
		if err != nil || p.String() != want.prefix || sc.String() != want.rule {
			t.Errorf("parseRuleFlag(%q) = %v, %q, %v; want %s, %q", in, p, sc, err, want.prefix, want.rule)
		}
	}
	for in, msg := range map[string]string{
		"10.99.0.1":          "want TARGET=RULE",
		"10.99.0.300=5ms":    "invalid -rule",
		"10.99.0.1=":         "empty rule",
		"10.99.0.1=5ms; 6ms": "needs a length",
	} {
		if _, _, err := parseRuleFlag(in); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("parseRuleFlag(%q) error %v, want it to mention %q", in, err, msg)
		}
	}
}
