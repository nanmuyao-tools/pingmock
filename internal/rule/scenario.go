package rule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Phase is one step of a Scenario. A phase ends after For has elapsed or
// after Count requests have been handled; with neither set it never ends.
type Phase struct {
	Spec  Spec
	For   time.Duration
	Count int
}

func (p Phase) bounded() bool { return p.For > 0 || p.Count > 0 }

// Scenario is a sequence of phases applied to a target. A plain rule such
// as "50ms" is a scenario with a single unbounded phase.
type Scenario struct {
	Phases []Phase
	// Repeat restarts the first phase after the last one ends. Without it
	// the last phase stays in effect once reached.
	Repeat bool
}

// Single returns a scenario that applies sp forever.
func Single(sp Spec) Scenario { return Scenario{Phases: []Phase{{Spec: sp}}} }

// ParseScenario parses phases separated by ";", each optionally bounded by
// "for DURATION" or "for N pings", plus an optional final "repeat":
//
//	50ms for 3 pings; delay=500ms,jitter=50ms for 10s; timeout
//	20ms for 5s; timeout for 2s; repeat
func ParseScenario(s string) (Scenario, error) { return parseScenario(s, protoPing) }

// parseScenario parses a scenario; tcp allows the settings of TCP rules.
func parseScenario(s string, kind proto) (Scenario, error) {
	var sc Scenario
	if strings.TrimSpace(s) == "" {
		return Scenario{}, fmt.Errorf("empty rule")
	}
	parts := splitUnquoted(s, ';')
	if last := strings.TrimSpace(parts[len(parts)-1]); strings.EqualFold(last, "repeat") {
		sc.Repeat = true
		parts = parts[:len(parts)-1]
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return Scenario{}, fmt.Errorf("invalid rule %q: empty phase", s)
		}
		var p Phase
		specText := part
		if i := lastIndexUnquoted(part, " for "); i >= 0 {
			specText = part[:i]
			if err := p.parseBound(part[i+len(" for "):]); err != nil {
				return Scenario{}, fmt.Errorf("invalid rule %q: %w", s, err)
			}
		}
		sp, err := parseSpec(specText, kind)
		if err != nil {
			return Scenario{}, err
		}
		p.Spec = sp
		sc.Phases = append(sc.Phases, p)
	}
	if err := sc.validateFor(kind); err != nil {
		return Scenario{}, fmt.Errorf("invalid rule %q: %w", s, err)
	}
	return sc, nil
}

func (p *Phase) parseBound(s string) error {
	s = strings.TrimSpace(s)
	if num, unit, ok := strings.Cut(s, " "); ok {
		switch strings.TrimSpace(unit) {
		case "ping", "pings", "request", "requests", "connection", "connections", "query", "queries":
			n, err := strconv.Atoi(num)
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid request count %q", num)
			}
			p.Count = n
			return nil
		}
		return fmt.Errorf("invalid phase length %q: want a duration such as 10s or a count such as 5 pings", s)
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return fmt.Errorf("invalid phase length %q: want a duration such as 10s or a count such as 5 pings", s)
	}
	p.For = d
	return nil
}

func (sc Scenario) validate() error { return sc.validateFor(protoPing) }

func (sc Scenario) validateFor(kind proto) error {
	if len(sc.Phases) == 0 {
		return fmt.Errorf("no phases")
	}
	for i, p := range sc.Phases {
		if p.For > 0 && p.Count > 0 {
			return fmt.Errorf("phase %d has both a duration and a count", i+1)
		}
		if p.For < 0 || p.Count < 0 {
			return fmt.Errorf("phase %d has a negative length", i+1)
		}
		if err := p.Spec.validateFor(kind); err != nil {
			return fmt.Errorf("phase %d: %w", i+1, err)
		}
		last := i == len(sc.Phases)-1
		if !p.bounded() && (!last || sc.Repeat) {
			return fmt.Errorf("phase %d needs a length (\"for 10s\" or \"for 5 pings\"): only the last phase of a non-repeating rule may last forever", i+1)
		}
	}
	return nil
}

// Cycle returns the total duration of one pass when every phase is
// time-bounded, or 0 otherwise.
func (sc Scenario) Cycle() time.Duration {
	var total time.Duration
	for _, p := range sc.Phases {
		if p.For <= 0 {
			return 0
		}
		total += p.For
	}
	return total
}

// String returns the scenario in the text form accepted by ParseScenario.
func (sc Scenario) String() string { return sc.format("ping") }

// format writes the scenario with unit as the name of what phases count.
func (sc Scenario) format(unit string) string {
	parts := make([]string, 0, len(sc.Phases)+1)
	for _, p := range sc.Phases {
		s := p.Spec.String()
		switch {
		case p.For > 0:
			s += " for " + p.For.String()
		case p.Count == 1:
			s += " for 1 " + unit
		case p.Count > 1:
			s += " for " + strconv.Itoa(p.Count) + " " + unit + "s"
		}
		parts = append(parts, s)
	}
	if sc.Repeat {
		parts = append(parts, "repeat")
	}
	return strings.Join(parts, "; ")
}

// TCPScenario is the scenario of a TCP rule. It accepts what a Scenario
// accepts, plus the settings only TCP rules have ("refuse", "after=", and
// 'r' in a pattern), and its phases count requests rather than pings.
type TCPScenario struct{ Scenario }

// ParseTCPScenario parses the scenario of a TCP rule, such as
// "refuse for 3 requests; 20ms".
func ParseTCPScenario(s string) (TCPScenario, error) {
	sc, err := parseScenario(s, protoTCP)
	return TCPScenario{sc}, err
}

func (sc TCPScenario) String() string { return sc.format("request") }

// DNSScenario is the scenario of a DNS rule. It accepts delays, loss and
// patterns as a Scenario does, plus the settings only DNS rules have
// ("nxdomain", "servfail", and 'n' and 's' in a pattern), and its phases
// count queries, printed as requests.
type DNSScenario struct{ Scenario }

// ParseDNSScenario parses the scenario of a DNS rule, such as
// "servfail for 2 requests; 10ms".
func ParseDNSScenario(s string) (DNSScenario, error) {
	sc, err := parseScenario(s, protoDNS)
	return DNSScenario{sc}, err
}

func (sc DNSScenario) String() string { return sc.format("request") }

// UDPScenario is the scenario of a UDP rule: how datagrams to an address
// and port fail, as refuse (ICMP port unreachable), unreachable, timeout
// and 'x', 'u' and 'r' in a pattern say. Its phases count datagrams,
// printed as requests.
type UDPScenario struct{ Scenario }

// ParseUDPScenario parses the scenario of a UDP rule, such as
// "timeout for 2 requests; refuse".
func ParseUDPScenario(s string) (UDPScenario, error) {
	sc, err := parseScenario(s, protoUDP)
	return UDPScenario{sc}, err
}

func (sc UDPScenario) String() string { return sc.format("request") }
