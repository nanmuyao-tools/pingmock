package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestLoadOutside(t *testing.T) {
	for in, want := range map[string]string{
		"outside: true\n":  "true []",
		"outside: false\n": "false []",
		"outside:\n  ports: [5432, 53/udp, 6379/tcp]\n": "true [5432/tcp 53/udp 6379/tcp]",
		"outside: {ports: []}\n":                        "true []",
		"outside: {}\n":                                 "true []",
	} {
		cfg, _, err := Load(writeFile(t, "o.yaml", in))
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		got := "nil"
		if cfg.Outside != nil {
			got = fmt.Sprint(cfg.Outside.On, " ", cfg.Outside.Ports)
		}
		if got != want {
			t.Errorf("%q: outside = %s, want %s", in, got, want)
		}
	}
	if cfg, _, _ := Load(writeFile(t, "o.yaml", "seed: 1\n")); cfg.Outside != nil {
		t.Errorf("no key: outside = %+v, want nil", cfg.Outside)
	}
	for in, want := range map[string]string{
		"outside: yes please\n":          "outside",
		"outside:\n  ports: [0]\n":       "invalid port",
		"outside:\n  ports: [70000]\n":   "invalid port",
		"outside:\n  ports: [53/sctp]\n": "invalid port",
		"outside:\n  port: [5432]\n":     "unknown field",
		"outside:\n  ports: 5432\n":      "outside",
	} {
		_, _, err := Load(writeFile(t, "o.yaml", in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want it to contain %q", in, err, want)
		}
	}
}

func TestParseOutsidePort(t *testing.T) {
	for in, want := range map[string]string{"5432": "5432/tcp", " 53/udp ": "53/udp", "80/tcp": "80/tcp", "65535": "65535/tcp", "0080": "80/tcp"} {
		if got, err := ParseOutsidePort(in); err != nil || got != want {
			t.Errorf("ParseOutsidePort(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0", "65536", "-1", "http", "53/", "/udp", "53/UDP", "53/icmp", "1.5"} {
		if got, err := ParseOutsidePort(bad); err == nil {
			t.Errorf("ParseOutsidePort(%q) = %q, want an error", bad, got)
		}
	}
}
