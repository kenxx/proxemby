package config

import (
	"errors"
	"fmt"
	"strings"

	"proxemby/internal/logging"
)

type configValues struct {
	Routes               []routeValues
	HTTPAddr             string
	TLSEnable            bool
	TLSAddr              string
	ACMEEmail            string
	ACMECacheDir         string
	AllowedHosts         []string
	PlaybackInfoMaxBytes int64
	AllowedClients       []string
	TrustProxyHeaders    bool
	HideClient           bool
	LogLevel             string
	LogFormat            string
	LogTime              bool
}

type routeValues struct {
	UpstreamURL string
	PublicURL   string
	ACMEDomain  string
}

type rawConfig struct {
	Routes               []routeValues
	HTTPAddr             *string
	TLSEnable            *bool
	TLSAddr              *string
	ACMEEmail            *string
	ACMECacheDir         *string
	AllowedHosts         []string
	PlaybackInfoMaxBytes *int64
	AllowedClients       []string
	TrustProxyHeaders    *bool
	HideClient           *bool
	Debug                *bool
	LogLevel             *string
	LogFormat            *string
	LogTime              *bool
}

func defaultConfigValues() configValues {
	return configValues{
		HTTPAddr:             defaultHTTPAddr,
		TLSAddr:              defaultTLSAddr,
		ACMECacheDir:         defaultACMECacheDir,
		PlaybackInfoMaxBytes: DefaultPlaybackInfoMaxBytes,
		LogLevel:             logging.DefaultLevel,
		LogFormat:            logging.DefaultFormat,
		LogTime:              true,
	}
}

func (values *configValues) applyRaw(raw rawConfig) {
	if raw.Routes != nil {
		values.Routes = raw.Routes
	}
	if raw.HTTPAddr != nil {
		values.HTTPAddr = valueOrDefault(*raw.HTTPAddr, defaultHTTPAddr)
	}
	if raw.TLSEnable != nil {
		values.TLSEnable = *raw.TLSEnable
	}
	if raw.TLSAddr != nil {
		values.TLSAddr = valueOrDefault(*raw.TLSAddr, defaultTLSAddr)
	}
	if raw.ACMEEmail != nil {
		values.ACMEEmail = strings.TrimSpace(*raw.ACMEEmail)
	}
	if raw.ACMECacheDir != nil {
		values.ACMECacheDir = valueOrDefault(*raw.ACMECacheDir, defaultACMECacheDir)
	}
	if raw.AllowedHosts != nil {
		values.AllowedHosts = cleanStrings(raw.AllowedHosts)
	}
	if raw.PlaybackInfoMaxBytes != nil {
		values.PlaybackInfoMaxBytes = *raw.PlaybackInfoMaxBytes
	}
	if raw.AllowedClients != nil {
		values.AllowedClients = cleanStrings(raw.AllowedClients)
	}
	if raw.TrustProxyHeaders != nil {
		values.TrustProxyHeaders = *raw.TrustProxyHeaders
	}
	if raw.HideClient != nil {
		values.HideClient = *raw.HideClient
	}
	if raw.Debug != nil {
		if *raw.Debug {
			values.LogLevel = "debug"
		} else {
			values.LogLevel = logging.DefaultLevel
		}
	}
	if raw.LogLevel != nil {
		values.LogLevel = strings.TrimSpace(*raw.LogLevel)
	}
	if raw.LogFormat != nil {
		values.LogFormat = strings.TrimSpace(*raw.LogFormat)
	}
	if raw.LogTime != nil {
		values.LogTime = *raw.LogTime
	}
}

