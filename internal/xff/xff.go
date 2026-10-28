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
// address is returned and X-Forwarded-For is ignored. Otherwise the first
// (leftmost) valid IP in X-Forwarded-For is used; if the header is missing or
// contains no valid IP, the forwarder's source address is returned.
func ClientIP(r *http.Request, forwarders []netip.Prefix) string {
	src, ok := sourceAddr(r)
	if !ok {
		return ""
	}
	if !isForwarder(src, forwarders) {
		return src.String()
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for _, part := range strings.Split(xff, ",") {
			if ip, err := netip.ParseAddr(strings.TrimSpace(part)); err == nil {
				return ip.Unmap().String()
			}
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
