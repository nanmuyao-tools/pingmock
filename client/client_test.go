package client

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/nanmuyao-tools/ghostwire/internal/api"
	"github.com/nanmuyao-tools/ghostwire/internal/engine"
	"github.com/nanmuyao-tools/ghostwire/internal/icmp"
	"github.com/nanmuyao-tools/ghostwire/internal/rule"
)

// serve runs the real API handler, so the client is tested against the
// server it is written for.
func serve(t *testing.T) (*Client, *engine.RuleSet, *engine.History) {
	t.Helper()
	def, _ := rule.ParseScenario("0ms")
	rs := engine.NewRuleSet(def, 42)
	hist := engine.NewHistory(100)
	dnsTarget := func(name string) (string, error) { return strings.ToLower(name), rule.ValidName(name) }
	srv := httptest.NewServer(api.NewHandler(rs, hist, noRoutes{}, "v0.0.0-test", dnsTarget, nil))
	t.Cleanup(srv.Close)
	return New(srv.URL), rs, hist
}

// noRoutes stands in for the device's routes, which the client's tests do
// not look at.
type noRoutes struct{}

func (noRoutes) Ensure(netip.Prefix) error { return nil }
func (noRoutes) Sync([]netip.Prefix) error { return nil }
func (noRoutes) Routes() []netip.Prefix    { return nil }

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

