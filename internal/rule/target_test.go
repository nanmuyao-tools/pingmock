package rule

import (
	"strings"
	"testing"
)

func TestParsePrefix(t *testing.T) {
	cases := map[string]string{
		"8.8.8.8":              "8.8.8.8/32",
		"10.99.0.130/25":       "10.99.0.128/25", // masked
		"fd99::1":              "fd99::1/128",
		"FD99::0:1":            "fd99::1/128",
		"fd99::abcd/64":        "fd99::/64",
		"2001:4860:4860::8888": "2001:4860:4860::8888/128",
		"::ffff:10.99.0.1":     "10.99.0.1/32",
		"::ffff:10.99.0.0/120": "10.99.0.0/24",
		" 10.99.0.1 ":          "10.99.0.1/32",
	}
	for in, want := range cases {
		if p, err := ParsePrefix(in); err != nil || p.String() != want {
			t.Errorf("ParsePrefix(%q) = %v, %v; want %s", in, p, err, want)
		}
	}
	for _, bad := range []string{"nope", "10.99.0.300", "fd99::1/129", "fe80::1%eth0", "::ffff:10.0.0.0/64", ""} {
		if _, err := ParsePrefix(bad); err == nil {
			t.Errorf("ParsePrefix(%q): expected error", bad)
		}
	}
}

func TestParseTarget(t *testing.T) {
	for in, want := range map[string]string{
		"10.99.0.1": "10.99.0.1/32", "10.99.0.0/24": "10.99.0.0/24", "fd99::1": "fd99::1/128",
		"api.example.com": "api.example.com", "API.Example.COM.": "api.example.com", "api": "api", "my_host.local": "my_host.local",
	} {
		got, err := ParseTarget(in)
		if err != nil || got.String() != want {
			t.Errorf("ParseTarget(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "10.99.0", "10.99.0.1/33", "fe80::1%eth0", "-api.example.com", "api-.example.com", "a..b", "a b", "api.example.com/24", "1.2.3", "x" + strings.Repeat("a", 63)} {
		if _, err := ParseTarget(bad); err == nil {
			t.Errorf("ParseTarget(%q): expected error", bad)
		}
	}
}
