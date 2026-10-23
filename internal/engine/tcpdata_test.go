package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/nanmuyao/pingmock/internal/icmp"
	"github.com/nanmuyao/pingmock/internal/rule"
)

const (
	psh    = icmp.FlagPSH
	rstAck = icmp.FlagRST | icmp.FlagACK
)

// segData is seg with the given number of data bytes, as a client sends
// them after the handshake.
func segData(dst string, port, srcPort uint16, seq, acked uint32, n int) icmp.Probe {
	return seg(dst, port, srcPort, ack|psh, seq, acked, n)
}

func TestTCPSend(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 1)
	setTCP(t, rs, "10.99.0.1:22", `send="SSH-2.0-pingmock\r\n"`)
	rs.DecideProbe(syn("10.99.0.1", 22, 40000, 100))
	// The handshake's last ACK: the banner, from pingmock's first number.
	a, ok := rs.DecideSegment(seg("10.99.0.1", 22, 40000, ack, 101, next, 0))
	if !ok || len(a.Ordered) != 1 || len(a.Now) != 0 || a.Sent != 18 || a.Delay != 0 || a.Action != nil {
		t.Fatalf("final ACK: %+v", a)
	}
	s := a.Ordered[0]
	if s.Flags != ack|psh || s.Seq != next || s.Ack != 101 || string(s.Data) != "SSH-2.0-pingmock\r\n" {
		t.Errorf("banner segment %+v", s)
	}
	// The client's data is acknowledged, from after the banner.
	a, ok = rs.DecideSegment(segData("10.99.0.1", 22, 40000, 101, next+18, 30))
	if !ok || len(a.Now) != 1 || a.Now[0].Flags != ack || a.Now[0].Seq != next+18 || a.Now[0].Ack != 131 || len(a.Ordered) != 0 {
		t.Errorf("client data: %+v", a)
	}
	// The same data again: acknowledged again, not counted twice.
	a, _ = rs.DecideSegment(segData("10.99.0.1", 22, 40000, 101, next+18, 30))
	if len(a.Now) != 1 || a.Now[0].Ack != 131 {
		t.Errorf("data sent again: %+v", a)
	}
	// A half close under hang: acknowledged, pingmock's side stays open.
	a, _ = rs.DecideSegment(seg("10.99.0.1", 22, 40000, finAck, 131, next+18, 0))
	if len(a.Now) != 1 || a.Now[0].Ack != 132 || len(a.Ordered) != 0 {
		t.Errorf("client's FIN: %+v", a)
	}
}

// Data longer than one segment goes out in segments of at most
// icmp.MaxSegment bytes, numbered one after the other.
func TestTCPSendSegments(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 1)
	big := strings.Repeat("x", 1200)
	setTCP(t, rs, "10.99.0.1:80", `send="`+big+`"`)
	rs.DecideProbe(syn("10.99.0.1", 80, 40000, 100))
	a, _ := rs.DecideSegment(seg("10.99.0.1", 80, 40000, ack, 101, next, 0))
	var got string
	seq := uint32(next)
	for i, s := range a.Ordered {
		if s.Seq != seq || len(s.Data) > icmp.MaxSegment || (s.Flags&psh != 0) != (i == len(a.Ordered)-1) {
			t.Errorf("segment %d: seq %d, %d bytes, flags %#x", i, s.Seq, len(s.Data), s.Flags)
		}
		seq += uint32(len(s.Data))
		got += string(s.Data)
	}
	if got != big || len(a.Ordered) != 3 || a.Sent != 1200 {
		t.Errorf("%d segments, %d bytes", len(a.Ordered), len(got))
	}
}

func TestTCPReplyThenClose(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 1)
	setTCP(t, rs, "10.99.0.1:80", `reply="HTTP/1.1 503 Service Unavailable\r\n\r\n",after=close`)
	rs.DecideProbe(syn("10.99.0.1", 80, 40000, 100))
	// Nothing on the handshake's ACK: the reply waits for the request.
	if a, ok := rs.DecideSegment(seg("10.99.0.1", 80, 40000, ack, 101, next, 0)); ok {
		t.Fatalf("final ACK answered: %+v", a)
	}
	a, _ := rs.DecideSegment(segData("10.99.0.1", 80, 40000, 101, next, 18))
	if len(a.Ordered) != 2 || string(a.Ordered[0].Data) != "HTTP/1.1 503 Service Unavailable\r\n\r\n" || a.Ordered[0].Ack != 119 {
		t.Fatalf("request: %+v", a)
	}
	fin := a.Ordered[1]
	if fin.Flags != finAck || fin.Seq != next+36 || fin.Ack != 119 || a.Action == nil || a.Action.Outcome != rule.Closed {
		t.Errorf("FIN after the reply: %+v, action %+v", fin, a.Action)
	}
	// The request sent again, late: acknowledged, not reset.
	a, _ = rs.DecideSegment(segData("10.99.0.1", 80, 40000, 101, next, 18))
	if len(a.Now) != 1 || a.Now[0].Flags != ack || a.Now[0].Ack != 119 {
		t.Errorf("request sent again: %+v", a)
	}
	// The client closes too: acknowledged.
	a, _ = rs.DecideSegment(seg("10.99.0.1", 80, 40000, finAck, 119, next+37, 0))
	if len(a.Now) != 1 || a.Now[0].Seq != next+37 || a.Now[0].Ack != 120 {
		t.Errorf("client's FIN: %+v", a)
	}
	// New data on a closed connection: a reset, as from a closed port.
	a, _ = rs.DecideSegment(segData("10.99.0.1", 80, 40000, 120, next+37, 5))
	if len(a.Ordered) != 1 || a.Ordered[0].Flags != rstAck {
		t.Errorf("data after both closed: %+v", a)
	}
}

