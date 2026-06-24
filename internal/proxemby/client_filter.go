package proxemby

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type clientFilter struct {
	allowed           []netip.Prefix
	trustProxyHeaders bool
	next              http.Handler
}

func newClientFilter(allowed []netip.Prefix, trustProxyHeaders bool, next http.Handler) http.Handler {
	if len(allowed) == 0 {
		return next
	}
	return &clientFilter{
		allowed:           allowed,
		trustProxyHeaders: trustProxyHeaders,
		next:              next,
	}
}

func (f *clientFilter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	addr, ok := f.clientAddr(req)
	if !ok || !f.isAllowed(addr) {
		http.Error(w, "client IP is not allowed", http.StatusForbidden)
		return
	}
	f.next.ServeHTTP(w, req)
}

func (f *clientFilter) clientAddr(req *http.Request) (netip.Addr, bool) {
	if f.trustProxyHeaders {
		if addr, ok := parseClientAddr(firstForwardedFor(req.Header.Get("X-Forwarded-For"))); ok {
			return addr, true
		}
		if addr, ok := parseClientAddr(req.Header.Get("X-Real-IP")); ok {
			return addr, true
		}
	}
	return parseRemoteAddr(req.RemoteAddr)
}

func (f *clientFilter) isAllowed(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, prefix := range f.allowed {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func parseRemoteAddr(remoteAddr string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return parseClientAddr(host)
	}
	return parseClientAddr(remoteAddr)
}

func parseClientAddr(raw string) (netip.Addr, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func firstForwardedFor(raw string) string {
	first, _, _ := strings.Cut(raw, ",")
	return strings.TrimSpace(first)
}
