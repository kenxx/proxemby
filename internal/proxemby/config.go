package proxemby

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	defaultHTTPAddr             = ":8080"
	defaultTLSAddr              = ":443"
	defaultACMECacheDir         = ".acme-cache"
	defaultPlaybackInfoMaxBytes = 8 << 20
)

type Config struct {
	UpstreamURL          *url.URL
	PublicURL            *url.URL
	HTTPAddr             string
	TLSEnable            bool
	TLSAddr              string
	ACMEDomains          []string
	ACMEEmail            string
	ACMECacheDir         string
	AllowedHosts         []string
	PlaybackInfoMaxBytes int64
	AllowedClients       []netip.Prefix
	TrustProxyHeaders    bool
}

func ConfigFromEnv() (Config, error) {
	return ConfigFromMap(envMap(os.Environ()))
}

func ConfigFromMap(env map[string]string) (Config, error) {
	cfg := Config{
		HTTPAddr:             valueOrDefault(env["PROXEMBY_HTTP_ADDR"], defaultHTTPAddr),
		TLSAddr:              valueOrDefault(env["PROXEMBY_TLS_ADDR"], defaultTLSAddr),
		ACMEEmail:            strings.TrimSpace(env["PROXEMBY_ACME_EMAIL"]),
		ACMECacheDir:         valueOrDefault(env["PROXEMBY_ACME_CACHE_DIR"], defaultACMECacheDir),
		AllowedHosts:         splitCSV(env["PROXEMBY_ALLOWED_HOSTS"]),
		PlaybackInfoMaxBytes: defaultPlaybackInfoMaxBytes,
		TrustProxyHeaders:    parseBool(env["PROXEMBY_TRUST_PROXY_HEADERS"]),
	}

	allowedClients, err := parseClientPrefixes(env["PROXEMBY_ALLOWED_CLIENTS"])
	if err != nil {
		return Config{}, err
	}
	cfg.AllowedClients = allowedClients

	if raw := strings.TrimSpace(env["PROXEMBY_PLAYBACKINFO_MAX_BYTES"]); raw != "" {
		maxBytes, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || maxBytes <= 0 {
			return Config{}, errors.New("PROXEMBY_PLAYBACKINFO_MAX_BYTES must be a positive integer")
		}
		cfg.PlaybackInfoMaxBytes = maxBytes
	}

	upstream, err := parseRequiredURL(env["PROXEMBY_UPSTREAM_URL"], "PROXEMBY_UPSTREAM_URL")
	if err != nil {
		return Config{}, err
	}
	cfg.UpstreamURL = upstream

	public, err := parseRequiredURL(env["PROXEMBY_PUBLIC_URL"], "PROXEMBY_PUBLIC_URL")
	if err != nil {
		return Config{}, err
	}
	cfg.PublicURL = public

	cfg.TLSEnable = parseBool(env["PROXEMBY_TLS_ENABLE"])
	if cfg.TLSEnable {
		cfg.ACMEDomains = splitCSV(env["PROXEMBY_ACME_DOMAINS"])
		if len(cfg.ACMEDomains) == 0 {
			return Config{}, errors.New("PROXEMBY_ACME_DOMAINS is required when PROXEMBY_TLS_ENABLE=true")
		}
	}

	return cfg, nil
}

func envMap(entries []string) map[string]string {
	env := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, val, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = val
		}
	}
	return env
}

func parseRequiredURL(raw, name string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%s is required", name)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s is invalid: %w", name, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%s must use http or https", name)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%s must include a host", name)
	}
	return u, nil
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return values
}

func parseClientPrefixes(raw string) ([]netip.Prefix, error) {
	values := splitCSV(raw)
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := parseClientPrefix(value)
		if err != nil {
			return nil, fmt.Errorf("PROXEMBY_ALLOWED_CLIENTS contains invalid value %q: %w", value, err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func parseClientPrefix(value string) (netip.Prefix, error) {
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return netip.Prefix{}, err
		}
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, err
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func valueOrDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
