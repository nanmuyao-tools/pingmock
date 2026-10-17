package rule

import (
	"fmt"
	"strings"
)

// Outcome is how a single echo request is answered.
type Outcome uint8

const (
	Reply       Outcome = iota // echo reply after the delay
	Drop                       // no answer at all
	Unreachable                // ICMP destination unreachable after the delay
)

func (o Outcome) String() string {
	switch o {
	case Drop:
		return "drop"
	case Unreachable:
		return "unreachable"
	}
	return "reply"
}

// UnreachCode is the reason given in a destination unreachable message.
type UnreachCode uint8

const (
	UnreachNone  UnreachCode = iota
	UnreachHost              // host unreachable (IPv6: address unreachable)
	UnreachNet               // network unreachable (IPv6: no route to destination)
	UnreachAdmin             // administratively prohibited, as sent by firewalls
)

var unreachNames = map[UnreachCode]string{UnreachHost: "host", UnreachNet: "net", UnreachAdmin: "admin"}

func (c UnreachCode) String() string { return unreachNames[c] }

func parseUnreachCode(s string) (UnreachCode, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return UnreachHost, nil
	}
	for c, name := range unreachNames {
		if s == name {
			return c, nil
		}
	}
	return UnreachNone, fmt.Errorf("unknown unreachable code %q: want host, net or admin", s)
}
