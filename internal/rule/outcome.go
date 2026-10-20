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
	// For TCP rules, on a connection already accepted (see After):
	Closed // TCP FIN: pingmock closed the connection
	Reset  // TCP RST: pingmock reset the connection
	// For DNS rules:
	NXDomain // the name does not exist
	ServFail // the server failed
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
	case Closed:
		return "closed"
	case Reset:
		return "reset"
	case NXDomain:
		return "nxdomain"
	case ServFail:
		return "servfail"
	}
	return "reply"
}

// After is what pingmock does on a TCP connection it accepted.
type After uint8

const (
	AfterHang  After = iota // nothing: the connection stays open, nothing is sent or acknowledged
	AfterClose              // close it (FIN) as soon as the handshake completes
	AfterReset              // reset it (RST) when the client first sends data or closes
)

var afterNames = map[After]string{AfterHang: "hang", AfterClose: "close", AfterReset: "reset"}

func (a After) String() string { return afterNames[a] }

func parseAfter(s string) (After, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if v, d, ok := strings.Cut(s, ":"); ok {
		if _, err := parseDuration(d); err == nil && v != "" {
			return AfterHang, fmt.Errorf("after=%s: a delay before closing or resetting is not supported yet", s)
		}
	}
	for a, name := range afterNames {
		if s == name {
			return a, nil
		}
	}
	return AfterHang, fmt.Errorf("unknown after %q: want close, reset or hang", s)
}

// DNSFail is the error a DNS rule answers with.
type DNSFail uint8

const (
	DNSFailNone DNSFail = iota
	DNSNXDomain         // NXDOMAIN: the name does not exist
	DNSServFail         // SERVFAIL: the server failed
)

func (f DNSFail) String() string {
	switch f {
	case DNSNXDomain:
		return "nxdomain"
	case DNSServFail:
		return "servfail"
	}
	return ""
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
