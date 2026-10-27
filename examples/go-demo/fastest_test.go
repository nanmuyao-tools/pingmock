package fastest_test

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"

	fastest "example.com/go-demo"
	"github.com/nanmuyao-tools/pingmock/client"
)

// These tests run inside pingmock exec, which mocks the pings they cause:
//
//	go test -exec 'pingmock exec -config pingmock.yaml --' ./...
//
// pingmock.yaml sets up three servers; each test adds the conditions it
// needs through pingmock's control API and removes them when it ends.

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

// rule sets a rule for the duration of the test.
func rule(t *testing.T, pm *client.Client, target, r string) {
	t.Helper()
	if err := pm.SetRule(context.Background(), target, r); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pm.DeleteRule(context.Background(), target) })
}

func near(t *testing.T, what string, got, want time.Duration) {
	t.Helper()
	if d := got - want; d < -5*time.Millisecond || d > 5*time.Millisecond {
		t.Errorf("%s: %v, want about %v", what, got, want)
	}
}

func TestPicksFastest(t *testing.T) {
	pingmock(t) // the servers come from pingmock.yaml

	best, results, err := fastest.Pick(context.Background(), []string{"10.99.0.1", "10.99.0.2", "10.99.0.3"}, fastest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if best != "10.99.0.2" {
		t.Errorf("picked %s, want 10.99.0.2", best)
	}
	order := []string{results[0].Addr, results[1].Addr, results[2].Addr}
	if order[0] != "10.99.0.2" || order[1] != "10.99.0.3" || order[2] != "10.99.0.1" {
		t.Errorf("results in order %v, want fastest first", order)
	}
	near(t, "latency of 10.99.0.2", results[0].Avg, 20*time.Millisecond)
	near(t, "latency of 10.99.0.1", results[2].Avg, 80*time.Millisecond)
}

func TestSkipsUnreachableServer(t *testing.T) {
	pm := pingmock(t)
	rule(t, pm, "10.99.0.21", "delay=10ms,unreachable") // fastest to answer, but with an error
	rule(t, pm, "10.99.0.22", "40ms")

	best, results, err := fastest.Pick(context.Background(), []string{"10.99.0.21", "10.99.0.22"}, fastest.Options{})
	if err != nil || best != "10.99.0.22" {
		t.Fatalf("picked %q (%v), want 10.99.0.22", best, err)
	}
	if r := results[1]; r.Addr != "10.99.0.21" || r.Received != 0 || r.Errors != 3 {
		t.Errorf("unreachable server: %+v, want 3 errors and no replies", r)
	}
}

func TestSkipsServerThatTimesOut(t *testing.T) {
	pm := pingmock(t)
	rule(t, pm, "10.99.0.31", "timeout")
	rule(t, pm, "10.99.0.32", "30ms")

	best, _, err := fastest.Pick(context.Background(), []string{"10.99.0.31", "10.99.0.32"}, fastest.Options{})
	if err != nil || best != "10.99.0.32" {
		t.Fatalf("picked %q (%v), want 10.99.0.32", best, err)
	}

	_, _, err = fastest.Pick(context.Background(), []string{"10.99.0.31"}, fastest.Options{})
	if !errors.Is(err, fastest.ErrNoServer) {
		t.Errorf("with every server down: %v, want ErrNoServer", err)
	}
}

func TestAvoidsLossyServer(t *testing.T) {
	pm := pingmock(t)
	// pattern=.x drops every second ping: always exactly half, unlike
	// loss=50%, which is random (though reproducible with the seed).
	rule(t, pm, "10.99.0.41", "delay=10ms,pattern=.x")
	rule(t, pm, "10.99.0.42", "30ms")

	best, results, err := fastest.Pick(context.Background(), []string{"10.99.0.41", "10.99.0.42"}, fastest.Options{Count: 4})
	if err != nil || best != "10.99.0.42" {
		t.Fatalf("picked %q (%v), want 10.99.0.42", best, err)
	}
	if r := results[1]; r.Received != 2 || r.Loss() != 0.5 {
		t.Errorf("lossy server: %+v, want 2 of 4 replies", r)
	}
}

func TestSwitchesWhenCurrentServerSlowsDown(t *testing.T) {
	pm := pingmock(t)
	ctx := context.Background()
	rule(t, pm, "10.99.0.51", "15ms")
	rule(t, pm, "10.99.0.52", "40ms")
	s := &fastest.Selector{Servers: []string{"10.99.0.51", "10.99.0.52"}, MaxLatency: 100 * time.Millisecond}

	if changed, err := s.Check(ctx); err != nil || !changed || s.Current() != "10.99.0.51" {
		t.Fatalf("first check: %s, changed %v (%v)", s.Current(), changed, err)
	}
	if changed, _ := s.Check(ctx); changed {
		t.Fatalf("switched away from a good server to %s", s.Current())
	}

	// Network conditions change in the middle of the test.
	rule(t, pm, "10.99.0.51", "300ms")
	if changed, err := s.Check(ctx); err != nil || !changed || s.Current() != "10.99.0.52" {
		t.Errorf("after the slowdown: %s, changed %v (%v), want 10.99.0.52", s.Current(), changed, err)
	}
}

func TestSwitchesWhenCurrentServerGoesDown(t *testing.T) {
	pm := pingmock(t)
	// A scenario: the server answers the first 3 pings, then stops. No API
	// call is needed in the middle of the test.
	rule(t, pm, "10.99.0.61", "15ms for 3 pings; timeout")
	rule(t, pm, "10.99.0.62", "40ms")
	s := &fastest.Selector{Servers: []string{"10.99.0.61", "10.99.0.62"}}

	if _, err := s.Check(context.Background()); err != nil || s.Current() != "10.99.0.61" {
		t.Fatalf("first check: %s (%v), want 10.99.0.61", s.Current(), err)
	}
	if changed, err := s.Check(context.Background()); err != nil || !changed || s.Current() != "10.99.0.62" {
		t.Errorf("after it went down: %s, changed %v (%v), want 10.99.0.62", s.Current(), changed, err)
	}
}

func TestSendsConfiguredNumberOfPings(t *testing.T) {
	pm := pingmock(t)
	ctx := context.Background()
	rule(t, pm, "10.99.0.71", "5ms")
	if err := pm.ClearHistory(ctx); err != nil {
		t.Fatal(err)
	}

	fastest.Measure(ctx, "10.99.0.71", fastest.Options{Count: 4})

	// pingmock's history shows what the code under test actually did.
	events, err := pm.History(ctx, "10.99.0.71", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("%d pings sent, want 4", len(events))
	}
	for i, e := range events {
		if int(e.Seq) != i+1 || e.Result != "reply" {
			t.Errorf("ping %d: seq %d, %s", i+1, e.Seq, e.Result)
		}
	}
}

// tcp sets a TCP rule for the duration of the test.
func tcp(t *testing.T, pm *client.Client, target, r string) {
	t.Helper()
	if err := pm.SetTCP(context.Background(), target, r); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pm.DeleteTCP(context.Background(), target) })
}

