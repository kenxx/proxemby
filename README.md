# proxemby

`proxemby` is a small Go edge proxy for Emby servers.

It proxies client traffic to one or more upstream Emby servers and rewrites
`PlaybackInfo` resource URLs so media files can also flow through proxemby.
Routes share one listener and are selected by the request `Host`.

## Getting Started

```sh
PROXEMBY_ROUTE=https://us.emby.com,https://proxemby.example.com \
go run ./cmd/proxemby
```

Or pass routes as command-line flags:

```sh
go run ./cmd/proxemby --route https://us.emby.com,https://proxemby.example.com
```

Then point the Emby client at the route's `public_url`.

## Docker

Release images are published to GitHub Container Registry:

```sh
docker run --rm \
  -p 8080:8080 \
  -e PROXEMBY_ROUTE=https://us.emby.com,https://proxemby.example.com \
  ghcr.io/kenxx/proxemby:latest
```

Or mount a config file:

```sh
docker run --rm \
  -p 8080:8080 \
  -v "$PWD/proxemby.toml:/etc/proxemby/proxemby.toml:ro" \
  ghcr.io/kenxx/proxemby:latest
```

Example Compose service:

```yaml
services:
  proxemby:
    image: ghcr.io/kenxx/proxemby:latest
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      PROXEMBY_ROUTE: "https://us.emby.com,https://proxemby.example.com"
```

## Configuration

Configuration is loaded in this order, with later sources overriding earlier ones:

```text
defaults < TOML config < PROXEMBY_* environment variables < command-line flags
```

By default proxemby reads `/etc/proxemby/proxemby.toml`. If the default config
file does not exist, proxemby continues without it. If `-c` or `--config` points
to a missing or invalid config file, startup fails.

Example TOML config:

```toml
# Required. Add one or more routes.
[[routes]]
# Required. The real Emby server proxemby forwards client traffic to.
upstream_url = "https://us.emby.com"

# Required. The URL Emby clients use to reach proxemby. Incoming requests are
# routed by this host, and rewritten media/resource URLs use this base URL.
public_url = "https://proxemby.example.com"

# Optional. Defaults to the public_url hostname. Used for ACME when TLS is enabled.
acme_domain = "proxemby.example.com"

[[routes]]
upstream_url = "https://us2.emby.com"
public_url = "https://proxemby2.example.com"

[server]
# Optional. Default: ":8080".
http_addr = ":8080"

[tls]
# Optional. Default: false.
enable = false

# Optional. Default: ":443".
addr = ":443"

# Optional. Default: empty.
acme_email = ""

# Optional. Default: ".acme-cache". Relative paths are resolved from the
# proxemby process working directory.
# Use an absolute path in production, for example "/var/lib/proxemby/acme-cache".
acme_cache_dir = ".acme-cache"

[proxy]
# Optional. Default: [].
# Hosts discovered from rewritten PlaybackInfo responses are allowed automatically.
allowed_hosts = ["vod.us.emby.com", "cdn.example.com"]

# Optional. Default: 8388608.
playbackinfo_max_bytes = 8388608

# Optional. Default: false.
hide_client = false

[clients]
# Optional. Default: [].
allowed = ["1.2.3.4", "192.168.0.0/24"]

# Optional. Default: false.
trust_proxy_headers = false

[logging]
# Optional. Default: "info". Values: "debug", "info", "warn", "error".
level = "info"

# Optional. Default: "text". Values: "text", "json".
format = "text"

# Optional. Default: true. Set false to remove the time field from log lines.
time = true

# Optional legacy alias. If true, this is the same as level = "debug".
# If both debug and level are set, level wins.
# debug = false
```

Command-line flags:

| Flag | Description |
| --- | --- |
| `-c`, `--config` | Config file path. |
| `--route` | Route as `upstream_url,public_url[,acme_domain]`; may be repeated and may contain semicolon-separated routes. |
| `-h`, `--http-addr` | HTTP listen address. |
| `-a`, `--allowed-hosts` | Comma-separated initial resource proxy host allowlist. |
| `-d`, `--debug` | Legacy alias for `--log-level debug`. |
| `--tls-enable` | Enable built-in HTTPS with ACME. |
| `--tls-addr` | HTTPS listen address when TLS is enabled. |
| `--acme-email` | ACME account email. |
| `--acme-cache-dir` | ACME certificate cache directory. Relative paths are resolved from the proxemby process working directory. |
| `--playbackinfo-max-bytes` | Maximum PlaybackInfo JSON body size to buffer for URL rewriting. |
| `--allowed-clients` | Comma-separated client IP/CIDR allowlist, for example `1.2.3.4,192.168.0.0/32`. Empty means unrestricted. |
| `--trust-proxy-headers` | Use `X-Forwarded-For`/`X-Real-IP` for client IP checks when proxemby is behind a trusted proxy. |
| `--hide-client` | Do not send `X-Forwarded-*` client/proxy headers to the upstream Emby server. |
| `--log-level` | Log level: `debug`, `info`, `warn`, or `error`. |
| `--log-format` | Log format: `text` or `json`. |
| `--log-time` | Include time in log output. Use `--log-time=false` to disable. |
| `--help` | Show command-line help. |

Environment variables:

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `PROXEMBY_ROUTE` | yes | | Semicolon-separated routes as `upstream_url,public_url[,acme_domain]`. |
| `PROXEMBY_HTTP_ADDR` | no | `:8080` | HTTP listen address. |
| `PROXEMBY_TLS_ENABLE` | no | `false` | Enable built-in HTTPS with ACME. |
| `PROXEMBY_TLS_ADDR` | no | `:443` | HTTPS listen address when TLS is enabled. |
| `PROXEMBY_ACME_EMAIL` | no | | ACME account email. |
| `PROXEMBY_ACME_CACHE_DIR` | no | `.acme-cache` | ACME certificate cache directory. Relative paths are resolved from the proxemby process working directory. |
| `PROXEMBY_ALLOWED_HOSTS` | no | | Comma-separated initial resource proxy host allowlist. |
| `PROXEMBY_PLAYBACKINFO_MAX_BYTES` | no | `8388608` | Maximum PlaybackInfo JSON body size to buffer for URL rewriting. |
| `PROXEMBY_ALLOWED_CLIENTS` | no | | Comma-separated client IP/CIDR allowlist, for example `1.2.3.4,192.168.0.0/32`. Empty means unrestricted. |
| `PROXEMBY_TRUST_PROXY_HEADERS` | no | `false` | Use `X-Forwarded-For`/`X-Real-IP` for client IP checks when proxemby is behind a trusted proxy. |
| `PROXEMBY_HIDE_CLIENT` | no | `false` | Do not send `X-Forwarded-*` client/proxy headers to the upstream Emby server. |
| `PROXEMBY_LOG_LEVEL` | no | `info` | Log level: `debug`, `info`, `warn`, or `error`. |
| `PROXEMBY_LOG_FORMAT` | no | `text` | Log format: `text` or `json`. |
| `PROXEMBY_LOG_TIME` | no | `true` | Include time in log output. |
| `PROXEMBY_DEBUG` | no | `false` | Legacy alias for `PROXEMBY_LOG_LEVEL=debug`. |

## Behavior

- Normal Emby API traffic is routed by request `Host` and reverse proxied to the matching route's `upstream_url`.
- WebSocket upgrade requests use the same reverse proxy path.
- `PlaybackInfo` JSON responses are scanned for absolute `http` or `https` URL strings.
- Rewritten resource URLs use the matching route's `public_url` as `public_url/_proxy/{scheme}/{host}/{path}`.
- `/_proxy/` only allows hosts discovered from that route's rewritten `PlaybackInfo` URLs or explicitly listed in `PROXEMBY_ALLOWED_HOSTS`.
- Media/resource proxying is streamed by Go's reverse proxy; only PlaybackInfo JSON is buffered, with a size limit.
- TLS ACME certificate domains come from each route's `acme_domain`; if omitted, the route's `public_url` hostname is used.
- Client IP allowlisting is disabled by default; set `PROXEMBY_ALLOWED_CLIENTS` to enable it.
- Set `PROXEMBY_HIDE_CLIENT=true` when the upstream should see requests as coming directly from the proxemby server.
- Set `PROXEMBY_LOG_LEVEL=debug` to inspect requests and rule decisions without logging common token query values.

## Development

```sh
go test ./...
```
