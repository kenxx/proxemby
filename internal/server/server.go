package server

import (
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"proxemby/internal/config"
	"proxemby/internal/hosts"
	"proxemby/internal/rewrite"
)

const resourcePrefix = "/_proxy/"

type Server struct {
	handlers map[string]http.Handler
	logger   *slog.Logger
}

type routeProxy struct {
	cfg            config.Config
	route          config.Route
	registry       *hosts.Registry
	rewriter       *rewrite.Rewriter
	upstreamProxy  *httputil.ReverseProxy
	resourceProxy  *httputil.ReverseProxy
	upstreamTarget *url.URL
	logger         *slog.Logger
}

func NewServer(cfg config.Config) *Server {
	return NewServerWithLogger(cfg, slog.Default())
}

func NewServerWithLogger(cfg config.Config, logger *slog.Logger) *Server {
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
