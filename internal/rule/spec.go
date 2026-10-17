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
}

// unreachCode returns the code to send for an unreachable outcome.
func (sp Spec) unreachCode() UnreachCode {
	if sp.Unreachable != UnreachNone {
		return sp.Unreachable
	}
	return UnreachHost
}

// ParseSpec parses either a bare duration ("120ms") or a comma-separated
// list of settings: "delay=120ms,jitter=10ms,loss=5%", "delay=50ms,pattern=..x"
// or "delay=20ms,unreachable=admin". The keyword "timeout" means no answer at
// all, and a bare "unreachable" means host unreachable.
func ParseSpec(s string) (Spec, error) {
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
			sp.Pattern, err = parsePattern(val)
		case "unreachable":
			sp.Unreachable, err = parseUnreachCode(val)
		default:
			err = fmt.Errorf("unknown key %q", key)
		}
		if err != nil {
			return Spec{}, fmt.Errorf("invalid spec %q: %w", s, err)
		}
	}
	if err := sp.validate(); err != nil {
		return Spec{}, fmt.Errorf("invalid spec %q: %w", s, err)
	}
	return sp, nil
}

func (sp Spec) validate() error {
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
		if _, err := parsePattern(sp.Pattern); err != nil {
			return err
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

// parsePattern validates a pattern made of '.' (reply), 'x' (drop) and
// 'u' (unreachable).
func parsePattern(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", fmt.Errorf("empty pattern")
	}
	if strings.Trim(s, ".xu") != "" {
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
	switch {
	case sp.Unreachable == UnreachHost:
		parts = append(parts, "unreachable")
	case sp.Unreachable != UnreachNone:
		parts = append(parts, "unreachable="+sp.Unreachable.String())
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
		}
	case sp.Loss > 0 && rng.Float64() < sp.Loss:
		return Drop, UnreachNone, 0
	case sp.Unreachable != UnreachNone:
		out = Unreachable
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
