package server

import (
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"proxemby/internal/auth"
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

	// auth is nil when no allowed users are configured.
	auth         *auth.Store
	allowedUsers map[string]struct{}
	routeKey     string
}

func NewServer(cfg config.Config) *Server {
	return NewServerWithLogger(cfg, slog.Default())
}

func NewServerWithLogger(cfg config.Config, logger *slog.Logger) *Server {
	return NewServerWithStore(cfg, logger, auth.NewMemoryStore())
}

// NewServerWithStore uses store to keep sessions accepted by the allowed users
// check. The store is ignored when cfg.AllowedUsers is empty.
func NewServerWithStore(cfg config.Config, logger *slog.Logger, store *auth.Store) *Server {
	if len(cfg.AllowedUsers) == 0 {
		store = nil
	}
	server := &Server{
		handlers: make(map[string]http.Handler, len(cfg.Routes)),
		logger:   logger,
	}
	for _, route := range cfg.Routes {
		proxy := newRouteProxy(cfg, route, store, logger.With(
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
