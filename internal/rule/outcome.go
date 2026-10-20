package rule

import (
	"fmt"
	"strings"
)

// Outcome is how a single request is answered.
type Outcome uint8

const (
	Reply       Outcome = iota // echo reply after the delay
	Drop                       // no answer at all
	Unreachable                // ICMP destination unreachable after the delay
	// For targets with a path:
	TimeExceeded    // ICMP time exceeded from the hop where the TTL ran out
	PortUnreachable // ICMP port unreachable from the destination, to a UDP probe
	SynAck          // TCP SYN-ACK from the destination, to a TCP probe
	// For TCP rules:
	Refused // TCP RST: the connection is refused
)

func (o Outcome) String() string {
	switch o {
	case Drop:
		return "drop"
	case Unreachable:
		return "unreachable"
	case TimeExceeded:
		return "time-exceeded"
	case PortUnreachable:
		return "port-unreachable"
	case SynAck:
		return "syn-ack"
	case Refused:
		return "refused"
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
