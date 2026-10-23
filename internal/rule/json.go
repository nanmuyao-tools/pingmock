package rule

// The JSON forms of specs and scenarios, used by the HTTP API and, after
// conversion from YAML, by configuration files. Both also accept the text
// form as a JSON string.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// specJSON is the JSON object form of a Spec.
type specJSON struct {
	Delay       numOrString `json:"delay,omitempty"`  // "120ms", or a number of milliseconds
	Jitter      numOrString `json:"jitter,omitempty"` // likewise
	Loss        numOrString `json:"loss,omitempty"`   // a fraction (0.05) or a percentage ("5%")
	Pattern     string      `json:"pattern,omitempty"`
	Unreachable *string     `json:"unreachable,omitempty"` // "host", "net", "admin" or "" for host
	TTL         numOrString `json:"ttl,omitempty"`         // 1 to 255; unset means rule.DefaultTTL
	Refuse      bool        `json:"refuse,omitempty"`      // TCP rules only
	After       string      `json:"after,omitempty"`       // "close", "reset" or "hang"; TCP rules only
	Send        *string     `json:"send,omitempty"`        // TCP rules only: the bytes, as text
	Reply       *string     `json:"reply,omitempty"`       // likewise
	Wait        numOrString `json:"wait,omitempty"`        // a duration, as delay
	Limit       int         `json:"limit,omitempty"`       // bytes
	NXDomain    bool        `json:"nxdomain,omitempty"`    // DNS rules only
	ServFail    bool        `json:"servfail,omitempty"`    // DNS rules only
}

// numOrString holds a JSON string or number as text. Configuration files,
// YAML ones especially, often write durations and loss as bare numbers.
type numOrString string

func (n *numOrString) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*n = numOrString(s)
		return nil
	}
	var f json.Number
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("want a string or a number, got %s", b)
	}
	*n = numOrString(f)
	return nil
}

// isJSONNumber reports whether b is a JSON number rather than a string or
// an object.
func isJSONNumber(b []byte) bool {
	var n json.Number
	return len(b) > 0 && b[0] != '"' && json.Unmarshal(b, &n) == nil
}

func (sp Spec) MarshalJSON() ([]byte, error) {
	j := specJSON{Delay: numOrString(sp.Delay.String()), Pattern: sp.Pattern}
	if sp.Jitter > 0 {
		j.Jitter = numOrString(sp.Jitter.String())
	}
	if sp.Loss > 0 {
		j.Loss = numOrString(strconv.FormatFloat(sp.Loss, 'g', -1, 64))
	}
	if sp.Unreachable != UnreachNone {
		code := sp.Unreachable.String()
		j.Unreachable = &code
	}
	if sp.TTL != 0 {
		j.TTL = numOrString(strconv.Itoa(int(sp.TTL)))
	}
	j.Refuse = sp.Refuse
	if sp.After != AfterHang {
		j.After = sp.After.String()
	}
	j.NXDomain, j.ServFail = sp.DNSFail == DNSNXDomain, sp.DNSFail == DNSServFail
	if len(sp.Send) > 0 {
		s := string(sp.Send)
		j.Send = &s
	}
	if len(sp.Reply) > 0 {
		s := string(sp.Reply)
		j.Reply = &s
	}
	if sp.Wait > 0 {
		j.Wait = numOrString(sp.Wait.String())
	}
	j.Limit = sp.Limit
	return json.Marshal(j)
}

// UnmarshalJSON accepts the object form, a string in the text form, or a
// number, which is a delay in milliseconds as on the command line.
func (sp *Spec) UnmarshalJSON(b []byte) error { return sp.unmarshal(b, protoPing) }

