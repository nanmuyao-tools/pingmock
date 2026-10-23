package rule

import "testing"

// The parsers take what users type: they must never panic, and what they
// accept must print back to text they accept again, unchanged.
func FuzzParseScenario(f *testing.F) {
	for _, s := range []string{
		"50ms", "delay=120ms,jitter=10ms,loss=5%", "30ms for 10 pings; timeout", "20ms for 5s; timeout for 2s; repeat",
		"pattern=..x", "unreachable=admin", "ttl=128", "refuse for 3 requests; 20ms", `send="SSH-2.0\r\n"`,
		`reply="a,b;c for 2 requests",after=close`, "limit=1000,after=reset", "nxdomain", "servfail for 2 requests; 10ms",
		"pattern=.ns", "timeout for 1 request; refuse", "pattern=xxr", `wait=2s,reply="\x00\xff"`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		check := func(name string, parse func(string) (string, error)) {
			out, err := parse(s)
			if err != nil {
				return
			}
			again, err := parse(out)
			if err != nil || again != out {
				t.Fatalf("%s(%q) = %q, which parses to %q, %v", name, s, out, again, err)
			}
		}
		check("ParseScenario", func(s string) (string, error) { sc, err := ParseScenario(s); return sc.String(), err })
		check("ParseTCPScenario", func(s string) (string, error) { sc, err := ParseTCPScenario(s); return sc.String(), err })
		check("ParseUDPScenario", func(s string) (string, error) { sc, err := ParseUDPScenario(s); return sc.String(), err })
		check("ParseDNSScenario", func(s string) (string, error) { sc, err := ParseDNSScenario(s); return sc.String(), err })
	})
}

func FuzzParsePathAndTargets(f *testing.F) {
	for _, s := range []string{
		"192.168.1.1@2ms > * > 10.0.0.1@20ms", "10.0.0.1@delay=8ms,jitter=2ms,cut=5%", "*", "fd99::1@1ms",
		"10.99.0.1:443", "[fd99::1]:443", "[fd99::/64]", "db.example.com:5432", "10.99.0.0/24", "fd99::1:443",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if p, err := ParsePath(s); err == nil {
			if again, err := ParsePath(p.String()); err != nil || again.String() != p.String() {
				t.Fatalf("ParsePath(%q) = %q, which parses to %q, %v", s, p, again, err)
			}
		}
		if tt, err := ParseTCPTarget(s); err == nil && tt.Name == "" {
			if again, err := ParseTCPTarget(tt.String()); err != nil || again.String() != tt.String() {
				t.Fatalf("ParseTCPTarget(%q) = %q, which parses to %q, %v", s, tt, again, err)
			}
		}
		ParseTarget(s)
		ParsePrefix(s)
	})
}
