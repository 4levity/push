package clientapi

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"push/internal/config"
	"push/internal/ratelimit"
	"push/internal/store"
	"push/internal/token"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProvider struct {
	cfg atomic.Pointer[config.Config]
}

func (f *fakeProvider) Config() *config.Config { return f.cfg.Load() }

func newTestServer(t *testing.T, maxBytes int64, rl config.RateLimit) (*Server, *store.Store) {
	t.Helper()
	f := &fakeProvider{}
	f.cfg.Store(&config.Config{
		ClientListen:   "0.0.0.0:80",
		ConsumerListen: "0.0.0.0:8000",
		Forwarders:     []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
		MaxStatusBytes: maxBytes,
		RateLimit:      rl,
		Clients: map[string]config.Client{
			"sensor-1": {Name: "sensor-1", TokenSHA256: token.Hash("client-secret")},
		},
	})
	st := store.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(f, st, ratelimit.New(), log).(*Server), st
}

func (srv *Server) doPost(t *testing.T, path, body, remoteAddr, xff, clientToken string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.RemoteAddr = remoteAddr
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	if clientToken != "" {
		req.Header.Set("X-Client-Token", clientToken)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func checkStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), want)
	}
}

func TestValidPush(t *testing.T) {
	srv, st := newTestServer(t, 1024, config.RateLimit{})
	rec := srv.doPost(t, "/sensor-1", `{"temp": 21.5}`, "10.0.0.5:41234", "203.0.113.7", "client-secret")
	checkStatus(t, rec, http.StatusOK)

	got, ok := st.Get("sensor-1")
	if !ok {
		t.Fatal("status not stored")
	}
	if got.SourceIP != "203.0.113.7" {
		t.Errorf("SourceIP = %q, want 203.0.113.7 (XFF from forwarder)", got.SourceIP)
	}
	if !bytes.Equal(got.Status, []byte(`{"temp": 21.5}`)) {
		t.Errorf("stored status = %s", got.Status)
	}
	if got.Name != "sensor-1" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Time.After(time.Now()) || time.Since(got.Time) > time.Minute {
		t.Errorf("Time = %v looks wrong", got.Time)
	}
}

func TestNonForwarderIgnoresXFF(t *testing.T) {
	srv, st := newTestServer(t, 1024, config.RateLimit{})
	rec := srv.doPost(t, "/sensor-1", `{}`, "203.0.113.9:1234", "127.0.0.1", "client-secret")
	checkStatus(t, rec, http.StatusOK)

	got, _ := st.Get("sensor-1")
	if got.SourceIP != "203.0.113.9" {
		t.Errorf("SourceIP = %q, want 203.0.113.9 (source, not XFF)", got.SourceIP)
	}
}

func TestForwarderWithoutXFF(t *testing.T) {
	srv, st := newTestServer(t, 1024, config.RateLimit{})
	rec := srv.doPost(t, "/sensor-1", `{}`, "10.0.0.5:1", "", "client-secret")
	checkStatus(t, rec, http.StatusOK)

	got, _ := st.Get("sensor-1")
	if got.SourceIP != "10.0.0.5" {
		t.Errorf("SourceIP = %q, want 10.0.0.5", got.SourceIP)
	}
}

// The response for a well-formed path must not reveal whether the name is
// registered or the token is valid: for the same body, unknown clients, bad
// tokens and valid auth all get the identical response.
func TestResponseIndependentOfAuth(t *testing.T) {
	srv, st := newTestServer(t, 10, config.RateLimit{})

	for _, tc := range []struct {
		name  string
		path  string
		body  string
		token string
	}{
		{"wrong token, invalid body", "/sensor-1", "not json", "wrong-token"},
		{"missing token, invalid body", "/sensor-1", "not json", ""},
		{"unknown client, invalid body", "/ghost", "not json", "client-secret"},
		{"valid auth, invalid body", "/sensor-1", "not json", "client-secret"},
		{"valid auth, empty body", "/sensor-1", "", "client-secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := srv.doPost(t, tc.path, tc.body, "203.0.113.9:1", "", tc.token)
			checkStatus(t, rec, http.StatusOK)
			if !strings.Contains(rec.Body.String(), `"ok":true`) {
				t.Errorf("body = %s, want ok:true", rec.Body.String())
			}
		})
	}
	if _, ok := st.Get("sensor-1"); ok {
		t.Fatal("dropped status must not be stored")
	}

	// Oversized body (11 > 10): 413 for everyone, nothing stored.
	for _, tc := range []struct {
		name  string
		path  string
		token string
	}{
		{"wrong token", "/sensor-1", "wrong-token"},
		{"unknown client", "/ghost", "client-secret"},
		{"valid auth", "/sensor-1", "client-secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := srv.doPost(t, tc.path, "12345678901", "203.0.113.9:1", "", tc.token)
			checkStatus(t, rec, http.StatusRequestEntityTooLarge)
		})
	}
	if _, ok := st.Get("sensor-1"); ok {
		t.Fatal("oversized status must not be stored")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	srv, _ := newTestServer(t, 1024, config.RateLimit{})
	req := httptest.NewRequest(http.MethodGet, "/sensor-1", nil)
	req.RemoteAddr = "203.0.113.9:1"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	checkStatus(t, rec, http.StatusMethodNotAllowed)
}

