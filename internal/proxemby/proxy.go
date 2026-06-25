package proxemby

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"log"
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
}

type routeProxy struct {
	cfg            Config
	route          Route
	registry       *HostRegistry
	rewriter       *Rewriter
	upstreamProxy  *httputil.ReverseProxy
	resourceProxy  *httputil.ReverseProxy
	upstreamTarget *url.URL
}

func NewServer(cfg Config) *Server {
	server := &Server{
		handlers: make(map[string]http.Handler, len(cfg.Routes)),
	}
	for _, route := range cfg.Routes {
		proxy := newRouteProxy(cfg, route)
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

func newRouteProxy(cfg Config, route Route) *routeProxy {
	registry := NewHostRegistry(cfg.AllowedHosts)
	proxy := &routeProxy{
		cfg:            cfg,
		route:          route,
		registry:       registry,
		rewriter:       NewRewriter(route.PublicURL, registry),
		upstreamTarget: route.UpstreamURL,
	}
	proxy.upstreamProxy = proxy.newUpstreamProxy()
	proxy.resourceProxy = proxy.newResourceProxy()
	return proxy
}

func (s *routeProxy) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(resourcePrefix, http.HandlerFunc(s.handleResourceProxy))
	mux.Handle("/", s.upstreamProxy)
	handler := newClientFilter(s.cfg.AllowedClients, s.cfg.TrustProxyHeaders, mux)
	return newDebugLogger(s.cfg.Debug, s.upstreamTarget, handler)
}

func (s *routeProxy) newUpstreamProxy() *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{
		Transport: noCompressionTransport(),
		Rewrite: func(req *httputil.ProxyRequest) {
			playbackInfo := isPlaybackInfoPath(req.In.URL.Path)
			req.SetURL(s.upstreamTarget)
			req.Out.Host = s.upstreamTarget.Host
			if s.cfg.HideClient {
				deleteProxyIdentityHeaders(req.Out.Header)
			} else {
				req.SetXForwarded()
			}
			if playbackInfo {
				req.Out.Header.Del("Accept-Encoding")
			}
		},
		ModifyResponse: s.modifyUpstreamResponse,
		ErrorLog:       log.Default(),
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
	if !s.cfg.Debug {
		return
	}
	log.Printf(
		"debug playbackinfo rewrite path=%s count=%d",
		sanitizeRequestURI(req.URL),
		len(events),
	)
	for _, event := range events {
		log.Printf(
			"debug playbackinfo rewrite item json_path=%s scheme=%s host=%s from=%s to=%s",
			event.Path,
			event.Scheme,
			event.Host,
			sanitizeURLString(event.Original),
			sanitizeURLString(event.Rewritten),
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
		ErrorLog: log.Default(),
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
		http.Error(w, "missing or invalid proxied resource scheme", http.StatusBadRequest)
		return
	}
	host, rest, ok := strings.Cut(remainder, "/")
	if !ok || host == "" {
		http.Error(w, "missing proxied resource host", http.StatusBadRequest)
		return
	}
	_, allowed := s.registry.Lookup(host)
	if !allowed {
		http.Error(w, "proxied resource host is not allowed", http.StatusForbidden)
		return
	}

	target := &url.URL{
		Scheme:   scheme,
		Host:     host,
		Path:     "/" + rest,
		RawQuery: req.URL.RawQuery,
	}

	ctx := withResourceTarget(req.Context(), target)
	s.resourceProxy.ServeHTTP(w, req.WithContext(ctx))
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
