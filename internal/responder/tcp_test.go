package responder

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/nanmuyao/pingmock/internal/engine"
	"github.com/nanmuyao/pingmock/internal/icmp"
	"github.com/nanmuyao/pingmock/internal/rule"
)

// tcpSYN builds an IPv4 TCP SYN from client to dst:port.
func tcpSYN(dst [4]byte, srcPort, port uint16, isn uint32) []byte {
	pkt := make([]byte, 40)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], 40)
	pkt[8] = 64
	pkt[9] = 6
	c := client.As4()
	copy(pkt[12:16], c[:])
	copy(pkt[16:20], dst[:])
	binary.BigEndian.PutUint16(pkt[10:12], icmp.Checksum(pkt[:20]))
	seg := pkt[20:]
	binary.BigEndian.PutUint16(seg[0:], srcPort)
	binary.BigEndian.PutUint16(seg[2:], port)
	binary.BigEndian.PutUint32(seg[4:], isn)
	seg[12] = 5 << 4
	seg[13] = 0x02
	return pkt
}

func TestHandleTCPRules(t *testing.T) {
	def, _ := rule.ParseScenario("0ms")
	rs := engine.NewRuleSet(def, 1)
	for target, text := range map[string]string{"10.99.0.1:5432": "delay=20ms", "10.99.0.2:5432": "refuse for 1 requests; timeout"} {
		tt, _ := rule.ParseTCPTarget(target)
		sc, err := rule.ParseTCPScenario(text)
		if err != nil {
			t.Fatal(err)
		}
		rs.SetTCP(tt.Prefix, tt.Port, sc.Scenario)
	}
	hist := engine.NewHistory(10)
	r := &Responder{Rules: rs, History: hist}

	answer, delay := r.handle(tcpSYN([4]byte{10, 99, 0, 1}, 40000, 5432, 9))
	if answer == nil || delay != 20*time.Millisecond || answer[33] != 0x12 {
		t.Fatalf("accepted: %x after %v", answer, delay)
	}
	answer, _ = r.handle(tcpSYN([4]byte{10, 99, 0, 2}, 40001, 5432, 9))
	if answer == nil || answer[33] != 0x14 || binary.BigEndian.Uint16(answer[20:]) != 5432 {
		t.Fatalf("refused: want a RST|ACK from port 5432, got %x", answer)
	}
	// The same SYN again: the same reset, recorded as a repeat.
	if again, _ := r.handle(tcpSYN([4]byte{10, 99, 0, 2}, 40001, 5432, 9)); again == nil || again[33] != 0x14 {
		t.Fatalf("repeated SYN: %x", again)
	}
	// A new attempt: the next phase, no answer.
	if answer, _ := r.handle(tcpSYN([4]byte{10, 99, 0, 2}, 40002, 5432, 10)); answer != nil {
		t.Fatalf("timeout phase answered %x", answer)
	}
	// A port without a rule, on a target without a path: ignored, not recorded.
	if answer, _ := r.handle(tcpSYN([4]byte{10, 99, 0, 1}, 40003, 80, 1)); answer != nil {
		t.Fatalf("port without a rule answered %x", answer)
	}

	events, _ := hist.Events(nil, 0)
	if len(events) != 4 {
		t.Fatalf("%d events: %+v", len(events), events)
	}
	want := []struct {
		result string
		repeat bool
	}{{"syn-ack", false}, {"refused", false}, {"refused", true}, {"drop", false}}
	for i, e := range events {
		if e.Probe != "tcp" || e.Port != 5432 || e.Result != want[i].result || e.Repeat != want[i].repeat || e.Reply {
			t.Errorf("event %d: %+v, want %s repeat=%v", i, e, want[i].result, want[i].repeat)
		}
	}
	if events[1].Rule != "tcp 10.99.0.2/32:5432" || events[3].Phase != 2 {
		t.Errorf("rule %q phase %d", events[1].Rule, events[3].Phase)
	}
}

// tcpSeg turns a SYN built by tcpSYN into a later segment of the same
// connection, with flags, sequence and acknowledgment numbers and data.
func tcpSeg(syn []byte, flags byte, seq, ack uint32, data string) []byte {
	pkt := append(append([]byte{}, syn...), data...)
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	pkt[10], pkt[11] = 0, 0
	binary.BigEndian.PutUint16(pkt[10:12], icmp.Checksum(pkt[:20]))
	binary.BigEndian.PutUint32(pkt[24:], seq)
	binary.BigEndian.PutUint32(pkt[28:], ack)
	pkt[33] = flags
	return pkt
}

