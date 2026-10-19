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

	"github.com/nanmuyao/pingmock/internal/engine"
	"github.com/nanmuyao/pingmock/internal/rule"
)

// NewHandler returns the HTTP control API. ensureRoute is called before a
// rule is installed, so that pings to its target reach pingmock. version is
// what GET /version reports.
func NewHandler(rs *engine.RuleSet, hist *engine.History, ensureRoute func(netip.Prefix) error, version string) http.Handler {
	h := &handler{rules: rs, history: hist, ensureRoute: ensureRoute, version: version}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /rules", h.getRules)                  // default, rules and per-target stats
	mux.HandleFunc("PUT /rules/{target...}", h.setRule)       // body: the text form or JSON
	mux.HandleFunc("DELETE /rules/{target...}", h.deleteRule) // the default applies again
	mux.HandleFunc("PUT /default", h.setDefault)              // body: the text form or JSON
	mux.HandleFunc("DELETE /stats", h.resetStats)             // zero all counters
	mux.HandleFunc("GET /history", h.getHistory)              // ?target=IP-or-CIDR&limit=N
	mux.HandleFunc("DELETE /history", h.clearHistory)         // forget recorded requests
	mux.HandleFunc("POST /reset", h.reset)                    // everything as at startup
	mux.HandleFunc("GET /version", h.getVersion)              // {"version": "v0.6.0"}
	mux.HandleFunc("PUT /paths/{target...}", h.setPath)       // body: hops such as "10.0.0.1@2ms > * > 10.0.0.3@20ms"
	mux.HandleFunc("DELETE /paths/{target...}", h.deletePath) // the target has no route again
	return mux
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
	for _, a := range append(path.Addrs(), p.Addr()) {
		if err := h.ensureRoute(netip.PrefixFrom(a, a.BitLen())); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
	}
	h.rules.SetPath(p.Addr(), path)
	writeJSON(w, http.StatusOK, map[string]any{"target": p.Addr().String(), "path": path})
}

func (h *handler) deletePath(w http.ResponseWriter, r *http.Request) {
	p, err := rule.ParsePrefix(r.PathValue("target"))
	if err != nil || !p.IsSingleIP() {
		httpError(w, http.StatusBadRequest, fmt.Errorf("a path's target must be a single address: %q", r.PathValue("target")))
		return
	}
	if !h.rules.DeletePath(p.Addr()) {
		httpError(w, http.StatusNotFound, fmt.Errorf("no path to %s", p.Addr()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type handler struct {
	rules       *engine.RuleSet
	history     *engine.History
	ensureRoute func(netip.Prefix) error
	version     string
}

func (h *handler) getRules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.rules.Snapshot())
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
	if !h.rules.Delete(p) {
		httpError(w, http.StatusNotFound, fmt.Errorf("no rule for %s", p))
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if t := r.URL.Query().Get("target"); t != "" {
		p, err := rule.ParsePrefix(t)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		filter = &p
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
	events, total := h.history.Events(filter, limit)
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "total": total, "capacity": h.history.Capacity()})
}

func (h *handler) clearHistory(w http.ResponseWriter, r *http.Request) {
	h.history.Clear()
	w.WriteHeader(http.StatusNoContent)
}

// reset puts rules, default, statistics and history back as they were at
// startup, so that a test starts from a known state whatever ran before.
func (h *handler) reset(w http.ResponseWriter, r *http.Request) {
	h.rules.Reset()
	h.history.Clear()
	w.WriteHeader(http.StatusNoContent)
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
