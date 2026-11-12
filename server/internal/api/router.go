// Package api serves the read model the dashboard renders. Every response is
// JSON. With TM_DEMO_MODE=true the API is read-only: any non-GET request is
// refused, so the public demo cannot be made to spend money.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/devashishbhat/totem/server/internal/config"
	"github.com/devashishbhat/totem/server/internal/store"
)

// Runner starts a replay experiment. replay.Runner implements it.
type Runner interface {
	Start(ctx context.Context, decisionID, memoryVersionID uuid.UUID, samplesPerArm int) (uuid.UUID, error)
}

// Server holds the API dependencies.
type Server struct {
	db     *store.Store
	cfg    config.Config
	runner Runner
	log    *slog.Logger
}

// Router builds the handler mounted at /api/v1/.
func Router(db *store.Store, cfg config.Config, runner Runner, log *slog.Logger) http.Handler {
	s := &Server{db: db, cfg: cfg, runner: runner, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/overview", s.overview)
	mux.HandleFunc("GET /api/v1/runs", s.runs)
	mux.HandleFunc("GET /api/v1/runs/{id}/timeline", s.timeline)
	mux.HandleFunc("GET /api/v1/decisions/{id}/explain", s.explain)
	mux.HandleFunc("GET /api/v1/memories/{id}", s.memory)
	mux.HandleFunc("GET /api/v1/sources/{id}", s.source)
	mux.HandleFunc("POST /api/v1/replays", s.createReplay)
	mux.HandleFunc("GET /api/v1/replays/{id}", s.replay)
	return s.middleware(mux)
}

// middleware applies CORS and the demo-mode read-only rule.
func (s *Server) middleware(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range s.cfg.CORSOrigins {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (allowed[origin] || allowed["*"]) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if s.cfg.DemoMode && r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusForbidden, "this instance is a read-only demo")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Default().Error("write response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// fail maps a store error to a status code.
func (s *Server) fail(w http.ResponseWriter, err error, what string) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, what+" not found")
		return
	}
	s.log.Error("query failed", "what", what, "err", err)
	writeError(w, http.StatusInternalServerError, "query failed")
}

func pathUUID(r *http.Request, name string) (uuid.UUID, error) {
	return uuid.Parse(r.PathValue(name))
}

func queryInt(r *http.Request, name string, def, min, max int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
