// Package config loads and validates the push YAML configuration.
package config

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"push/internal/token"
)

// DefaultMaxStatusBytes is the default status payload limit: 100 KiB.
const DefaultMaxStatusBytes = int64(100 * 1024)

var (
	// nameRe: one or more alphanumerics, optionally followed by dash+alphanumerics.
	// Allows "a", "sensor-1", "a-b-c"; rejects "-", "a-", "-a", "a--b".
	nameRe      = regexp.MustCompile(`^[A-Za-z0-9]+(-?[A-Za-z0-9]+)*$`)
	tokenHashRe = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// ValidName reports whether s is a legal client name.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// Config is a validated push configuration.
type Config struct {
	ClientListen   string
	ConsumerListen string
	Forwarders     []netip.Prefix
	MaxStatusBytes int64
	RateLimit      RateLimit
	Clients        map[string]Client
	Consumers      map[string]Consumer
}

// RateLimit is the basic fixed-window limit applied to the client service.
type RateLimit struct {
	Enabled     bool
	MaxRequests int
	Window      time.Duration
}

// Client is a registered status sender.
type Client struct {
	Name        string
	TokenSHA256 string // lowercase hex
}

// Consumer is a registered status reader.
type Consumer struct {
	Name        string
	TokenSHA256 string // lowercase hex
}

// IsForwarder reports whether ip is one of the configured reverse proxies.
func (c *Config) IsForwarder(ip netip.Addr) bool {
	for _, p := range c.Forwarders {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// Client returns the registered client with the given name.
func (c *Config) Client(name string) (Client, bool) {
	cl, ok := c.Clients[name]
	return cl, ok
}

// VerifyClientToken reports whether raw is the client token for name.
func (c *Config) VerifyClientToken(name, raw string) bool {
	cl, ok := c.Clients[name]
	if !ok {
		return false
	}
	return token.Matches(cl.TokenSHA256, raw)
}

// VerifyConsumerToken reports whether raw matches any registered consumer token.
func (c *Config) VerifyConsumerToken(raw string) bool {
	h := token.Hash(raw)
	for _, cs := range c.Consumers {
		if subtle.ConstantTimeCompare([]byte(cs.TokenSHA256), []byte(h)) == 1 {
			return true
		}
	}
	return false
}

// Load reads and validates the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(data)
}

// Parse decodes and validates raw YAML config bytes.
func Parse(data []byte) (*Config, error) {
	var rc rawConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&rc); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return validate(rc)
}

type rawConfig struct {
	ClientListen   string        `yaml:"client_listen"`
	ConsumerListen string        `yaml:"consumer_listen"`
	ForwarderIPs   []string      `yaml:"forwarder_ips"`
	MaxStatusBytes int64         `yaml:"max_status_bytes"`
	RateLimit      *rawRateLimit `yaml:"rate_limit"`
	Clients        []rawEntity   `yaml:"clients"`
	Consumers      []rawEntity   `yaml:"consumers"`
}

type rawRateLimit struct {
	Enabled     bool   `yaml:"enabled"`
	MaxRequests int    `yaml:"max_requests"`
	Window      string `yaml:"window"`
}

type rawEntity struct {
	Name        string `yaml:"name"`
	TokenSHA256 string `yaml:"token_sha256"`
}

func validate(rc rawConfig) (*Config, error) {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	cfg := &Config{
		ClientListen:   strings.TrimSpace(rc.ClientListen),
		ConsumerListen: strings.TrimSpace(rc.ConsumerListen),
		MaxStatusBytes: DefaultMaxStatusBytes,
		Clients:        map[string]Client{},
		Consumers:      map[string]Consumer{},
	}

	if cfg.ClientListen == "" {
		add("client_listen is required")
	} else if err := checkListenAddr(cfg.ClientListen); err != nil {
		add("client_listen %q: %v", rc.ClientListen, err)
	}
	if cfg.ConsumerListen == "" {
		add("consumer_listen is required")
	} else if err := checkListenAddr(cfg.ConsumerListen); err != nil {
		add("consumer_listen %q: %v", rc.ConsumerListen, err)
	}

	for i, s := range rc.ForwarderIPs {
		pfx, err := parseForwarder(s)
		if err != nil {
			add("forwarder_ips[%d] %q: %v", i, s, err)
			continue
		}
		cfg.Forwarders = append(cfg.Forwarders, pfx)
	}

	if rc.MaxStatusBytes != 0 {
		if rc.MaxStatusBytes <= 0 {
			add("max_status_bytes must be a positive number of bytes")
		} else {
			cfg.MaxStatusBytes = rc.MaxStatusBytes
		}
	}

	if rc.RateLimit != nil {
		rl := RateLimit{Enabled: rc.RateLimit.Enabled, MaxRequests: rc.RateLimit.MaxRequests}
		if rc.RateLimit.Window == "" {
			add("rate_limit.window is required when the rate_limit section is present")
		} else {
			d, err := time.ParseDuration(rc.RateLimit.Window)
			if err != nil {
				add("rate_limit.window %q: invalid duration (examples: 30s, 5m, 1h)", rc.RateLimit.Window)
			} else if d <= 0 {
				add("rate_limit.window must be positive")
			} else {
				rl.Window = d
			}
		}
		if rl.Enabled && rl.MaxRequests <= 0 {
			add("rate_limit.max_requests must be a positive number when rate_limit.enabled is true")
		}
		cfg.RateLimit = rl
	}

	registerEntities(rc.Clients, cfg.Clients, func(n, h string) Client {
		return Client{Name: n, TokenSHA256: h}
	}, add, "clients")
	registerEntities(rc.Consumers, cfg.Consumers, func(n, h string) Consumer {
		return Consumer{Name: n, TokenSHA256: h}
	}, add, "consumers")

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return cfg, nil
}

func registerEntities[E any](list []rawEntity, out map[string]E, newEntity func(name, hash string) E, add func(string, ...any), kind string) {
	seen := map[string]bool{}
	for i, e := range list {
		name := strings.TrimSpace(e.Name)
		if !ValidName(name) {
			add("%s[%d]: name %q must contain only letters, numbers and dashes", kind, i, e.Name)
			continue
		}
		if seen[name] {
			add("%s[%d]: duplicate name %q", kind, i, name)
			continue
		}
		h := strings.ToLower(strings.TrimSpace(e.TokenSHA256))
		if !tokenHashRe.MatchString(h) {
			add("%s[%d] %q: token_sha256 must be 64 hex characters (the sha256 of the raw token)", kind, i, name)
			continue
		}
		seen[name] = true
		out[name] = newEntity(name, h)
	}
}

func checkListenAddr(s string) error {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return fmt.Errorf("must be of the form host:port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("invalid port %q", port)
	}
	// The host must be empty (bind all interfaces) or a valid IP address.
	// Hostnames are rejected: a bind address that depends on DNS resolution
	// is surprising and can change under the service.
	if host != "" {
		if _, err := netip.ParseAddr(host); err != nil {
			return fmt.Errorf("invalid address %q: must be an IP address", host)
		}
	}
	return nil
}

func parseForwarder(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		pfx, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("invalid IP or CIDR %q", s)
		}
		return pfx.Masked(), nil
	}
	ip, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid IP address %q", s)
	}
	// Unmap first so a v4-mapped value (e.g. ::ffff:1.2.3.4) yields a /32
	// prefix, not a /128. The bit length must be taken from the unmapped
	// address.
	ip = ip.Unmap()
	pfx, err := ip.Prefix(ip.BitLen())
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid IP address %q", s)
	}
	return pfx, nil
}
