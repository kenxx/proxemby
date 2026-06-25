package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

type cliConfig struct {
	rawConfig
	configPath *string
	help       bool
}

type routeFlags []string

func (flags *routeFlags) String() string {
	return strings.Join(*flags, ";")
}

func (flags *routeFlags) Set(value string) error {
	*flags = append(*flags, value)
	return nil
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
		routes              routeFlags
		httpAddr            string
		tlsEnable           bool
		tlsAddr             string
		acmeEmail           string
		acmeCacheDir        string
		allowedHosts        string
		playbackInfoMaxSize int64
		allowedClients      string
		trustProxyHeaders   bool
		hideClient          bool
		debug               bool
		logLevel            string
		logFormat           string
		logTime             bool
		help                bool
	)

	flags.StringVar(&configPath, "c", "", "config file path")
	flags.StringVar(&configPath, "config", "", "config file path")
	flags.Var(&routes, "route", "route as upstream_url,public_url[,acme_domain]")
	flags.StringVar(&httpAddr, "h", "", "HTTP listen address")
	flags.StringVar(&httpAddr, "http-addr", "", "HTTP listen address")
	flags.BoolVar(&tlsEnable, "tls-enable", false, "enable built-in HTTPS with ACME")
	flags.StringVar(&tlsAddr, "tls-addr", "", "HTTPS listen address")
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
	flags.StringVar(&logLevel, "log-level", "", "log level")
	flags.StringVar(&logFormat, "log-format", "", "log format")
	flags.BoolVar(&logTime, "log-time", true, "include time in log output")
	flags.BoolVar(&help, "help", false, "show help")

	if err := flags.Parse(args); err != nil {
		return cliConfig{}, err
	}
	if flags.NArg() > 0 {
		return cliConfig{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}

	cli := cliConfig{help: help}
	var routeErr error
	flags.Visit(func(f *flag.Flag) {
		if routeErr != nil {
			return
		}
		switch f.Name {
		case "c", "config":
			cli.configPath = &configPath
		case "route":
			cli.Routes, routeErr = parseRouteValues(strings.Join(routes, ";"))
		case "h", "http-addr":
			cli.HTTPAddr = &httpAddr
		case "tls-enable":
			cli.TLSEnable = &tlsEnable
		case "tls-addr":
			cli.TLSAddr = &tlsAddr
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
		case "log-level":
			cli.LogLevel = &logLevel
		case "log-format":
			cli.LogFormat = &logFormat
		case "log-time":
			cli.LogTime = &logTime
		}
	})
	if routeErr != nil {
		return cliConfig{}, routeErr
	}
	return cli, nil
}

func parseRouteValues(raw string) ([]routeValues, error) {
	entries := strings.Split(raw, ";")
	routes := make([]routeValues, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, ",")
		if len(parts) != 2 && len(parts) != 3 {
			return nil, fmt.Errorf("route %q must have upstream_url,public_url[,acme_domain]", entry)
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		if parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("route %q must include upstream_url and public_url", entry)
		}
		route := routeValues{
			UpstreamURL: parts[0],
			PublicURL:   parts[1],
		}
		if len(parts) == 3 {
			if parts[2] == "" {
				return nil, fmt.Errorf("route %q has empty acme_domain", entry)
			}
			route.ACMEDomain = parts[2]
		}
		routes = append(routes, route)
	}
	if len(routes) == 0 {
		return nil, errors.New("at least one route is required")
	}
	return routes, nil
}
