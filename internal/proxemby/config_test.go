package proxemby

import "testing"

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
	if len(cfg.AllowedHosts) != 2 {
		t.Fatalf("AllowedHosts length = %d, want 2", len(cfg.AllowedHosts))
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
