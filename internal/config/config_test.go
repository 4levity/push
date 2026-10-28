package config

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sha256("abc")
const abcHash = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

const validYAML = `
client_listen: "127.0.0.1:18080"
consumer_listen: "127.0.0.1:18000"
forwarder_ips:
  - "10.0.0.5"
  - "192.168.0.0/16"
max_status_bytes: 4096
rate_limit:
  enabled: true
  max_requests: 10
  window: "30s"
clients:
  - name: sensor-1
    token_sha256: "` + abcHash + `"
  - name: backup-box
    token_sha256: "BA7816BF8F01CFEA414140DE5DAE2223B00361A396177A9CB410FF61F20015AD"
consumers:
  - name: dashboard
    token_sha256: "` + abcHash + `"
`

func TestParseValid(t *testing.T) {
	cfg, err := Parse([]byte(validYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.ClientListen != "127.0.0.1:18080" {
		t.Errorf("ClientListen = %q", cfg.ClientListen)
	}
	if cfg.ConsumerListen != "127.0.0.1:18000" {
		t.Errorf("ConsumerListen = %q", cfg.ConsumerListen)
	}
	if len(cfg.Forwarders) != 2 {
		t.Fatalf("Forwarders = %v", cfg.Forwarders)
	}
	if !cfg.Forwarders[0].Contains(netip.MustParseAddr("10.0.0.5")) {
		t.Errorf("first forwarder = %v", cfg.Forwarders[0])
	}
	if !cfg.Forwarders[1].Contains(netip.MustParseAddr("192.168.7.9")) {
		t.Errorf("second forwarder = %v", cfg.Forwarders[1])
	}
	if cfg.MaxStatusBytes != 4096 {
		t.Errorf("MaxStatusBytes = %d", cfg.MaxStatusBytes)
	}
	rl := cfg.RateLimit
	if !rl.Enabled || rl.MaxRequests != 10 || rl.Window != 30*time.Second {
		t.Errorf("RateLimit = %+v", rl)
	}
	if _, ok := cfg.Client("sensor-1"); !ok {
		t.Error("client sensor-1 missing")
	}
	if got := cfg.Clients["backup-box"].TokenSHA256; got != abcHash {
		t.Errorf("uppercase hash not normalized: %q", got)
	}
	if !cfg.VerifyClientToken("sensor-1", "abc") {
		t.Error("VerifyClientToken should accept the raw token")
	}
	if cfg.VerifyClientToken("sensor-1", "nope") {
		t.Error("VerifyClientToken accepted a wrong token")
	}
	if cfg.VerifyClientToken("ghost", "abc") {
		t.Error("VerifyClientToken accepted an unknown client")
	}
	if !cfg.VerifyConsumerToken("abc") {
		t.Error("VerifyConsumerToken should accept the raw token")
	}
	if cfg.VerifyConsumerToken("nope") {
		t.Error("VerifyConsumerToken accepted a wrong token")
	}
}

func TestParseDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`
client_listen: "0.0.0.0:80"
consumer_listen: "0.0.0.0:8000"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.MaxStatusBytes != DefaultMaxStatusBytes {
		t.Errorf("MaxStatusBytes = %d, want default %d", cfg.MaxStatusBytes, DefaultMaxStatusBytes)
	}
	if cfg.RateLimit.Enabled {
		t.Error("rate limit should be disabled by default")
	}
	if len(cfg.Forwarders) != 0 {
		t.Errorf("Forwarders = %v", cfg.Forwarders)
	}
	if len(cfg.Clients) != 0 || len(cfg.Consumers) != 0 {
		t.Error("clients/consumers should be empty")
	}
}

func TestParseErrors(t *testing.T) {
	listens := `client_listen: "127.0.0.1:18080"
consumer_listen: "127.0.0.1:18000"
`
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"missing client listen", "consumer_listen: \"127.0.0.1:1\"\n", "client_listen is required"},
		{"missing consumer listen", "client_listen: \"127.0.0.1:1\"\n", "consumer_listen is required"},
		{"bad listen", "client_listen: \"not-a-port\"\nconsumer_listen: \"127.0.0.1:1\"\n", "client_listen"},
		{"bad port", "client_listen: \"127.0.0.1:99999\"\nconsumer_listen: \"127.0.0.1:1\"\n", "client_listen"},
		{"unknown field", listens + "bogus: 1\n", "bogus"},
		{"bad client name", listens + "clients:\n  - name: under_score\n    token_sha256: \"" + abcHash + "\"\n", "under_score"},
		{"bad token hash", listens + "clients:\n  - name: ok-name\n    token_sha256: \"zzz\"\n", "token_sha256"},
		{"duplicate client", listens + "clients:\n  - name: dup\n    token_sha256: \"" + abcHash + "\"\n  - name: dup\n    token_sha256: \"" + abcHash + "\"\n", "duplicate"},
		{"bad forwarder", listens + "forwarder_ips:\n  - \"not-an-ip\"\n", "forwarder_ips"},
		{"negative max bytes", listens + "max_status_bytes: -5\n", "max_status_bytes"},
		{"bad window", listens + "rate_limit:\n  enabled: true\n  max_requests: 5\n  window: \"soon\"\n", "window"},
		{"enabled without max", listens + "rate_limit:\n  enabled: true\n  window: \"1m\"\n", "max_requests"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.yaml)); err == nil {
				t.Fatalf("Parse: want error containing %q, got nil", tc.want)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "push.yaml")
	if err := os.WriteFile(path, []byte(validYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Clients) != 2 || len(cfg.Consumers) != 1 {
		t.Errorf("clients=%d consumers=%d", len(cfg.Clients), len(cfg.Consumers))
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("Load: want error for missing file")
	}
}

func TestValidName(t *testing.T) {
	valid := []string{"a", "sensor-1", "AB-9-x", "0"}
	for _, s := range valid {
		if !ValidName(s) {
			t.Errorf("ValidName(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "-", "a_b", "a.b", "a b", "a/b", "ünïcode"}
	for _, s := range invalid {
		if ValidName(s) {
			t.Errorf("ValidName(%q) = true, want false", s)
		}
	}
}
