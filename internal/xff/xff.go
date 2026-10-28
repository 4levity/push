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
// If the source address is not a configured forwarder, the source address is
// returned and X-Forwarded-For is ignored. For forwarder sources, the
// rightmost valid X-Forwarded-For entry that is not itself a forwarder is
// returned: with an appending proxy (nginx default) the real client address
// is always rightmost, so forged entries cannot win; with a replacing proxy
// the client's header is discarded entirely. If no such entry exists, the
// forwarder's own address is returned.
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
