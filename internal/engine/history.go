package engine

import (
	"net/netip"
	"sync"
	"time"
)

// Event records how one echo request was handled.
type Event struct {
	Time   time.Time  `json:"time"`
	Src    netip.Addr `json:"src"`
	Dst    netip.Addr `json:"dst"`
	ID     uint16     `json:"id"`
	Seq    uint16     `json:"seq"`
	Result string     `json:"result"`          // "reply", "drop", "unreachable", for paths "time-exceeded", "port-unreachable", "syn-ack", for TCP rules also "refused", "sent", "closed" and "reset", for DNS rules "answer", "nxdomain" and "servfail"
	Code   string     `json:"code,omitempty"`  // unreachable code: "host", "net" or "admin"
	Reply  bool       `json:"reply"`           // true for echo replies; kept for compatibility
	Delay  string     `json:"delay,omitempty"` // for replies and unreachable messages
	Rule   string     `json:"rule"`
	Phase  int        `json:"phase"` // 1-based
	// For targets with a path.
	Probe string `json:"probe,omitempty"` // "udp", "tcp" or "dns"; echo requests leave it out
	TTL   uint8  `json:"ttl,omitempty"`   // the probe's TTL, as traceroute set it
	Hop   int    `json:"hop,omitempty"`   // the hop that answered or lost it, from 1
	From  string `json:"from,omitempty"`  // the address the answer came from; a string, so that omitempty leaves it out
	// For TCP probes.
	Port   uint16 `json:"port,omitempty"`   // the destination port
	Repeat bool   `json:"repeat,omitempty"` // a SYN sent again for an attempt already answered; not counted again
	Bytes  int    `json:"bytes,omitempty"`  // for "sent": the bytes of send= or reply= pingmock sent
	// For DNS queries, under pingmock exec.
	Name  string `json:"name,omitempty"`  // the name asked for
	QType string `json:"qtype,omitempty"` // "A", "AAAA", ...
}

// History keeps the most recent events in a fixed-size ring buffer.
type History struct {
	mu    sync.Mutex
	buf   []Event
	next  int
	full  bool
	total uint64
}

// NewHistory returns a history holding up to size events; size 0 keeps none.
func NewHistory(size int) *History { return &History{buf: make([]Event, size)} }

func (h *History) Add(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.total++
	if len(h.buf) == 0 {
		return
	}
	h.buf[h.next] = e
	h.next = (h.next + 1) % len(h.buf)
	if h.next == 0 {
		h.full = true
	}
}

// Events returns stored events in chronological order, optionally only
// those whose destination is within filter, keeping the last limit
// (limit <= 0 means all). It also reports how many events were ever added.
func (h *History) Events(filter *netip.Prefix, limit int) ([]Event, uint64) {
	return h.events(func(e Event) bool { return filter == nil || filter.Contains(e.Dst) }, limit)
}

// EventsNamed is like Events, for the DNS queries for one name.
func (h *History) EventsNamed(name string, limit int) ([]Event, uint64) {
	name = dnsName(name)
	return h.events(func(e Event) bool { return e.Name == name }, limit)
}

func (h *History) events(match func(Event) bool, limit int) ([]Event, uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var ordered []Event
	if h.full {
		ordered = append(ordered, h.buf[h.next:]...)
	}
	ordered = append(ordered, h.buf[:h.next]...)
	out := []Event{} // [] rather than null in JSON when nothing matches
	for _, e := range ordered {
		if match(e) {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, h.total
}

func (h *History) Clear() {
	h.mu.Lock()
	clear(h.buf)
	h.next, h.full, h.total = 0, false, 0
	h.mu.Unlock()
}

func (h *History) Capacity() int { return len(h.buf) }
