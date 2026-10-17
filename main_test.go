package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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

func parse(t *testing.T, args ...string) (*settings, []string, error) {
	t.Helper()
	return parseSettings("pingmock", args, serverUsage, io.Discard)
}

func TestParseSettingsDefaults(t *testing.T) {
	s, rest, err := parse(t)
	if err != nil || len(rest) != 0 {
		t.Fatalf("parse: %v %v", rest, err)
	}
	if s.dev != "pingmock0" || s.addr.String() != "10.99.0.254/24" || s.addr6.String() != "fd99::fe/64" || s.addr6Explicit ||
		s.api != "127.0.0.1:9100" || s.history != 1000 || s.def.String() != "delay=0s" {
		t.Errorf("defaults = %+v", s)
	}
}

func TestParseSettingsConfigAndOverrides(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(cfg, []byte(`{"seed": 7, "api": "", "addr6": "", "default": "timeout",
		"rules": {"10.99.0.1": "20ms", "10.99.0.2": "30ms"}}`), 0o644)
	s, _, err := parse(t, "-config", cfg, "-rule", "10.99.0.2=99ms", "-rule", "fd99::1=5ms", "-seed", "9")
	if err != nil {
		t.Fatal(err)
	}
	if s.seed != 9 || s.api != "" || s.addr6.IsValid() || !s.addr6Explicit || s.def.String() != "timeout" {
		t.Errorf("settings = %+v", s)
	}
	got := []string{}
	for _, r := range s.rules {
		got = append(got, r.prefix.String()+"="+r.sc.String())
	}
	if strings.Join(got, " ") != "10.99.0.1/32=delay=20ms 10.99.0.2/32=delay=99ms fd99::1/128=delay=5ms" {
		t.Errorf("rules = %v", got)
	}
	if s, _, _ := parse(t, "-config", cfg); s.seed != 7 {
		t.Errorf("seed from config = %d, want 7", s.seed)
	}
}

func TestParseSettingsRest(t *testing.T) {
	_, rest, err := parse(t, "-rule", "10.99.0.1=5ms", "--", "ping", "-c", "1", "10.99.0.1")
	if err != nil || strings.Join(rest, " ") != "ping -c 1 10.99.0.1" {
		t.Errorf("rest = %q, %v", rest, err)
	}
}

func TestParseSettingsErrors(t *testing.T) {
	for _, args := range [][]string{
		{"-addr", "fd99::1/64"},
		{"-addr6", "10.0.0.1/24"},
		{"-history", "-1"},
		{"-default", "5ms; 6ms"},
		{"-rule", "nope"},
		{"-config", "/does/not/exist.json"},
		{"-bogus"},
	} {
		if _, _, err := parse(t, args...); err == nil {
			t.Errorf("parse(%q): expected error", args)
		}
	}
	if _, _, err := parse(t, "-h"); !isHelp(err) {
		t.Errorf("-h: %v, want the help error", err)
	}
}

func TestExitStatus(t *testing.T) {
	if got := exitStatus(exec.Command("sh", "-c", "exit 3").Run()); got != 3 {
		t.Errorf("exit 3: %d", got)
	}
	if got := exitStatus(exec.Command("sh", "-c", "kill -TERM $$").Run()); got != 128+int(syscall.SIGTERM) {
		t.Errorf("killed by SIGTERM: %d, want %d", got, 128+int(syscall.SIGTERM))
	}
	if got := exitStatus(nil); got != 0 {
		t.Errorf("success: %d", got)
	}
}

func TestExplain(t *testing.T) {
	tunErr := errors.New("x")
	if explain(tunErr, false) != tunErr {
		t.Error("explain changed an error for root")
	}
	eacces := &os.PathError{Op: "open", Path: "/dev/net/tun", Err: syscall.EACCES}
	if msg := explain(eacces, true).Error(); !strings.Contains(msg, "chmod 0666 /dev/net/tun") || !errors.Is(explain(eacces, true), syscall.EACCES) {
		t.Errorf("EACCES advice: %s", msg)
	}
	if msg := explain(errors.New("RTNETLINK answers: Operation not permitted"), true).Error(); !strings.Contains(msg, "apparmor_restrict_unprivileged_userns") {
		t.Errorf("EPERM advice: %s", msg)
	}
}
