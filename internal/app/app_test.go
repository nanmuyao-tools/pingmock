package app

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

func TestParseSettingsHosts(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(cfg, []byte("hosts:\n  api.example.com: 10.99.0.1\n  v6.example.com: fd99::7\n"), 0o644)
	s, _, err := parse(t, "-config", cfg, "-host", "api.example.com=10.99.0.2", "-host", "db.example.com=10.99.0.3")
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, h := range s.hosts {
		got = append(got, h.Name+"="+h.Addr.String())
	}
	// The file's hosts first, in name order; a -host for the same name
	// replaces the address, a new one is appended.
	if strings.Join(got, " ") != "api.example.com=10.99.0.2 v6.example.com=fd99::7 db.example.com=10.99.0.3" {
		t.Errorf("hosts = %v", got)
	}
	if s, _, _ := parse(t); len(s.hosts) != 0 {
		t.Errorf("hosts without -host = %v", s.hosts)
	}
	for _, bad := range []string{"nope", "=10.99.0.1", "api.example.com=", "api.example.com=10.99.0", "10.99.0.1=10.99.0.2",
		"a b=10.99.0.1", "-x=10.99.0.1", "api.example.com=fe80::1%eth0"} {
		if _, _, err := parse(t, "-host", bad); err == nil {
			t.Errorf("-host %q: expected error", bad)
		}
	}
}

func TestParseSettingsNameRules(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(cfg, []byte("rules:\n  db.example.com: 30ms\n  10.99.0.253: 1ms\nhosts:\n  fixed.example.com: 10.99.0.7\n"), 0o644)
	s, _, err := parse(t, "-config", cfg, "-rule", "api.example.com=20ms", "-rule", "fixed.example.com=timeout", "-rule", "DB.example.com=40ms")
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, r := range s.rules {
		got = append(got, r.name+"@"+r.prefix.String()+"="+r.sc.String())
	}
	// Names get addresses from the top of the subnet down (.254 is the
	// device, .253 is taken by an explicit rule), in name order; a name
	// given with -host keeps that address; a -rule for a name the file
	// has replaces its rule.
	want := "@10.99.0.253/32=delay=1ms db.example.com@10.99.0.251/32=delay=40ms api.example.com@10.99.0.252/32=delay=20ms fixed.example.com@10.99.0.7/32=timeout"
	if strings.Join(got, " ") != want {
		t.Errorf("rules = %v\n  want %s", got, want)
	}
	hosts := []string{}
	for _, h := range s.hosts {
		hosts = append(hosts, h.Name+"="+h.Addr.String())
	}
	if strings.Join(hosts, " ") != "fixed.example.com=10.99.0.7 api.example.com=10.99.0.252 db.example.com=10.99.0.251" {
		t.Errorf("hosts = %v", hosts)
	}
	if !s.names() {
		t.Error("names() = false with name rules")
	}
	// Running out of addresses is an error, not a silent reuse.
	args := []string{"-addr", "10.99.0.254/30"} // .252 network, .253 free, .254 device, .255 broadcast
	args = append(args, "-rule", "a.example=1ms", "-rule", "b.example=1ms")
	if _, _, err := parse(t, args...); err == nil || !strings.Contains(err.Error(), "no free address") {
		t.Errorf("subnet exhausted: %v", err)
	}
}

