package client

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/nanmuyao/pingmock/internal/api"
	"github.com/nanmuyao/pingmock/internal/engine"
	"github.com/nanmuyao/pingmock/internal/rule"
)

// serve runs the real API handler, so the client is tested against the
// server it is written for.
func serve(t *testing.T) (*Client, *engine.RuleSet, *engine.History) {
	t.Helper()
	def, _ := rule.ParseScenario("0ms")
	rs := engine.NewRuleSet(def, 42)
	hist := engine.NewHistory(100)
	srv := httptest.NewServer(api.NewHandler(rs, hist, func(netip.Prefix) error { return nil }, "v0.0.0-test"))
	t.Cleanup(srv.Close)
	return New(srv.URL), rs, hist
}

func TestRulesAndState(t *testing.T) {
	c, rs, _ := serve(t)
	ctx := context.Background()
	if err := c.SetRule(ctx, "10.99.0.1", "delay=5ms,pattern=.x"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetRule(ctx, "fd99::/64", "unreachable=admin"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetDefault(ctx, "timeout"); err != nil {
		t.Fatal(err)
	}
	rs.Decide(netip.MustParseAddr("10.99.0.1"))
	rs.Decide(netip.MustParseAddr("10.99.0.1"))

	s, err := c.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.Seed != 42 || s.Default != "timeout" || len(s.Rules) != 2 {
		t.Fatalf("state = %+v", s)
	}
	if s.Rules[0] != (Rule{"10.99.0.1/32", "delay=5ms,pattern=.x"}) || s.Rules[1] != (Rule{"fd99::/64", "delay=0s,unreachable=admin"}) {
		t.Errorf("rules = %+v", s.Rules)
	}
	st, err := c.Stats(ctx, "10.99.0.1")
	if err != nil || st.Requests != 2 || st.Replied != 1 || st.Dropped != 1 || st.Phase != 1 {
		t.Errorf("stats = %+v, %v", st, err)
	}
	if err := c.ResetStats(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := c.Stats(ctx, "10.99.0.1"); st.Requests != 0 {
		t.Errorf("after reset: %+v", st)
	}
	if st, _ := c.Stats(ctx, "10.99.0.77"); st != (TargetStats{}) {
		t.Errorf("unknown target: %+v", st)
	}
}

func TestDeleteRule(t *testing.T) {
	c, _, _ := serve(t)
	ctx := context.Background()
	c.SetRule(ctx, "10.99.0.1", "5ms")
	if err := c.DeleteRule(ctx, "10.99.0.1"); err != nil {
		t.Fatal(err)
	}
	err := c.DeleteRule(ctx, "10.99.0.1")
	if !IsNotFound(err) {
		t.Errorf("second delete: %v, want not found", err)
	}
}

func TestErrors(t *testing.T) {
	c, _, _ := serve(t)
	err := c.SetRule(context.Background(), "10.99.0.1", "5ms; 6ms")
	var e *Error
	if !errors.As(err, &e) || e.StatusCode != 400 || e.Message == "" {
		t.Fatalf("invalid rule: %v", err)
	}
	if IsNotFound(err) {
		t.Error("a bad request reported as not found")
	}
	unreachable := New("http://127.0.0.1:1")
	if err := unreachable.SetDefault(context.Background(), "5ms"); err == nil || errors.As(err, &e) {
		t.Errorf("connection failure: %v, want a non-API error", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.SetDefault(ctx, "5ms"); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled context: %v", err)
	}
}

func TestHistory(t *testing.T) {
	c, _, hist := serve(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	hist.Add(engine.Event{Time: at, Dst: netip.MustParseAddr("10.99.0.1"), Seq: 1, Result: "reply", Reply: true, Delay: "5ms", Rule: "default", Phase: 1})
	hist.Add(engine.Event{Time: at, Dst: netip.MustParseAddr("10.99.0.1"), Seq: 2, Result: "unreachable", Code: "host", Delay: "5ms", Rule: "default", Phase: 1})
	hist.Add(engine.Event{Time: at, Dst: netip.MustParseAddr("fd99::1"), Seq: 3, Result: "drop", Rule: "default", Phase: 1})

	all, err := c.History(ctx, "", 0)
	if err != nil || len(all) != 3 {
		t.Fatalf("all: %d events, %v", len(all), err)
	}
	if !all[0].Time.Equal(at) || all[1].Result != "unreachable" || all[1].Code != "host" || all[2].Delay != "" {
		t.Errorf("events = %+v", all)
	}
	last, _ := c.History(ctx, "10.99.0.1", 1)
	if len(last) != 1 || last[0].Seq != 2 {
		t.Errorf("filtered and limited: %+v", last)
	}
	v6, _ := c.History(ctx, "fd99::/64", 0)
	if len(v6) != 1 || v6[0].Seq != 3 {
		t.Errorf("IPv6 prefix: %+v", v6)
	}
	if err := c.ClearHistory(ctx); err != nil {
		t.Fatal(err)
	}
	if after, _ := c.History(ctx, "", 0); len(after) != 0 {
		t.Errorf("after clear: %+v", after)
	}
}

func TestNewAndFromEnv(t *testing.T) {
	t.Setenv(EnvAPI, "")
	if New("").URL() != DefaultURL {
		t.Errorf("default URL = %s", New("").URL())
	}
	if _, err := FromEnv(); !errors.Is(err, ErrNotUnderExec) {
		t.Errorf("FromEnv without the variable: %v", err)
	}
	t.Setenv(EnvAPI, "http://127.0.0.1:9200/")
	c, err := FromEnv()
	if err != nil || c.URL() != "http://127.0.0.1:9200" {
		t.Errorf("FromEnv = %v, %v", c, err)
	}
	if New("http://example.test:1").URL() != "http://example.test:1" {
		t.Error("explicit URL not used")
	}
}

func TestResetAndVersion(t *testing.T) {
	c, rs, hist := serve(t)
	ctx := context.Background()
	if err := c.SetRule(ctx, "10.99.0.1", "5ms"); err != nil {
		t.Fatal(err)
	}
	rs.SaveBaseline()
	c.SetRule(ctx, "10.99.0.1", "timeout")
	c.SetDefault(ctx, "timeout")
	rs.Decide(netip.MustParseAddr("10.99.0.1"))
	hist.Add(engine.Event{Dst: netip.MustParseAddr("10.99.0.1")})

	if err := c.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := c.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.Default != "delay=0s" || len(s.Rules) != 1 || s.Rules[0] != (Rule{"10.99.0.1/32", "delay=5ms"}) || len(s.Stats) != 0 {
		t.Errorf("state after reset = %+v", s)
	}
	if events, _ := c.History(ctx, "", 0); len(events) != 0 {
		t.Errorf("history after reset = %+v", events)
	}
	if v, err := c.Version(ctx); err != nil || v != "v0.0.0-test" {
		t.Errorf("Version() = %q, %v", v, err)
	}
}

func TestPaths(t *testing.T) {
	c, rs, _ := serve(t)
	ctx := context.Background()
	if err := c.SetPath(ctx, "8.8.8.8", "192.168.1.1@2ms > * > 72.14.0.1@25ms"); err != nil {
		t.Fatal(err)
	}
	s, err := c.State(ctx)
	if err != nil || len(s.Paths) != 1 || s.Paths[0] != (Path{"8.8.8.8", "192.168.1.1@delay=2ms > * > 72.14.0.1@delay=25ms"}) {
		t.Errorf("paths = %+v, %v", s.Paths, err)
	}
	if _, ok := rs.Path(netip.MustParseAddr("8.8.8.8")); !ok {
		t.Error("path not installed")
	}
	if err := c.SetPath(ctx, "8.8.8.8", "nope"); err == nil {
		t.Error("a bad path was accepted")
	}
	if err := c.DeletePath(ctx, "8.8.8.8"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeletePath(ctx, "8.8.8.8"); !IsNotFound(err) {
		t.Errorf("second delete: %v", err)
	}
}
