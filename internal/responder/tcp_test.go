package responder

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/nanmuyao-tools/pingmock/internal/engine"
	"github.com/nanmuyao-tools/pingmock/internal/icmp"
	"github.com/nanmuyao-tools/pingmock/internal/rule"
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
	// A port without a rule, on a target without a path: not answered, but
	// recorded as ignored.
	if answer, _ := r.handle(tcpSYN([4]byte{10, 99, 0, 1}, 40003, 80, 1)); answer != nil {
		t.Fatalf("port without a rule answered %x", answer)
	}

	events, _ := hist.Events(nil, 0)
	if len(events) != 5 {
		t.Fatalf("%d events: %+v", len(events), events)
	}
	if e := events[4]; e.Result != "ignored" || e.Rule != "none" || e.Probe != "tcp" || e.Port != 80 || e.Delay != "" || e.Phase != 0 {
		t.Errorf("ignored event: %+v", e)
	}
	events = events[:4]
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

// Under wait=, the client's data is acknowledged at once, and what comes
// after the delayed reply, here the reset of the client's next data,
// waits for it: on the wire the reply comes before the reset.
func TestServeKeepsConnectionOrderAcrossWait(t *testing.T) {
	def, _ := rule.ParseScenario("0ms")
	rs := engine.NewRuleSet(def, 1)
	tt, _ := rule.ParseTCPTarget("10.99.0.1:80")
	sc, err := rule.ParseTCPScenario(`reply="later",wait=150ms,after=reset`)
	if err != nil {
		t.Fatal(err)
	}
	rs.SetTCP(tt.Prefix, tt.Port, sc.Scenario)
	dev := newFakeDevice()
	hist := engine.NewHistory(10)
	done := make(chan error, 1)
	go func() { done <- (&Responder{Rules: rs, History: hist}).Serve(dev) }()
	const next = icmp.SynAckSeq + 1
	syn := tcpSYN([4]byte{10, 99, 0, 1}, 40000, 80, 100)
	start := time.Now()
	dev.in <- syn
	dev.in <- tcpSeg(syn, icmp.FlagACK, 101, next, "")                 // the handshake: nothing
	dev.in <- tcpSeg(syn, icmp.FlagACK|icmp.FlagPSH, 101, next, "one") // ACK now, reply later
	dev.in <- tcpSeg(syn, icmp.FlagACK|icmp.FlagPSH, 104, next, "two") // the reset, after the reply
	for i := 0; i < 4; i++ {
		select {
		case <-dev.wrote:
		case <-time.After(2 * time.Second):
			t.Fatalf("%d answers written, want 4", i)
		}
	}
	close(dev.in)
	<-done
	dev.mu.Lock()
	defer dev.mu.Unlock()
	want := []byte{0x12, icmp.FlagACK, icmp.FlagACK | icmp.FlagPSH, icmp.FlagRST | icmp.FlagACK}
	for i, w := range dev.writes {
		if w.pkt[33] != want[i] {
			t.Fatalf("answer %d has flags %#x, want %#x", i, w.pkt[33], want[i])
		}
	}
	if string(dev.writes[2].pkt[40:]) != "later" || dev.writes[2].at.Sub(start) < 150*time.Millisecond {
		t.Errorf("reply %q after %v", dev.writes[2].pkt[40:], dev.writes[2].at.Sub(start))
	}
	if dev.writes[1].at.Sub(start) > 100*time.Millisecond {
		t.Errorf("the ACK waited %v", dev.writes[1].at.Sub(start))
	}
	events, _ := hist.Events(nil, 0)
	var got []string
	for _, e := range events {
		got = append(got, e.Result)
		if e.Result == "sent" && (e.Bytes != 5 || e.Delay != "150ms") {
			t.Errorf("sent event %+v", e)
		}
	}
	if strings.Join(got, " ") != "syn-ack sent reset" {
		t.Errorf("events %v", got)
	}
}

