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
	rl := cfg.RateLimit
	if !rl.Enabled || rl.MaxRequests != DefaultRateLimitMaxRequests || rl.Window != DefaultRateLimitWindow {
		t.Errorf("RateLimit = %+v, want enabled default %+v",
			rl, RateLimit{Enabled: true, MaxRequests: DefaultRateLimitMaxRequests, Window: DefaultRateLimitWindow})
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
		{"multi-document", listens + "---\nclient_listen: \"127.0.0.1:1\"\n", "one YAML document"},
		{"whole-space forwarder", listens + "forwarder_ips:\n  - \"0.0.0.0/0\"\n", "address space"},
		{"whole-space forwarder v6", listens + "forwarder_ips:\n  - \"::/0\"\n", "address space"},
		{"negative max bytes", listens + "max_status_bytes: -5\n", "max_status_bytes"},
		{"bad window", listens + "rate_limit:\n  enabled: true\n  max_requests: 5\n  window: \"soon\"\n", "window"},
		{"enabled without max", listens + "rate_limit:\n  enabled: true\n  window: \"1m\"\n", "max_requests"},
		{"enabled without window", listens + "rate_limit:\n  enabled: true\n  max_requests: 5\n", "window"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.yaml)); err == nil {
				t.Fatalf("Parse: want error containing %q, got nil", tc.want)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestParseRateLimitExplicitlyDisabled(t *testing.T) {
	// An explicit `enabled: false` disables the limit even though the
	// default (omitted section) is on.
	cfg, err := Parse([]byte(`
client_listen: "0.0.0.0:80"
consumer_listen: "0.0.0.0:8000"
rate_limit:
  enabled: false
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.RateLimit.Enabled {
		t.Error("rate limit should be disabled by explicit enabled: false")
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

func TestCheckListenAddr(t *testing.T) {
	ok := []string{"127.0.0.1:8000", "0.0.0.0:80", "[::1]:8000", ":8000"}
	for _, s := range ok {
		if err := checkListenAddr(s); err != nil {
			t.Errorf("checkListenAddr(%q) = %v, want nil", s, err)
		}
	}
	// Invalid dotted "IPs" and hostnames must be rejected, not accepted just
	// because they contain a dot. Bare IPv6 (no brackets) is also rejected,
	// matching net.Listen.
	bad := []string{"999.999.999.999:80", "myhost.example.com:80", "10.0.0.256:80", "::1:8000", "127.0.0.1:0", "127.0.0.1:99999", "127.0.0.1", "no-port-here"}
	for _, s := range bad {
		if err := checkListenAddr(s); err == nil {
			t.Errorf("checkListenAddr(%q) = nil, want error", s)
		}
	}
}

func TestParseForwarderV4Mapped(t *testing.T) {
	// A v4-mapped IPv6 forwarder must resolve to a /32, not be rejected.
	pfx, err := parseForwarder("::ffff:203.0.113.7")
	if err != nil {
		t.Fatalf("parseForwarder(::ffff:203.0.113.7) = %v", err)
	}
	want := netip.MustParsePrefix("203.0.113.7/32")
	if pfx != want {
		t.Errorf("parseForwarder = %v, want %v", pfx, want)
	}
	// Plain forms still work and produce the same /32.
	if p, err := parseForwarder("203.0.113.7"); err != nil || p != want {
		t.Errorf("parseForwarder(203.0.113.7) = %v, %v; want %v", p, err, want)
	}
	if _, err := parseForwarder("not-an-ip"); err == nil {
		t.Error("parseForwarder(not-an-ip) = nil, want error")
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
