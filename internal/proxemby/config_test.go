package proxemby

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigFromMapRequiresUpstreamAndPublicURL(t *testing.T) {
	_, err := ConfigFromMap(map[string]string{})
	if err == nil {
		t.Fatal("expected missing upstream error")
	}

	_, err = ConfigFromMap(map[string]string{
		"PROXEMBY_UPSTREAM_URL": "https://us.emby.com",
	})
	if err == nil {
		t.Fatal("expected missing public URL error")
	}
}

func TestConfigFromMapDefaultsAndAllowedHosts(t *testing.T) {
	cfg, err := ConfigFromMap(map[string]string{
		"PROXEMBY_UPSTREAM_URL":  "https://us.emby.com",
		"PROXEMBY_PUBLIC_URL":    "http://proxemby",
		"PROXEMBY_ALLOWED_HOSTS": "vod.us.emby.com, cdn.example.com ",
	})
	if err != nil {
		t.Fatal(err)
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

func TestConfigFromMapDebug(t *testing.T) {
	cfg, err := ConfigFromMap(map[string]string{
		"PROXEMBY_UPSTREAM_URL": "https://us.emby.com",
		"PROXEMBY_PUBLIC_URL":   "http://proxemby",
		"PROXEMBY_DEBUG":        "true",
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
		"PROXEMBY_UPSTREAM_URL": "https://us.emby.com",
		"PROXEMBY_PUBLIC_URL":   "http://proxemby",
		"PROXEMBY_HIDE_CLIENT":  "true",
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
		"PROXEMBY_UPSTREAM_URL":        "https://us.emby.com",
		"PROXEMBY_PUBLIC_URL":          "http://proxemby",
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
		"PROXEMBY_UPSTREAM_URL":    "https://us.emby.com",
		"PROXEMBY_PUBLIC_URL":      "http://proxemby",
		"PROXEMBY_ALLOWED_CLIENTS": "not-an-ip",
	})
	if err == nil {
		t.Fatal("expected allowed clients validation error")
	}
}

func TestConfigFromMapPlaybackInfoMaxBytes(t *testing.T) {
	cfg, err := ConfigFromMap(map[string]string{
		"PROXEMBY_UPSTREAM_URL":           "https://us.emby.com",
		"PROXEMBY_PUBLIC_URL":             "http://proxemby",
		"PROXEMBY_PLAYBACKINFO_MAX_BYTES": "1024",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PlaybackInfoMaxBytes != 1024 {
		t.Fatalf("PlaybackInfoMaxBytes = %d, want 1024", cfg.PlaybackInfoMaxBytes)
	}

	_, err = ConfigFromMap(map[string]string{
		"PROXEMBY_UPSTREAM_URL":           "https://us.emby.com",
		"PROXEMBY_PUBLIC_URL":             "http://proxemby",
		"PROXEMBY_PLAYBACKINFO_MAX_BYTES": "0",
	})
	if err == nil {
		t.Fatal("expected max bytes validation error")
	}
}

func TestConfigFromMapTLSRequiresACMEDomains(t *testing.T) {
	_, err := ConfigFromMap(map[string]string{
		"PROXEMBY_UPSTREAM_URL": "https://us.emby.com",
		"PROXEMBY_PUBLIC_URL":   "https://proxemby.example.com",
		"PROXEMBY_TLS_ENABLE":   "true",
	})
	if err == nil {
		t.Fatal("expected ACME domains error")
	}

	cfg, err := ConfigFromMap(map[string]string{
		"PROXEMBY_UPSTREAM_URL":   "https://us.emby.com",
		"PROXEMBY_PUBLIC_URL":     "https://proxemby.example.com",
		"PROXEMBY_TLS_ENABLE":     "true",
		"PROXEMBY_ACME_DOMAINS":   "proxemby.example.com,www.example.com",
		"PROXEMBY_ACME_CACHE_DIR": "/tmp/proxemby-acme",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.TLSEnable {
		t.Fatal("TLSEnable = false, want true")
	}
	if len(cfg.ACMEDomains) != 2 {
		t.Fatalf("ACMEDomains length = %d, want 2", len(cfg.ACMEDomains))
	}
}

func TestConfigFromSourcesIgnoresMissingDefaultConfig(t *testing.T) {
	cfg, err := configFromSources([]string{
		"-u", "https://cli.emby.com",
		"-p", "http://proxemby",
	}, nil, filepath.Join(t.TempDir(), "missing-default.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpstreamURL.String() != "https://cli.emby.com" {
		t.Fatalf("UpstreamURL = %q, want https://cli.emby.com", cfg.UpstreamURL.String())
	}
}

func TestConfigFromSourcesExplicitMissingConfigErrors(t *testing.T) {
	_, err := ConfigFromSources([]string{
		"-c", filepath.Join(t.TempDir(), "missing.toml"),
		"-u", "https://cli.emby.com",
		"-p", "http://proxemby",
	}, nil)
	if err == nil {
		t.Fatal("expected missing config error")
	}
}

func TestConfigFromSourcesLoadsSectionedTOML(t *testing.T) {
	path := writeTestConfig(t, `
[upstream]
url = "https://toml.emby.com"

[public]
url = "http://proxemby"

[server]
http_addr = ":9090"

[tls]
enable = true
addr = ":9443"
acme_domains = ["proxemby.example.com"]
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
	if cfg.UpstreamURL.String() != "https://toml.emby.com" {
		t.Fatalf("UpstreamURL = %q, want https://toml.emby.com", cfg.UpstreamURL.String())
	}
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("HTTPAddr = %q, want :9090", cfg.HTTPAddr)
	}
	if !cfg.TLSEnable || cfg.TLSAddr != ":9443" || len(cfg.ACMEDomains) != 1 {
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
[upstream]
url = "https://toml.emby.com"

[public]
url = "http://toml-public"

[server]
http_addr = ":8081"

[logging]
debug = false
`)

	cfg, err := ConfigFromSources([]string{
		"--config", path,
		"--upstream-url", "https://cli.emby.com",
		"--http-addr", ":9090",
		"--debug",
	}, []string{
		"PROXEMBY_UPSTREAM_URL=https://env.emby.com",
		"PROXEMBY_PUBLIC_URL=http://env-public",
		"PROXEMBY_HTTP_ADDR=:8082",
		"PROXEMBY_DEBUG=false",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpstreamURL.String() != "https://cli.emby.com" {
		t.Fatalf("UpstreamURL = %q, want CLI value", cfg.UpstreamURL.String())
	}
	if cfg.PublicURL.String() != "http://env-public" {
		t.Fatalf("PublicURL = %q, want env value", cfg.PublicURL.String())
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
[upstream]
url = "https://toml.emby.com"

[public]
url = "http://toml-public"
`)

	cfg, err := ConfigFromSources([]string{
		"-c", path,
		"-u", "https://short.emby.com",
		"-p", "http://short-public",
		"-h", ":9091",
		"-a", "vod.example.com, cdn.example.com",
		"-d",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpstreamURL.String() != "https://short.emby.com" {
		t.Fatalf("UpstreamURL = %q, want short flag value", cfg.UpstreamURL.String())
	}
	if cfg.PublicURL.String() != "http://short-public" {
		t.Fatalf("PublicURL = %q, want short flag value", cfg.PublicURL.String())
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

func TestConfigFromSourcesValidationErrors(t *testing.T) {
	invalidTOML := writeTestConfig(t, `[[[`)
	_, err := ConfigFromSources([]string{"--config", invalidTOML}, nil)
	if err == nil {
		t.Fatal("expected invalid TOML error")
	}

	_, err = ConfigFromSources([]string{
		"-u", "ftp://example.com",
		"-p", "http://proxemby",
	}, nil)
	if err == nil {
		t.Fatal("expected invalid URL error")
	}

	_, err = ConfigFromSources([]string{
		"-u", "https://us.emby.com",
		"-p", "http://proxemby",
		"--allowed-clients", "not-an-ip",
	}, nil)
	if err == nil {
		t.Fatal("expected invalid CIDR error")
	}

	_, err = ConfigFromSources([]string{
		"-u", "https://us.emby.com",
		"-p", "http://proxemby",
		"--playbackinfo-max-bytes", "0",
	}, nil)
	if err == nil {
		t.Fatal("expected max bytes validation error")
	}
}

func TestConfigFromSourcesTLSRequiresACMEDomains(t *testing.T) {
	_, err := ConfigFromSources([]string{
		"-u", "https://us.emby.com",
		"-p", "https://proxemby.example.com",
		"--tls-enable",
	}, nil)
	if err == nil {
		t.Fatal("expected ACME domains error")
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
