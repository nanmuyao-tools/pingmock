package main

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
)

// Spec describes how the mock answers a single echo request.
type Spec struct {
	Delay  time.Duration // base round-trip delay added before replying
	Jitter time.Duration // reply delay is uniform in [Delay-Jitter, Delay+Jitter]
	Loss   float64       // probability in [0,1] that a request gets no reply
	// Pattern is a deterministic alternative to Loss: '.' replies and 'x'
	// drops, applied to successive requests and repeated cyclically.
	Pattern string
}

// ParseSpec parses either a bare duration ("120ms") or a comma-separated
// list of key=value pairs: "delay=120ms,jitter=10ms,loss=5%" or
// "delay=50ms,pattern=..x". The keyword "timeout" means no reply at all.
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

// parsePattern validates a drop pattern made of '.' (reply) and 'x' (drop).
func parsePattern(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", fmt.Errorf("empty pattern")
	}
	if strings.Trim(s, ".x") != "" {
		return "", fmt.Errorf("pattern %q may only contain '.' (reply) and 'x' (drop)", s)
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
	return strings.Join(parts, ",")
}

// decide determines the outcome for the idx-th request handled under this
// spec, drawing any randomness from rng.
func (sp Spec) decide(idx int, rng *rand.Rand) (time.Duration, bool) {
	if sp.Pattern != "" {
		if sp.Pattern[idx%len(sp.Pattern)] == 'x' {
			return 0, false
		}
	} else if sp.Loss > 0 && rng.Float64() < sp.Loss {
		return 0, false
	}
	d := sp.Delay
	if sp.Jitter > 0 {
		d += time.Duration(rng.Int64N(int64(2*sp.Jitter)+1)) - sp.Jitter
	}
	if d < 0 {
		d = 0
	}
	return d, true
}

// specJSON is the JSON object form of a Spec.
type specJSON struct {
	Delay   string  `json:"delay,omitempty"`
	Jitter  string  `json:"jitter,omitempty"`
	Loss    float64 `json:"loss,omitempty"`
	Pattern string  `json:"pattern,omitempty"`
}

func (sp Spec) MarshalJSON() ([]byte, error) {
	j := specJSON{Delay: sp.Delay.String(), Loss: sp.Loss, Pattern: sp.Pattern}
	if sp.Jitter > 0 {
		j.Jitter = sp.Jitter.String()
	}
	return json.Marshal(j)
}

// UnmarshalJSON accepts the object form or a string in the text form.
func (sp *Spec) UnmarshalJSON(b []byte) error {
	var text string
	if err := json.Unmarshal(b, &text); err == nil {
		out, err := ParseSpec(text)
		if err != nil {
			return err
		}
		*sp = out
		return nil
	}
	var j specJSON
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&j); err != nil {
		return err
	}
	var out Spec
	var err error
	if j.Delay != "" {
		if out.Delay, err = parseDuration(j.Delay); err != nil {
			return err
		}
	}
	if j.Jitter != "" {
		if out.Jitter, err = parseDuration(j.Jitter); err != nil {
			return err
		}
	}
	out.Loss = j.Loss
	if j.Pattern != "" {
		if out.Pattern, err = parsePattern(j.Pattern); err != nil {
			return err
		}
	}
	if err := out.validate(); err != nil {
		return err
	}
	*sp = out
	return nil
}
