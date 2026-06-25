package config

import (
	"net/netip"
	"net/url"

	"proxemby/internal/logging"
)

const (
	defaultHTTPAddr             = ":8080"
	defaultTLSAddr              = ":443"
	defaultACMECacheDir         = ".acme-cache"
	DefaultPlaybackInfoMaxBytes = 8 << 20
	DefaultConfigPath           = "/etc/proxemby/proxemby.toml"
)

type Config struct {
	Routes               []Route
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
	HideClient           bool
	Logging              logging.Config
}

type Route struct {
	UpstreamURL *url.URL
	PublicURL   *url.URL
	ACMEDomain  string
}
