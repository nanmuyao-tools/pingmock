package rule

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
)

// Spec describes how the mock answers a single echo request.
type Spec struct {
	Delay  time.Duration // base round-trip delay added before answering
	Jitter time.Duration // the delay is uniform in [Delay-Jitter, Delay+Jitter]
	Loss   float64       // probability in [0,1] that a request gets no answer
	// Pattern is a deterministic alternative to Loss, applied to successive
	// requests and repeated cyclically: '.' replies, 'x' drops and 'u'
	// answers with destination unreachable.
	Pattern string
	// Unreachable answers with destination unreachable instead of an echo
	// reply. With a Pattern it only selects the code used for 'u'.
	Unreachable UnreachCode
	// TTL is the IP TTL (IPv6 hop limit) of everything the target sends;
	// 0 means DefaultTTL.
	TTL uint8
	// Refuse answers a TCP connection attempt with a reset, so that the
	// connection is refused. TCP rules only.
	Refuse bool
	// After is what happens to a connection once it is accepted: it
	// stays open (the default), is closed or is reset. TCP rules only.
	After After
}

// DefaultTTL is the TTL of answers whose rule does not set one: what Linux
// hosts send, so ping shows ttl=64 as it would for a nearby machine.
const DefaultTTL = 64

// unreachCode returns the code to send for an unreachable outcome.
func (sp Spec) unreachCode() UnreachCode {
	if sp.Unreachable != UnreachNone {
		return sp.Unreachable
	}
	return UnreachHost
}

// EffectiveTTL returns the TTL to send: TTL, or DefaultTTL if unset.
func (sp Spec) EffectiveTTL() uint8 {
	if sp.TTL != 0 {
		return sp.TTL
	}
	return DefaultTTL
}

// ParseSpec parses either a bare duration ("120ms") or a comma-separated
// list of settings: "delay=120ms,jitter=10ms,loss=5%", "delay=50ms,pattern=..x"
// or "delay=20ms,unreachable=admin". The keyword "timeout" means no answer at
// all, and a bare "unreachable" means host unreachable.
func ParseSpec(s string) (Spec, error) { return parseSpec(s, false) }

// ParseTCPSpec parses a spec of a TCP rule: what ParseSpec accepts, plus
// "refuse", "after=close|reset|hang" and the pattern letter 'r'.
func ParseTCPSpec(s string) (Spec, error) { return parseSpec(s, true) }

// parseSpec parses a spec; tcp allows the settings of TCP rules.
func parseSpec(s string, tcp bool) (Spec, error) {
	var sp Spec
	s = strings.TrimSpace(s)
	if s == "" {
		return sp, fmt.Errorf("empty spec")
	}
	if s == "timeout" {
		sp.Loss = 1
		return sp, nil
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		key, val, found := strings.Cut(part, "=")
		if !found {
			key, val = "delay", part
			if strings.EqualFold(part, "unreachable") {
				key, val = "unreachable", ""
			}
			if strings.EqualFold(part, "refuse") {
				key = "refuse"
			}
		}
		var err error
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "delay":
			sp.Delay, err = parseDuration(val)
		case "jitter":
			sp.Jitter, err = parseDuration(val)
		case "loss":
			sp.Loss, err = parseLoss(val)
		case "pattern":
			sp.Pattern, err = parsePatternFor(val, tcp)
		case "unreachable":
			sp.Unreachable, err = parseUnreachCode(val)
		case "ttl":
			sp.TTL, err = parseTTL(val)
		case "after":
			if !tcp {
				err = fmt.Errorf("after is for TCP rules (-tcp)")
			} else {
				sp.After, err = parseAfter(val)
			}
		case "refuse":
			switch {
			case !tcp:
				err = fmt.Errorf("refuse is for TCP rules (-tcp)")
			case found:
				err = fmt.Errorf("refuse takes no value")
			default:
				sp.Refuse = true
			}
		default:
			err = fmt.Errorf("unknown key %q", key)
		}
		if err != nil {
			return Spec{}, fmt.Errorf("invalid spec %q: %w", s, err)
		}
	}
	if err := sp.validateFor(tcp); err != nil {
		return Spec{}, fmt.Errorf("invalid spec %q: %w", s, err)
	}
	return sp, nil
}

func (sp Spec) validate() error { return sp.validateFor(false) }

