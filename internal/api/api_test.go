package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nanmuyao/pingmock/internal/engine"
	"github.com/nanmuyao/pingmock/internal/icmp"
	"github.com/nanmuyao/pingmock/internal/rule"
)

type apiFixture struct {
	t      *testing.T
	srv    *httptest.Server
	rs     *engine.RuleSet
	hist   *engine.History
	routed []netip.Prefix
	fail   bool
}

func newAPIFixture(t *testing.T) *apiFixture {
	f := &apiFixture{t: t, hist: engine.NewHistory(100)}
	def, _ := rule.ParseScenario("0ms")
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f.rs = engine.NewRuleSet(def, 5, engine.WithClock(func() time.Time { return clock }))
	dnsTarget := func(name string) (string, error) {
		if name == "in-hosts.example" {
			return "", errors.New("in the command's /etc/hosts")
		}
		if err := rule.ValidName(name); err != nil {
			return "", err
		}
		return strings.ToLower(name), nil
	}
	f.srv = httptest.NewServer(NewHandler(f.rs, f.hist, &fakeRoutes{f: f, set: map[netip.Prefix]bool{}}, "v0.0.0-test", dnsTarget, nil))
	t.Cleanup(f.srv.Close)
	return f
}

// fakeRoutes stands in for the device's routes: it records what was
// routed, and fails when f.fail is set.
type fakeRoutes struct {
	f   *apiFixture
	set map[netip.Prefix]bool
}

func (r *fakeRoutes) Ensure(p netip.Prefix) error {
	if r.f.fail {
		return errors.New("ip route failed")
	}
	r.f.routed = append(r.f.routed, p)
	r.set[p] = true
	return nil
}

func (r *fakeRoutes) Sync(want []netip.Prefix) error {
	r.set = map[netip.Prefix]bool{}
	for _, p := range want {
		r.set[p] = true
	}
	return nil
}