func TestParseSettingsPaths(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(cfg, []byte("rules:\n  api.example.com: 30ms\npaths:\n  api.example.com: 10.0.0.1@2ms > *\n  8.8.8.8: 10.0.0.2@3ms\n"), 0o644)
	s, _, err := parse(t, "-config", cfg, "-path", "8.8.8.8=10.0.0.9@9ms", "-path", "db.example.com=10.0.0.3@1ms")
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, p := range s.paths {
		got = append(got, p.name+"@"+p.addr.String()+"="+p.path.String())
	}
	// The file's paths in target order, a -path for the same target
	// replacing one; names get the address of their rule, or one of
	// their own.
	want := "@8.8.8.8=10.0.0.9@delay=9ms api.example.com@10.99.0.253=10.0.0.1@delay=2ms > * db.example.com@10.99.0.252=10.0.0.3@delay=1ms"
	if strings.Join(got, " ") != want {
		t.Errorf("paths = %v\n  want %s", got, want)
	}
	for _, bad := range []string{"nope", "10.0.0.0/24=1.1.1.1@1ms", "8.8.8.8=", "8.8.8.8=x@1ms"} {
		if _, _, err := parse(t, "-path", bad); err == nil {
			t.Errorf("-path %q: expected error", bad)
		}
	}
}

func TestParseSettingsTCP(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(cfg, []byte("rules:\n  api.example.com: 30ms\ntcp:\n  10.99.0.1:5432: timeout\n  api.example.com:443: refuse\n"), 0o644)
	s, _, err := parse(t, "-config", cfg,
		"-tcp", "10.99.0.1:5432=5ms",
		"-tcp", "[fd99::1]:443=refuse for 2 requests; 1ms",
		"-tcp", "10.99.0.0/24=pattern=.r",
		"-tcp", "db.example.com:5432=loss=50%")
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, r := range s.tcp {
		got = append(got, r.name+"@"+r.prefix.String()+":"+itoa(r.port)+"="+r.sc.String())
	}
	// The file's rules first, a -tcp for the same target and port
	// replacing one; names get the address of their ping rule, or one of
	// their own, as for paths.
	want := "@10.99.0.1/32:5432=delay=5ms api.example.com@10.99.0.253/32:443=delay=0s,refuse " +
		"@fd99::1/128:443=delay=0s,refuse for 2 pings; delay=1ms @10.99.0.0/24:0=delay=0s,pattern=.r " +
		"db.example.com@10.99.0.252/32:5432=delay=0s,loss=50%"
	if strings.Join(got, " ") != want {
		t.Errorf("tcp = %v\n  want %s", got, want)
	}
	if !s.names() {
		t.Error("names() = false with TCP rules for names, so root mode would not refuse them")
	}
	for _, bad := range []string{"nope", "10.99.0.1:5432", "fd99::1:443=5ms", "10.99.0.1:0=5ms", "10.99.0.1:443=pattern=.q", "10.99.0.1:443="} {
		if _, _, err := parse(t, "-tcp", bad); err == nil {
			t.Errorf("-tcp %q: expected error", bad)
		}
	}
	// Ping rules still refuse what only TCP rules have.
	for _, bad := range []string{"10.99.0.1=refuse", "10.99.0.1=pattern=.r"} {
		if _, _, err := parse(t, "-rule", bad); err == nil || !strings.Contains(err.Error(), "TCP") {
			t.Errorf("-rule %q: %v, want an error naming TCP rules", bad, err)
		}
	}
	if _, _, err := parse(t, "-default", "refuse"); err == nil {
		t.Error("-default refuse accepted")
	}
}

func itoa(n uint16) string { return strconv.Itoa(int(n)) }

