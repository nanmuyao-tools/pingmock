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
	// Send is what pingmock sends as soon as a connection is accepted,
	// and Reply what it sends when the client first sends data: fixed
	// bytes, such as a banner or an error reply. Wait delays them. TCP
	// rules only.
	Send, Reply []byte
	Wait        time.Duration
	// Limit, if positive, is how many bytes the client may send before
	// After (close or reset) ends the connection. TCP rules only.
	Limit int
	// DNSFail answers a DNS query with an error instead of the answer:
	// the name does not exist, or the server failed. DNS rules only.
	DNSFail DNSFail
}

// proto is the protocol a rule is for, which decides the settings it may
// have.
type proto uint8

const (
	protoPing proto = iota
	protoTCP
	protoDNS
	protoUDP
)

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
func ParseSpec(s string) (Spec, error) { return parseSpec(s, protoPing) }

// ParseTCPSpec parses a spec of a TCP rule: what ParseSpec accepts, plus
// "refuse", "after=close|reset|hang" and the pattern letter 'r'.
func ParseTCPSpec(s string) (Spec, error) { return parseSpec(s, protoTCP) }

// ParseDNSSpec parses a spec of a DNS rule: delays, loss and patterns as
// in ParseSpec, plus "nxdomain", "servfail" and the pattern letters 'n'
// and 's'.
func ParseDNSSpec(s string) (Spec, error) { return parseSpec(s, protoDNS) }

// ParseUDPSpec parses a spec of a UDP rule: refuse (ICMP port
// unreachable), unreachable, timeout or loss, the pattern letters 'x', 'u'
// and 'r', and the delay and TTL of the error.
func ParseUDPSpec(s string) (Spec, error) { return parseSpec(s, protoUDP) }

// parseSpec parses a spec; kind decides which settings it may have.
func parseSpec(s string, kind proto) (Spec, error) {
	var sp Spec
	s = strings.TrimSpace(s)
	if s == "" {
		return sp, fmt.Errorf("empty spec")
	}
	if s == "timeout" {
		sp.Loss = 1
		return sp, nil
	}
	for _, part := range splitUnquoted(s, ',') {
		part = strings.TrimSpace(part)
		key, val, found := strings.Cut(part, "=")
		if !found {
			key, val = "delay", part
			if strings.EqualFold(part, "unreachable") {
				key, val = "unreachable", ""
			}
			for _, k := range []string{"refuse", "nxdomain", "servfail"} {
				if strings.EqualFold(part, k) {
					key = k
				}
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
			sp.Pattern, err = parsePatternFor(val, kind)
		case "unreachable":
			sp.Unreachable, err = parseUnreachCode(val)
		case "ttl":
			sp.TTL, err = parseTTL(val)
		case "after":
			if kind != protoTCP {
				err = fmt.Errorf("after is for TCP rules (-tcp)")
			} else {
				sp.After, err = parseAfter(val)
			}
		case "send", "reply", "limit", "wait":
			k := strings.ToLower(strings.TrimSpace(key))
			switch {
			case kind != protoTCP:
				err = fmt.Errorf("%s is for TCP rules (-tcp)", k)
			case k == "send":
				sp.Send, err = parseData(k, val)
			case k == "reply":
				sp.Reply, err = parseData(k, val)
			case k == "wait":
				sp.Wait, err = parseDuration(val)
			default:
				sp.Limit, err = parseLimit(val)
			}
		case "refuse":
			switch {
			case kind != protoTCP && kind != protoUDP:
				err = fmt.Errorf("refuse is for TCP and UDP rules (-tcp, -udp)")
			case found:
				err = fmt.Errorf("refuse takes no value")
			default:
				sp.Refuse = true
			}
		case "nxdomain", "servfail":
			k := strings.ToLower(strings.TrimSpace(key))
			switch {
			case kind != protoDNS:
				err = fmt.Errorf("%s is for DNS rules (-dns)", k)
			case found:
				err = fmt.Errorf("%s takes no value", k)
			case sp.DNSFail != DNSFailNone:
				err = fmt.Errorf("nxdomain and servfail cannot be combined")
			case k == "nxdomain":
				sp.DNSFail = DNSNXDomain
			default:
				sp.DNSFail = DNSServFail
			}
		default:
			err = fmt.Errorf("unknown key %q", key)
		}
		if err != nil {
			return Spec{}, fmt.Errorf("invalid spec %q: %w", s, err)
		}
	}
	if err := sp.validateFor(kind); err != nil {
		return Spec{}, fmt.Errorf("invalid spec %q: %w", s, err)
	}
	return sp, nil
}

func (sp Spec) validate() error { return sp.validateFor(protoPing) }

// validateFor checks a spec; kind decides which settings it may have.
func (sp Spec) validateFor(kind proto) error {
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
		if _, err := parsePatternFor(sp.Pattern, kind); err != nil {
			return err
		}
	}
	if sp.Refuse {
		switch {
		case kind != protoTCP && kind != protoUDP:
			return fmt.Errorf("refuse is for TCP and UDP rules (-tcp, -udp)")
		case sp.Unreachable != UnreachNone:
			return fmt.Errorf("refuse and unreachable cannot be combined")
		case sp.Pattern != "":
			return fmt.Errorf("refuse and pattern cannot be combined; use r in the pattern")
		}
	}
	if sp.After != AfterHang {
		switch {
		case kind != protoTCP:
			return fmt.Errorf("after is for TCP rules (-tcp)")
		case sp.Refuse || sp.Unreachable != UnreachNone || (sp.Loss == 1 && sp.Pattern == "") ||
			(sp.Pattern != "" && !strings.Contains(sp.Pattern, ".")):
			return fmt.Errorf("after=%s applies to accepted connections, and this rule accepts none", sp.After)
		}
	}
	if sp.sends() || sp.Limit != 0 || sp.Wait != 0 {
		switch {
		case kind != protoTCP:
			return fmt.Errorf("send, reply, limit and wait are for TCP rules (-tcp)")
		case sp.Refuse || sp.Unreachable != UnreachNone || (sp.Loss == 1 && sp.Pattern == "") ||
			(sp.Pattern != "" && !strings.Contains(sp.Pattern, ".")):
			return fmt.Errorf("send, reply and limit apply to accepted connections, and this rule accepts none")
		case sp.Wait < 0 || sp.Limit < 0:
			return fmt.Errorf("negative wait or limit")
		case sp.Wait != 0 && !sp.sends():
			return fmt.Errorf("wait delays send or reply, and this rule has neither")
		case sp.Limit != 0 && sp.After == AfterHang:
			return fmt.Errorf("limit=%d needs after=close or after=reset, which says how the connection ends", sp.Limit)
		case len(sp.Send) > MaxData || len(sp.Reply) > MaxData:
			return fmt.Errorf("send and reply may hold %d bytes each", MaxData)
		}
	}
	if sp.DNSFail != DNSFailNone {
		switch {
		case kind != protoDNS:
			return fmt.Errorf("%s is for DNS rules (-dns)", sp.DNSFail)
		case sp.Pattern != "":
			return fmt.Errorf("%s and pattern cannot be combined; use n or s in the pattern", sp.DNSFail)
		}
	}
	if kind == protoUDP && !sp.Refuse && sp.Unreachable == UnreachNone && sp.Loss < 1 && sp.Pattern == "" {
		return fmt.Errorf("a UDP rule says how datagrams fail: refuse, unreachable, timeout or a pattern of x, u and r; pingmock sends no UDP answers")
	}
	if kind == protoDNS {
		switch {
		case sp.Unreachable != UnreachNone:
			return fmt.Errorf("unreachable is not for DNS rules: use servfail, nxdomain or timeout")
		case sp.TTL != 0:
			return fmt.Errorf("ttl is not for DNS rules: their answers always have TTL 0, so that nothing caches them")
		}
	}
	return nil
}

