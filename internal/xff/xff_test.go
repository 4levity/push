package xff

import (
	"net/http"
	"net/netip"
	"testing"
)

func req(remote, xff string) *http.Request {
	r := &http.Request{RemoteAddr: remote, Header: http.Header{}}
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

var fw = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("192.168.7.1/32"),
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name   string
		remote string
		xff    string
		want   string
	}{
		{"non-forwarder ignores XFF", "203.0.113.9:1", "127.0.0.1, 10.1.1.1", "203.0.113.9"},
		{"non-forwarder no XFF", "203.0.113.9:1", "", "203.0.113.9"},
		{"forwarder single XFF", "10.0.0.5:1", "203.0.113.7", "203.0.113.7"},
		// Append mode (nginx default): the client forges the leftmost entry
		// and the trusted proxy appends the real source. The real (rightmost)
		// non-forwarder entry must win, not the forged leftmost one.
		{"append mode real IP beats forged leftmost", "10.0.0.5:1", "1.1.1.1, 203.0.113.7", "203.0.113.7"},
		// Multi-proxy chain: trailing forwarder hops are skipped, the client
		// entry to their left is returned.
		{"skips trailing forwarder hops", "10.0.0.5:1", "203.0.113.7, 10.9.9.9", "203.0.113.7"},
		{"skips garbage and forwarders to reach client", "10.0.0.5:1", "junk, 203.0.113.7, 10.9.9.9", "203.0.113.7"},
		{"client forges a forwarder on the right but real source is appended", "10.0.0.5:1", "1.1.1.1, 10.9.9.9, 203.0.113.7", "203.0.113.7"},
		{"forwarder all garbage falls back to source", "10.0.0.5:1", "junk, also-junk", "10.0.0.5"},
		{"forwarder without XFF uses source", "10.0.0.5:1", "", "10.0.0.5"},
		{"exact IP forwarder", "192.168.7.1:1", "203.0.113.9", "203.0.113.9"},
		{"CIDR non-member is not a forwarder", "11.0.0.1:1", "203.0.113.5", "11.0.0.1"},
		{"v4-mapped XFF is unmapped", "10.0.0.5:1", "::ffff:203.0.113.7", "203.0.113.7"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClientIP(req(tc.remote, tc.xff), fw); got != tc.want {
				t.Errorf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClientIPv6(t *testing.T) {
	fwd := []netip.Prefix{netip.MustParsePrefix("fd00::/64")}
	tests := []struct {
		remote string
		xff    string
		want   string
	}{
		{"[fd00::1]:1", "2001:db8::9", "2001:db8::9"},
		{"[fd00::1]:1", "", "fd00::1"},
		{"[2001:db8::1]:1", "fd00::9", "2001:db8::1"},
	}
	for _, tc := range tests {
		if got := ClientIP(req(tc.remote, tc.xff), fwd); got != tc.want {
			t.Errorf("ClientIP(%q, xff=%q) = %q, want %q", tc.remote, tc.xff, got, tc.want)
		}
	}
}
