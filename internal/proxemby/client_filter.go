package proxemby

import (
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type clientFilter struct {
	allowed           []netip.Prefix
	trustProxyHeaders bool
	logger            *slog.Logger
	next              http.Handler
}

func newClientFilter(allowed []netip.Prefix, trustProxyHeaders bool, logger *slog.Logger, next http.Handler) http.Handler {
	if len(allowed) == 0 {
		return next
	}
	return &clientFilter{
		allowed:           allowed,
		trustProxyHeaders: trustProxyHeaders,
		logger:            logger,
		next:              next,
	}
}

func (f *clientFilter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	addr, source, ok := f.clientAddr(req)
	if !ok {
		f.logDecision(req, netip.Addr{}, source, false, "client_ip_unavailable")
		http.Error(w, "client IP is not allowed", http.StatusForbidden)
		return
	}
	allowed := f.isAllowed(addr)
	f.logDecision(req, addr, source, allowed, "")
	if !allowed {
		http.Error(w, "client IP is not allowed", http.StatusForbidden)
		return
	}
	f.next.ServeHTTP(w, req)
}

func (f *clientFilter) clientAddr(req *http.Request) (netip.Addr, string, bool) {
	if f.trustProxyHeaders {
		if addr, ok := parseClientAddr(firstForwardedFor(req.Header.Get("X-Forwarded-For"))); ok {
			return addr, "x_forwarded_for", true
		}
		if addr, ok := parseClientAddr(req.Header.Get("X-Real-IP")); ok {
			return addr, "x_real_ip", true
		}
	}
	addr, ok := parseRemoteAddr(req.RemoteAddr)
	return addr, "remote_addr", ok
}

func (f *clientFilter) logDecision(req *http.Request, addr netip.Addr, source string, allowed bool, reason string) {
	client := "unknown"
	if addr.IsValid() {
		client = addr.String()
	}
	if reason == "" && !allowed {
		reason = "client_ip_not_allowed"
	}
	attrs := []any{
		"allowed", allowed,
		"reason", reason,
		"client", client,
		"source", source,
		"trust_proxy_headers", f.trustProxyHeaders,
		"path", sanitizeRequestURI(req.URL),
		"remote", req.RemoteAddr,
		"x_forwarded_for", req.Header.Get("X-Forwarded-For"),
		"x_real_ip", req.Header.Get("X-Real-IP"),
	}
	if allowed {
		f.logger.Debug("client filter decision", attrs...)
		return
	}
	f.logger.Warn("client filter rejected request", attrs...)
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
