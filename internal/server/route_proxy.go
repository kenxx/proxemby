package server

import (
	"log/slog"
	"net/http"

	"proxemby/internal/config"
	"proxemby/internal/hosts"
	"proxemby/internal/rewrite"
)

func newRouteProxy(cfg config.Config, route config.Route, logger *slog.Logger) *routeProxy {
	registry := hosts.NewRegistry(cfg.AllowedHosts)
	proxy := &routeProxy{
		cfg:            cfg,
		route:          route,
		registry:       registry,
		rewriter:       rewrite.NewRewriter(route.PublicURL, registry),
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
