package config

import (
	"fmt"
	"io"
)

func WriteConfigUsage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  proxemby [options]

Options:
  -c, --config PATH                  Config file path (default /etc/proxemby/proxemby.toml)
      --route ROUTE                  Route as upstream_url,public_url[,acme_domain]; may be repeated
  -h, --http-addr ADDR               HTTP listen address
  -a, --allowed-hosts HOSTS          Comma-separated resource proxy host allowlist
  -d, --debug                        Enable debug logging (same as --log-level debug)
      --tls-enable                   Enable built-in HTTPS with ACME
      --tls-addr ADDR                HTTPS listen address
      --acme-email EMAIL             ACME account email
      --acme-cache-dir DIR           ACME certificate cache directory
      --playbackinfo-max-bytes N     Maximum PlaybackInfo JSON body size
      --allowed-clients CLIENTS      Comma-separated client IP/CIDR allowlist
      --trust-proxy-headers          Trust X-Forwarded-For/X-Real-IP for client checks
      --hide-client                  Hide client identity headers from upstream
      --log-level LEVEL              Log level: debug, info, warn, or error
      --log-format FORMAT            Log format: text or json
      --log-time                     Include time in log output
      --help                         Show this help
`)
}
