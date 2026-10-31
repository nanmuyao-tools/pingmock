package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Outside gives the command of ghostwire exec the host's network, and the
// host's loopback ports it reaches:
//
//	outside: true
//	outside:
//	  ports: [5432, 6379, 514/udp]
//
// Ports implies On; outside: false is the same as no key.
type Outside struct {
	On    bool
	Ports []string // as ParseOutsidePort returns them, such as "5432/tcp"
}

// UnmarshalJSON accepts true, false, or an object with ports, each a
// number or a string such as "53/udp".
func (o *Outside) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("true")) || bytes.Equal(b, []byte("false")) {
		*o = Outside{On: b[0] == 't'}
		return nil
	}
	var v struct {
		Ports []json.RawMessage `json:"ports"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("outside: want true, false or {ports: [PORT, ...]}: %w", err)
	}
	*o = Outside{On: true}
	for _, raw := range v.Ports {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			text = string(raw) // a number
		}
		p, err := ParseOutsidePort(text)
		if err != nil {
			return fmt.Errorf("outside: %w", err)
		}
		o.Ports = append(o.Ports, p)
	}
	return nil
}

// ParseOutsidePort checks a host loopback port for ghostwire exec's outside
// access, PORT or PORT/tcp or PORT/udp, and returns it as PORT/PROTOCOL.
func ParseOutsidePort(s string) (string, error) {
	port, proto, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok {
		proto = "tcp"
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 || (proto != "tcp" && proto != "udp") {
		return "", fmt.Errorf("invalid port %q: want PORT, PORT/tcp or PORT/udp, with PORT from 1 to 65535", s)
	}
	return fmt.Sprintf("%d/%s", n, proto), nil
}