// udpDatagram builds an IPv4 UDP datagram from client to dst:port.
func udpDatagram(dst [4]byte, port uint16) []byte {
	pkt := make([]byte, 28+4)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = 17
	c := client.As4()
	copy(pkt[12:16], c[:])
	copy(pkt[16:20], dst[:])
	binary.BigEndian.PutUint16(pkt[10:12], icmp.Checksum(pkt[:20]))
	binary.BigEndian.PutUint16(pkt[20:], 40000)
	binary.BigEndian.PutUint16(pkt[22:], port)
	binary.BigEndian.PutUint16(pkt[24:], 12)
	return pkt
}

func TestHandleUDPRules(t *testing.T) {
	def, _ := rule.ParseScenario("0ms")
	rs := engine.NewRuleSet(def, 1)
	for target, text := range map[string]string{"10.99.0.1:514": "delay=5ms,refuse", "10.99.0.2:123": "unreachable=net", "10.99.0.3:9": "timeout"} {
		tt, _ := rule.ParseTCPTarget(target)
		sc, err := rule.ParseUDPScenario(text)
		if err != nil {
			t.Fatal(err)
		}
		rs.SetUDP(tt.Prefix, tt.Port, sc.Scenario)
	}
	hist := engine.NewHistory(10)
	r := &Responder{Rules: rs, History: hist}
	// refuse: ICMP port unreachable (type 3, code 3), after the delay.
	if a, delay := r.handle(udpDatagram([4]byte{10, 99, 0, 1}, 514)); a == nil || a[20] != 3 || a[21] != 3 || delay != 5*time.Millisecond {
		t.Errorf("refuse: % x after %v", a, delay)
	}
	// unreachable=net: type 3, code 0.
	if a, _ := r.handle(udpDatagram([4]byte{10, 99, 0, 2}, 123)); a == nil || a[20] != 3 || a[21] != 0 {
		t.Errorf("unreachable: % x", a)
	}
	if a, _ := r.handle(udpDatagram([4]byte{10, 99, 0, 3}, 9)); a != nil {
		t.Errorf("timeout answered % x", a)
	}
	// A port without a rule: not answered, recorded as ignored.
	if a, _ := r.handle(udpDatagram([4]byte{10, 99, 0, 1}, 9999)); a != nil {
		t.Errorf("a port without a rule answered % x", a)
	}
	events, _ := hist.Events(nil, 0)
	if len(events) != 4 || events[0].Probe != "udp" || events[0].Port != 514 || events[0].Result != "refused" || events[2].Result != "drop" ||
		events[3].Result != "ignored" || events[3].Port != 9999 || events[3].Probe != "udp" || events[3].Rule != "none" {
		t.Errorf("events %+v", events)
	}
}

// Ignored probes leave a target's state alone: its ping replies, phases
// and random sequence are those of a run without them.
func TestIgnoredProbesKeepSeededResults(t *testing.T) {
	run := func(withIgnored bool) string {
		sc, err := rule.ParseScenario("delay=5ms,jitter=4ms,loss=40% for 6 pings; 1ms")
		if err != nil {
			t.Fatal(err)
		}
		def, _ := rule.ParseScenario("0ms")
		rs := engine.NewRuleSet(def, 7)
		rs.Set(netip.MustParsePrefix("10.99.0.1/32"), sc)
		r := &Responder{Rules: rs, History: engine.NewHistory(100)}
		var out []string
		for i := 0; i < 10; i++ {
			if withIgnored {
				r.handle(tcpSYN([4]byte{10, 99, 0, 1}, uint16(40000+i), 5432, uint32(i)))
				r.handle(udpDatagram([4]byte{10, 99, 0, 1}, 9999))
			}
			a, delay := r.handle(icmp.BuildEchoRequest(netip.MustParseAddr("10.99.0.254"), netip.MustParseAddr("10.99.0.1"), 1, uint16(i), nil))
			out = append(out, fmt.Sprint(a != nil, delay))
		}
		return strings.Join(out, " ")
	}
	if a, b := run(false), run(true); a != b {
		t.Errorf("ping results changed by ignored probes:\n without %s\n with    %s", a, b)
	}
}
