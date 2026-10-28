package consumerapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

const consumerToken = "consumer-secret"

// newTestServer returns the handler exactly as New builds it (including the
// Cache-Control wrapper), so the tests exercise the served handler.
func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	f := &fakeProvider{}
	f.cfg.Store(&config.Config{
		ClientListen:   "0.0.0.0:80",
		ConsumerListen: "0.0.0.0:8000",
		MaxStatusBytes: 1024,
		Clients: map[string]config.Client{
			"alpha": {Name: "alpha", TokenSHA256: token.Hash("client-a")},
			"beta":  {Name: "beta", TokenSHA256: token.Hash("client-b")},
		},
		Consumers: map[string]config.Consumer{
			"dashboard": {Name: "dashboard", TokenSHA256: token.Hash(consumerToken)},
		},
	})
	st := store.New()
	st.Set(store.Status{
		Name:     "alpha",
		Time:     time.Date(2025, 5, 4, 3, 2, 1, 0, time.UTC),
		SourceIP: "198.51.100.4",
		Status:   []byte(`{"ok":true}`),
	})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(f, st, ratelimit.New(), log)
}

func doGet(t *testing.T, h http.Handler, path, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "127.0.0.1:5555"
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func checkStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), want)
	}
}

func TestHealth(t *testing.T) {
	srv := newTestServer(t)
	rec := doGet(t, srv, "/", "")
	checkStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestListAuth(t *testing.T) {
	srv := newTestServer(t)
	checkStatus(t, doGet(t, srv, "/status", ""), http.StatusUnauthorized)
	checkStatus(t, doGet(t, srv, "/status", "Bearer wrong"), http.StatusUnauthorized)
	checkStatus(t, doGet(t, srv, "/status", "Token "+consumerToken), http.StatusUnauthorized)

	rec := doGet(t, srv, "/status", "Bearer "+consumerToken)
	checkStatus(t, rec, http.StatusOK)

	var out struct {
		Clients []struct {
			Name     string     `json:"name"`
			LastTime *time.Time `json:"last_status_time"`
		} `json:"clients"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, rec.Body.String())
	}
	if len(out.Clients) != 2 {
		t.Fatalf("clients = %+v", out.Clients)
	}
	if out.Clients[0].Name != "alpha" || out.Clients[0].LastTime == nil {
		t.Errorf("clients[0] = %+v", out.Clients[0])
	}
	if out.Clients[1].Name != "beta" || out.Clients[1].LastTime != nil {
		t.Errorf("clients[1] = %+v", out.Clients[1])
	}
}

func TestSingleStatus(t *testing.T) {
	srv := newTestServer(t)
	rec := doGet(t, srv, "/status/alpha", "Bearer "+consumerToken)
	checkStatus(t, rec, http.StatusOK)

	var got store.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, rec.Body.String())
	}
	if got.Name != "alpha" || got.SourceIP != "198.51.100.4" {
		t.Errorf("got = %+v", got)
	}
	if string(got.Status) != `{"ok":true}` {
		t.Errorf("status = %s", got.Status)
	}
	if !got.Time.Equal(time.Date(2025, 5, 4, 3, 2, 1, 0, time.UTC)) {
		t.Errorf("time = %v", got.Time)
	}
}

func TestSingleNoStatusYet(t *testing.T) {
	srv := newTestServer(t)
	rec := doGet(t, srv, "/status/beta", "Bearer "+consumerToken)
	checkStatus(t, rec, http.StatusNotFound)
	if !strings.Contains(rec.Body.String(), "no status recorded yet") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestSingleUnknownClient(t *testing.T) {
	srv := newTestServer(t)
	rec := doGet(t, srv, "/status/ghost", "Bearer "+consumerToken)
	checkStatus(t, rec, http.StatusNotFound)
}

func TestSingleBadPath(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/status/", "/status/bad_name", "/status/a/b"} {
		rec := doGet(t, srv, path, "Bearer "+consumerToken)
		checkStatus(t, rec, http.StatusNotFound)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	req.RemoteAddr = "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	checkStatus(t, rec, http.StatusMethodNotAllowed)
}

func TestCacheControlNoStore(t *testing.T) {
	srv := newTestServer(t)
	// The header must be set on every response, including errors.
	for _, tc := range []struct{ path, auth string }{
		{"/", ""},
		{"/status", ""},
		{"/status/alpha", "Bearer " + consumerToken},
	} {
		rec := doGet(t, srv, tc.path, tc.auth)
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", tc.path, got)
		}
	}
}

// The consumer port must cap floods from a single source IP (the port has
// no proxy in front, so the TCP source is the identity). The limit is
// generous; the test lowers nothing and just checks the boundary.
func TestRateLimit(t *testing.T) {
	srv := newTestServer(t)
	for i := 0; i < LimitRequests; i++ {
		rec := doGet(t, srv, "/", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, rec.Code)
		}
	}
	rec := doGet(t, srv, "/", "")
	checkStatus(t, rec, http.StatusTooManyRequests)

	// A different source is unaffected.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.9:1234"
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, req)
	checkStatus(t, rec2, http.StatusOK)

	// Auth failures from the limited source also count (no bypass via 401).
	rec3 := doGet(t, srv, "/status", "")
	checkStatus(t, rec3, http.StatusTooManyRequests)
}

func TestUnknownPath(t *testing.T) {
	srv := newTestServer(t)
	checkStatus(t, doGet(t, srv, "/nope", "Bearer "+consumerToken), http.StatusNotFound)
	checkStatus(t, doGet(t, srv, "/statusx", "Bearer "+consumerToken), http.StatusNotFound)
}