func TestConnectFailsOverWhenTheConnectionTimesOut(t *testing.T) {
	pm := pingmock(t)
	tcp(t, pm, "10.99.0.81:5432", "timeout") // this database hangs

	start := time.Now()
	conn, server, failed, err := fastest.Connect(context.Background(), []string{"10.99.0.81", "10.99.0.3"}, "5432", 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if server != "10.99.0.3" {
		t.Errorf("connected to %s, want 10.99.0.3", server)
	}
	var ne net.Error
	if len(failed) != 1 || !errors.As(failed[0], &ne) || !ne.Timeout() {
		t.Errorf("failures %v, want one timeout", failed)
	}
	// 300ms waiting for the first, then 50ms to connect to the second
	// (pingmock.yaml), and a little time for the program itself.
	if d := time.Since(start); d < 350*time.Millisecond || d > 400*time.Millisecond {
		t.Errorf("time to connect: %v, want 350ms to 400ms", d)
	}
}

func TestConnectFailsOverAtOnceWhenRefused(t *testing.T) {
	pm := pingmock(t)
	ctx := context.Background()
	if err := pm.ClearHistory(ctx); err != nil {
		t.Fatal(err)
	}
	tcp(t, pm, "10.99.0.82:5432", "refuse") // this one is down: a reset at once

	start := time.Now()
	conn, server, failed, err := fastest.Connect(ctx, []string{"10.99.0.82", "10.99.0.3"}, "5432", 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if server != "10.99.0.3" || len(failed) != 1 || !errors.Is(failed[0], syscall.ECONNREFUSED) {
		t.Errorf("connected to %s after %v, want 10.99.0.3 after one refusal", server, failed)
	}
	near(t, "time to connect", time.Since(start), 50*time.Millisecond)

	// pingmock's history shows what the code under test did: one attempt,
	// refused, and no retry.
	events, err := pm.History(ctx, "10.99.0.82", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Probe != "tcp" || events[0].Port != 5432 || events[0].Result != "refused" {
		t.Errorf("history of 10.99.0.82: %+v, want one refused connection to port 5432", events)
	}
}