func (r *fakeRoutes) Routes() []netip.Prefix {
	var out []netip.Prefix
	for p := range r.set {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// do sends a request and decodes a JSON response body into out (if non-nil).
func (f *apiFixture) do(method, path, body string, out any) int {
	f.t.Helper()
	req, err := http.NewRequest(method, f.srv.URL+path, strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(data, out); err != nil {
			f.t.Fatalf("%s %s: decoding %q: %v", method, path, data, err)
		}
	}
	return resp.StatusCode
}

func TestAPISetRuleTextAndJSON(t *testing.T) {
	f := newAPIFixture(t)
	var resp struct{ Target, Rule string }
	if code := f.do("PUT", "/rules/10.99.0.1", "50ms for 2 pings; timeout", &resp); code != 200 {
		t.Fatalf("PUT text: %d", code)
	}
	if resp.Target != "10.99.0.1/32" || resp.Rule != "delay=50ms for 2 pings; timeout" {
		t.Errorf("response = %+v", resp)
	}
	if code := f.do("PUT", "/rules/10.99.0.128/25", `{"delay":"80ms","pattern":".x"}`, &resp); code != 200 || resp.Rule != "delay=80ms,pattern=.x" {
		t.Errorf("PUT JSON spec: %d %+v", code, resp)
	}
	if code := f.do("PUT", "/rules/10.99.0.2", `{"phases":[{"spec":"5ms","count":1},{"spec":"timeout"}]}`, &resp); code != 200 || resp.Rule != "delay=5ms for 1 ping; timeout" {
		t.Errorf("PUT JSON phases: %d %+v", code, resp)
	}
	if d := f.rs.Decide(netip.MustParseAddr("10.99.0.200")); d.Rule != "10.99.0.128/25" || d.Outcome != rule.Reply {
		t.Errorf("rule not applied: %+v", d)
	}
	if len(f.routed) != 3 || f.routed[1].String() != "10.99.0.128/25" {
		t.Errorf("ensureRoute calls: %v", f.routed)
	}
	if code := f.do("PUT", "/rules/fd99::7", "unreachable=admin", &resp); code != 200 || resp.Target != "fd99::7/128" || resp.Rule != "delay=0s,unreachable=admin" {
		t.Errorf("PUT IPv6: %d %+v", code, resp)
	}
}

func TestAPIRulesAndDelete(t *testing.T) {
	f := newAPIFixture(t)
	f.do("PUT", "/rules/10.99.0.1", "delay=1ms,pattern=.x", nil)
	f.rs.Decide(netip.MustParseAddr("10.99.0.1"))
	f.rs.Decide(netip.MustParseAddr("10.99.0.1"))
	var snap struct {
		Seed    uint64
		Default string
		Rules   []struct{ Target, Rule string }
		Stats   map[string]engine.TargetState
	}
	if code := f.do("GET", "/rules", "", &snap); code != 200 {
		t.Fatalf("GET /rules: %d", code)
	}
	if snap.Seed != 5 || snap.Default != "delay=0s" || len(snap.Rules) != 1 || snap.Rules[0].Rule != "delay=1ms,pattern=.x" {
		t.Errorf("snapshot = %+v", snap)
	}
	if st := snap.Stats["10.99.0.1"]; st.Requests != 2 || st.Dropped != 1 || st.Phase != 1 {
		t.Errorf("stats = %+v", st)
	}
	if code := f.do("DELETE", "/rules/10.99.0.1", "", nil); code != 204 {
		t.Errorf("DELETE: %d", code)
	}
	if code := f.do("DELETE", "/rules/10.99.0.1", "", nil); code != 404 {
		t.Errorf("second DELETE: %d, want 404", code)
	}
	if code := f.do("DELETE", "/stats", "", nil); code != 204 {
		t.Errorf("DELETE /stats: %d", code)
	}
}

func TestAPIDefault(t *testing.T) {
	f := newAPIFixture(t)
	if code := f.do("PUT", "/default", "timeout", nil); code != 200 {
		t.Fatalf("PUT /default: %d", code)
	}
	if d := f.rs.Decide(netip.MustParseAddr("10.99.0.42")); d.Outcome != rule.Drop {
		t.Error("default timeout not applied")
	}
}

func TestAPIErrors(t *testing.T) {
	f := newAPIFixture(t)
	cases := []struct {
		method, path, body string
		want               int
	}{
		{"PUT", "/rules/10.99.0.1", "delay=abc", 400},
		{"PUT", "/rules/10.99.0.1", "5ms; 6ms", 400},
		{"PUT", "/rules/10.99.0.1", `{"delay":`, 400},
		{"PUT", "/rules/fe80::1%25eth0", "5ms", 400},
		{"PUT", "/rules/not-an-ip", "5ms", 400},
		{"PUT", "/default", "", 400},
		{"DELETE", "/rules/nope", "", 400},
		{"GET", "/history?limit=-1", "", 400},
		{"GET", "/history?target=10.0.0.300", "", 400},
		{"POST", "/rules", "", 405},
	}
	for _, c := range cases {
		var body map[string]string
		code := f.do(c.method, c.path, c.body, &body)
		if code != c.want {
			t.Errorf("%s %s %q: %d, want %d", c.method, c.path, c.body, code, c.want)
		}
		if c.want == 400 && body["error"] == "" {
			t.Errorf("%s %s: no error message", c.method, c.path)
		}
	}
	f.fail = true
	if code := f.do("PUT", "/rules/8.8.8.8", "5ms", nil); code != 500 {
		t.Errorf("route failure: %d, want 500", code)
	}
	if d := f.rs.Decide(netip.MustParseAddr("8.8.8.8")); d.Rule != "default" {
		t.Error("rule was installed although routing failed")
	}
}

func TestAPIHistory(t *testing.T) {
	f := newAPIFixture(t)
	for i := uint16(1); i <= 3; i++ {
		f.hist.Add(engine.Event{Dst: netip.MustParseAddr("10.99.0.1"), Seq: i, Reply: true, Delay: "5ms", Rule: "default", Phase: 1})
	}
	f.hist.Add(engine.Event{Dst: netip.MustParseAddr("10.99.0.2"), Seq: 9})
	var resp struct {
		Events   []engine.Event
		Total    uint64
		Capacity int
	}
	if code := f.do("GET", "/history?target=10.99.0.1&limit=2", "", &resp); code != 200 {
		t.Fatalf("GET /history: %d", code)
	}
	if len(resp.Events) != 2 || resp.Events[0].Seq != 2 || resp.Events[1].Seq != 3 || resp.Total != 4 || resp.Capacity != 100 {
		t.Errorf("history = %+v", resp)
	}
	if code := f.do("DELETE", "/history", "", nil); code != 204 {
		t.Errorf("DELETE /history: %d", code)
	}
	f.do("GET", "/history", "", &resp)
	if len(resp.Events) != 0 || resp.Total != 0 {
		t.Errorf("after clear: %+v", resp)
	}
}

func TestAPIResetAndVersion(t *testing.T) {
	f := newAPIFixture(t)
	f.do("PUT", "/rules/10.99.0.1", "5ms", nil)
	f.rs.SaveBaseline() // as the command does once its startup rules are in
	f.do("PUT", "/rules/10.99.0.1", "timeout", nil)
	f.do("PUT", "/rules/10.99.0.2", "timeout", nil)
	f.do("PUT", "/default", "unreachable", nil)
	f.rs.Decide(netip.MustParseAddr("10.99.0.1"))
	f.hist.Add(engine.Event{Dst: netip.MustParseAddr("10.99.0.1"), Seq: 1})

	if code := f.do("POST", "/reset", "", nil); code != 204 {
		t.Fatalf("POST /reset: %d", code)
	}
	var snap struct {
		Default string
		Rules   []struct{ Target, Rule string }
		Stats   map[string]engine.TargetState
	}
	f.do("GET", "/rules", "", &snap)
	if snap.Default != "delay=0s" || len(snap.Rules) != 1 || snap.Rules[0].Rule != "delay=5ms" || len(snap.Stats) != 0 {
		t.Errorf("after reset: %+v", snap)
	}
	var hist struct{ Total uint64 }
	f.do("GET", "/history", "", &hist)
	if hist.Total != 0 {
		t.Errorf("history after reset: %+v", hist)
	}

	var v struct{ Version string }
	if code := f.do("GET", "/version", "", &v); code != 200 || v.Version != "v0.0.0-test" {
		t.Errorf("GET /version: %d %+v", code, v)
	}
	if code := f.do("GET", "/reset", "", nil); code != 405 {
		t.Errorf("GET /reset: %d, want 405", code)
	}
}

func TestAPIPaths(t *testing.T) {
	f := newAPIFixture(t)
	var resp struct{ Target, Path string }
	if code := f.do("PUT", "/paths/8.8.8.8", "192.168.1.1@2ms > * > 72.14.0.1@delay=25ms,cut=5%", &resp); code != 200 {
		t.Fatalf("PUT /paths: %d", code)
	}
	if resp.Target != "8.8.8.8" || resp.Path != "192.168.1.1@delay=2ms > * > 72.14.0.1@delay=25ms,cut=5%" {
		t.Errorf("response = %+v", resp)
	}
	// Routes for the hops and the target, so that their answers are accepted.
	routed := []string{}
	for _, p := range f.routed {
		routed = append(routed, p.String())
	}
	if strings.Join(routed, " ") != "192.168.1.1/32 72.14.0.1/32 8.8.8.8/32" {
		t.Errorf("ensureRoute calls: %v", routed)
	}
	if code := f.do("PUT", "/paths/fd99::9", `"fd99::1:1@3ms"`, &resp); code != 200 || resp.Path != "fd99::1:1@delay=3ms" {
		t.Errorf("PUT JSON string: %d %+v", code, resp)
	}
	var snap struct {
		Paths []struct{ Target, Path string }
	}
	f.do("GET", "/rules", "", &snap)
	if len(snap.Paths) != 2 || snap.Paths[0].Target != "8.8.8.8" || snap.Paths[1].Target != "fd99::9" {
		t.Errorf("paths in snapshot = %+v", snap.Paths)
	}
	if d := f.rs.DecideProbe(icmp.Probe{Dst: netip.MustParseAddr("8.8.8.8"), TTL: 1, Kind: icmp.UDP}); d.Outcome != rule.TimeExceeded || d.From.String() != "192.168.1.1" {
		t.Errorf("path not installed: %+v", d)
	}
	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{"PUT", "/paths/10.0.0.0/24", "1.1.1.1@1ms", 400},
		{"PUT", "/paths/8.8.8.8", "nope", 400},
		{"PUT", "/paths/8.8.8.8", "", 400},
		{"DELETE", "/paths/1.1.1.1", "", 404},
		{"DELETE", "/paths/8.8.8.8", "", 204},
		{"DELETE", "/paths/8.8.8.8", "", 404},
	} {
		if code := f.do(c.method, c.path, c.body, nil); code != c.want {
			t.Errorf("%s %s %q: %d, want %d", c.method, c.path, c.body, code, c.want)
		}
	}
	f.fail = true
	if code := f.do("PUT", "/paths/9.9.9.9", "1.1.1.1@1ms", nil); code != 500 {
		t.Errorf("route failure: %d, want 500", code)
	}
	if _, ok := f.rs.Path(netip.MustParseAddr("9.9.9.9")); ok {
		t.Error("path installed although routing failed")
	}
}

func TestAPIDNS(t *testing.T) {
	f := newAPIFixture(t)
	var got map[string]any
	if code := f.do("PUT", "/dns/API.example.com", "servfail for 2 requests; 10ms", &got); code != 200 ||
		got["target"] != "api.example.com" || got["rule"] != "delay=0s,servfail for 2 requests; delay=10ms" {
		t.Fatalf("PUT text: %d %v", code, got)
	}
	if code := f.do("PUT", "/dns/gone.example", `{"nxdomain": true}`, &got); code != 200 || got["rule"] != "delay=0s,nxdomain" {
		t.Fatalf("PUT JSON: %d %v", code, got)
	}
	if d, ok := f.rs.DecideDNS("api.example.com", 1); !ok || d.Outcome != rule.ServFail {
		t.Errorf("rule not installed: %+v", d)
	}
	for path, body := range map[string]string{
		"/dns/in-hosts.example": "nxdomain", // rejected by dnsTarget
		"/dns/bad_name..":       "nxdomain",
		"/dns/ok.example":       "refuse", // not a DNS setting
		"/dns/ok2.example":      "{bad json",
	} {
		var e map[string]string
		if code := f.do("PUT", path, body, &e); code != 400 || e["error"] == "" {
			t.Errorf("PUT %s %q: %d %v", path, body, code, e)
		}
	}
	var state struct {
		DNS []map[string]string `json:"dns"`
	}
	f.do("GET", "/rules", "", &state)
	if len(state.DNS) != 2 || state.DNS[0]["target"] != "api.example.com" {
		t.Errorf("GET /rules dns: %v", state.DNS)
	}
	if code := f.do("DELETE", "/dns/gone.example", "", nil); code != 204 {
		t.Errorf("DELETE: %d", code)
	}
	if code := f.do("DELETE", "/dns/gone.example", "", nil); code != 404 {
		t.Errorf("DELETE again: %d", code)
	}
	// The history of a name: GET /history?target=NAME.
	f.hist.Add(engine.Event{Probe: "dns", Name: "api.example.com", QType: "A", Result: "servfail"})
	f.hist.Add(engine.Event{Dst: netip.MustParseAddr("10.99.0.1"), Result: "reply"})
	var h struct {
		Events []engine.Event `json:"events"`
	}
	if code := f.do("GET", "/history?target=api.example.com", "", &h); code != 200 || len(h.Events) != 1 || h.Events[0].QType != "A" {
		t.Errorf("history by name: %d %+v", code, h.Events)
	}
	if code := f.do("GET", "/history?target=10.99.0.1", "", &h); code != 200 || len(h.Events) != 1 || h.Events[0].Result != "reply" {
		t.Errorf("history by address: %d %+v", code, h.Events)
	}
	if code := f.do("GET", "/history?target=not%20a%20name", "", nil); code != 400 {
		t.Errorf("history with a bad target: %d", code)
	}
}

// Routes follow the rules: GET /rules lists what is routed, a delete keeps
// what another rule still needs, a replaced path drops its old hops, and
// a reset brings back the routes of the startup rules.
func TestAPIRoutesFollowRules(t *testing.T) {
	f := newAPIFixture(t)
	routes := func() string {
		var st struct {
			Routes []string `json:"routes"`
		}
		if code := f.do("GET", "/rules", "", &st); code != 200 || st.Routes == nil {
			t.Fatalf("GET /rules: %d, routes %v (must be a list)", code, st.Routes)
		}
		return strings.Join(st.Routes, " ")
	}
	if r := routes(); r != "" {
		t.Errorf("at start: routes %q", r)
	}
	f.rs.Set(netip.MustParsePrefix("192.0.2.9/32"), mustScenario(t, "1ms"))
	f.rs.SaveBaseline() // a startup rule
	for _, req := range [][3]string{
		{"PUT", "/rules/8.8.8.8", "5ms"}, {"PUT", "/tcp/8.8.8.8:443", "refuse"},
		{"PUT", "/paths/9.9.9.9", "192.0.2.1@1ms > 192.0.2.2@2ms"},
	} {
		if code := f.do(req[0], req[1], req[2], nil); code != 200 {
			t.Fatalf("%s %s: %d", req[0], req[1], code)
		}
	}
	if r := routes(); r != "192.0.2.1/32 192.0.2.2/32 192.0.2.9/32 8.8.8.8/32 9.9.9.9/32" {
		t.Errorf("after PUTs: routes %q", r)
	}
	steps := []struct{ method, path, body, want string }{
		{"DELETE", "/rules/8.8.8.8", "", "192.0.2.1/32 192.0.2.2/32 192.0.2.9/32 8.8.8.8/32 9.9.9.9/32"}, // the TCP rule needs it
		{"DELETE", "/tcp/8.8.8.8:443", "", "192.0.2.1/32 192.0.2.2/32 192.0.2.9/32 9.9.9.9/32"},
		{"PUT", "/paths/9.9.9.9", "192.0.2.5@1ms", "192.0.2.5/32 192.0.2.9/32 9.9.9.9/32"},
		{"DELETE", "/paths/9.9.9.9", "", "192.0.2.9/32"},
		{"DELETE", "/rules/192.0.2.9", "", ""},
		{"POST", "/reset", "", "192.0.2.9/32"},
	}
	for _, s := range steps {
		if code := f.do(s.method, s.path, s.body, nil); code >= 300 {
			t.Fatalf("%s %s: %d", s.method, s.path, code)
		}
		if r := routes(); r != s.want {
			t.Errorf("after %s %s: routes %q, want %q", s.method, s.path, r, s.want)
		}
	}
}

func mustScenario(t *testing.T, s string) rule.Scenario {
	t.Helper()
	sc, err := rule.ParseScenario(s)
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

// Lists are [] when empty, never null, so that a client can loop over
// them without a check: an empty history, and the rules of a fresh start.
func TestAPIEmptyListsAreNotNull(t *testing.T) {
	f := newAPIFixture(t)
	for _, path := range []string{"/history", "/history?target=10.99.0.1", "/history?target=api.example.com", "/rules"} {
		var raw map[string]json.RawMessage
		if code := f.do("GET", path, "", &raw); code != 200 {
			t.Fatalf("GET %s: %d", path, code)
		}
		for k, v := range raw {
			if string(v) == "null" {
				t.Errorf("GET %s: %q is null", path, k)
			}
		}
	}
}