func TestHandleTCPAfter(t *testing.T) {
	def, _ := rule.ParseScenario("0ms")
	rs := engine.NewRuleSet(def, 1)
	for target, text := range map[string]string{"10.99.0.1:80": "delay=20ms,after=close", "10.99.0.2:80": "after=reset", "10.99.0.3:80": "10ms"} {
		tt, _ := rule.ParseTCPTarget(target)
		sc, err := rule.ParseTCPScenario(text)
		if err != nil {
			t.Fatal(err)
		}
		rs.SetTCP(tt.Prefix, tt.Port, sc.Scenario)
	}
	hist := engine.NewHistory(10)
	r := &Responder{Rules: rs, History: hist}
	const next = icmp.SynAckSeq + 1

	// after=close: the final ACK gets a FIN|ACK at once, from port 80.
	syn := tcpSYN([4]byte{10, 99, 0, 1}, 40000, 80, 100)
	if answer, delay := r.handle(syn); answer == nil || delay != 20*time.Millisecond {
		t.Fatalf("SYN: %x after %v", answer, delay)
	}
	answer, delay := r.handle(tcpSeg(syn, icmp.FlagACK, 101, next, ""))
	if answer == nil || delay != 0 || answer[33] != icmp.FlagFIN|icmp.FlagACK || binary.BigEndian.Uint16(answer[20:]) != 80 ||
		binary.BigEndian.Uint32(answer[24:]) != next || binary.BigEndian.Uint32(answer[28:]) != 101 {
		t.Fatalf("final ACK: %x after %v", answer, delay)
	}
	// The client's FIN: acknowledged, not recorded.
	if answer, _ := r.handle(tcpSeg(syn, icmp.FlagFIN|icmp.FlagACK, 101, next+1, "")); answer == nil || answer[33] != icmp.FlagACK {
		t.Fatalf("client's FIN: %x", answer)
	}

	// after=reset: nothing on the final ACK, a reset on the first data.
	syn = tcpSYN([4]byte{10, 99, 0, 2}, 40001, 80, 7)
	r.handle(syn)
	if answer, _ := r.handle(tcpSeg(syn, icmp.FlagACK, 8, next, "")); answer != nil {
		t.Fatalf("final ACK under after=reset: %x", answer)
	}
	answer, _ = r.handle(tcpSeg(syn, icmp.FlagACK|0x08, 8, next, "GET / HTTP/1.0\r\n\r\n"))
	if answer == nil || answer[33] != icmp.FlagRST|icmp.FlagACK || binary.BigEndian.Uint32(answer[24:]) != next ||
		binary.BigEndian.Uint32(answer[28:]) != 8+18 {
		t.Fatalf("first data: %x", answer)
	}

	// after=hang, as in v0.9.0: segments are ignored and not recorded.
	syn = tcpSYN([4]byte{10, 99, 0, 3}, 40002, 80, 1)
	r.handle(syn)
	if answer, _ := r.handle(tcpSeg(syn, icmp.FlagACK|0x08, 2, next, "hello")); answer != nil {
		t.Fatalf("after=hang answered data: %x", answer)
	}

	events, _ := hist.Events(nil, 0)
	var got []string
	for _, e := range events {
		got = append(got, e.Result)
		if e.Probe != "tcp" || e.Port != 80 || e.Repeat {
			t.Errorf("event %+v", e)
		}
	}
	if want := "syn-ack closed syn-ack reset syn-ack"; strings.Join(got, " ") != want {
		t.Errorf("events %v, want %s", got, want)
	}
	if events[1].Rule != "tcp 10.99.0.1/32:80" || events[1].Phase != 1 || events[1].Delay != "0s" {
		t.Errorf("closed event: %+v", events[1])
	}
}

// Answers without a delay are written in the order of what they answer:
// the reset that answers data sent after pingmock's FIN must not overtake
// the FIN, or the client sees a reset instead of the connection closing.
func TestServeKeepsSegmentOrder(t *testing.T) {
	def, _ := rule.ParseScenario("0ms")
	rs := engine.NewRuleSet(def, 1)
	tt, _ := rule.ParseTCPTarget("10.99.0.1:80")
	sc, _ := rule.ParseTCPScenario("after=close")
	rs.SetTCP(tt.Prefix, tt.Port, sc.Scenario)
	dev := newFakeDevice()
	done := make(chan error, 1)
	go func() { done <- (&Responder{Rules: rs, History: engine.NewHistory(10)}).Serve(dev) }()
	const next = icmp.SynAckSeq + 1
	const conns = 5
	for i := 0; i < conns; i++ {
		syn := tcpSYN([4]byte{10, 99, 0, 1}, uint16(40000+i), 80, 100)
		dev.in <- syn
		dev.in <- tcpSeg(syn, icmp.FlagACK, 101, next, "")             // FIN
		dev.in <- tcpSeg(syn, icmp.FlagACK|0x08, 101, next, "request") // RST
	}
	for i := 0; i < 3*conns; i++ {
		select {
		case <-dev.wrote:
		case <-time.After(2 * time.Second):
			t.Fatalf("%d answers written, want %d", i, 3*conns)
		}
	}
	close(dev.in)
	<-done
	dev.mu.Lock()
	defer dev.mu.Unlock()
	for i, w := range dev.writes {
		if want := []byte{0x12, 0x11, 0x14}[i%3]; w.pkt[33] != want {
			t.Fatalf("answer %d has flags %#x, want %#x", i, w.pkt[33], want)
		}
	}
}
