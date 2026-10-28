package app

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

const cfgA = `
client_listen: "127.0.0.1:18080"
consumer_listen: "127.0.0.1:18000"
clients:
  - name: alpha
    token_sha256: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
`

const cfgB = cfgA + `  - name: beta
    token_sha256: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
`

func writeCfg(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "push.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNewAndReload(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	path := writeCfg(t, cfgA)

	a, err := New(path, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := a.Config().Client("beta"); ok {
		t.Fatal("beta should not exist yet")
	}

	// add a client, reload, verify atomic swap
	if err := os.WriteFile(path, []byte(cfgB), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.Reload(path); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if _, ok := a.Config().Client("beta"); !ok {
		t.Fatal("beta should exist after reload")
	}
	if _, ok := a.Config().Client("alpha"); !ok {
		t.Fatal("alpha should still exist after reload")
	}

	// invalid reload keeps the previous config
	if err := os.WriteFile(path, []byte("client_listen: oops\nconsumer_listen: oops\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.Reload(path); err == nil {
		t.Fatal("Reload: want error for invalid config")
	}
	if _, ok := a.Config().Client("beta"); !ok {
		t.Fatal("previous config should be kept after a failed reload")
	}
}

func TestNewBadConfig(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := New(writeCfg(t, "not: [valid"), log); err == nil {
		t.Fatal("New: want error for invalid config")
	}
}
