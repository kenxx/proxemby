package proxemby

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	defaultHTTPAddr             = ":8080"
	defaultTLSAddr              = ":443"
	defaultACMECacheDir         = ".acme-cache"
	defaultPlaybackInfoMaxBytes = 8 << 20
	DefaultConfigPath           = "/etc/proxemby/proxemby.toml"
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
	HideClient           bool
	Debug                bool
}

func ConfigFromEnv() (Config, error) {
	return ConfigFromMap(envMap(os.Environ()))
}

func ConfigFromMap(env map[string]string) (Config, error) {
	values := defaultConfigValues()
	if err := values.applyEnv(env); err != nil {
		return Config{}, err
	}
	return values.config()
}

func ConfigFromSources(args []string, env []string) (Config, error) {
	return configFromSources(args, env, DefaultConfigPath)
}

func configFromSources(args []string, env []string, defaultConfigPath string) (Config, error) {
	cli, err := parseConfigFlags(args)
	if err != nil {
		return Config{}, err
	}
	if cli.help {
		return Config{}, flag.ErrHelp
	}

	values := defaultConfigValues()

	configPath := defaultConfigPath
	explicitConfig := cli.configPath != nil
	if cli.configPath != nil {
		configPath = strings.TrimSpace(*cli.configPath)
	}
	if configPath != "" {
		raw, err := configValuesFromTOMLFile(configPath)
		if err != nil {
			if !explicitConfig && errors.Is(err, os.ErrNotExist) {
				// The default config file is optional so env-only and CLI-only runs keep working.
			} else {
				return Config{}, err
			}
		} else {
			values.applyRaw(raw)
		}
	}

	if err := values.applyEnv(envMap(env)); err != nil {
		return Config{}, err
	}
	values.applyRaw(cli.rawConfig)
	return values.config()
}

func WriteConfigUsage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  proxemby [options]

Options:
  -c, --config PATH                  Config file path (default /etc/proxemby/proxemby.toml)
  -u, --upstream-url URL             Upstream Emby server URL
  -p, --public-url URL               Public proxemby URL
  -h, --http-addr ADDR               HTTP listen address
  -a, --allowed-hosts HOSTS          Comma-separated resource proxy host allowlist
  -d, --debug                        Enable debug logging
      --tls-enable                   Enable built-in HTTPS with ACME
      --tls-addr ADDR                HTTPS listen address
      --acme-domains DOMAINS         Comma-separated ACME certificate domains
      --acme-email EMAIL             ACME account email
      --acme-cache-dir DIR           ACME certificate cache directory
      --playbackinfo-max-bytes N     Maximum PlaybackInfo JSON body size
      --allowed-clients CLIENTS      Comma-separated client IP/CIDR allowlist
      --trust-proxy-headers          Trust X-Forwarded-For/X-Real-IP for client checks
      --hide-client                  Hide client identity headers from upstream
      --help                         Show this help