// unmarshal decodes a spec; tcp allows the settings of TCP rules.
func (sp *Spec) unmarshal(b []byte, kind proto) error {
	var text string
	if err := json.Unmarshal(b, &text); err == nil || isJSONNumber(b) {
		if err != nil {
			text = string(b)
		}
		out, err := parseSpec(text, kind)
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
		if out.Delay, err = parseDuration(string(j.Delay)); err != nil {
			return err
		}
	}
	if j.Jitter != "" {
		if out.Jitter, err = parseDuration(string(j.Jitter)); err != nil {
			return err
		}
	}
	if j.Loss != "" {
		if out.Loss, err = parseLoss(string(j.Loss)); err != nil {
			return err
		}
	}
	if j.Pattern != "" {
		if out.Pattern, err = parsePatternFor(j.Pattern, kind); err != nil {
			return err
		}
	}
	if j.Unreachable != nil {
		if out.Unreachable, err = parseUnreachCode(*j.Unreachable); err != nil {
			return err
		}
	}
	if j.TTL != "" {
		if out.TTL, err = parseTTL(string(j.TTL)); err != nil {
			return err
		}
	}
	out.Refuse = j.Refuse
	if j.After != "" {
		if kind != protoTCP {
			return fmt.Errorf("after is for TCP rules (-tcp)")
		}
		if out.After, err = parseAfter(j.After); err != nil {
			return err
		}
	}
	for _, d := range []struct {
		key string
		in  *string
		out *[]byte
	}{{"send", j.Send, &out.Send}, {"reply", j.Reply, &out.Reply}} {
		if d.in == nil {
			continue
		}
		if kind != protoTCP {
			return fmt.Errorf("%s is for TCP rules (-tcp)", d.key)
		}
		if *d.out, err = checkData(d.key, []byte(*d.in)); err != nil {
			return err
		}
	}
	if j.Wait != "" {
		if out.Wait, err = parseDuration(string(j.Wait)); err != nil {
			return err
		}
	}
	out.Limit = j.Limit
	switch {
	case j.NXDomain && j.ServFail:
		return fmt.Errorf("nxdomain and servfail cannot be combined")
	case j.NXDomain:
		out.DNSFail = DNSNXDomain
	case j.ServFail:
		out.DNSFail = DNSServFail
	}
	if err := out.validateFor(kind); err != nil {
		return err
	}
	*sp = out
	return nil
}

func (sc Scenario) MarshalJSON() ([]byte, error) { return json.Marshal(sc.String()) }

type phaseJSON struct {
	Spec  json.RawMessage `json:"spec"` // decoded as a Spec, for ping or TCP
	For   string          `json:"for,omitempty"`
	Count int             `json:"count,omitempty"`
}

type scenarioJSON struct {
	Phases []phaseJSON `json:"phases"`
	Repeat bool        `json:"repeat,omitempty"`
}

// UnmarshalJSON accepts the text form as a string, a number of
// milliseconds, a single spec object ({"delay":"50ms"}) or a phases object:
//
//	{"phases":[{"spec":"50ms","count":3},{"spec":{"delay":"500ms"},"for":"10s"}],"repeat":true}
func (sc *Scenario) UnmarshalJSON(b []byte) error { return sc.unmarshal(b, protoPing) }

// UnmarshalJSON accepts the forms a Scenario accepts, with the settings of
// TCP rules.
func (sc *TCPScenario) UnmarshalJSON(b []byte) error { return sc.Scenario.unmarshal(b, protoTCP) }

// MarshalJSON writes the text form, with phases counting requests.
func (sc TCPScenario) MarshalJSON() ([]byte, error) { return json.Marshal(sc.String()) }

// UnmarshalJSON accepts what Scenario's does, with the settings of DNS
// rules.
func (sc *DNSScenario) UnmarshalJSON(b []byte) error { return sc.Scenario.unmarshal(b, protoDNS) }

// MarshalJSON writes the text form, with phases counting requests.
func (sc DNSScenario) MarshalJSON() ([]byte, error) { return json.Marshal(sc.String()) }

// unmarshal decodes a scenario; tcp allows the settings of TCP rules.
func (sc *Scenario) unmarshal(b []byte, kind proto) error {
	b = bytes.TrimSpace(b)
	var text string
	if err := json.Unmarshal(b, &text); err == nil || isJSONNumber(b) {
		if err != nil {
			text = string(b) // a bare number: milliseconds, as on the command line
		}
		out, err := parseScenario(text, kind)
		if err != nil {
			return err
		}
		*sc = out
		return nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return fmt.Errorf("a rule must be a string, a number or an object: %w", err)
	}
	if _, ok := probe["phases"]; !ok {
		var sp Spec
		if err := sp.unmarshal(b, kind); err != nil {
			return err
		}
		*sc = Single(sp)
		return nil
	}
	var j scenarioJSON
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&j); err != nil {
		return err
	}
	out := Scenario{Repeat: j.Repeat}
	for _, pj := range j.Phases {
		p := Phase{Count: pj.Count}
		if len(pj.Spec) > 0 { // without one, the zero spec, as before
			if err := p.Spec.unmarshal(pj.Spec, kind); err != nil {
				return err
			}
		}
		if pj.For != "" {
			d, err := time.ParseDuration(pj.For)
			if err != nil || d <= 0 {
				return fmt.Errorf("invalid phase length %q", pj.For)
			}
			p.For = d
		}
		out.Phases = append(out.Phases, p)
	}
	if err := out.validateFor(kind); err != nil {
		return err
	}
	*sc = out
	return nil
}
