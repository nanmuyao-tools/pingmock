package engine

import (
	"fmt"
	"net/netip"
	"testing"
)

func ev(dst string, seq uint16) Event { return Event{Dst: dst, Seq: seq} }

func seqs(events []Event) string {
	s := ""
	for _, e := range events {
		s += fmt.Sprintf("%d,", e.Seq)
	}
	return s
}

func TestHistoryRing(t *testing.T) {
	h := NewHistory(3)
	for i := uint16(1); i <= 2; i++ {
		h.Add(ev("10.99.0.1", i))
	}
	if got, total := h.Events(nil, 0); seqs(got) != "1,2," || total != 2 {
		t.Errorf("before wrapping: %s total %d", seqs(got), total)
	}
	for i := uint16(3); i <= 5; i++ {
		h.Add(ev("10.99.0.1", i))
	}
	if got, total := h.Events(nil, 0); seqs(got) != "3,4,5," || total != 5 {
		t.Errorf("after wrapping: %s total %d, want the 3 newest in order", seqs(got), total)
	}
	if got, _ := h.Events(nil, 2); seqs(got) != "4,5," {
		t.Errorf("limit 2: %s", seqs(got))
	}
	h.Clear()
	if got, total := h.Events(nil, 0); len(got) != 0 || total != 0 {
		t.Errorf("after clear: %v total %d", got, total)
	}
}

func TestHistoryFilter(t *testing.T) {
	h := NewHistory(10)
	h.Add(ev("10.99.0.1", 1))
	h.Add(ev("10.99.0.200", 2))
	h.Add(ev("10.99.0.1", 3))
	one := netip.MustParsePrefix("10.99.0.1/32")
	if got, _ := h.Events(&one, 0); seqs(got) != "1,3," {
		t.Errorf("single address: %s", seqs(got))
	}
	upper := netip.MustParsePrefix("10.99.0.128/25")
	if got, _ := h.Events(&upper, 0); seqs(got) != "2," {
		t.Errorf("prefix: %s", seqs(got))
	}
}

func TestHistoryDisabled(t *testing.T) {
	h := NewHistory(0)
	h.Add(ev("10.99.0.1", 1))
	if got, total := h.Events(nil, 0); len(got) != 0 || total != 1 {
		t.Errorf("disabled history: %v total %d", got, total)
	}
}
