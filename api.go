package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// newAPI returns the HTTP control API:
//
//	GET    /rules             current default, rules and per-target stats
//	PUT    /rules/{target}    set a rule; body is a spec string or JSON
//	DELETE /rules/{target}    remove a rule
//	PUT    /default           set the default spec
//	DELETE /stats             reset counters
func newAPI(rs *RuleSet, router *Router) http.Handler {
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
		sp, err := readSpec(r)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if err := router.Ensure(p); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		rs.Set(p, sp)
		writeJSON(w, http.StatusOK, map[string]any{"target": p.String(), "spec": sp})
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
		sp, err := readSpec(r)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		rs.SetDefault(sp)
		writeJSON(w, http.StatusOK, map[string]any{"default": sp})
	})
	mux.HandleFunc("DELETE /stats", func(w http.ResponseWriter, r *http.Request) {
		rs.ResetStats()
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// readSpec accepts either JSON ({"delay":"120ms","jitter":"10ms","loss":0.05})
// or the same text form as the -rule flag ("delay=120ms,loss=5%").
func readSpec(r *http.Request) (Spec, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		return Spec{}, err
	}
	text := strings.TrimSpace(string(body))
	if strings.HasPrefix(text, "{") {
		var sp Spec
		if err := json.Unmarshal([]byte(text), &sp); err != nil {
			return Spec{}, fmt.Errorf("invalid JSON spec: %w", err)
		}
		return sp, nil
	}
	return ParseSpec(text)
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
