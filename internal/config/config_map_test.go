package config

import (
	"log/slog"
	"path/filepath"
	"testing"
)

func TestConfigFromMapRequiresRoute(t *testing.T) {
	_, err := ConfigFromMap(map[string]string{})
	if err == nil {
		t.Fatal("expected missing route error")
	}
}

func TestConfigFromMapDefaultsAndAllowedHosts(t *testing.T) {
	cfg, err := ConfigFromMap(map[string]string{
		"PROXEMBY_ROUTE":         "https://us.emby.com,http://proxemby",
		"PROXEMBY_ALLOWED_HOSTS": "vod.us.emby.com, cdn.example.com ",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Routes) != 1 {
		t.Fatalf("Routes length = %d, want 1", len(cfg.Routes))
	}
	if cfg.Routes[0].UpstreamURL.String() != "https://us.emby.com" {
		t.Fatalf("UpstreamURL = %q, want https://us.emby.com", cfg.Routes[0].UpstreamURL.String())
	}
	if cfg.Routes[0].PublicURL.String() != "http://proxemby" {
		t.Fatalf("PublicURL = %q, want http://proxemby", cfg.Routes[0].PublicURL.String())
	}
	if cfg.Routes[0].ACMEDomain != "proxemby" {
		t.Fatalf("ACMEDomain = %q, want proxemby", cfg.Routes[0].ACMEDomain)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.TLSAddr != ":443" {
		t.Fatalf("TLSAddr = %q, want :443", cfg.TLSAddr)
	}
	if cfg.ACMECacheDir != ".acme-cache" {
		t.Fatalf("ACMECacheDir = %q, want .acme-cache", cfg.ACMECacheDir)
	}
	if cfg.PlaybackInfoMaxBytes != DefaultPlaybackInfoMaxBytes {
		t.Fatalf("PlaybackInfoMaxBytes = %d, want %d", cfg.PlaybackInfoMaxBytes, DefaultPlaybackInfoMaxBytes)
	}
	if len(cfg.AllowedClients) != 0 {
		t.Fatalf("AllowedClients length = %d, want 0", len(cfg.AllowedClients))
	}
	if cfg.HideClient {
		t.Fatal("HideClient = true, want false")
	}
	if cfg.Logging.Level != slog.LevelInfo {
		t.Fatalf("Log level = %v, want info", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "text" {
		t.Fatalf("Log format = %q, want text", cfg.Logging.Format)
	}
	if !cfg.Logging.Time {
		t.Fatal("Log time = false, want true")
	}
	if len(cfg.AllowedHosts) != 2 {
		t.Fatalf("AllowedHosts length = %d, want 2", len(cfg.AllowedHosts))
	}
}

func TestConfigFromMapParsesMultipleRoutes(t *testing.T) {
	cfg, err := ConfigFromMap(map[string]string{
		"PROXEMBY_ROUTE": "https://us.emby.com,https://proxemby.jp,cdn.proxemby.jp;https://us2.emby.com,https://proxemby2.jp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Routes) != 2 {
		t.Fatalf("Routes length = %d, want 2", len(cfg.Routes))
	}
	if cfg.Routes[0].ACMEDomain != "cdn.proxemby.jp" {
		t.Fatalf("first ACMEDomain = %q, want cdn.proxemby.jp", cfg.Routes[0].ACMEDomain)
	}
	if cfg.Routes[1].ACMEDomain != "proxemby2.jp" {
		t.Fatalf("second ACMEDomain = %q, want proxemby2.jp", cfg.Routes[1].ACMEDomain)
	}
	if len(cfg.ACMEDomains) != 2 || cfg.ACMEDomains[0] != "cdn.proxemby.jp" || cfg.ACMEDomains[1] != "proxemby2.jp" {
		t.Fatalf("ACMEDomains = %v, want [cdn.proxemby.jp proxemby2.jp]", cfg.ACMEDomains)
	}
}

func TestConfigFromMapLogging(t *testing.T) {
	cfg, err := ConfigFromMap(map[string]string{
		"PROXEMBY_ROUTE":      "https://us.emby.com,http://proxemby",
		"PROXEMBY_DEBUG":      "true",
		"PROXEMBY_LOG_LEVEL":  "warn",
		"PROXEMBY_LOG_FORMAT": "json",
		"PROXEMBY_LOG_TIME":   "false",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Logging.Level != slog.LevelWarn {
		t.Fatalf("Log level = %v, want warn", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "json" {
		t.Fatalf("Log format = %q, want json", cfg.Logging.Format)
	}
	if cfg.Logging.Time {
		t.Fatal("Log time = true, want false")
	}

	cfg, err = ConfigFromMap(map[string]string{
		"PROXEMBY_ROUTE": "https://us.emby.com,http://proxemby",
		"PROXEMBY_DEBUG": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Logging.Level != slog.LevelDebug {
		t.Fatalf("legacy debug log level = %v, want debug", cfg.Logging.Level)
	}
}

func TestConfigFromMapHideClient(t *testing.T) {
	cfg, err := ConfigFromMap(map[string]string{
		"PROXEMBY_ROUTE":       "https://us.emby.com,http://proxemby",
		"PROXEMBY_HIDE_CLIENT": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HideClient {
		t.Fatal("HideClient = false, want true")
	}
}

func TestConfigFromMapAllowedClients(t *testing.T) {
	cfg, err := ConfigFromMap(map[string]string{
		"PROXEMBY_ROUTE":               "https://us.emby.com,http://proxemby",
		"PROXEMBY_ALLOWED_CLIENTS":     "1.2.3.4, 192.168.0.0/24",
		"PROXEMBY_TRUST_PROXY_HEADERS": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AllowedClients) != 2 {
		t.Fatalf("AllowedClients length = %d, want 2", len(cfg.AllowedClients))
	}
	if !cfg.TrustProxyHeaders {
		t.Fatal("TrustProxyHeaders = false, want true")
	}

	_, err = ConfigFromMap(map[string]string{
		"PROXEMBY_ROUTE":           "https://us.emby.com,http://proxemby",
		"PROXEMBY_ALLOWED_CLIENTS": "not-an-ip",
	})
	if err == nil {
		t.Fatal("expected allowed clients validation error")
	}
}

func TestConfigFromMapPlaybackInfoMaxBytes(t *testing.T) {
	cfg, err := ConfigFromMap(map[string]string{
		"PROXEMBY_ROUTE":                  "https://us.emby.com,http://proxemby",
		"PROXEMBY_PLAYBACKINFO_MAX_BYTES": "1024",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PlaybackInfoMaxBytes != 1024 {
		t.Fatalf("PlaybackInfoMaxBytes = %d, want 1024", cfg.PlaybackInfoMaxBytes)
	}

	_, err = ConfigFromMap(map[string]string{
		"PROXEMBY_ROUTE":                  "https://us.emby.com,http://proxemby",
		"PROXEMBY_PLAYBACKINFO_MAX_BYTES": "0",
	})
	if err == nil {
		t.Fatal("expected max bytes validation error")
	}
}

func TestConfigFromMapTLSUsesRouteACMEDomains(t *testing.T) {
	cfg, err := ConfigFromMap(map[string]string{
		"PROXEMBY_ROUTE":      "https://us.emby.com,https://proxemby.example.com;https://us2.emby.com,https://proxemby2.example.com,cert.example.com",
		"PROXEMBY_TLS_ENABLE": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.TLSEnable {
		t.Fatal("TLSEnable = false, want true")
	}
	if len(cfg.ACMEDomains) != 2 || cfg.ACMEDomains[0] != "proxemby.example.com" || cfg.ACMEDomains[1] != "cert.example.com" {
		t.Fatalf("ACMEDomains = %v, want [proxemby.example.com cert.example.com]", cfg.ACMEDomains)
	}
}

func TestConfigFromSourcesIgnoresMissingDefaultConfig(t *testing.T) {
	cfg, err := configFromSources([]string{
		"--route", "https://cli.emby.com,http://proxemby",
	}, nil, filepath.Join(t.TempDir(), "missing-default.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Routes[0].UpstreamURL.String() != "https://cli.emby.com" {
		t.Fatalf("UpstreamURL = %q, want https://cli.emby.com", cfg.Routes[0].UpstreamURL.String())
	}
}
