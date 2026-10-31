package responder

import (
	"encoding/json"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nanmuyao-tools/ghostwire/internal/engine"
	"github.com/nanmuyao-tools/ghostwire/internal/icmp"
	"github.com/nanmuyao-tools/ghostwire/internal/rule"
)

// fakeDevice delivers queued packets to Read and records what is written,
// with the time each write happened.
type fakeDevice struct {
	in     chan []byte
	mu     sync.Mutex
	writes []written
	wrote  chan struct{}
}

type written struct {
	pkt []byte
	at  time.Time
}

func newFakeDevice() *fakeDevice {
	return &fakeDevice{in: make(chan []byte, 16), wrote: make(chan struct{}, 16)}
}

func (d *fakeDevice) Read(b []byte) (int, error) {
	pkt, ok := <-d.in
	if !ok {
		return 0, os.ErrClosed
	}
	return copy(b, pkt), nil
}

func (d *fakeDevice) Write(b []byte) (int, error) {
	d.mu.Lock()
	d.writes = append(d.writes, written{append([]byte(nil), b...), time.Now()})
	d.mu.Unlock()
	d.wrote <- struct{}{}
	return len(b), nil
}

var (
	client = netip.MustParseAddr("10.99.0.254")
	fast   = netip.MustParseAddr("10.99.0.1")
	lossy  = netip.MustParseAddr("10.99.0.2")
)

func start(t *testing.T) (*fakeDevice, *engine.History, chan error) {
	t.Helper()
	def, _ := rule.ParseScenario("0ms")
	rs := engine.NewRuleSet(def, 1)
	sc, _ := rule.ParseScenario("40ms")
	rs.Set(netip.PrefixFrom(fast, 32), sc)
	sc, _ = rule.ParseScenario("delay=1ms,pattern=x.")
	rs.Set(netip.PrefixFrom(lossy, 32), sc)
	hist := engine.NewHistory(10)
	dev := newFakeDevice()
	done := make(chan error, 1)
	go func() { done <- (&Responder{Rules: rs, History: hist}).Serve(dev) }()
	return dev, hist, done
}

func TestServeRepliesAfterDelay(t *testing.T) {
	dev, hist, done := start(t)
	sent := time.Now()
	dev.in <- icmp.BuildEchoRequest(client, fast, 7, 1, []byte("hi"))
	select {
	case <-dev.wrote:
	case <-time.After(2 * time.Second):
		t.Fatal("no reply written")
	}
	dev.mu.Lock()
	w := dev.writes[0]
	dev.mu.Unlock()
	if elapsed := w.at.Sub(sent); elapsed < 40*time.Millisecond {
		t.Errorf("reply after %v, want at least 40ms", elapsed)
	}
	req, ok := icmp.ParseEchoRequest(w.pkt)
	if ok {
		t.Errorf("wrote an echo request instead of a reply: %+v", req)
	}
	if w.pkt[20] != 0 || string(w.pkt[28:]) != "hi" {
		t.Errorf("unexpected reply bytes %x", w.pkt)
	}
	close(dev.in)
	if err := <-done; err != nil {
		t.Errorf("Serve returned %v after close, want nil", err)
	}
	if events, _ := hist.Events(nil, 0); len(events) != 1 || !events[0].Reply || events[0].Delay != "40ms" || events[0].Seq != 1 {
		t.Errorf("history = %+v", events)
	}
	// Fields for path probes stay out of the history of a plain ping.
	events, _ := hist.Events(nil, 0)
	b, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"from"`, `"probe"`, `"ttl"`, `"hop"`} {
		if strings.Contains(string(b), field) {
			t.Errorf("a ping's history event has %s: %s", field, b)
		}
	}
}

func TestServeDropsAndIgnoresOtherPackets(t *testing.T) {
	dev, hist, done := start(t)
	dev.in <- []byte{0x60, 0, 0, 0}                           // not IPv4: ignored
	dev.in <- icmp.BuildEchoRequest(client, lossy, 7, 1, nil) // pattern x: dropped
	dev.in <- icmp.BuildEchoRequest(client, lossy, 7, 2, nil) // pattern .: replied
	select {
	case <-dev.wrote:
	case <-time.After(2 * time.Second):
		t.Fatal("no reply written")
	}
	close(dev.in)
	<-done
	time.Sleep(20 * time.Millisecond) // a wrongly scheduled second reply would land here
	dev.mu.Lock()
	n := len(dev.writes)
	dev.mu.Unlock()
	if n != 1 {
		t.Errorf("%d packets written, want 1", n)
	}
	events, _ := hist.Events(nil, 0)
	if len(events) != 2 || events[0].Reply || !events[1].Reply {
		t.Errorf("history = %+v", events)
	}
}

func TestServeIPv6(t *testing.T) {
	def, _ := rule.ParseScenario("delay=5ms,pattern=.u")
	rs := engine.NewRuleSet(def, 1)
	hist := engine.NewHistory(10)
	dev := newFakeDevice()
	done := make(chan error, 1)
	go func() { done <- (&Responder{Rules: rs, History: hist}).Serve(dev) }()

	client, target := netip.MustParseAddr("fd99::fe"), netip.MustParseAddr("fd99::1")
	dev.in <- icmp.BuildEchoRequest(client, target, 3, 1, []byte("v6"))
	dev.in <- icmp.BuildEchoRequest(client, target, 3, 2, []byte("v6"))
	for i := 0; i < 2; i++ {
		select {
		case <-dev.wrote:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d packets written", i)
		}
	}
	close(dev.in)
	<-done
	dev.mu.Lock()
	defer dev.mu.Unlock()
	types := map[byte]bool{}
	for _, w := range dev.writes {
		if w.pkt[0]>>4 != 6 {
			t.Fatalf("wrote a non-IPv6 packet: %x", w.pkt[:8])
		}
		types[w.pkt[40]] = true
	}
	if !types[129] || !types[1] {
		t.Errorf("ICMPv6 types written %v, want an echo reply (129) and an unreachable (1)", types)
	}
	events, _ := hist.Events(nil, 0)
	if len(events) != 2 || events[0].Dst.String() != "fd99::1" || events[1].Result != "unreachable" {
		t.Errorf("history = %+v", events)
	}
}

// handle is decide for a test that expects one packet: the first one the
// plan writes, and its delay.
func (r *Responder) handle(pkt []byte) ([]byte, time.Duration) {
	pl := r.decide(pkt)
	if all := append(pl.now, pl.later...); len(all) > 0 {
		return all[0], pl.delay
	}
	return nil, 0
}
