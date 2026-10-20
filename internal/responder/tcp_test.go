package responder

import (
	"encoding/binary"
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
