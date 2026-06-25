package config

import (
	"errors"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigFromSourcesExplicitMissingConfigErrors(t *testing.T) {
	_, err := ConfigFromSources([]string{
		"-c", filepath.Join(t.TempDir(), "missing.toml"),
		"--route", "https://cli.emby.com,http://proxemby",
	}, nil)
	if err == nil {
		t.Fatal("expected missing config error")
	}
}

func TestConfigFromSourcesLoadsSectionedTOML(t *testing.T) {
	path := writeTestConfig(t, `
[[routes]]
upstream_url = "https://toml.emby.com"
public_url = "https://proxemby.example.com"
acme_domain = "cert.example.com"

[[routes]]
upstream_url = "https://toml2.emby.com"
public_url = "https://proxemby2.example.com"

[server]
http_addr = ":9090"

[tls]
enable = true
addr = ":9443"
acme_email = "ops@example.com"
acme_cache_dir = "/tmp/proxemby-acme"

[proxy]
allowed_hosts = ["vod.example.com", "cdn.example.com"]
playbackinfo_max_bytes = 2048
hide_client = true

[clients]
allowed = ["1.2.3.4", "192.168.0.0/24"]
trust_proxy_headers = true

[logging]
debug = true
level = "error"
format = "json"
time = false
`)

	cfg, err := ConfigFromSources([]string{"--config", path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Routes) != 2 {
		t.Fatalf("Routes length = %d, want 2", len(cfg.Routes))
	}
	if cfg.Routes[0].UpstreamURL.String() != "https://toml.emby.com" {
		t.Fatalf("UpstreamURL = %q, want https://toml.emby.com", cfg.Routes[0].UpstreamURL.String())
	}
	if cfg.Routes[0].ACMEDomain != "cert.example.com" || cfg.Routes[1].ACMEDomain != "proxemby2.example.com" {
		t.Fatalf("route ACME domains = %q/%q", cfg.Routes[0].ACMEDomain, cfg.Routes[1].ACMEDomain)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("HTTPAddr = %q, want :9090", cfg.HTTPAddr)
	}
	if !cfg.TLSEnable || cfg.TLSAddr != ":9443" || len(cfg.ACMEDomains) != 2 {
		t.Fatalf("TLS config = enable:%v addr:%q domains:%v", cfg.TLSEnable, cfg.TLSAddr, cfg.ACMEDomains)
	}
	if cfg.ACMEEmail != "ops@example.com" || cfg.ACMECacheDir != "/tmp/proxemby-acme" {
		t.Fatalf("ACME config = email:%q cache:%q", cfg.ACMEEmail, cfg.ACMECacheDir)
	}
	if len(cfg.AllowedHosts) != 2 {
		t.Fatalf("AllowedHosts length = %d, want 2", len(cfg.AllowedHosts))
	}
	if cfg.PlaybackInfoMaxBytes != 2048 {
		t.Fatalf("PlaybackInfoMaxBytes = %d, want 2048", cfg.PlaybackInfoMaxBytes)
	}
	if len(cfg.AllowedClients) != 2 || !cfg.TrustProxyHeaders {
		t.Fatalf("client config = allowed:%v trust:%v", cfg.AllowedClients, cfg.TrustProxyHeaders)
	}
	if !cfg.HideClient || cfg.Logging.Level != slog.LevelError || cfg.Logging.Format != "json" || cfg.Logging.Time {
		t.Fatalf("HideClient/logging = %v/%v/%q/%v, want true/error/json/false", cfg.HideClient, cfg.Logging.Level, cfg.Logging.Format, cfg.Logging.Time)
	}
}

func TestConfigFromSourcesPrecedence(t *testing.T) {
	path := writeTestConfig(t, `
[[routes]]
upstream_url = "https://toml.emby.com"
public_url = "http://toml-public"

[server]
http_addr = ":8081"

[logging]
level = "warn"
`)

	cfg, err := ConfigFromSources([]string{
		"--config", path,
		"--route", "https://cli.emby.com,http://cli-public",
		"--http-addr", ":9090",
		"--debug",
		"--log-level", "error",
		"--log-format", "json",
		"--log-time=false",
	}, []string{
		"PROXEMBY_ROUTE=https://env.emby.com,http://env-public",
		"PROXEMBY_HTTP_ADDR=:8082",
		"PROXEMBY_DEBUG=false",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Routes[0].UpstreamURL.String() != "https://cli.emby.com" {
		t.Fatalf("UpstreamURL = %q, want CLI value", cfg.Routes[0].UpstreamURL.String())
	}
	if cfg.Routes[0].PublicURL.String() != "http://cli-public" {
		t.Fatalf("PublicURL = %q, want CLI value", cfg.Routes[0].PublicURL.String())
	}
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("HTTPAddr = %q, want CLI value", cfg.HTTPAddr)
	}
	if cfg.Logging.Level != slog.LevelError {
		t.Fatalf("Log level = %v, want CLI error", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "json" {
		t.Fatalf("Log format = %q, want CLI json", cfg.Logging.Format)
	}
	if cfg.Logging.Time {
		t.Fatal("Log time = true, want CLI false")
	}
}

func TestConfigFromSourcesShortFlags(t *testing.T) {
	path := writeTestConfig(t, `
[[routes]]
upstream_url = "https://toml.emby.com"
public_url = "http://toml-public"
`)

	cfg, err := ConfigFromSources([]string{
		"-c", path,
		"--route", "https://short.emby.com,http://short-public",
		"-h", ":9091",
		"-a", "vod.example.com, cdn.example.com",
		"-d",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Routes[0].UpstreamURL.String() != "https://short.emby.com" {
		t.Fatalf("UpstreamURL = %q, want short flag value", cfg.Routes[0].UpstreamURL.String())
	}
	if cfg.Routes[0].PublicURL.String() != "http://short-public" {
		t.Fatalf("PublicURL = %q, want short flag value", cfg.Routes[0].PublicURL.String())
	}
	if cfg.HTTPAddr != ":9091" {
		t.Fatalf("HTTPAddr = %q, want :9091", cfg.HTTPAddr)
	}
	if len(cfg.AllowedHosts) != 2 {
		t.Fatalf("AllowedHosts length = %d, want 2", len(cfg.AllowedHosts))
	}
	if cfg.Logging.Level != slog.LevelDebug {
		t.Fatalf("Log level = %v, want debug", cfg.Logging.Level)
	}
}

func TestConfigFromSourcesRepeatedRouteFlags(t *testing.T) {
	cfg, err := ConfigFromSources([]string{
		"--route", "https://one.emby.com,http://one.example.com",
		"--route", "https://two.emby.com,http://two.example.com,two-cert.example.com",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Routes) != 2 {
		t.Fatalf("Routes length = %d, want 2", len(cfg.Routes))
	}
	if cfg.Routes[1].ACMEDomain != "two-cert.example.com" {
		t.Fatalf("second ACMEDomain = %q, want two-cert.example.com", cfg.Routes[1].ACMEDomain)
	}
}

func TestConfigFromSourcesValidationErrors(t *testing.T) {
	invalidTOML := writeTestConfig(t, `[[[`)
	_, err := ConfigFromSources([]string{"--config", invalidTOML}, nil)
	if err == nil {
		t.Fatal("expected invalid TOML error")
	}

	for _, args := range [][]string{
		{"--route", "ftp://example.com,http://proxemby"},
		{"--route", "https://us.emby.com,ftp://proxemby"},
		{"--route", "https://us.emby.com"},
		{"--route", "https://us.emby.com,http://proxemby,"},
		{"--route", "https://us.emby.com,http://same.example.com;https://us2.emby.com,http://same.example.com"},
		{"--route", "https://us.emby.com,http://proxemby", "--allowed-clients", "not-an-ip"},
		{"--route", "https://us.emby.com,http://proxemby", "--playbackinfo-max-bytes", "0"},
		{"--route", "https://us.emby.com,http://proxemby", "--log-level", "verbose"},
		{"--route", "https://us.emby.com,http://proxemby", "--log-format", "plain"},
		{"-u", "https://us.emby.com", "-p", "http://proxemby"},
	} {
		_, err = ConfigFromSources(args, nil)
		if err == nil {
			t.Fatalf("args %v: expected validation error", args)
		}
	}
}

func TestConfigFromSourcesHelp(t *testing.T) {
	_, err := ConfigFromSources([]string{"--help"}, nil)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err = %v, want flag.ErrHelp", err)
	}

	_, err = ConfigFromSources([]string{"-help"}, nil)
	if err == nil {
		t.Fatal("expected -help error because -h is http addr")
	}
}

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "proxemby.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