func TestBadNames(t *testing.T) {
	srv, _ := newTestServer(t, 1024, config.RateLimit{})
	for _, path := range []string{"/", "/under_score", "/a/b", "/.."} {
		rec := srv.doPost(t, path, `{}`, "203.0.113.9:1", "", "client-secret")
		checkStatus(t, rec, http.StatusNotFound)
	}
}

// postPath sends a POST whose URL.Path is set to exactly path, bypassing
// httptest's URL parsing (which would mangle "//..." targets).
func (srv *Server) postPath(t *testing.T, path, body, clientToken string) *httptest.ResponseRecorder {
	t.Helper()
	req := &http.Request{
		Method:     http.MethodPost,
		URL:        &url.URL{Path: path},
		RemoteAddr: "203.0.113.9:1",
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	if clientToken != "" {
		req.Header.Set("X-Client-Token", clientToken)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// Non-canonical paths must be rejected (exact "/<name>" only), not
// normalized: trailing slashes, repeated slashes, and a missing leading
// slash all get 404 even when they would normalize to a known client.
func TestNonCanonicalPaths(t *testing.T) {
	srv, _ := newTestServer(t, 1024, config.RateLimit{})
	for _, path := range []string{"/sensor-1/", "//sensor-1", "/sensor-1//", "/", "sensor-1"} {
		rec := srv.postPath(t, path, `{}`, "client-secret")
		checkStatus(t, rec, http.StatusNotFound)
	}
	// The canonical form still works.
	rec := srv.postPath(t, "/sensor-1", `{}`, "client-secret")
	checkStatus(t, rec, http.StatusOK)
}

func TestRateLimit(t *testing.T) {
	rl := config.RateLimit{Enabled: true, MaxRequests: 2, Window: time.Minute}
	srv, _ := newTestServer(t, 1024, rl)

	for i := 1; i <= 2; i++ {
		rec := srv.doPost(t, "/sensor-1", `{}`, "203.0.113.9:1", "", "client-secret")
		checkStatus(t, rec, http.StatusOK)
	}
	rec := srv.doPost(t, "/sensor-1", `{}`, "203.0.113.9:1", "", "client-secret")
	checkStatus(t, rec, http.StatusTooManyRequests)

	// a different IP is unaffected
	rec = srv.doPost(t, "/sensor-1", `{}`, "203.0.113.10:1", "", "client-secret")
	checkStatus(t, rec, http.StatusOK)
}

// Non-POST requests count against the limit too: a GET/PUT flood (which
// would get 405) must not bypass the limiter.
func TestRateLimitAppliesToNonPost(t *testing.T) {
	rl := config.RateLimit{Enabled: true, MaxRequests: 2, Window: time.Minute}
	srv, _ := newTestServer(t, 1024, rl)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/sensor-1", nil)
		req.RemoteAddr = "203.0.113.9:1"
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		checkStatus(t, rec, http.StatusMethodNotAllowed)
	}
	// Third request from the same IP is rejected even as a valid POST.
	rec := srv.doPost(t, "/sensor-1", `{}`, "203.0.113.9:1", "", "client-secret")
	checkStatus(t, rec, http.StatusTooManyRequests)
}

func TestRateLimitDisabled(t *testing.T) {
	srv, _ := newTestServer(t, 1024, config.RateLimit{})
	for i := 0; i < 5; i++ {
		rec := srv.doPost(t, "/sensor-1", `{}`, "203.0.113.9:1", "", "client-secret")
		checkStatus(t, rec, http.StatusOK)
	}
}
