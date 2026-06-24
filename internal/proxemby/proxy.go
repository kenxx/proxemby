package proxemby

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

const resourcePrefix = "/_proxy/"

var errResponseTooLarge = errors.New("response body exceeds limit")

type Server struct {
	cfg            Config
	registry       *HostRegistry
	rewriter       *Rewriter
	upstreamProxy  *httputil.ReverseProxy
	resourceProxy  *httputil.ReverseProxy
	upstreamTarget *url.URL
}

func NewServer(cfg Config) *Server {
	registry := NewHostRegistry(cfg.AllowedHosts)
	server := &Server{
		cfg:            cfg,
		registry:       registry,
		rewriter:       NewRewriter(cfg.PublicURL, registry),
		upstreamTarget: cfg.UpstreamURL,
	}
	server.upstreamProxy = server.newUpstreamProxy()
	server.resourceProxy = server.newResourceProxy()
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(resourcePrefix, http.HandlerFunc(s.handleResourceProxy))
	mux.Handle("/", s.upstreamProxy)
	return newClientFilter(s.cfg.AllowedClients, s.cfg.TrustProxyHeaders, mux)
}

func (s *Server) newUpstreamProxy() *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(s.upstreamTarget)
	proxy.Transport = noCompressionTransport()
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		playbackInfo := isPlaybackInfoPath(req.URL.Path)
		originalHost := req.Host
		originalDirector(req)
		req.Host = s.upstreamTarget.Host
		req.Header.Set("X-Forwarded-Host", originalHost)
		if playbackInfo {
			req.Header.Del("Accept-Encoding")
		}
	}
	proxy.ModifyResponse = s.modifyUpstreamResponse
	proxy.ErrorLog = log.Default()
	return proxy
}

func (s *Server) modifyUpstreamResponse(resp *http.Response) error {
	if !isPlaybackInfoPath(resp.Request.URL.Path) || !isJSONContentType(resp.Header.Get("Content-Type")) {
		return nil
	}

	body, err := readResponseBody(resp, s.cfg.PlaybackInfoMaxBytes)
	if err != nil {
		return err
	}
	rewritten, changed, err := s.rewriter.RewritePlaybackInfo(body)
	if err != nil {
		return err
	}
	if !changed {
		rewritten = body
	}

	resp.Body = io.NopCloser(bytes.NewReader(rewritten))
	resp.ContentLength = int64(len(rewritten))
	resp.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
	resp.Header.Del("Content-Encoding")
	return nil
}

func (s *Server) newResourceProxy() *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{
		Transport: http.DefaultTransport,
		Director: func(req *http.Request) {
			target, _ := req.Context().Value(resourceTargetKey{}).(*url.URL)
			if target == nil {
				return
			}
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = target.Path
			req.URL.RawPath = target.RawPath
			req.URL.RawQuery = target.RawQuery
			req.Host = target.Host
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

func (s *Server) handleResourceProxy(w http.ResponseWriter, req *http.Request) {
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
