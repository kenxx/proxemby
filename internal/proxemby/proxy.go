package proxemby

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

const resourcePrefix = "/_proxy/"

var errResponseTooLarge = errors.New("response body exceeds limit")

type Server struct {
	handlers map[string]http.Handler
	logger   *slog.Logger
}

type routeProxy struct {
	cfg            Config
	route          Route
	registry       *HostRegistry
	rewriter       *Rewriter
	upstreamProxy  *httputil.ReverseProxy
	resourceProxy  *httputil.ReverseProxy
	upstreamTarget *url.URL
	logger         *slog.Logger
}

func NewServer(cfg Config) *Server {
	return NewServerWithLogger(cfg, slog.Default())
}

func NewServerWithLogger(cfg Config, logger *slog.Logger) *Server {
	server := &Server{
		handlers: make(map[string]http.Handler, len(cfg.Routes)),
		logger:   logger,
	}
	for _, route := range cfg.Routes {
		proxy := newRouteProxy(cfg, route, logger.With(
			"route", route.PublicURL.Hostname(),
			"public_url", route.PublicURL.String(),
			"upstream_url", route.UpstreamURL.String(),
		))
		server.handlers[strings.ToLower(route.PublicURL.Hostname())] = proxy.Handler()
	}
	return server
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.ServeHTTP)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	host := requestHostname(req.Host)
	handler, ok := s.handlers[strings.ToLower(host)]
	if !ok {
		s.logger.Debug("route miss", "host", req.Host, "path", sanitizeRequestURI(req.URL))
		http.NotFound(w, req)
		return
	}
	handler.ServeHTTP(w, req)
}

func requestHostname(host string) string {
	host = strings.TrimSpace(host)
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		return strings.Trim(parsed, "[]")
	}
	return strings.Trim(host, "[]")
}

func newRouteProxy(cfg Config, route Route, logger *slog.Logger) *routeProxy {
	registry := NewHostRegistry(cfg.AllowedHosts)
	proxy := &routeProxy{
		cfg:            cfg,
		route:          route,
		registry:       registry,
		rewriter:       NewRewriter(route.PublicURL, registry),
		upstreamTarget: route.UpstreamURL,
		logger:         logger,
	}
	proxy.upstreamProxy = proxy.newUpstreamProxy()
	proxy.resourceProxy = proxy.newResourceProxy()
	return proxy
}

func (s *routeProxy) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(resourcePrefix, http.HandlerFunc(s.handleResourceProxy))
	mux.Handle("/", s.upstreamProxy)
	handler := newClientFilter(s.cfg.AllowedClients, s.cfg.TrustProxyHeaders, s.logger, mux)
	return newRequestLogger(s.logger, s.upstreamTarget, s.cfg.TrustProxyHeaders, handler)
}

func (s *routeProxy) newUpstreamProxy() *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{
		Transport: noCompressionTransport(),
		Rewrite: func(req *httputil.ProxyRequest) {
			playbackInfo := isPlaybackInfoPath(req.In.URL.Path)
			req.SetURL(s.upstreamTarget)
			req.Out.Host = s.upstreamTarget.Host
			if s.cfg.HideClient {
				s.logger.Debug("upstream request hides client identity headers", "path", sanitizeRequestURI(req.In.URL), "target", s.upstreamTarget.String())
				deleteProxyIdentityHeaders(req.Out.Header)
			} else {
				req.SetXForwarded()
			}
			if playbackInfo {
				req.Out.Header.Del("Accept-Encoding")
			}
		},
		ModifyResponse: s.modifyUpstreamResponse,
		ErrorLog:       log.New(slogWriter{logger: s.logger, level: slog.LevelError}, "", 0),
	}
	return proxy
}

func deleteProxyIdentityHeaders(header http.Header) {
	for _, name := range []string{
		"Forwarded",
		"Via",
		"X-Forwarded-For",
		"X-Forwarded-Host",
		"X-Forwarded-Proto",
		"X-Forwarded-Protocol",
		"X-Forwarded-Ssl",
		"X-Real-IP",
	} {
		header.Del(name)
	}
}

func (s *routeProxy) modifyUpstreamResponse(resp *http.Response) error {
	if !isPlaybackInfoPath(resp.Request.URL.Path) || !isJSONContentType(resp.Header.Get("Content-Type")) {
		return nil
	}

	body, err := readResponseBody(resp, s.cfg.PlaybackInfoMaxBytes)
	if err != nil {
		if errors.Is(err, errResponseTooLarge) {
			s.logger.Warn("playbackinfo response rejected", "reason", "response_too_large", "max_bytes", s.cfg.PlaybackInfoMaxBytes, "path", sanitizeRequestURI(resp.Request.URL))
		}
		return err
	}
	rewritten, events, err := s.rewriter.RewritePlaybackInfo(body)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		rewritten = body
	}
	s.logRewriteEvents(resp.Request, events)

	resp.Body = io.NopCloser(bytes.NewReader(rewritten))
	resp.ContentLength = int64(len(rewritten))
	resp.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
	resp.Header.Del("Content-Encoding")
	return nil
}

func (s *routeProxy) logRewriteEvents(req *http.Request, events []RewriteEvent) {
	s.logger.Debug("playbackinfo rewrite", "path", sanitizeRequestURI(req.URL), "count", len(events))
	for _, event := range events {
		s.logger.Debug(
			"playbackinfo rewrite item",
			"json_path", event.Path,
			"scheme", event.Scheme,
			"host", event.Host,
			"from", sanitizeURLString(event.Original),
			"to", sanitizeURLString(event.Rewritten),
		)
	}
}

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
		ErrorLog: log.New(slogWriter{logger: s.logger, level: slog.LevelError}, "", 0),
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

func readResponseBody(resp *http.Response, maxBytes int64) ([]byte, error) {
	defer resp.Body.Close()
	if maxBytes <= 0 {
		maxBytes = defaultPlaybackInfoMaxBytes
	}

	var reader io.Reader = resp.Body
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer gzipReader.Close()
		reader = gzipReader
	}

	limited := io.LimitReader(reader, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, errResponseTooLarge
	}
	return body, nil
}
