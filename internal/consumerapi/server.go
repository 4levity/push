// Package consumerapi implements the local service that serves stored
// status to authenticated consumers.
package consumerapi

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"push/internal/config"
	"push/internal/httpjson"
	"push/internal/store"
)

// Provider supplies the active configuration.
type Provider interface {
	Config() *config.Config
}

// Server is the consumer-facing HTTP handler.
type Server struct {
	cfg   Provider
	store *store.Store
	log   *slog.Logger
}

// New builds the consumer-facing handler.
func New(cfg Provider, st *store.Store, log *slog.Logger) http.Handler {
	return &Server{cfg: cfg, store: st, log: log}
}

// ServeHTTP handles:
//
//	GET /             -> health (200 if the service is up)
//	GET /status       -> all client names with last status times (authenticated)
//	GET /status/{name} -> last status for one client (authenticated)
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	path := r.URL.Path

	logLine := func(status int) {
		s.log.Info("consumer request",
			"method", r.Method,
			"path", path,
			"ip", r.RemoteAddr,
			"status", status,
			"duration_ms", time.Since(start).Milliseconds())
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		logLine(http.StatusMethodNotAllowed)
		httpjson.Error(w, http.StatusMethodNotAllowed, "method not allowed, use GET")
		return
	}

	switch {
	case path == "/":
		logLine(http.StatusOK)
		httpjson.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})

	case path == "/status":
		if !s.authorize(w, r) {
			logLine(http.StatusUnauthorized)
			return
		}
		cfg := s.cfg.Config()
		names := make([]string, 0, len(cfg.Clients))
		for n := range cfg.Clients {
			names = append(names, n)
		}
		logLine(http.StatusOK)
		httpjson.WriteJSON(w, http.StatusOK, map[string]any{"clients": s.store.List(names)})

	case strings.HasPrefix(path, "/status/"):
		if !s.authorize(w, r) {
			logLine(http.StatusUnauthorized)
			return
		}
		name := strings.TrimPrefix(path, "/status/")
		if name == "" || strings.Contains(name, "/") || !config.ValidName(name) {
			logLine(http.StatusNotFound)
			httpjson.Error(w, http.StatusNotFound, "not found")
			return
		}
		if _, known := s.cfg.Config().Client(name); !known {
			logLine(http.StatusNotFound)
			httpjson.Error(w, http.StatusNotFound, "unknown client")
			return
		}
		st, ok := s.store.Get(name)
		if !ok {
			logLine(http.StatusNotFound)
			httpjson.WriteJSON(w, http.StatusNotFound, map[string]any{
				"name":  name,
				"error": "no status recorded yet",
			})
			return
		}
		logLine(http.StatusOK)
		httpjson.WriteJSON(w, http.StatusOK, st)

	default:
		logLine(http.StatusNotFound)
		httpjson.Error(w, http.StatusNotFound, "not found")
	}
}

// authorize checks the Bearer consumer token and writes a 401 on failure.
// It returns true if the request is authorized.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) bool {
	const realm = `Bearer realm="push"`
	scheme, tok, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || tok == "" {
		w.Header().Set("WWW-Authenticate", realm)
		httpjson.Error(w, http.StatusUnauthorized, "missing Bearer token in Authorization header")
		return false
	}
	if !s.cfg.Config().VerifyConsumerToken(tok) {
		w.Header().Set("WWW-Authenticate", realm)
		httpjson.Error(w, http.StatusUnauthorized, "invalid consumer token")
		return false
	}
	return true
}