func TestParseSettingsDNS(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(cfg, []byte("rules:\n  api.example.com: 30ms\ndns:\n  API.example.com.: nxdomain\n  db.example.com: {servfail: true}\n"), 0o644)
	s, _, err := parse(t, "-config", cfg, "-host", "kept.example=10.99.0.5",
		"-dns", "db.example.com=delay=2s",
		"-dns", "github.com=servfail for 2 requests; 10ms")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range s.dns {
		got = append(got, r.Name+"="+r.Scenario.String())
	}
	// The file's rules first, names in canonical form, a -dns for the same
	// name replacing one.
	want := "api.example.com=delay=0s,nxdomain db.example.com=delay=2s github.com=delay=0s,servfail for 2 pings; delay=10ms"
	if strings.Join(got, " ") != want {
		t.Errorf("dns = %v\n  want %s", got, want)
	}
	// A name with a DNS rule stays out of /etc/hosts, though it still has
	// its mocked address for the DNS server.
	var inFile []string
	for _, h := range s.hostsFile() {
		inFile = append(inFile, h.Name)
	}
	if strings.Join(inFile, " ") != "kept.example" || len(s.hosts) != 2 {
		t.Errorf("hosts file %v, hosts %v", inFile, s.hosts)
	}
	// DNS rules alone switch name resolution on.
	s, _, _ = parse(t, "-dns", "github.com=nxdomain")
	if !s.names() {
		t.Error("names() = false with only DNS rules")
	}
	for _, bad := range []string{"nope", "github.com", "bad..name=nxdomain", "github.com=refuse", "github.com=unreachable", "10.99.0.1=nxdomain", "api.example.com/AAAA=nxdomain"} {
		if _, _, err := parse(t, "-dns", bad); err == nil {
			t.Errorf("-dns %q: expected error", bad)
		}
	}
	for _, bad := range []string{"10.99.0.1=nxdomain", "10.99.0.1:80=servfail"} {
		flag := "-rule"
		if strings.Contains(bad, ":") {
			flag = "-tcp"
		}
		if _, _, err := parse(t, flag, bad); err == nil || !strings.Contains(err.Error(), "DNS") {
			t.Errorf("%s %q: %v, want an error naming DNS rules", flag, bad, err)
		}
	}
}

// What the API may set: names under exec with a DNS server, not names
// the command finds in its /etc/hosts.
func TestDNSTarget(t *testing.T) {
	s, _, _ := parse(t, "-host", "fixed.example=10.99.0.5", "-dns", "ruled.example=0ms", "-rule", "ruled.example=5ms")
	if _, err := s.dnsTarget("github.com"); err == nil || !strings.Contains(err.Error(), "exec only") {
		t.Errorf("root mode: %v", err)
	}
	s.exec = true
	for name, want := range map[string]string{"GitHub.com.": "", "ruled.example": "", "fixed.example": "/etc/hosts", "bad..name": "invalid"} {
		n, err := s.dnsTarget(name)
		switch {
		case want == "" && (err != nil || n != dnsCanon(name)):
			t.Errorf("%s: %q, %v", name, n, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%s: %v, want an error containing %q", name, err, want)
		}
	}
	plain, _, _ := parse(t)
	plain.exec = true
	if _, err := plain.dnsTarget("github.com"); err == nil || !strings.Contains(err.Error(), "-dns github.com=0ms") {
		t.Errorf("exec without a DNS server: %v", err)
	}
}

func dnsCanon(n string) string { return strings.ToLower(strings.TrimSuffix(n, ".")) }

func TestWithoutNames(t *testing.T) {
	in := "127.0.0.1\tlocalhost\n# a comment\n10.0.0.1 api.example.com\n10.0.0.2 db.example.com DB.example.com. other # note\n"
	want := "127.0.0.1\tlocalhost\n# a comment\n# pingmock exec: has a DNS rule: 10.0.0.1 api.example.com\n10.0.0.2\tother # note\n"
	if got := withoutNames(in, []string{"api.example.com", "db.example.com"}); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if got := withoutNames(in, nil); got != in {
		t.Errorf("without names: %q", got)
	}
}

// A DNS rule is refused as root, with a hint, as names are.
func TestServerRefusesDNS(t *testing.T) {
	if os.Getenv("PINGMOCK_TEST_MAIN") == "1" {
		os.Exit(Main([]string{"-api", "", "-dns", "github.com=nxdomain"}))
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestServerRefusesDNS")
	cmd.Env = append(os.Environ(), "PINGMOCK_TEST_MAIN=1")
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 || !strings.Contains(string(out), "only work with pingmock exec") {
		t.Errorf("exit %v, output %s", err, out)
	}
}
