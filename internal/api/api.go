// Package api serves the HTTP control API used to change rules and read
// statistics and history while pingmock runs.
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"

	"github.com/nanmuyao-tools/pingmock/internal/engine"
	"github.com/nanmuyao-tools/pingmock/internal/rule"
)

// Routes are the routes that bring traffic for mocked targets to pingmock.
type Routes interface {
	Ensure(netip.Prefix) error      // before a rule for the prefix is installed
	Sync(want []netip.Prefix) error // after rules are removed: exactly these stay routed
	Routes() []netip.Prefix         // what is routed, besides the device's subnets
}

// NewHandler returns the HTTP control API. routes get the route a rule's
// target needs before the rule is installed, so that its traffic reaches
// pingmock, and lose the ones no rule needs once rules go. version is
// what GET /version reports. dnsTarget checks the name of a DNS rule and
// whether such a rule can take effect, and returns it as rules are keyed.
// facts are added to GET /rules as they are: what was set up at startup
// and cannot change, such as pingmock exec's outside access.
func NewHandler(rs *engine.RuleSet, hist *engine.History, routes Routes, version string,
	dnsTarget func(name string) (string, error), facts map[string]any) http.Handler {
	h := &handler{rules: rs, history: hist, routes: routes, version: version, dnsTarget: dnsTarget, facts: facts}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /rules", h.getRules)                  // default, rules and per-target stats
	mux.HandleFunc("PUT /rules/{target...}", h.setRule)       // body: the text form or JSON
	mux.HandleFunc("DELETE /rules/{target...}", h.deleteRule) // the default applies again
	mux.HandleFunc("PUT /default", h.setDefault)              // body: the text form or JSON
	mux.HandleFunc("DELETE /stats", h.resetStats)             // zero all counters
	mux.HandleFunc("GET /history", h.getHistory)              // ?target=IP, CIDR or, for DNS queries, a name&limit=N
	mux.HandleFunc("DELETE /history", h.clearHistory)         // forget recorded requests
	mux.HandleFunc("POST /reset", h.reset)                    // everything as at startup
	mux.HandleFunc("GET /version", h.getVersion)              // {"version": "vX.Y.Z"}
	mux.HandleFunc("PUT /paths/{target...}", h.setPath)       // body: hops such as "10.0.0.1@2ms > * > 10.0.0.3@20ms"
	mux.HandleFunc("DELETE /paths/{target...}", h.deletePath) // the target has no route again
	mux.HandleFunc("PUT /tcp/{target...}", h.setTCP)          // target ADDR[:PORT], PREFIX[:PORT], [IPv6]:PORT; body as for rules
	mux.HandleFunc("DELETE /tcp/{target...}", h.deleteTCP)    // connections to it are ignored again
	mux.HandleFunc("PUT /udp/{target...}", h.setUDP)          // target as for /tcp/; body as for rules
	mux.HandleFunc("DELETE /udp/{target...}", h.deleteUDP)    // datagrams to it are ignored again
	mux.HandleFunc("PUT /dns/{name}", h.setDNS)               // pingmock exec only; body as for rules
	mux.HandleFunc("DELETE /dns/{name}", h.deleteDNS)         // queries for it are answered as without a rule
	return mux
}

// setDNS installs a DNS rule for a name.
func (h *handler) setDNS(w http.ResponseWriter, r *http.Request) {
	name, err := h.dnsTarget(r.PathValue("name"))
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	text := strings.TrimSpace(string(body))
	var sc rule.DNSScenario
	if strings.HasPrefix(text, "{") {
		if err := json.Unmarshal([]byte(text), &sc); err != nil {
			httpError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON rule: %w", err))
			return
		}
	} else if sc, err = rule.ParseDNSScenario(text); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	h.rules.SetDNS(name, sc.Scenario)
	writeJSON(w, http.StatusOK, map[string]any{"target": name, "rule": sc})
}

