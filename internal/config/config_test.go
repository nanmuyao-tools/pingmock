package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nanmuyao/pingmock/internal/rule"
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
		fmt.Fprintf(&b, "%s=%s; ", r.Target, r.Scenario)
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
		got[r.Target.String()] = r.Scenario.String()
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
	if rules[0].Target.String() != "10.99.0.1/32" || rules[len(rules)-1].Target.String() != "fd99::1/128" {
		t.Errorf("order: first %s, last %s", rules[0].Target, rules[len(rules)-1].Target)
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
		"rules:\n  10.99.0.1: 5ms\n  -nope: 6ms\n":              "c.yaml:3: rule \"-nope\"",
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

func TestLoadHosts(t *testing.T) {
	cfg, _, err := Load(writeFile(t, "h.yaml", "hosts:\n  www.example.com: 10.99.0.1\n  api.example.com: fd99::1\n"))
	if err != nil {
		t.Fatal(err)
	}
	hosts, err := cfg.SortedHosts("h.yaml")
	if err != nil || len(hosts) != 2 || hosts[0].Name != "api.example.com" || hosts[0].Addr.String() != "fd99::1" ||
		hosts[1].Name != "www.example.com" || hosts[1].Addr.String() != "10.99.0.1" {
		t.Errorf("hosts = %+v, %v", hosts, err)
	}
	for _, bad := range []string{"hosts:\n  www.example.com: nope\n", "hosts:\n  10.99.0.1: 10.99.0.2\n", "hosts:\n  '': 10.99.0.1\n"} {
		if _, _, err := Load(writeFile(t, "bad.yaml", bad)); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestLoadNameRules(t *testing.T) {
	_, rules, err := Load(writeFile(t, "n.yaml", "rules:\n  API.example.com.: 20ms\n  10.99.0.1: 5ms\n  db.example.com: timeout\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := describe(&Config{}, rules)
	if got != "10.99.0.1/32=delay=5ms; api.example.com=delay=20ms; db.example.com=timeout; " {
		t.Errorf("rules = %s", got)
	}
	if _, _, err := Load(writeFile(t, "dup.yaml", "rules:\n  api.example.com: 20ms\n  API.EXAMPLE.COM: 5ms\n")); err == nil || !strings.Contains(err.Error(), "same target") {
		t.Errorf("duplicate name: %v", err)
	}
}

func TestLoadPaths(t *testing.T) {
	// A * in the middle of a value is fine; one at the start would be a
	// YAML alias, so that value is quoted.
	cfg, _, err := Load(writeFile(t, "p.yaml", "paths:\n  8.8.8.8: 192.168.1.1@2ms > 10.0.0.1@15ms > * > 72.14.0.1@25ms\n  api.example.com: \"* > 10.0.0.2@5ms\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	paths, err := cfg.SortedPaths("p.yaml")
	if err != nil || len(paths) != 2 || paths[0].Target.String() != "8.8.8.8/32" || paths[0].Path.String() != "192.168.1.1@delay=2ms > 10.0.0.1@delay=15ms > * > 72.14.0.1@delay=25ms" || paths[1].Target.Name != "api.example.com" {
		t.Errorf("paths = %+v, %v", paths, err)
	}
	for in, want := range map[string]string{
		"paths:\n  8.8.8.8: * > 2.2.2.2@3ms\n":    "",               // an unquoted leading * is a YAML alias: any error will do
		"paths:\n  10.0.0.0/24: 1.1.1.1@2ms\n":    "single address", // a prefix cannot be traced
		"paths:\n  8.8.8.8: 1.1.1.1@nope\n":       "p.yaml:2: path \"8.8.8.8\"",
		"paths:\n  8.8.8.8:\n    - 1.1.1.1@2ms\n": "want a string",
	} {
		_, _, err := Load(writeFile(t, "p.yaml", in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want it to contain %q", in, err, want)
		}
	}
}

func TestLoadTCP(t *testing.T) {
	cfg, _, err := Load(writeFile(t, "t.yaml", `tcp:
  10.99.0.1:5432: timeout
  10.99.0.2:5432: 5ms
  10.99.0.3:443: refuse for 3 requests; 20ms
  "[fd99::1]:443": delay=200ms,jitter=20ms
  10.99.0.0/24:
    delay: 50ms
    refuse: true
  10.99.0.5:80:
    phases:
      - {spec: refuse, count: 2}
      - spec: 10ms
  db.example.com:5432: loss=30%
  10.99.0.6:80: 10ms,after=close
  10.99.0.7:80:
    delay: 1ms
    after: reset
`))
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := cfg.SortedTCP("t.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range tcp {
		got = append(got, r.Target.String()+"="+(rule.TCPScenario{Scenario: r.Scenario}).String())
	}
	want := []string{
		"10.99.0.0/24=delay=50ms,refuse",
		"10.99.0.1/32:5432=timeout",
		"10.99.0.2/32:5432=delay=5ms",
		"10.99.0.3/32:443=delay=0s,refuse for 3 requests; delay=20ms",
		"10.99.0.5/32:80=delay=0s,refuse for 2 requests; delay=10ms",
		"10.99.0.6/32:80=delay=10ms,after=close",
		"10.99.0.7/32:80=delay=1ms,after=reset",
		"[fd99::1/128]:443=delay=200ms,jitter=20ms",
		"db.example.com:5432=delay=0s,loss=30%",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("tcp =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for in, want := range map[string]string{
		"tcp:\n  fd99::1:443: 10ms\n":                            "t.yaml:2: tcp \"fd99::1:443\": ", // brackets needed
		"tcp:\n  10.99.0.1:443: pattern=.q\n":                    "t.yaml:2: tcp \"10.99.0.1:443\"", // not a rule
		"tcp:\n  10.99.0.1:443: 10ms\n  10.99.0.1/32:443: 5ms\n": "same target",                     // duplicates
		"tcp:\n  - 10.99.0.1:443\n":                              "tcp must be a mapping",
		"rules:\n  10.99.0.1: refuse\n":                          "TCP and UDP rules", // ping rules do not refuse
		"default: delay=1ms,pattern=.r\n":                        "TCP and UDP rules",
		"rules:\n  10.99.0.1: {refuse: true}\n":                  "TCP and UDP rules",
		"rules:\n  10.99.0.1: {after: close}\n":                  "TCP rules",
		"tcp:\n  10.99.0.1:80: refuse,after=close\n":             "accepts none",
	} {
		_, _, err := Load(writeFile(t, "t.yaml", in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want it to contain %q", in, err, want)
		}
	}
}

func TestLoadDNS(t *testing.T) {
	cfg, _, err := Load(writeFile(t, "d.yaml", `dns:
  API.example.com.: nxdomain
  backup.example.com: servfail for 2 requests; 10ms
  github.com:
    delay: 2s
  slow.example: {phases: [{spec: timeout, count: 1}, {spec: 5ms}]}
`))
	if err != nil {
		t.Fatal(err)
	}
	rules, err := cfg.SortedDNS("d.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rules {
		got = append(got, r.Name+"="+(rule.DNSScenario{Scenario: r.Scenario}).String())
	}
	want := []string{
		"api.example.com=delay=0s,nxdomain",
		"backup.example.com=delay=0s,servfail for 2 requests; delay=10ms",
		"github.com=delay=2s",
		"slow.example=timeout for 1 request; delay=5ms",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("dns =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for in, want := range map[string]string{
		"dns:\n  bad..name: nxdomain\n":                         "dns \"bad..name\"",
		"dns:\n  a.example: refuse\n":                           "TCP and UDP rules",
		"dns:\n  a.example: nxdomain\n  A.example.: servfail\n": "same name",
		"rules:\n  10.99.0.1: nxdomain\n":                       "DNS rules",
		"dns:\n  - a.example\n":                                 "dns",
		"dns:\n  a.example/AAAA: nxdomain\n":                    "not supported yet",
	} {
		_, _, err := Load(writeFile(t, "d.yaml", in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want it to contain %q", in, err, want)
		}
	}
}

// The bytes of send= and reply= in both forms of a configuration file:
// the mapping takes text, with YAML's escapes in double quotes, and the
// text form in single quotes reaches pingmock as written.
func TestLoadTCPData(t *testing.T) {
	cfg, _, err := Load(writeFile(t, "d.yaml", `tcp:
  10.99.0.1:80:
    reply: "HTTP/1.1 503 x\r\n\r\n"
    after: close
  10.99.0.2:80: 'send="\xff\x00",limit=5,after=reset'
  10.99.0.3:80: {send: "a,b;c", wait: 1s}
`))
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := cfg.SortedTCP("d.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]rule.Spec{}
	for _, r := range tcp {
		got[r.Target.String()] = r.Scenario.Phases[0].Spec
	}
	if sp := got["10.99.0.1/32:80"]; string(sp.Reply) != "HTTP/1.1 503 x\r\n\r\n" || sp.After != rule.AfterClose {
		t.Errorf("mapping: %+v", sp)
	}
	if sp := got["10.99.0.2/32:80"]; string(sp.Send) != "\xff\x00" || sp.Limit != 5 {
		t.Errorf("text form: %+v", sp)
	}
	if sp := got["10.99.0.3/32:80"]; string(sp.Send) != "a,b;c" || sp.Wait != time.Second {
		t.Errorf("flow mapping: %+v", sp)
	}
}

func TestLoadUDP(t *testing.T) {
	cfg, _, err := Load(writeFile(t, "u.yaml", "udp:\n  10.99.0.1:514: refuse\n  10.99.0.2:123: {refuse: true, delay: 5ms}\n  \"[fd99::1]:8125\": timeout for 2 requests; unreachable\n"))
	if err != nil {
		t.Fatal(err)
	}
	udp, err := cfg.SortedUDP("u.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range udp {
		got = append(got, r.Target.String()+"="+(rule.UDPScenario{Scenario: r.Scenario}).String())
	}
	if want := "10.99.0.1/32:514=delay=0s,refuse 10.99.0.2/32:123=delay=5ms,refuse [fd99::1/128]:8125=timeout for 2 requests; delay=0s,unreachable"; strings.Join(got, " ") != want {
		t.Errorf("udp = %v\n want %s", got, want)
	}
	for in, want := range map[string]string{
		"udp:\n  10.99.0.1:514: 20ms\n":        "t.yaml:2: udp",
		"udp:\n  - 10.99.0.1:514\n":            "udp must be a mapping",
		"udp:\n  10.99.0.1:514: after=close\n": "TCP rules",
	} {
		if _, _, err := Load(writeFile(t, "t.yaml", in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", in, err, want)
		}
	}
}
