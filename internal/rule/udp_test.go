package rule

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseUDPScenario(t *testing.T) {
	for in, want := range map[string]string{
		"refuse":                           "delay=0s,refuse",
		"timeout for 2 requests; refuse":   "timeout for 2 requests; delay=0s,refuse",
		"unreachable=admin":                "delay=0s,unreachable=admin",
		"delay=5ms,pattern=xxr":            "delay=5ms,pattern=xxr",
		"loss=30%,refuse,ttl=128":          "delay=0s,loss=30%,refuse,ttl=128",
		"timeout for 1 query; unreachable": "timeout for 1 request; delay=0s,unreachable",
	} {
		got, err := ParseUDPScenario(in)
		if err != nil || got.String() != want {
			t.Errorf("ParseUDPScenario(%q) = %q, %v; want %q", in, got, err, want)
			continue
		}
		var back UDPScenario
		b, _ := json.Marshal(got)
		if err := json.Unmarshal(b, &back); err != nil || back.String() != want {
			t.Errorf("JSON round trip of %s: %v", b, err)
		}
	}
	for bad, want := range map[string]string{
		"20ms":                      "pingmock sends no UDP answers",
		"loss=30%":                  "pingmock sends no UDP answers",
		"refuse for 1 request; 5ms": "pingmock sends no UDP answers",
		"pattern=.r":                "may only contain 'x'",
		"after=close":               "TCP rules",
		`reply="x",refuse`:          "TCP rules",
		"nxdomain":                  "DNS rules",
	} {
		if _, err := ParseUDPScenario(bad); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseUDPScenario(%q) = %v, want an error containing %q", bad, err, want)
		}
	}
	var sc UDPScenario
	if err := json.Unmarshal([]byte(`{"refuse": true, "delay": "5ms"}`), &sc); err != nil || sc.String() != "delay=5ms,refuse" {
		t.Errorf("JSON object: %q, %v", sc, err)
	}
	// Ping rules still refuse refuse, with a message naming both.
	if _, err := ParseScenario("refuse"); err == nil || !strings.Contains(err.Error(), "-udp") {
		t.Errorf("ParseScenario(refuse) = %v", err)
	}
}
