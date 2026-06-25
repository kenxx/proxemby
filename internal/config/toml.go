package config

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

type tomlConfig struct {
	Routes []struct {
		UpstreamURL string `toml:"upstream_url"`
		PublicURL   string `toml:"public_url"`
		ACMEDomain  string `toml:"acme_domain"`
	} `toml:"routes"`
	Server struct {
		HTTPAddr string `toml:"http_addr"`
	} `toml:"server"`
	TLS struct {
		Enable       bool   `toml:"enable"`
		Addr         string `toml:"addr"`
		ACMEEmail    string `toml:"acme_email"`
		ACMECacheDir string `toml:"acme_cache_dir"`
	} `toml:"tls"`
	Proxy struct {
		AllowedHosts         []string `toml:"allowed_hosts"`
		PlaybackInfoMaxBytes int64    `toml:"playbackinfo_max_bytes"`
		HideClient           bool     `toml:"hide_client"`
	} `toml:"proxy"`
	Clients struct {
		Allowed           []string `toml:"allowed"`
		TrustProxyHeaders bool     `toml:"trust_proxy_headers"`
	} `toml:"clients"`
	Logging struct {
		Debug  bool   `toml:"debug"`
		Level  string `toml:"level"`
		Format string `toml:"format"`
		Time   bool   `toml:"time"`
	} `toml:"logging"`
}

func configValuesFromTOMLFile(path string) (rawConfig, error) {
	var cfg tomlConfig
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return rawConfig{}, fmt.Errorf("load config %s: %w", path, err)
	}
	return rawConfigFromTOML(cfg, meta), nil
}

func rawConfigFromTOML(cfg tomlConfig, meta toml.MetaData) rawConfig {
	raw := rawConfig{}
	if meta.IsDefined("routes") {
		raw.Routes = make([]routeValues, 0, len(cfg.Routes))
		for _, route := range cfg.Routes {
			raw.Routes = append(raw.Routes, routeValues{
				UpstreamURL: route.UpstreamURL,
				PublicURL:   route.PublicURL,
				ACMEDomain:  route.ACMEDomain,
			})
		}
	}
	if meta.IsDefined("server", "http_addr") {
		raw.HTTPAddr = &cfg.Server.HTTPAddr
	}
	if meta.IsDefined("tls", "enable") {
		raw.TLSEnable = &cfg.TLS.Enable
	}
	if meta.IsDefined("tls", "addr") {
		raw.TLSAddr = &cfg.TLS.Addr
	}
	if meta.IsDefined("tls", "acme_email") {
		raw.ACMEEmail = &cfg.TLS.ACMEEmail
	}
	if meta.IsDefined("tls", "acme_cache_dir") {
		raw.ACMECacheDir = &cfg.TLS.ACMECacheDir
	}
	if meta.IsDefined("proxy", "allowed_hosts") {
		raw.AllowedHosts = cfg.Proxy.AllowedHosts
	}
	if meta.IsDefined("proxy", "playbackinfo_max_bytes") {
		raw.PlaybackInfoMaxBytes = &cfg.Proxy.PlaybackInfoMaxBytes
	}
	if meta.IsDefined("proxy", "hide_client") {
		raw.HideClient = &cfg.Proxy.HideClient
	}
	if meta.IsDefined("clients", "allowed") {
		raw.AllowedClients = cfg.Clients.Allowed
	}
	if meta.IsDefined("clients", "trust_proxy_headers") {
		raw.TrustProxyHeaders = &cfg.Clients.TrustProxyHeaders
	}
	if meta.IsDefined("logging", "debug") {
		raw.Debug = &cfg.Logging.Debug
	}
	if meta.IsDefined("logging", "level") {
		raw.LogLevel = &cfg.Logging.Level
	}
	if meta.IsDefined("logging", "format") {
		raw.LogFormat = &cfg.Logging.Format
	}
	if meta.IsDefined("logging", "time") {
		raw.LogTime = &cfg.Logging.Time
	}
	return raw
}
