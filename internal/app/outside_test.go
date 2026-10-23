package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSettingsOutside(t *testing.T) {
	s, _, err := parse(t)
	if err != nil || s.outside || s.names() {
		t.Fatalf("default: outside %v, names %v, %v", s.outside, s.names(), err)
	}
	// -outside-port implies -outside, and outside access brings the
	// command's own DNS server.
	s, _, err = parse(t, "-outside-port", "5432", "-outside-port", "514/udp", "-outside-port", "5432/tcp")
	if err != nil || !s.outside || !s.names() || strings.Join(s.outsidePorts, " ") != "5432/tcp 514/udp" {
		t.Errorf("ports: outside %v, names %v, ports %v, %v", s.outside, s.names(), s.outsidePorts, err)
	}
	// The file's ports come first, flags add to them, and -outside=false
	// turns off what the file turned on.
	cfg := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(cfg, []byte("outside:\n  ports: [6379]\n"), 0o644)
	s, _, err = parse(t, "-config", cfg, "-outside-port", "5432")
	if err != nil || !s.outside || strings.Join(s.outsidePorts, " ") != "6379/tcp 5432/tcp" {
		t.Errorf("file and flag: outside %v, ports %v, %v", s.outside, s.outsidePorts, err)
	}
	os.WriteFile(cfg, []byte("outside: true\n"), 0o644)
	if s, _, err = parse(t, "-config", cfg, "-outside=false"); err != nil || s.outside {
		t.Errorf("-outside=false over the file: outside %v, %v", s.outside, err)
	}
	// The API's port is free once the API moves or is off, and 9100/udp
	// was never taken.
	for _, args := range [][]string{{"-api", "127.0.0.1:9200", "-outside-port", "9100"}, {"-api", "", "-outside-port", "9100"}, {"-outside-port", "9100/udp"}} {
		if _, _, err := parse(t, args...); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
	os.WriteFile(cfg, []byte("outside:\n  ports: [53/udp]\n"), 0o644)
	if _, _, err := parse(t, "-config", cfg); err == nil || !strings.Contains(err.Error(), "DNS server") {
		t.Errorf("port 53 in the file: %v", err)
	}
	for args, want := range map[string]string{
		"-outside-port 0":                   "invalid -outside-port",
		"-outside-port 53/sctp":             "invalid -outside-port",
		"-outside=false -outside-port 5432": "-outside=false",
		// Ports pingmock listens on in the command's network.
		"-outside-port 53/udp":                       "DNS server",
		"-outside-port 53":                           "DNS server",
		"-outside-port 9100":                         "control API",
		"-api 127.0.0.1:9200 -outside-port 9200/tcp": "control API",
	} {
		if _, _, err := parse(t, strings.Fields(args)...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want an error mentioning %q", args, err, want)
		}
	}
}

func TestPastaArgs(t *testing.T) {
	got := strings.Join(pastaArgs([]string{"5432/tcp", "53/udp", "6379/tcp"}, false, 42, "/tmp/p"), " ")
	// No forwarding but the host ports asked for: pasta's defaults would
	// publish the control API on the host.
	for _, want := range []string{"-t none -u none -T 5432,6379 -U 53 ", "-a 10.0.2.15 -n 24 -g 10.0.2.2", "-P /tmp/p"} {
		if !strings.Contains(got, want) {
			t.Errorf("pastaArgs = %s, want it to contain %q", got, want)
		}
	}
	if !strings.HasSuffix(got, " 42") || strings.Contains(got, "--runas") {
		t.Errorf("rootless pastaArgs = %s", got)
	}
	got = strings.Join(pastaArgs(nil, true, 7, "/tmp/p"), " ")
	if !strings.Contains(got, "-T none -U none") || !strings.HasSuffix(got, "--runas 0 7") {
		t.Errorf("root pastaArgs = %s", got)
	}
	if m := allIDs()[0]; m.ContainerID != 0 || m.HostID != 0 || m.Size < 1<<31-1 {
		t.Errorf("allIDs = %+v, want every ID mapped to itself", m)
	}
}

func TestServerRefusesOutside(t *testing.T) {
	if os.Getenv("PINGMOCK_TEST_MAIN") == "1" {
		os.Exit(Main([]string{"-api", "", "-outside"}))
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestServerRefusesOutside")
	cmd.Env = append(os.Environ(), "PINGMOCK_TEST_MAIN=1")
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 || !strings.Contains(string(out), "-outside") {
		t.Errorf("exit %v, output %s", err, out)
	}
}

func TestAPIFacts(t *testing.T) {
	if f := (&settings{}).apiFacts(); f != nil {
		t.Errorf("without outside access: %v", f)
	}
	o, _ := (&settings{outside: true}).apiFacts()["outside"].(map[string]any)
	if o["address"] != "10.0.2.15/24" || o["gateway"] != "10.0.2.2" || o["ports"] == nil {
		t.Errorf("outside facts %v; ports must be a list, not null", o)
	}
}
