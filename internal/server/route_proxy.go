package server

import (
	"log/slog"
	"net/http"
	"strings"

	"proxemby/internal/auth"
	"proxemby/internal/config"
	"proxemby/internal/hosts"
	"proxemby/internal/rewrite"
)

func newRouteProxy(cfg config.Config, route config.Route, store *auth.Store, logger *slog.Logger) *routeProxy {
	registry := hosts.NewRegistry(cfg.AllowedHosts)
	proxy := &routeProxy{
		cfg:            cfg,
		route:          route,
		registry:       registry,
		rewriter:       rewrite.NewRewriter(route.PublicURL, registry),
		upstreamTarget: route.UpstreamURL,
		logger:         logger,
		routeKey:       strings.ToLower(route.PublicURL.Hostname()),
	}
	if store != nil {
		proxy.auth = store
		proxy.allowedUsers = make(map[string]struct{}, len(cfg.AllowedUsers))
		for _, user := range cfg.AllowedUsers {
			proxy.allowedUsers[strings.ToLower(user)] = struct{}{}
		}
		proxy.rewriter = rewrite.NewSignedRewriter(route.PublicURL, registry, proxy.signResourceHost)
	}
	proxy.upstreamProxy = proxy.newUpstreamProxy()
	proxy.resourceProxy = proxy.newResourceProxy()
	return proxy
}

func (s *routeProxy) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(resourcePrefix, http.HandlerFunc(s.handleResourceProxy))
	mux.Handle("/", s.upstreamProxy)
	handler := s.authGate(mux)
	handler = newClientFilter(s.cfg.AllowedClients, s.cfg.TrustProxyHeaders, s.logger, handler)
	return newRequestLogger(s.logger, s.upstreamTarget, s.cfg.TrustProxyHeaders, handler)
}
