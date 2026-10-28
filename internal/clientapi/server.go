// Package clientapi implements the public, reverse-proxy-fronted service
// that receives status reports from clients.
package clientapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"push/internal/config"
	"push/internal/httpjson"
	"push/internal/logsafe"
	"push/internal/ratelimit"
	"push/internal/store"
	"push/internal/xff"
	"strings"
	"time"
)

// Provider supplies the active configuration.
type Provider interface {
	Config() *config.Config
}

// Server is the client-facing HTTP handler.
type Server struct {
	cfg     Provider
	store   *store.Store
	limiter *ratelimit.Limiter
	log     *slog.Logger
}

// New builds the client-facing handler.
func New(cfg Provider, st *store.Store, limiter *ratelimit.Limiter, log *slog.Logger) http.Handler {
	return &Server{cfg: cfg, store: st, limiter: limiter, log: log}
}

// ServeHTTP handles POST /{client-name} status submissions.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	cfg := s.cfg.Config()
	ip := xff.ClientIP(r, cfg.Forwarders)

	name := ""
	// Request-derived values are logged via logsafe.Sanitize (see logsafe).
	logLine := func(status int, extra ...any) {
		s.log.Info("client request",
			append([]any{
				"method", logsafe.Sanitize(r.Method),
				"path", logsafe.Sanitize(r.URL.Path),
				"client", logsafe.Sanitize(name),
				"ip", logsafe.Sanitize(ip),
				"status", status,
				"duration_ms", time.Since(start).Milliseconds(),
			}, extra...)...)
	}

	// The rate limit runs first so every request from a source IP counts,
	// not only POSTs: a non-POST flood (405) must not bypass the limit.
	if cfg.RateLimit.Enabled && !s.limiter.Allow(ip, cfg.RateLimit.MaxRequests, cfg.RateLimit.Window) {
		logLine(http.StatusTooManyRequests)
		httpjson.Error(w, http.StatusTooManyRequests, "too many requests")
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		logLine(http.StatusMethodNotAllowed)
		httpjson.Error(w, http.StatusMethodNotAllowed, "method not allowed, use POST")
		return
	}

	// The path must be exactly "/<name>"; non-canonical forms are rejected,
	// not normalized.
	if !strings.HasPrefix(r.URL.Path, "/") {
		logLine(http.StatusNotFound)
		httpjson.Error(w, http.StatusNotFound, "not found")
		return
	}
	name = r.URL.Path[1:]
	if name == "" || strings.Contains(name, "/") || !config.ValidName(name) {
		logLine(http.StatusNotFound)
		httpjson.Error(w, http.StatusNotFound, "not found")
		return
	}

	// The body is read before the token check on purpose, bounded by
	// max_status_bytes: an oversized or unreadable body is rejected with the
	// same response whether or not the token is valid, and nothing larger
	// than the limit is ever loaded or parsed (chunked or not). Any residual
	// timing difference (the token hash) is deliberately not defended.
	r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxStatusBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			logLine(http.StatusRequestEntityTooLarge)
			httpjson.Error(w, http.StatusRequestEntityTooLarge, "status exceeds max_status_bytes")
			return
		}
		logLine(http.StatusBadRequest)
		httpjson.Error(w, http.StatusBadRequest, "could not read request body")
		return
	}

	if !cfg.VerifyClientToken(name, r.Header.Get("X-Client-Token")) {
		// Unknown client and invalid token are indistinguishable on purpose:
		// the request is dropped (nothing stored) but reported as accepted,
		// so callers cannot enumerate client names.
		logLine(http.StatusOK, "dropped", "auth")
		httpjson.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	if !json.Valid(raw) {
		// 200, same as the unauthenticated case: the body must not reveal
		// that the token was valid.
		logLine(http.StatusOK, "dropped", "body")
		httpjson.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	s.store.Set(store.Status{
		Name:     name,
		Time:     time.Now(),
		SourceIP: ip,
		Status:   raw,
	})
	logLine(http.StatusOK)
	httpjson.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}
