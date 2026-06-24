package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"golang.org/x/crypto/acme/autocert"

	"proxemby/internal/proxemby"
)

func main() {
	cfg, err := proxemby.ConfigFromSources(os.Args[1:], os.Environ())
	if errors.Is(err, flag.ErrHelp) {
		proxemby.WriteConfigUsage(os.Stdout)
		return
	}
	if err != nil {
		log.Fatal(err)
	}

	server := proxemby.NewServer(cfg)
	handler := server.Handler()
	httpHandler := handler

	var tlsConfig *tls.Config
	if cfg.TLSEnable {
		manager := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.ACMEDomains...),
			Cache:      autocert.DirCache(cfg.ACMECacheDir),
			Email:      strings.TrimSpace(cfg.ACMEEmail),
		}
		httpHandler = manager.HTTPHandler(handler)
		tlsConfig = &tls.Config{GetCertificate: manager.GetCertificate}
	}

	errCh := make(chan error, 2)
	go func() {
		log.Printf("proxemby listening on http://%s", cfg.HTTPAddr)
		errCh <- http.ListenAndServe(cfg.HTTPAddr, httpHandler)
	}()

	if cfg.TLSEnable {
		go func() {
			tlsServer := &http.Server{
				Addr:      cfg.TLSAddr,
				Handler:   handler,
				TLSConfig: tlsConfig,
			}
			log.Printf("proxemby listening on https://%s", cfg.TLSAddr)
			errCh <- tlsServer.ListenAndServeTLS("", "")
		}()
	}

	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
