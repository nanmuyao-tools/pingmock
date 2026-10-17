package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
)

// newAPI returns the HTTP control API:
//
//	GET    /rules             current default, rules and per-target stats
//	PUT    /rules/{target}    set a rule; body is the text form or JSON
//	DELETE /rules/{target}    remove a rule
//	PUT    /default           set the default rule
//	DELETE /stats             reset counters
func newAPI(rs *RuleSet, ensureRoute func(netip.Prefix) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /rules", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, rs.Snapshot())
	})
	mux.HandleFunc("PUT /rules/{target...}", func(w http.ResponseWriter, r *http.Request) {
		p, err := ParsePrefix(r.PathValue("target"))
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		sc, err := readScenario(r)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if err := ensureRoute(p); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		rs.Set(p, sc)
		writeJSON(w, http.StatusOK, map[string]any{"target": p.String(), "rule": sc})
	})
	mux.HandleFunc("DELETE /rules/{target...}", func(w http.ResponseWriter, r *http.Request) {
		p, err := ParsePrefix(r.PathValue("target"))
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if !rs.Delete(p) {
			httpError(w, http.StatusNotFound, fmt.Errorf("no rule for %s", p))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PUT /default", func(w http.ResponseWriter, r *http.Request) {
		sc, err := readScenario(r)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		rs.SetDefault(sc)
		writeJSON(w, http.StatusOK, map[string]any{"default": sc})
	})
	mux.HandleFunc("DELETE /stats", func(w http.ResponseWriter, r *http.Request) {
		rs.ResetStats()
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// readScenario accepts the text form used by the -rule flag
// ("delay=120ms,loss=5%", "50ms for 3 pings; timeout") or JSON.
func readScenario(r *http.Request) (Scenario, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return Scenario{}, err
	}
	text := strings.TrimSpace(string(body))
	if strings.HasPrefix(text, "{") {
		var sc Scenario
		if err := json.Unmarshal([]byte(text), &sc); err != nil {
			return Scenario{}, fmt.Errorf("invalid JSON rule: %w", err)
		}
		return sc, nil
	}
	return ParseScenario(text)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func httpError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
