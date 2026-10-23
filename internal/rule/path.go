package rule

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Hop is one router on the path to a target: the address it answers
// from, and how it answers probes whose TTL runs out there. A hop with no
// address never answers, as routers that do not send ICMP errors; it is
// written "*", what traceroute prints for it.
type Hop struct {
	Addr   netip.Addr    // invalid for a silent hop
	Delay  time.Duration // round trip to this hop
	Jitter time.Duration
	// Loss is the probability that this hop does not answer a probe that
	// ends here, as routers that rate-limit ICMP errors; nothing else is
	// affected.
	Loss float64
	// Cut is the probability that this hop loses a packet going through
	// it, whatever its TTL: the probes for later hops and the destination,
	// and pings to the destination. A lossy link looks like this.
	Cut float64
	TTL uint8 // of this hop's answers; 0 means DefaultTTL
}

// Silent reports whether the hop never answers.
func (h Hop) Silent() bool { return !h.Addr.IsValid() }

// Path is the route to a target: its hops in order, the destination
// being one past the last. A target with a path answers UDP and TCP
// probes too, so that traceroute and mtr reach it.
type Path []Hop

// ParsePath parses hops separated by ">", each "ADDR@SPEC" where SPEC is
// a delay or settings as in a rule (delay, jitter, loss, ttl, and cut for
// link loss), "ADDR" alone for a hop answering at once, or "*" for a hop
// that never answers:
//
//	192.168.1.1@2ms > 10.0.0.1@delay=15ms,jitter=3ms > * > 72.14.0.1@25ms
func ParsePath(s string) (Path, error) {
	var p Path
	for _, part := range strings.Split(s, ">") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("invalid path %q: empty hop", s)
		}
		h, err := parseHop(part)
		if err != nil {
			return nil, fmt.Errorf("invalid path %q: %w", s, err)
		}
		p = append(p, h)
	}
	if len(p) == 0 {
		return nil, fmt.Errorf("invalid path %q: no hops", s)
	}
	return p, nil
}

func parseHop(s string) (Hop, error) {
	if s == "*" {
		return Hop{}, nil
	}
	addrText, spec, hasSpec := strings.Cut(s, "@")
	a, err := netip.ParseAddr(strings.TrimSpace(addrText))
	if err != nil || a.Zone() != "" {
		return Hop{}, fmt.Errorf("hop %q: invalid address %q", s, addrText)
	}
	h := Hop{Addr: a.Unmap()}
	if !hasSpec {
		return h, nil
	}
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Hop{}, fmt.Errorf("hop %q: empty settings after @", s)
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		key, val, found := strings.Cut(part, "=")
		if !found {
			key, val = "delay", part
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "delay":
			h.Delay, err = parseDuration(val)
		case "jitter":
			h.Jitter, err = parseDuration(val)
		case "loss":
			h.Loss, err = parseLoss(val)
		case "cut":
			h.Cut, err = parseLoss(val)
		case "ttl":
			h.TTL, err = parseTTL(val)
		default:
			err = fmt.Errorf("unknown key %q (a hop takes delay, jitter, loss, cut and ttl)", key)
		}
		if err != nil {
			return Hop{}, fmt.Errorf("hop %q: %w", s, err)
		}
	}
	return h, nil
}

// String returns the path in the form ParsePath accepts.
func (p Path) String() string {
	parts := make([]string, 0, len(p))
	for _, h := range p {
		parts = append(parts, h.String())
	}
	return strings.Join(parts, " > ")
}

func (h Hop) String() string {
	if h.Silent() {
		return "*"
	}
	settings := []string{"delay=" + h.Delay.String()}
	if h.Jitter > 0 {
		settings = append(settings, "jitter="+h.Jitter.String())
	}
	if h.Loss > 0 {
		settings = append(settings, "loss="+strconv.FormatFloat(h.Loss*100, 'g', -1, 64)+"%")
	}
	if h.Cut > 0 {
		settings = append(settings, "cut="+strconv.FormatFloat(h.Cut*100, 'g', -1, 64)+"%")
	}
	if h.TTL != 0 {
		settings = append(settings, "ttl="+strconv.Itoa(int(h.TTL)))
	}
	return h.Addr.String() + "@" + strings.Join(settings, ",")
}

// EffectiveTTL returns the TTL of this hop's answers.
func (h Hop) EffectiveTTL() uint8 {
	if h.TTL != 0 {
		return h.TTL
	}
	return DefaultTTL
}

// DecideHop says whether a probe that ends at this hop is answered, and
// after how long, drawing the randomness from rng.
func (h Hop) DecideHop(rng *rand.Rand) (answered bool, delay time.Duration) {
	if h.Silent() || (h.Loss > 0 && rng.Float64() < h.Loss) {
		return false, 0
	}
	d := h.Delay
	if h.Jitter > 0 {
		d += time.Duration(rng.Int64N(int64(2*h.Jitter)+1)) - h.Jitter
	}
	if d < 0 {
		d = 0
	}
	return true, d
}

// Cuts reports whether a packet going through this hop is lost there.
func (h Hop) Cuts(rng *rand.Rand) bool {
	return h.Cut > 0 && rng.Float64() < h.Cut
}

// Addrs returns the addresses of the hops that answer, for routing.
func (p Path) Addrs() []netip.Addr {
	var out []netip.Addr
	for _, h := range p {
		if !h.Silent() {
			out = append(out, h.Addr)
		}
	}
	return out
}

// The JSON form of a path is its text form.
// MarshalJSON writes the text form, with ">" as it is rather than escaped
// for HTML.
func (p Path) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p.String()); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

func (p *Path) UnmarshalJSON(b []byte) error {
	var text string
	if err := json.Unmarshal(b, &text); err != nil {
		return fmt.Errorf("a path must be a string such as %q", "192.168.1.1@2ms > * > 10.0.0.1@20ms")
	}
	out, err := ParsePath(text)
	if err != nil {
		return err
	}
	*p = out
	return nil
}