func (h *handler) deleteDNS(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !h.rules.DeleteDNS(name) {
		httpError(w, http.StatusNotFound, fmt.Errorf("no DNS rule for %s", name))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// tcpTarget parses the target of a TCP or UDP rule in the URL. Names are only
// resolved at startup, so the API takes addresses and prefixes.
func tcpTarget(r *http.Request) (rule.TCPTarget, error) {
	t, err := rule.ParseTCPTarget(r.PathValue("target"))
	if err != nil {
		return rule.TCPTarget{}, err
	}
	if t.Name != "" {
		return rule.TCPTarget{}, fmt.Errorf("%s: the API takes an address or prefix, such as 10.99.0.1:443", t.Name)
	}
	return t, nil
}

// setTCP installs a TCP rule. Its addresses get a route to the device
// first, as for ping rules.
func (h *handler) setTCP(w http.ResponseWriter, r *http.Request) {
	t, err := tcpTarget(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	text := strings.TrimSpace(string(body))
	var sc rule.TCPScenario
	if strings.HasPrefix(text, "{") {
		if err := json.Unmarshal([]byte(text), &sc); err != nil {
			httpError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON rule: %w", err))
			return
		}
	} else if sc, err = rule.ParseTCPScenario(text); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	h.routing.Lock()
	defer h.routing.Unlock()
	if err := h.ensureRoute(t.Prefix); err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	h.rules.SetTCP(t.Prefix, t.Port, sc.Scenario)
	writeJSON(w, http.StatusOK, map[string]any{"target": t.String(), "rule": sc})
}

// setUDP installs a UDP rule, as setTCP does a TCP rule.
func (h *handler) setUDP(w http.ResponseWriter, r *http.Request) {
	t, err := tcpTarget(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	text := strings.TrimSpace(string(body))
	var sc rule.UDPScenario
	if strings.HasPrefix(text, "{") {
		if err := json.Unmarshal([]byte(text), &sc); err != nil {
			httpError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON rule: %w", err))
			return
		}
	} else if sc, err = rule.ParseUDPScenario(text); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	h.routing.Lock()
	defer h.routing.Unlock()
	if err := h.ensureRoute(t.Prefix); err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	h.rules.SetUDP(t.Prefix, t.Port, sc.Scenario)
	writeJSON(w, http.StatusOK, map[string]any{"target": t.String(), "rule": sc})
}

func (h *handler) deleteUDP(w http.ResponseWriter, r *http.Request) {
	t, err := tcpTarget(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	h.routing.Lock()
	defer h.routing.Unlock()
	if !h.rules.DeleteUDP(t.Prefix, t.Port) {
		httpError(w, http.StatusNotFound, fmt.Errorf("no UDP rule for %s", t))
		return
	}
	if h.syncRoutes(w) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *handler) deleteTCP(w http.ResponseWriter, r *http.Request) {
	t, err := tcpTarget(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	h.routing.Lock()
	defer h.routing.Unlock()
	if !h.rules.DeleteTCP(t.Prefix, t.Port) {
		httpError(w, http.StatusNotFound, fmt.Errorf("no TCP rule for %s", t))
		return
	}
	if h.syncRoutes(w) {
		w.WriteHeader(http.StatusNoContent)
	}
}

// setPath gives a target a route. The target and its hops get routes to
// the device first, so that hops can answer from their addresses.
func (h *handler) setPath(w http.ResponseWriter, r *http.Request) {
	p, err := rule.ParsePrefix(r.PathValue("target"))
	if err != nil || !p.IsSingleIP() {
		httpError(w, http.StatusBadRequest, fmt.Errorf("a path's target must be a single address: %q", r.PathValue("target")))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	text := strings.TrimSpace(string(body))
	var path rule.Path
	if strings.HasPrefix(text, "\"") {
		err = json.Unmarshal([]byte(text), &path)
	} else {
		path, err = rule.ParsePath(text)
	}
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	h.routing.Lock()
	defer h.routing.Unlock()
	for _, a := range append(path.Addrs(), p.Addr()) {
		if err := h.ensureRoute(netip.PrefixFrom(a, a.BitLen())); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
	}
	h.rules.SetPath(p.Addr(), path)
	// A path replaced may leave hops of the old one without a rule.
	if !h.syncRoutes(w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"target": p.Addr().String(), "path": path})
}

func (h *handler) deletePath(w http.ResponseWriter, r *http.Request) {
	p, err := rule.ParsePrefix(r.PathValue("target"))
	if err != nil || !p.IsSingleIP() {
		httpError(w, http.StatusBadRequest, fmt.Errorf("a path's target must be a single address: %q", r.PathValue("target")))
		return
	}
	h.routing.Lock()
	defer h.routing.Unlock()
	if !h.rules.DeletePath(p.Addr()) {
		httpError(w, http.StatusNotFound, fmt.Errorf("no path to %s", p.Addr()))
		return
	}
	if h.syncRoutes(w) {
		w.WriteHeader(http.StatusNoContent)
	}
}

type handler struct {
	rules   *engine.RuleSet
	history *engine.History
	routes  Routes
	// routing orders the changes of rules and their routes, so that a
	// rule's route cannot be removed between being added and the rule
	// being installed.
	routing   sync.Mutex
	version   string
	dnsTarget func(string) (string, error)
	facts     map[string]any
}

// ensureRoute routes p to pingmock, for a rule about to be installed; the
// caller holds h.routing.
func (h *handler) ensureRoute(p netip.Prefix) error { return h.routes.Ensure(p) }

// syncRoutes removes the routes no rule needs any more, and adds back those
// a reset brought rules back for; the caller holds h.routing. It reports
// whether it succeeded, having written the error otherwise.
func (h *handler) syncRoutes(w http.ResponseWriter) bool {
	if err := h.routes.Sync(h.rules.Routed()); err != nil {
		httpError(w, http.StatusInternalServerError, fmt.Errorf("routes: %w", err))
		return false
	}
	return true
}

func (h *handler) getRules(w http.ResponseWriter, r *http.Request) {
	snap := h.rules.Snapshot()
	routes := []string{}
	for _, p := range h.routes.Routes() {
		routes = append(routes, p.String())
	}
	snap["routes"] = routes
	for k, v := range h.facts {
		snap[k] = v
	}
	writeJSON(w, http.StatusOK, snap)
}

func (h *handler) setRule(w http.ResponseWriter, r *http.Request) {
	p, err := rule.ParsePrefix(r.PathValue("target"))
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	sc, err := readScenario(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	h.routing.Lock()
	defer h.routing.Unlock()
	if err := h.ensureRoute(p); err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	h.rules.Set(p, sc)
	writeJSON(w, http.StatusOK, map[string]any{"target": p.String(), "rule": sc})
}

func (h *handler) deleteRule(w http.ResponseWriter, r *http.Request) {
	p, err := rule.ParsePrefix(r.PathValue("target"))
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	h.routing.Lock()
	defer h.routing.Unlock()
	if !h.rules.Delete(p) {
		httpError(w, http.StatusNotFound, fmt.Errorf("no rule for %s", p))
		return
	}
	if h.syncRoutes(w) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *handler) setDefault(w http.ResponseWriter, r *http.Request) {
	sc, err := readScenario(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	h.rules.SetDefault(sc)
	writeJSON(w, http.StatusOK, map[string]any{"default": sc})
}

func (h *handler) resetStats(w http.ResponseWriter, r *http.Request) {
	h.rules.ResetStats()
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) getHistory(w http.ResponseWriter, r *http.Request) {
	var filter *netip.Prefix
	name := ""
	if t := r.URL.Query().Get("target"); t != "" {
		p, err := rule.ParsePrefix(t)
		if err != nil {
			if !isName(t) {
				httpError(w, http.StatusBadRequest, err)
				return
			}
			name = t // the DNS queries for a name
		} else {
			filter = &p
		}
	}
	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 0 {
			httpError(w, http.StatusBadRequest, fmt.Errorf("invalid limit %q", l))
			return
		}
		limit = n
	}
	var events []engine.Event
	var total uint64
	if name != "" {
		events, total = h.history.EventsNamed(name, limit)
	} else {
		events, total = h.history.Events(filter, limit)
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "total": total, "capacity": h.history.Capacity()})
}

// isName reports whether s is a host name rather than a mistyped address:
// valid, and with a top-level label that is not a number (RFC 3696 2).
func isName(s string) bool {
	if rule.ValidName(s) != nil {
		return false
	}
	labels := strings.Split(strings.TrimSuffix(s, "."), ".")
	return strings.Trim(labels[len(labels)-1], "0123456789") != ""
}

func (h *handler) clearHistory(w http.ResponseWriter, r *http.Request) {
	h.history.Clear()
	w.WriteHeader(http.StatusNoContent)
}

// reset puts rules, default, statistics, history and routes back as they
// were at startup, so that a test starts from a known state whatever ran
// before.
func (h *handler) reset(w http.ResponseWriter, r *http.Request) {
	h.routing.Lock()
	defer h.routing.Unlock()
	h.rules.Reset()
	h.history.Clear()
	if h.syncRoutes(w) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *handler) getVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": h.version})
}

// readScenario accepts the text form used by the -rule flag
// ("delay=120ms,loss=5%", "50ms for 3 pings; timeout") or JSON.
func readScenario(r *http.Request) (rule.Scenario, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return rule.Scenario{}, err
	}
	text := strings.TrimSpace(string(body))
	if strings.HasPrefix(text, "{") {
		var sc rule.Scenario
		if err := json.Unmarshal([]byte(text), &sc); err != nil {
			return rule.Scenario{}, fmt.Errorf("invalid JSON rule: %w", err)
		}
		return sc, nil
	}
	return rule.ParseScenario(text)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // paths contain ">", which should read as such
	enc.Encode(v)
}

func httpError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
