// Package xff resolves the originating client IP for requests that may
// arrive through a trusted reverse proxy.
package xff

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP returns the originating client IP of r.
//
// If the source address is not one of the configured forwarders, the source
// address is returned and X-Forwarded-For is ignored.
//
// Otherwise the X-Forwarded-For entries are scanned from right to left and
// the rightmost valid IP that is not itself a configured forwarder is
// returned. This is the correct origin whether the trusted proxy appends to
// the header (the nginx default, $proxy_add_x_forwarded_for) or replaces it,
// and it composes across multi-proxy chains: every hop our own proxies add
// is skipped, and a client that forges entries on the left cannot win because
// the last proxy's appended real source address is always to the right of the
// forged entries. If the header is missing or has no such entry, the
// forwarder's source address is returned.
func ClientIP(r *http.Request, forwarders []netip.Prefix) string {
	src, ok := sourceAddr(r)
	if !ok {
		return ""
	}
	if !isForwarder(src, forwarders) {
		return src.String()
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			entry := strings.TrimSpace(parts[i])
			if entry == "" {
				continue
			}
			ip, err := netip.ParseAddr(entry)
			if err != nil {
				continue
			}
			ip = ip.Unmap()
			if isForwarder(ip, forwarders) {
				continue // our own proxy hop, keep looking left
			}
			return ip.String()
		}
	}
	return src.String()
}

func sourceAddr(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return ip, true
}

func isForwarder(ip netip.Addr, forwarders []netip.Prefix) bool {
	for _, p := range forwarders {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
