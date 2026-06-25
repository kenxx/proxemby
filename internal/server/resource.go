package server

import (
	"log"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"proxemby/internal/logging"
)

func (s *routeProxy) newResourceProxy() *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{
		Transport: http.DefaultTransport,
		Rewrite: func(req *httputil.ProxyRequest) {
			target, _ := req.In.Context().Value(resourceTargetKey{}).(*url.URL)
			if target == nil {
				return
			}
			req.Out.URL.Scheme = target.Scheme
			req.Out.URL.Host = target.Host
			req.Out.URL.Path = target.Path
			req.Out.URL.RawPath = target.RawPath
			req.Out.URL.RawQuery = target.RawQuery
			req.Out.Host = target.Host
		},
		ErrorLog: log.New(logging.NewSlogWriter(s.logger, slog.LevelError), "", 0),
	}
	return proxy
}

func noCompressionTransport() http.RoundTripper {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	clone := transport.Clone()
	clone.DisableCompression = true
	return clone
}

func (s *routeProxy) handleResourceProxy(w http.ResponseWriter, req *http.Request) {
	scheme, remainder, ok := strings.Cut(strings.TrimPrefix(req.URL.Path, resourcePrefix), "/")
	if !ok || !isHTTPProxyScheme(scheme) {
		s.logResourceProxyDecision(req, false, "invalid_scheme", scheme, "")
		http.Error(w, "missing or invalid proxied resource scheme", http.StatusBadRequest)
		return
	}
	host, rest, ok := strings.Cut(remainder, "/")
	if !ok || host == "" {
		s.logResourceProxyDecision(req, false, "missing_host", scheme, host)
		http.Error(w, "missing proxied resource host", http.StatusBadRequest)
		return
	}
	_, allowed := s.registry.Lookup(host)
	if !allowed {
		s.logResourceProxyDecision(req, false, "host_not_allowed", scheme, host)
		http.Error(w, "proxied resource host is not allowed", http.StatusForbidden)
		return
	}
	s.logResourceProxyDecision(req, true, "", scheme, host)

	target := &url.URL{
		Scheme:   scheme,
		Host:     host,
		Path:     "/" + rest,
		RawQuery: req.URL.RawQuery,
	}

	ctx := withResourceTarget(req.Context(), target)
	s.resourceProxy.ServeHTTP(w, req.WithContext(ctx))
}

func (s *routeProxy) logResourceProxyDecision(req *http.Request, allowed bool, reason, scheme, host string) {
	attrs := []any{
		"allowed", allowed,
		"reason", reason,
		"scheme", scheme,
		"host", host,
		"path", sanitizeRequestURI(req.URL),
	}
	if allowed {
		s.logger.Debug("resource proxy decision", attrs...)
		return
	}
	s.logger.Warn("resource proxy rejected request", attrs...)
}

func isHTTPProxyScheme(scheme string) bool {
	return scheme == "http" || scheme == "https"
}

func isPlaybackInfoPath(path string) bool {
	for _, part := range strings.Split(path, "/") {
		if strings.EqualFold(part, "PlaybackInfo") {
			return true
		}
	}
	return false
}

func isJSONContentType(contentType string) bool {
	contentType = strings.ToLower(contentType)
	return strings.Contains(contentType, "application/json") || strings.Contains(contentType, "+json")
}
