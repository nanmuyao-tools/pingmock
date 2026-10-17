package main

import (
	"net/netip"
	"sync"
	"time"
)

// Event records how one echo request was handled.
type Event struct {
	Time  time.Time `json:"time"`
	Src   string    `json:"src"`
	Dst   string    `json:"dst"`
	ID    uint16    `json:"id"`
	Seq   uint16    `json:"seq"`
	Reply bool      `json:"reply"`
	Delay string    `json:"delay,omitempty"` // only for replies
	Rule  string    `json:"rule"`
	Phase int       `json:"phase"` // 1-based
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
	h.mu.Lock()
	defer h.mu.Unlock()
	var ordered []Event
	if h.full {
		ordered = append(ordered, h.buf[h.next:]...)
	}
	ordered = append(ordered, h.buf[:h.next]...)
	out := ordered[:0:0]
	for _, e := range ordered {
		if filter != nil {
			a, err := netip.ParseAddr(e.Dst)
			if err != nil || !filter.Contains(a) {
				continue
			}
		}
		out = append(out, e)
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