func TestTCPRules(t *testing.T) {
	c, rs, hist := serve(t)
	ctx := context.Background()
	if err := c.SetTCP(ctx, "10.99.0.1:5432", "refuse for 1 requests; 5ms"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetTCP(ctx, "[fd99::1]:443", "timeout"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetTCP(ctx, "fd99::1:443", "timeout"); err == nil {
		t.Error("IPv6 without brackets accepted")
	}
	probe := icmp.Probe{Kind: icmp.TCP, Src: netip.MustParseAddr("10.99.0.254"), Dst: netip.MustParseAddr("10.99.0.1"),
		TTL: 64, SrcPort: 40000, DstPort: 5432, TCPSeq: 1}
	d := rs.DecideProbe(probe)
	hist.Add(engine.Event{Dst: probe.Dst, Result: d.Outcome.String(), Probe: "tcp", Port: 5432, Rule: d.Rule, Phase: d.Phase})

	s, err := c.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.TCP) != 2 || s.TCP[0].Target != "10.99.0.1/32:5432" || s.TCP[0].Rule != "delay=0s,refuse for 1 request; delay=5ms" || s.TCP[1].Target != "[fd99::1/128]:443" {
		t.Errorf("TCP rules = %+v", s.TCP)
	}
	st, err := c.TCPStats(ctx, "10.99.0.1:5432")
	if err != nil || st.Requests != 1 || st.Refused != 1 || st.Rule != "tcp 10.99.0.1/32:5432" {
		t.Errorf("TCPStats = %+v, %v", st, err)
	}
	events, err := c.History(ctx, "10.99.0.1", 0)
	if err != nil || len(events) != 1 || events[0].Port != 5432 || events[0].Result != "refused" || events[0].Probe != "tcp" {
		t.Errorf("history = %+v, %v", events, err)
	}
	if err := c.DeleteTCP(ctx, "[fd99::1]:443"); err != nil {
		t.Errorf("DeleteTCP: %v", err)
	}
	if err := c.DeleteTCP(ctx, "[fd99::1]:443"); !IsNotFound(err) {
		t.Errorf("DeleteTCP again: %v, want not found", err)
	}
	// after=close: the connection is closed on the handshake's final ACK,
	// and TCPStats says so.
	if err := c.SetTCP(ctx, "10.99.0.2:80", "after=close"); err != nil {
		t.Fatal(err)
	}
	probe.Dst, probe.DstPort = netip.MustParseAddr("10.99.0.2"), 80
	rs.DecideProbe(probe)
	probe.Kind, probe.TCPFlags, probe.TCPSeq, probe.TCPAck = icmp.Segment, icmp.FlagACK, 2, icmp.SynAckSeq+1
	rs.DecideSegment(probe)
	if st, err := c.TCPStats(ctx, "10.99.0.2:80"); err != nil || st.Accepted != 1 || st.Closed != 1 || st.Reset != 0 {
		t.Errorf("TCPStats after=close = %+v, %v", st, err)
	}
}

func TestDNSRules(t *testing.T) {
	c, rs, hist := serve(t)
	ctx := context.Background()
	if err := c.SetDNS(ctx, "API.example.com", "servfail for 1 request; 5ms"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetDNS(ctx, "gone.example", "refuse"); err == nil {
		t.Error("a TCP setting accepted in a DNS rule")
	}
	d, _ := rs.DecideDNS("api.example.com", 28)
	hist.Add(engine.Event{Probe: "dns", Name: "api.example.com", QType: "AAAA", Result: d.Outcome.String(), Rule: d.Rule, Phase: d.Phase})
	s, err := c.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.DNS) != 1 || s.DNS[0].Target != "api.example.com" || s.DNS[0].Rule != "delay=0s,servfail for 1 request; delay=5ms" {
		t.Errorf("DNS rules: %+v", s.DNS)
	}
	st, err := c.DNSStats(ctx, "api.example.com.", "AAAA")
	if err != nil || st.Requests != 1 || st.ServFail != 1 || st.Rule != "dns api.example.com" {
		t.Errorf("DNSStats: %+v, %v", st, err)
	}
	events, err := c.History(ctx, "api.example.com", 0)
	if err != nil || len(events) != 1 || events[0].Name != "api.example.com" || events[0].QType != "AAAA" || events[0].Result != "servfail" {
		t.Errorf("History: %+v, %v", events, err)
	}
	if err := c.DeleteDNS(ctx, "api.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteDNS(ctx, "api.example.com"); !IsNotFound(err) {
		t.Errorf("deleting again: %v", err)
	}
}

func TestUDPRules(t *testing.T) {
	c, rs, _ := serve(t)
	ctx := context.Background()
	if err := c.SetUDP(ctx, "10.99.0.1:514", "refuse"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetUDP(ctx, "10.99.0.1:515", "20ms"); err == nil {
		t.Error("a UDP rule that answers nothing was accepted")
	}
	rs.DecideProbe(icmp.Probe{Kind: icmp.UDP, Src: netip.MustParseAddr("10.99.0.254"), Dst: netip.MustParseAddr("10.99.0.1"), TTL: 64, SrcPort: 4, DstPort: 514})
	st, err := c.UDPStats(ctx, "10.99.0.1:514")
	if err != nil || st.Requests != 1 || st.Refused != 1 || st.Rule != "udp 10.99.0.1/32:514" {
		t.Errorf("UDPStats = %+v, %v", st, err)
	}
	s, _ := c.State(ctx)
	if len(s.UDP) != 1 || s.UDP[0].Rule != "delay=0s,refuse" {
		t.Errorf("State.UDP = %+v", s.UDP)
	}
	if err := c.DeleteUDP(ctx, "10.99.0.1:514"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteUDP(ctx, "10.99.0.1:514"); !IsNotFound(err) {
		t.Errorf("deleting again: %v", err)
	}
}

// The facts ghostwire exec sets up at startup, such as its outside access,
// come with the state; without them, Outside is nil.
func TestStateOutside(t *testing.T) {
	c, _, _ := serve(t)
	if st, err := c.State(context.Background()); err != nil || st.Outside != nil {
		t.Fatalf("without outside access: %+v, %v", st, err)
	}
	def, _ := rule.ParseScenario("0ms")
	facts := map[string]any{"outside": map[string]any{"address": "10.0.2.15/24", "gateway": "10.0.2.2", "ports": []string{"5432/tcp"}}}
	srv := httptest.NewServer(api.NewHandler(engine.NewRuleSet(def, 1), engine.NewHistory(1), noRoutes{}, "v0.0.0-test", nil, facts))
	t.Cleanup(srv.Close)
	st, err := New(srv.URL).State(context.Background())
	if err != nil || st.Outside == nil || st.Outside.Address != "10.0.2.15/24" || st.Outside.Gateway != "10.0.2.2" || strings.Join(st.Outside.Ports, ",") != "5432/tcp" {
		t.Errorf("outside = %+v, %v", st.Outside, err)
	}
}