`)
}

type configValues struct {
	UpstreamURL          string
	PublicURL            string
	HTTPAddr             string
	TLSEnable            bool
	TLSAddr              string
	ACMEDomains          []string
	ACMEEmail            string
	ACMECacheDir         string
	AllowedHosts         []string
	PlaybackInfoMaxBytes int64
	AllowedClients       []string
	TrustProxyHeaders    bool
	HideClient           bool
	Debug                bool
}

type rawConfig struct {
	UpstreamURL          *string
	PublicURL            *string
	HTTPAddr             *string
	TLSEnable            *bool
	TLSAddr              *string
	ACMEDomains          []string
	ACMEEmail            *string
	ACMECacheDir         *string
	AllowedHosts         []string
	PlaybackInfoMaxBytes *int64
	AllowedClients       []string
	TrustProxyHeaders    *bool
	HideClient           *bool
	Debug                *bool
}

func defaultConfigValues() configValues {
	return configValues{
		HTTPAddr:             defaultHTTPAddr,
		TLSAddr:              defaultTLSAddr,
		ACMECacheDir:         defaultACMECacheDir,
		PlaybackInfoMaxBytes: defaultPlaybackInfoMaxBytes,
	}
}

func (values *configValues) applyRaw(raw rawConfig) {
	if raw.UpstreamURL != nil {
		values.UpstreamURL = strings.TrimSpace(*raw.UpstreamURL)
	}
	if raw.PublicURL != nil {
		values.PublicURL = strings.TrimSpace(*raw.PublicURL)
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
	if raw.ACMEDomains != nil {
		values.ACMEDomains = cleanStrings(raw.ACMEDomains)
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
		values.Debug = *raw.Debug
	}
}

func (values *configValues) applyEnv(env map[string]string) error {
	raw := rawConfig{}
	if value, ok := nonEmptyEnv(env, "PROXEMBY_UPSTREAM_URL"); ok {
		raw.UpstreamURL = &value
	}
	if value, ok := nonEmptyEnv(env, "PROXEMBY_PUBLIC_URL"); ok {
		raw.PublicURL = &value
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
	if value, ok := nonEmptyEnv(env, "PROXEMBY_ACME_DOMAINS"); ok {
		raw.ACMEDomains = splitCSV(value)
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

	upstream, err := parseRequiredURL(values.UpstreamURL, "upstream URL")
	if err != nil {
		return Config{}, err
	}

	public, err := parseRequiredURL(values.PublicURL, "public URL")
	if err != nil {
		return Config{}, err
	}

	if values.TLSEnable && len(values.ACMEDomains) == 0 {
		return Config{}, errors.New("ACME domains are required when TLS is enabled")
	}

	return Config{
		UpstreamURL:          upstream,
		PublicURL:            public,
		HTTPAddr:             values.HTTPAddr,
		TLSEnable:            values.TLSEnable,
		TLSAddr:              values.TLSAddr,
		ACMEDomains:          cleanStrings(values.ACMEDomains),
		ACMEEmail:            strings.TrimSpace(values.ACMEEmail),
		ACMECacheDir:         values.ACMECacheDir,
		AllowedHosts:         cleanStrings(values.AllowedHosts),
		PlaybackInfoMaxBytes: values.PlaybackInfoMaxBytes,
		AllowedClients:       allowedClients,
		TrustProxyHeaders:    values.TrustProxyHeaders,
		HideClient:           values.HideClient,
		Debug:                values.Debug,
	}, nil
}

type tomlConfig struct {
	Upstream struct {
		URL string `toml:"url"`
	} `toml:"upstream"`
	Public struct {
		URL string `toml:"url"`
	} `toml:"public"`
	Server struct {
		HTTPAddr string `toml:"http_addr"`
	} `toml:"server"`
	TLS struct {
		Enable       bool     `toml:"enable"`
		Addr         string   `toml:"addr"`
		ACMEDomains  []string `toml:"acme_domains"`
		ACMEEmail    string   `toml:"acme_email"`
		ACMECacheDir string   `toml:"acme_cache_dir"`
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
		Debug bool `toml:"debug"`
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
	if meta.IsDefined("upstream", "url") {
		raw.UpstreamURL = &cfg.Upstream.URL
	}
	if meta.IsDefined("public", "url") {
		raw.PublicURL = &cfg.Public.URL
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
	if meta.IsDefined("tls", "acme_domains") {
		raw.ACMEDomains = cfg.TLS.ACMEDomains
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
	return raw
}

type cliConfig struct {
	rawConfig
	configPath *string
	help       bool
}

func parseConfigFlags(args []string) (cliConfig, error) {
	for _, arg := range args {
		if arg == "-help" || strings.HasPrefix(arg, "-help=") {
			return cliConfig{}, errors.New("use --help for help; -h is --http-addr")
		}
	}

	flags := flag.NewFlagSet("proxemby", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var (
		configPath          string
		upstreamURL         string
		publicURL           string
		httpAddr            string
		tlsEnable           bool
		tlsAddr             string
		acmeDomains         string
		acmeEmail           string
		acmeCacheDir        string
		allowedHosts        string
		playbackInfoMaxSize int64
		allowedClients      string
		trustProxyHeaders   bool
		hideClient          bool
		debug               bool
		help                bool
	)

	flags.StringVar(&configPath, "c", "", "config file path")
	flags.StringVar(&configPath, "config", "", "config file path")
	flags.StringVar(&upstreamURL, "u", "", "upstream Emby server URL")
	flags.StringVar(&upstreamURL, "upstream-url", "", "upstream Emby server URL")
	flags.StringVar(&publicURL, "p", "", "public proxemby URL")
	flags.StringVar(&publicURL, "public-url", "", "public proxemby URL")
	flags.StringVar(&httpAddr, "h", "", "HTTP listen address")
	flags.StringVar(&httpAddr, "http-addr", "", "HTTP listen address")
	flags.BoolVar(&tlsEnable, "tls-enable", false, "enable built-in HTTPS with ACME")
	flags.StringVar(&tlsAddr, "tls-addr", "", "HTTPS listen address")
	flags.StringVar(&acmeDomains, "acme-domains", "", "comma-separated ACME certificate domains")
	flags.StringVar(&acmeEmail, "acme-email", "", "ACME account email")
	flags.StringVar(&acmeCacheDir, "acme-cache-dir", "", "ACME certificate cache directory")
	flags.StringVar(&allowedHosts, "a", "", "comma-separated resource proxy host allowlist")
	flags.StringVar(&allowedHosts, "allowed-hosts", "", "comma-separated resource proxy host allowlist")
	flags.Int64Var(&playbackInfoMaxSize, "playbackinfo-max-bytes", 0, "maximum PlaybackInfo JSON body size")
	flags.StringVar(&allowedClients, "allowed-clients", "", "comma-separated client IP/CIDR allowlist")
	flags.BoolVar(&trustProxyHeaders, "trust-proxy-headers", false, "trust proxy client IP headers")
	flags.BoolVar(&hideClient, "hide-client", false, "hide client identity headers from upstream")
	flags.BoolVar(&debug, "d", false, "enable debug logging")
	flags.BoolVar(&debug, "debug", false, "enable debug logging")
	flags.BoolVar(&help, "help", false, "show help")

	if err := flags.Parse(args); err != nil {
		return cliConfig{}, err
	}
	if flags.NArg() > 0 {
		return cliConfig{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}

	cli := cliConfig{help: help}
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "c", "config":
			cli.configPath = &configPath
		case "u", "upstream-url":
			cli.UpstreamURL = &upstreamURL
		case "p", "public-url":
			cli.PublicURL = &publicURL
		case "h", "http-addr":
			cli.HTTPAddr = &httpAddr
		case "tls-enable":
			cli.TLSEnable = &tlsEnable
		case "tls-addr":
			cli.TLSAddr = &tlsAddr
		case "acme-domains":
			cli.ACMEDomains = splitCSV(acmeDomains)
		case "acme-email":
			cli.ACMEEmail = &acmeEmail
		case "acme-cache-dir":
			cli.ACMECacheDir = &acmeCacheDir
		case "a", "allowed-hosts":
			cli.AllowedHosts = splitCSV(allowedHosts)
		case "playbackinfo-max-bytes":
			cli.PlaybackInfoMaxBytes = &playbackInfoMaxSize
		case "allowed-clients":
			cli.AllowedClients = splitCSV(allowedClients)
		case "trust-proxy-headers":
			cli.TrustProxyHeaders = &trustProxyHeaders
		case "hide-client":
			cli.HideClient = &hideClient
		case "d", "debug":
			cli.Debug = &debug
		}
	})
	return cli, nil
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

func nonEmptyEnv(env map[string]string, name string) (string, bool) {
	value, ok := env[name]
	value = strings.TrimSpace(value)
	return value, ok && value != ""
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
	return parseClientPrefixValues(splitCSV(raw))
}

func parseClientPrefixValues(values []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := parseClientPrefix(value)
		if err != nil {
			return nil, fmt.Errorf("allowed clients contains invalid value %q: %w", value, err)
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

func cleanStrings(values []string) []string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	return cleaned
}

func parsePositiveInt(raw, name string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}
