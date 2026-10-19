// Tests of a ping library's behavior, run inside pingmock exec:
//
//	go test -exec 'pingmock exec -config pingmock.yaml --' ./...
//
// The library under test is pro-bing, as it is: nothing here is mocked
// except the network. pingmock.yaml defines one host per behavior (answers,
// never answers, unreachable, loses every other ping, TTL 128), so each
// test pings the host it is about and checks what the library reports.
package pinglib_test

import (
	"context"
	"testing"
	"time"

	"github.com/nanmuyao/pingmock/client"
	probing "github.com/prometheus-community/pro-bing"
)

// pingmock connects to the pingmock running the tests, or skips the test
// when there is none.
func pingmock(t *testing.T) *client.Client {
	t.Helper()
	pm, err := client.FromEnv()
	if err != nil {
		t.Skip(err)
	}
	return pm
}

// pinger returns a pinger for host that sends count pings 100ms apart and
// gives up after timeout. It uses a raw socket: inside pingmock exec the
// test process is root in its own user namespace, so no sudo is needed.
func pinger(t *testing.T, host string, count int, timeout time.Duration) *probing.Pinger {
	t.Helper()
	p, err := probing.NewPinger(host)
	if err != nil {
		t.Fatal(err)
	}
	p.SetPrivileged(true)
	p.Count, p.Interval, p.Timeout = count, 100*time.Millisecond, timeout
	return p
}

// history returns what pingmock recorded for host: one event per request
// the library sent, with how it was answered.
func history(t *testing.T, pm *client.Client, host string) []client.Event {
	t.Helper()
	events, err := pm.History(context.Background(), host, 0)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestRepliesGiveTheRtt(t *testing.T) {
	pm := pingmock(t)
	p := pinger(t, "10.99.0.1", 4, 5*time.Second)
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	s := p.Statistics()
	if s.PacketsSent != 4 || s.PacketsRecv != 4 || s.PacketLoss != 0 {
		t.Errorf("sent %d, received %d, loss %g%%", s.PacketsSent, s.PacketsRecv, s.PacketLoss)
	}
	for _, rtt := range s.Rtts {
		if rtt < 20*time.Millisecond || rtt > 25*time.Millisecond {
			t.Errorf("rtt %v, want about 20ms", rtt)
		}
	}
	// The library sent exactly what it says it sent.
	if events := history(t, pm, "10.99.0.1"); len(events) != 4 {
		t.Errorf("pingmock saw %d requests, want 4", len(events))
	}
}

func TestTimeoutEndsTheRun(t *testing.T) {
	pingmock(t)
	// 10.99.0.2 never answers. Run must come back when Timeout is over, not
	// hang until Count replies arrive, which they never will.
	p := pinger(t, "10.99.0.2", 4, time.Second)
	start := time.Now()
	err := p.Run()
	elapsed := time.Since(start)
	if elapsed < time.Second || elapsed > 2*time.Second {
		t.Errorf("Run took %v, want about the 1s timeout", elapsed)
	}
	// pro-bing reports a timeout through the statistics, not as an error:
	// the caller has to look at PacketsRecv. This is the behavior discussed
	// in pro-bing issue #70; a test like this one pins it down either way.
	if err != nil {
		t.Errorf("Run returned %v; pro-bing does not report timeouts as errors", err)
	}
	s := p.Statistics()
	if s.PacketsRecv != 0 || s.PacketLoss != 100 {
		t.Errorf("received %d, loss %g%%, want 0 and 100%%", s.PacketsRecv, s.PacketLoss)
	}
}

func TestUnreachableLooksLikeLoss(t *testing.T) {
	pm := pingmock(t)
	// 10.99.0.3 answers every ping at once with "destination host
	// unreachable". pro-bing only counts echo replies, so to it the host is
	// as silent as 10.99.0.2, and Run again lasts until the timeout.
	p := pinger(t, "10.99.0.3", 4, time.Second)
	start := time.Now()
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Errorf("Run took %v: it ended before the timeout", elapsed)
	}
	if s := p.Statistics(); s.PacketsRecv != 0 {
		t.Errorf("received %d, want 0: unreachable errors are not replies", s.PacketsRecv)
	}
	// pingmock shows the difference the library cannot: every request was
	// answered, with an error.
	events := history(t, pm, "10.99.0.3")
	if len(events) < 4 {
		t.Fatalf("pingmock saw %d requests, want at least 4", len(events))
	}
	for _, e := range events {
		if e.Result != "unreachable" || e.Code != "host" {
			t.Errorf("request %d: %s %s, want unreachable host", e.Seq, e.Result, e.Code)
		}
	}
}

func TestLossIsCounted(t *testing.T) {
	pingmock(t)
	// 10.99.0.4 answers one ping and loses the next, deterministically, so
	// the numbers are the same on every run. Count counts replies, so with
	// loss the run lasts until the timeout: 6 pings go out in 600ms.
	p := pinger(t, "10.99.0.4", 6, 1500*time.Millisecond)
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	s := p.Statistics()
	if s.PacketsSent != 6 || s.PacketsRecv != 3 || s.PacketLoss != 50 {
		t.Errorf("sent %d, received %d, loss %g%%, want 6, 3 and 50%%", s.PacketsSent, s.PacketsRecv, s.PacketLoss)
	}
}

func TestTTLIsReported(t *testing.T) {
	pingmock(t)
	// 10.99.0.5 answers with TTL 128. The TTL reaches the OnRecv callback
	// and the statistics.
	p := pinger(t, "10.99.0.5", 3, 5*time.Second)
	var seen []int
	p.OnRecv = func(pkt *probing.Packet) { seen = append(seen, pkt.TTL) }
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Fatalf("OnRecv called %d times, want 3", len(seen))
	}
	for _, ttl := range seen {
		if ttl != 128 {
			t.Errorf("OnRecv got ttl %d, want 128", ttl)
		}
	}
	for _, ttl := range p.Statistics().TTLs {
		if ttl != 128 {
			t.Errorf("statistics have ttl %d, want 128", ttl)
		}
	}
}