// sends reports whether the spec sends bytes on a connection.
func (sp Spec) sends() bool { return len(sp.Send) > 0 || len(sp.Reply) > 0 }

// Talks reports whether connections accepted under the spec exchange
// data: pingmock sends bytes or counts the client's, and so acknowledges
// what the client sends. Under a spec that does not, an accepted
// connection gets nothing after the handshake but what After sends.
func (sp Spec) Talks() bool { return sp.sends() || sp.Limit > 0 }

// parseLimit accepts a number of bytes, at least 1.
func parseLimit(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("limit %q must be a number of bytes, at least 1", s)
	}
	return n, nil
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
func parsePattern(s string) (string, error) { return parsePatternFor(s, protoPing) }

// parsePatternFor validates a pattern; TCP rules also allow 'r'
// (refuse), and DNS rules have 'n' (NXDOMAIN) and 's' (SERVFAIL) instead
// of 'u'.
func parsePatternFor(s string, kind proto) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", fmt.Errorf("empty pattern")
	}
	if kind == protoDNS {
		if strings.Trim(s, ".xns") != "" {
			return "", fmt.Errorf("pattern %q may only contain '.' (answer), 'x' (no answer), 'n' (NXDOMAIN) and 's' (SERVFAIL)", s)
		}
		return s, nil
	}
	if strings.Trim(s, ".xu") != "" && strings.Trim(s, ".xuns") == "" && strings.ContainsAny(s, "ns") {
		return "", fmt.Errorf("pattern %q: 'n' (NXDOMAIN) and 's' (SERVFAIL) are for DNS rules (-dns)", s)
	}
	if kind == protoUDP {
		if strings.Trim(s, "xur") != "" {
			return "", fmt.Errorf("pattern %q may only contain 'x' (no answer), 'u' (unreachable) and 'r' (refuse): pingmock sends no UDP answers", s)
		}
		return s, nil
	}
	if kind == protoTCP {
		if strings.Trim(s, ".xur") != "" {
			return "", fmt.Errorf("pattern %q may only contain '.' (accept), 'x' (drop), 'u' (unreachable) and 'r' (refuse)", s)
		}
		return s, nil
	}
	if strings.Trim(s, ".xu") != "" {
		if strings.Trim(s, ".xur") == "" {
			return "", fmt.Errorf("pattern %q: 'r' (refuse) is for TCP and UDP rules (-tcp, -udp)", s)
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
	if len(sp.Send) > 0 {
		parts = append(parts, "send="+quoteData(sp.Send))
	}
	if len(sp.Reply) > 0 {
		parts = append(parts, "reply="+quoteData(sp.Reply))
	}
	if sp.Wait > 0 {
		parts = append(parts, "wait="+sp.Wait.String())
	}
	if sp.Limit > 0 {
		parts = append(parts, "limit="+strconv.Itoa(sp.Limit))
	}
	if sp.After != AfterHang {
		parts = append(parts, "after="+sp.After.String())
	}
	if sp.DNSFail != DNSFailNone {
		parts = append(parts, sp.DNSFail.String())
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
		case 'n':
			out = NXDomain
		case 's':
			out = ServFail
		}
	case sp.Loss > 0 && rng.Float64() < sp.Loss:
		return Drop, UnreachNone, 0
	case sp.Unreachable != UnreachNone:
		out = Unreachable
	case sp.Refuse:
		out = Refused
	case sp.DNSFail == DNSNXDomain:
		out = NXDomain
	case sp.DNSFail == DNSServFail:
		out = ServFail
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
