package proxemby

import (
	"errors"
	"flag"
	"os"
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
	if cfg.PlaybackInfoMaxBytes != defaultPlaybackInfoMaxBytes {
		t.Fatalf("PlaybackInfoMaxBytes = %d, want %d", cfg.PlaybackInfoMaxBytes, defaultPlaybackInfoMaxBytes)
	}
	if len(cfg.AllowedClients) != 0 {
		t.Fatalf("AllowedClients length = %d, want 0", len(cfg.AllowedClients))
	}
	if cfg.HideClient {
		t.Fatal("HideClient = true, want false")
	}
	if cfg.Debug {
		t.Fatal("Debug = true, want false")
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

func TestConfigFromMapDebug(t *testing.T) {
	cfg, err := ConfigFromMap(map[string]string{
		"PROXEMBY_ROUTE": "https://us.emby.com,http://proxemby",
		"PROXEMBY_DEBUG": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Debug {
		t.Fatal("Debug = false, want true")
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
	if !cfg.HideClient || !cfg.Debug {
		t.Fatalf("HideClient/Debug = %v/%v, want true/true", cfg.HideClient, cfg.Debug)
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
debug = false
`)

	cfg, err := ConfigFromSources([]string{
		"--config", path,
		"--route", "https://cli.emby.com,http://cli-public",
		"--http-addr", ":9090",
		"--debug",
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
	if !cfg.Debug {
		t.Fatal("Debug = false, want CLI true")
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
	if !cfg.Debug {
		t.Fatal("Debug = false, want true")
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