func (values *configValues) applyEnv(env map[string]string) error {
	raw := rawConfig{}
	if value, ok := nonEmptyEnv(env, "PROXEMBY_ROUTE"); ok {
		routes, err := parseRouteValues(value)
		if err != nil {
			return err
		}
		raw.Routes = routes
	}
	if value, ok := nonEmptyEnv(env, "PROXEMBY_HTTP_ADDR"); ok {
		raw.HTTPAddr = &value
	}
	if value, ok := env["PROXEMBY_TLS_ENABLE"]; ok {
		parsed := parseBool(value)
		raw.TLSEnable = &parsed
	}
	if value, ok := nonEmptyEnv(env, "PROXEMBY_TLS_ADDR"); ok {
		raw.TLSAddr = &value
	}
	if value, ok := nonEmptyEnv(env, "PROXEMBY_ACME_EMAIL"); ok {
		raw.ACMEEmail = &value
	}
	if value, ok := nonEmptyEnv(env, "PROXEMBY_ACME_CACHE_DIR"); ok {
		raw.ACMECacheDir = &value
	}
	if value, ok := nonEmptyEnv(env, "PROXEMBY_ALLOWED_HOSTS"); ok {
		raw.AllowedHosts = splitCSV(value)
	}
	if value, ok := nonEmptyEnv(env, "PROXEMBY_PLAYBACKINFO_MAX_BYTES"); ok {
		maxBytes, err := parsePositiveInt(value, "PROXEMBY_PLAYBACKINFO_MAX_BYTES")
		if err != nil {
			return err
		}
		raw.PlaybackInfoMaxBytes = &maxBytes
	}
	if value, ok := nonEmptyEnv(env, "PROXEMBY_ALLOWED_CLIENTS"); ok {
		raw.AllowedClients = splitCSV(value)
	}
	if value, ok := env["PROXEMBY_TRUST_PROXY_HEADERS"]; ok {
		parsed := parseBool(value)
		raw.TrustProxyHeaders = &parsed
	}
	if value, ok := env["PROXEMBY_HIDE_CLIENT"]; ok {
		parsed := parseBool(value)
		raw.HideClient = &parsed
	}
	if value, ok := env["PROXEMBY_DEBUG"]; ok {
		parsed := parseBool(value)
		raw.Debug = &parsed
	}
	if value, ok := nonEmptyEnv(env, "PROXEMBY_LOG_LEVEL"); ok {
		raw.LogLevel = &value
	}
	if value, ok := nonEmptyEnv(env, "PROXEMBY_LOG_FORMAT"); ok {
		raw.LogFormat = &value
	}
	if value, ok := env["PROXEMBY_LOG_TIME"]; ok {
		parsed := parseBool(value)
		raw.LogTime = &parsed
	}
	values.applyRaw(raw)
	return nil
}

func (values configValues) config() (Config, error) {
	if values.PlaybackInfoMaxBytes <= 0 {
		return Config{}, errors.New("playbackinfo max bytes must be a positive integer")
	}

	allowedClients, err := parseClientPrefixValues(values.AllowedClients)
	if err != nil {
		return Config{}, err
	}

	routes, acmeDomains, err := parseRoutes(values.Routes)
	if err != nil {
		return Config{}, err
	}

	if values.TLSEnable && len(acmeDomains) == 0 {
		return Config{}, errors.New("ACME domains are required when TLS is enabled")
	}

	logConfig, err := logging.ParseConfig(values.LogLevel, values.LogFormat, values.LogTime)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Routes:               routes,
		HTTPAddr:             values.HTTPAddr,
		TLSEnable:            values.TLSEnable,
		TLSAddr:              values.TLSAddr,
		ACMEDomains:          acmeDomains,
		ACMEEmail:            strings.TrimSpace(values.ACMEEmail),
		ACMECacheDir:         values.ACMECacheDir,
		AllowedHosts:         cleanStrings(values.AllowedHosts),
		PlaybackInfoMaxBytes: values.PlaybackInfoMaxBytes,
		AllowedClients:       allowedClients,
		TrustProxyHeaders:    values.TrustProxyHeaders,
		HideClient:           values.HideClient,
		Logging:              logConfig,
	}, nil
}

func parseRoutes(values []routeValues) ([]Route, []string, error) {
	if len(values) == 0 {
		return nil, nil, errors.New("at least one route is required")
	}

	routes := make([]Route, 0, len(values))
	acmeDomains := make([]string, 0, len(values))
	seenPublicHosts := map[string]struct{}{}
	seenACMEDomains := map[string]struct{}{}

	for i, value := range values {
		routeName := fmt.Sprintf("route %d", i+1)
		upstream, err := parseRequiredURL(value.UpstreamURL, routeName+" upstream URL")
		if err != nil {
			return nil, nil, err
		}
		public, err := parseRequiredURL(value.PublicURL, routeName+" public URL")
		if err != nil {
			return nil, nil, err
		}

		publicHost := strings.ToLower(public.Hostname())
		if _, ok := seenPublicHosts[publicHost]; ok {
			return nil, nil, fmt.Errorf("duplicate route public host %q", publicHost)
		}
		seenPublicHosts[publicHost] = struct{}{}

		acmeDomain := strings.TrimSpace(value.ACMEDomain)
		if acmeDomain == "" {
			acmeDomain = publicHost
		}
		acmeDomain = strings.ToLower(acmeDomain)
		if _, ok := seenACMEDomains[acmeDomain]; !ok {
			acmeDomains = append(acmeDomains, acmeDomain)
			seenACMEDomains[acmeDomain] = struct{}{}
		}

		routes = append(routes, Route{
			UpstreamURL: upstream,
			PublicURL:   public,
			ACMEDomain:  acmeDomain,
		})
	}

	return routes, acmeDomains, nil
}
