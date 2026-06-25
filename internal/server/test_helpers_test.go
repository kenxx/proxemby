package server

import (
	"io"
	"log/slog"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"proxemby/internal/config"
	"proxemby/internal/logging"
)

func testConfig(upstreamURL, publicURL *url.URL) config.Config {
	return config.Config{
		Routes: []config.Route{{
			UpstreamURL: upstreamURL,
			PublicURL:   publicURL,
		}},
	}
}

func testLogger(t *testing.T, w io.Writer, level slog.Level, format string, logTime bool) *slog.Logger {
	t.Helper()
	logger, err := logging.NewLogger(logging.Config{
		Level:  level,
		Format: format,
		Time:   logTime,
	}, w)
	if err != nil {
		t.Fatal(err)
	}
	return logger
}

func parseClientPrefixes(raw string) ([]netip.Prefix, error) {
	parts := strings.Split(raw, ",")
	prefixes := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		prefix, err := parseClientPrefix(part)
		if err != nil {
			return nil, err
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