// validateFor checks a spec; tcp allows the settings of TCP rules.
func (sp Spec) validateFor(tcp bool) error {
	if sp.Delay < 0 || sp.Jitter < 0 {
		return fmt.Errorf("negative duration")
	}
	if sp.Loss < 0 || sp.Loss > 1 {
		return fmt.Errorf("loss %g out of range [0,1]", sp.Loss)
	}
	if sp.Pattern != "" && sp.Loss > 0 {
		return fmt.Errorf("loss and pattern cannot be combined")
	}
	if sp.Pattern != "" {
		if _, err := parsePatternFor(sp.Pattern, tcp); err != nil {
			return err
		}
	}
	if sp.Refuse {
		switch {
		case !tcp:
			return fmt.Errorf("refuse is for TCP rules (-tcp)")
		case sp.Unreachable != UnreachNone:
			return fmt.Errorf("refuse and unreachable cannot be combined")
		case sp.Pattern != "":
			return fmt.Errorf("refuse and pattern cannot be combined; use r in the pattern")
		}
	}
	if sp.After != AfterHang {
		switch {
		case !tcp:
			return fmt.Errorf("after is for TCP rules (-tcp)")
		case sp.Refuse || sp.Unreachable != UnreachNone || (sp.Loss == 1 && sp.Pattern == "") ||
			(sp.Pattern != "" && !strings.Contains(sp.Pattern, ".")):
			return fmt.Errorf("after=%s applies to accepted connections, and this rule accepts none", sp.After)
		}
	}
	return nil
}

// parseDuration accepts Go durations ("1.5s", "120ms") and plain numbers,
// which are taken as milliseconds.
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		s = strconv.FormatFloat(f, 'f', -1, 64) + "ms"
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, fmt.Errorf("negative duration %s", s)
	}
	return d, nil
}

// parseLoss accepts "5%" or a fraction such as "0.05".
func parseLoss(s string) (float64, error) {
	s = strings.TrimSpace(s)
	pct := strings.HasSuffix(s, "%")
	f, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil {
		return 0, err
	}
	if pct {
		f /= 100
	}
	if f < 0 || f > 1 {
		return 0, fmt.Errorf("loss %s out of range", s)
	}
	return f, nil
}

// parseTTL accepts a TTL from 1 to 255.
func parseTTL(s string) (uint8, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 8)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("ttl %q must be a number from 1 to 255", s)
	}
	return uint8(n), nil
}

// parsePattern validates a pattern made of '.' (reply), 'x' (drop) and
// 'u' (unreachable).
func parsePattern(s string) (string, error) { return parsePatternFor(s, false) }

// parsePatternFor validates a pattern; tcp also allows 'r' (refuse).
func parsePatternFor(s string, tcp bool) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", fmt.Errorf("empty pattern")
	}
	if tcp {
		if strings.Trim(s, ".xur") != "" {
			return "", fmt.Errorf("pattern %q may only contain '.' (accept), 'x' (drop), 'u' (unreachable) and 'r' (refuse)", s)
		}
		return s, nil
	}
	if strings.Trim(s, ".xu") != "" {
		if strings.Trim(s, ".xur") == "" {
			return "", fmt.Errorf("pattern %q: 'r' (refuse) is for TCP rules (-tcp)", s)
		}
		return "", fmt.Errorf("pattern %q may only contain '.' (reply), 'x' (drop) and 'u' (unreachable)", s)
	}
	return s, nil
}

// String returns the spec in the text form accepted by ParseSpec.
func (sp Spec) String() string {
	if sp.Loss == 1 && sp.Pattern == "" {
		return "timeout"
	}
	parts := []string{"delay=" + sp.Delay.String()}
	if sp.Jitter > 0 {
		parts = append(parts, "jitter="+sp.Jitter.String())
	}
	if sp.Pattern != "" {
		parts = append(parts, "pattern="+sp.Pattern)
	} else if sp.Loss > 0 {
		parts = append(parts, "loss="+strconv.FormatFloat(sp.Loss*100, 'g', -1, 64)+"%")
	}
	if sp.Refuse {
		parts = append(parts, "refuse")
	}
	if sp.After != AfterHang {
		parts = append(parts, "after="+sp.After.String())
	}
	switch {
	case sp.Unreachable == UnreachHost:
		parts = append(parts, "unreachable")
	case sp.Unreachable != UnreachNone:
		parts = append(parts, "unreachable="+sp.Unreachable.String())
	}
	if sp.TTL != 0 {
		parts = append(parts, "ttl="+strconv.Itoa(int(sp.TTL)))
	}
	return strings.Join(parts, ",")
}

// Decide determines the outcome for the idx-th request handled under this
// spec, drawing any randomness from rng. For Unreachable it also returns
// the code to send.
func (sp Spec) Decide(idx int, rng *rand.Rand) (Outcome, UnreachCode, time.Duration) {
	out := Reply
	switch {
	case sp.Pattern != "":
		switch sp.Pattern[idx%len(sp.Pattern)] {
		case 'x':
			return Drop, UnreachNone, 0
		case 'u':
			out = Unreachable
		case 'r':
			out = Refused
		}
	case sp.Loss > 0 && rng.Float64() < sp.Loss:
		return Drop, UnreachNone, 0
	case sp.Unreachable != UnreachNone:
		out = Unreachable
	case sp.Refuse:
		out = Refused
	}
	var code UnreachCode
	if out == Unreachable {
		code = sp.unreachCode()
	}
	d := sp.Delay
	if sp.Jitter > 0 {
		d += time.Duration(rng.Int64N(int64(2*sp.Jitter)+1)) - sp.Jitter
	}
	if d < 0 {
		d = 0
	}
	return out, code, d
}
