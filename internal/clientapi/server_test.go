package clientapi

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"push/internal/config"
	"push/internal/ratelimit"
	"push/internal/store"
	"push/internal/token"
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

func TestAuthFailures(t *testing.T) {
	srv, _ := newTestServer(t, 1024, config.RateLimit{})

	rec := srv.doPost(t, "/sensor-1", `{}`, "203.0.113.9:1", "", "wrong-token")
	checkStatus(t, rec, http.StatusUnauthorized)

	rec = srv.doPost(t, "/sensor-1", `{}`, "203.0.113.9:1", "", "")
	checkStatus(t, rec, http.StatusUnauthorized)
}

func TestUnknownClient(t *testing.T) {
	srv, _ := newTestServer(t, 1024, config.RateLimit{})
	rec := srv.doPost(t, "/ghost", `{}`, "203.0.113.9:1", "", "client-secret")
	checkStatus(t, rec, http.StatusNotFound)
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

func TestTooLarge(t *testing.T) {
	srv, st := newTestServer(t, 10, config.RateLimit{})
	rec := srv.doPost(t, "/sensor-1", "12345678901", "203.0.113.9:1", "", "client-secret") // 11 bytes of valid JSON
	checkStatus(t, rec, http.StatusRequestEntityTooLarge)
	if _, ok := st.Get("sensor-1"); ok {
		t.Fatal("oversized status should not be stored")
	}
}

func TestInvalidJSON(t *testing.T) {
	srv, st := newTestServer(t, 1024, config.RateLimit{})
	rec := srv.doPost(t, "/sensor-1", "not json", "203.0.113.9:1", "", "client-secret")
	checkStatus(t, rec, http.StatusBadRequest)
	if _, ok := st.Get("sensor-1"); ok {
		t.Fatal("invalid status should not be stored")
	}
}

func TestEmptyBody(t *testing.T) {
	srv, _ := newTestServer(t, 1024, config.RateLimit{})
	rec := srv.doPost(t, "/sensor-1", "", "203.0.113.9:1", "", "client-secret")
	checkStatus(t, rec, http.StatusBadRequest)
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

func TestRateLimitDisabled(t *testing.T) {
	srv, _ := newTestServer(t, 1024, config.RateLimit{})
	for i := 0; i < 5; i++ {
		rec := srv.doPost(t, "/sensor-1", `{}`, "203.0.113.9:1", "", "client-secret")
		checkStatus(t, rec, http.StatusOK)
	}
}
