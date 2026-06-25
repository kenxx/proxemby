package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"golang.org/x/crypto/acme/autocert"

	"proxemby/internal/config"
	"proxemby/internal/logging"
	"proxemby/internal/server"
)

func main() {
	cfg, err := config.ConfigFromSources(os.Args[1:], os.Environ())
	if errors.Is(err, flag.ErrHelp) {
		config.WriteConfigUsage(os.Stdout)
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	logger, err := logging.NewLogger(cfg.Logging, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	for _, route := range cfg.Routes {
		logger.Info("proxemby route configured", "public_url", route.PublicURL.String(), "upstream_url", route.UpstreamURL.String(), "acme_domain", route.ACMEDomain)
	}

	proxyServer := server.NewServerWithLogger(cfg, logger)
	handler := proxyServer.Handler()
	httpHandler := handler

	var tlsConfig *tls.Config
	if cfg.TLSEnable {
		manager := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.ACMEDomains...),
			Cache:      autocert.DirCache(cfg.ACMECacheDir),
			Email:      strings.TrimSpace(cfg.ACMEEmail),
		}
		logger.Info("proxemby tls acme configured", "domains", cfg.ACMEDomains, "cache_dir", cfg.ACMECacheDir)
		httpHandler = manager.HTTPHandler(handler)
		tlsConfig = &tls.Config{GetCertificate: manager.GetCertificate}
	}

	errCh := make(chan error, 2)
	go func() {
		logger.Info("proxemby listening", "scheme", "http", "addr", cfg.HTTPAddr)
		errCh <- http.ListenAndServe(cfg.HTTPAddr, httpHandler)
	}()

	if cfg.TLSEnable {
		go func() {
			tlsServer := &http.Server{
				Addr:      cfg.TLSAddr,
				Handler:   handler,
				TLSConfig: tlsConfig,
			}
			logger.Info("proxemby listening", "scheme", "https", "addr", cfg.TLSAddr)
			errCh <- tlsServer.ListenAndServeTLS("", "")
		}()
	}

	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("proxemby server failed", "error", err)
		os.Exit(1)
	}
}