// A first segment that already carries data completes the handshake and
// brings data: send goes out, then the data is answered.
func TestTCPFirstSegmentWithData(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 1)
	setTCP(t, rs, "10.99.0.1:80", `send="hello",reply="bye"`)
	rs.DecideProbe(syn("10.99.0.1", 80, 40000, 100))
	a, _ := rs.DecideSegment(segData("10.99.0.1", 80, 40000, 101, next, 4))
	if len(a.Ordered) != 2 || string(a.Ordered[0].Data) != "hello" || string(a.Ordered[1].Data) != "bye" ||
		a.Ordered[1].Seq != next+5 || a.Ordered[1].Ack != 105 || a.Sent != 8 {
		t.Errorf("%+v", a)
	}
}

func TestTCPLimit(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 1)
	setTCP(t, rs, "10.99.0.1:5432", `limit=1000,after=reset`)
	setTCP(t, rs, "10.99.0.2:5432", `limit=10,after=close`)
	rs.DecideProbe(syn("10.99.0.1", 5432, 40000, 100))
	rs.DecideSegment(seg("10.99.0.1", 5432, 40000, ack, 101, next, 0))
	// 600 bytes: acknowledged.
	a, _ := rs.DecideSegment(segData("10.99.0.1", 5432, 40000, 101, next, 600))
	if len(a.Now) != 1 || a.Now[0].Ack != 701 || a.Action != nil {
		t.Fatalf("under the limit: %+v", a)
	}
	// 600 more: the first 400 reach the limit, and the connection is reset there.
	a, _ = rs.DecideSegment(segData("10.99.0.1", 5432, 40000, 701, next, 600))
	if len(a.Ordered) != 1 || a.Ordered[0].Flags != rstAck || a.Ordered[0].Ack != 1101 || a.Action == nil || a.Action.Outcome != rule.Reset {
		t.Errorf("at the limit: %+v", a)
	}
	rs.DecideProbe(syn("10.99.0.2", 5432, 40000, 100))
	a, _ = rs.DecideSegment(segData("10.99.0.2", 5432, 40000, 101, next, 20))
	if len(a.Ordered) != 1 || a.Ordered[0].Flags != finAck || a.Ordered[0].Ack != 111 || a.Action.Outcome != rule.Closed {
		t.Errorf("limit with close: %+v", a)
	}
	st := rs.Snapshot()["tcp_stats"].(map[string]TCPTargetState)
	if st["10.99.0.1:5432"].Reset != 1 || st["10.99.0.2:5432"].Closed != 1 {
		t.Errorf("stats %+v", st)
	}
}

// wait= delays the data, not the acknowledgment: the client's data is
// acknowledged at once, from the number the delayed data will start at,
// so that the client does not send it again while pingmock waits.
func TestTCPWait(t *testing.T) {
	rs, clock := newTestRuleSet(t, "0ms", 1)
	setTCP(t, rs, "10.99.0.1:80", `reply="late",wait=2s,after=close`)
	rs.DecideProbe(syn("10.99.0.1", 80, 40000, 100))
	rs.DecideSegment(seg("10.99.0.1", 80, 40000, ack, 101, next, 0))
	a, _ := rs.DecideSegment(segData("10.99.0.1", 80, 40000, 101, next, 10))
	if a.Delay != 2*time.Second || len(a.Now) != 1 || a.Now[0].Seq != next || a.Now[0].Ack != 111 ||
		len(a.Ordered) != 2 || string(a.Ordered[0].Data) != "late" || a.Ordered[1].Flags != finAck {
		t.Fatalf("%+v", a)
	}
	// The client sends its data again before the reply: the ACK still
	// starts at the delayed data.
	clock.advance(time.Second)
	a, _ = rs.DecideSegment(segData("10.99.0.1", 80, 40000, 101, next, 10))
	if len(a.Now) != 1 || a.Now[0].Seq != next || a.Now[0].Ack != 111 {
		t.Errorf("during the wait: %+v", a)
	}
}

// after=reset with data: the reset comes on the client's first data once
// pingmock has sent all it has to send.
func TestTCPSendThenReset(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 1)
	setTCP(t, rs, "10.99.0.1:22", `send="banner",after=reset`)
	rs.DecideProbe(syn("10.99.0.1", 22, 40000, 100))
	if a, _ := rs.DecideSegment(seg("10.99.0.1", 22, 40000, ack, 101, next, 0)); string(a.Ordered[0].Data) != "banner" || a.Action != nil {
		t.Fatalf("handshake: %+v", a)
	}
	a, _ := rs.DecideSegment(segData("10.99.0.1", 22, 40000, 101, next+6, 5))
	if len(a.Ordered) != 1 || a.Ordered[0].Flags != rstAck || a.Ordered[0].Seq != next+6 || a.Action.Outcome != rule.Reset {
		t.Errorf("first data: %+v", a)
	}
}

// Rules without data keep the v0.10 behavior exactly: nothing is
// acknowledged under after=hang.
func TestTCPNoDataNoAck(t *testing.T) {
	rs, _ := newTestRuleSet(t, "0ms", 1)
	setTCP(t, rs, "10.99.0.1:80", "5ms")
	rs.DecideProbe(syn("10.99.0.1", 80, 40000, 100))
	for _, p := range []icmp.Probe{seg("10.99.0.1", 80, 40000, ack, 101, next, 0), segData("10.99.0.1", 80, 40000, 101, next, 10)} {
		if a, ok := rs.DecideSegment(p); ok {
			t.Errorf("answered %+v", a)
		}
	}
}
