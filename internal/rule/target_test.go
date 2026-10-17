package rule

import "testing"

func TestParsePrefix(t *testing.T) {
	if p, err := ParsePrefix("8.8.8.8"); err != nil || p.String() != "8.8.8.8/32" {
		t.Errorf("single address: %v %v", p, err)
	}
	if p, err := ParsePrefix("10.99.0.130/25"); err != nil || p.String() != "10.99.0.128/25" {
		t.Errorf("CIDR should be masked: %v %v", p, err)
	}
	for _, bad := range []string{"::1", "2001:db8::/32", "nope"} {
		if _, err := ParsePrefix(bad); err == nil {
			t.Errorf("ParsePrefix(%q): expected error", bad)
		}
	}
}
