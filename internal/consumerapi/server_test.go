package consumerapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"push/internal/config"
	"push/internal/store"
	"push/internal/token"
)

type fakeProvider struct {
	cfg atomic.Pointer[config.Config]
}

func (f *fakeProvider) Config() *config.Config { return f.cfg.Load() }

const consumerToken = "consumer-secret"

func newTestServer(t *testing.T) (*Server, *store.Store) {
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
	return New(f, st, log).(*Server), st
}

func (srv *Server) doGet(t *testing.T, path, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "127.0.0.1:5555"
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
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

func TestHealth(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := srv.doGet(t, "/", "")
	checkStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestListAuth(t *testing.T) {
	srv, _ := newTestServer(t)
	checkStatus(t, srv.doGet(t, "/status", ""), http.StatusUnauthorized)
	checkStatus(t, srv.doGet(t, "/status", "Bearer wrong"), http.StatusUnauthorized)
	checkStatus(t, srv.doGet(t, "/status", "Token "+consumerToken), http.StatusUnauthorized)

	rec := srv.doGet(t, "/status", "Bearer "+consumerToken)
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
	srv, _ := newTestServer(t)
	rec := srv.doGet(t, "/status/alpha", "Bearer "+consumerToken)
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
	srv, _ := newTestServer(t)
	rec := srv.doGet(t, "/status/beta", "Bearer "+consumerToken)
	checkStatus(t, rec, http.StatusNotFound)
	if !strings.Contains(rec.Body.String(), "no status recorded yet") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestSingleUnknownClient(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := srv.doGet(t, "/status/ghost", "Bearer "+consumerToken)
	checkStatus(t, rec, http.StatusNotFound)
}

func TestSingleBadPath(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, path := range []string{"/status/", "/status/bad_name", "/status/a/b"} {
		rec := srv.doGet(t, path, "Bearer "+consumerToken)
		checkStatus(t, rec, http.StatusNotFound)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	checkStatus(t, rec, http.StatusMethodNotAllowed)
}

func TestUnknownPath(t *testing.T) {
	srv, _ := newTestServer(t)
	checkStatus(t, srv.doGet(t, "/nope", "Bearer "+consumerToken), http.StatusNotFound)
	checkStatus(t, srv.doGet(t, "/statusx", "Bearer "+consumerToken), http.StatusNotFound)
}
