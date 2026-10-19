package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
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
	ensure := func(p netip.Prefix) error {
		if f.fail {
			return errors.New("ip route failed")
		}
		f.routed = append(f.routed, p)
		return nil
	}
	f.srv = httptest.NewServer(NewHandler(f.rs, f.hist, ensure, "v0.0.0-test"))
	t.Cleanup(f.srv.Close)
	return f
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
	if d := f.rs.DecideProbe(netip.MustParseAddr("8.8.8.8"), 1, icmp.UDP); d.Outcome != rule.TimeExceeded || d.From.String() != "192.168.1.1" {
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
