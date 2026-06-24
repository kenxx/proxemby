# proxemby

`proxemby` is a small Go edge proxy for Emby servers.

It proxies client traffic to one upstream Emby server and rewrites `PlaybackInfo`
resource URLs so media files can also flow through proxemby.

## Getting Started

```sh
PROXEMBY_UPSTREAM_URL=https://us.emby.com \
PROXEMBY_PUBLIC_URL=http://proxemby \
go run ./cmd/proxemby
```

Or pass the required URLs as command-line flags:

```sh
go run ./cmd/proxemby -u https://us.emby.com -p http://proxemby
```

Then point the Emby client at `PROXEMBY_PUBLIC_URL`.

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
[upstream]
url = "https://us.emby.com"

[public]
url = "http://proxemby"

[server]
http_addr = ":8080"

[tls]
enable = false
addr = ":443"
acme_domains = ["proxemby.example.com"]
acme_email = ""
acme_cache_dir = ".acme-cache"

[proxy]
allowed_hosts = ["vod.us.emby.com", "cdn.example.com"]
playbackinfo_max_bytes = 8388608
hide_client = false

[clients]
allowed = ["1.2.3.4", "192.168.0.0/24"]
trust_proxy_headers = false

[logging]
debug = false
```

Command-line flags:

| Flag | Description |
| --- | --- |
| `-c`, `--config` | Config file path. |
| `-u`, `--upstream-url` | Upstream Emby server, for example `https://us.emby.com`. |
| `-p`, `--public-url` | Public URL clients use to reach proxemby. |
| `-h`, `--http-addr` | HTTP listen address. |
| `-a`, `--allowed-hosts` | Comma-separated initial resource proxy host allowlist. |
| `-d`, `--debug` | Log request method, sanitized path/query, status, bytes, duration, client IP, and target. |
| `--tls-enable` | Enable built-in HTTPS with ACME. |
| `--tls-addr` | HTTPS listen address when TLS is enabled. |
| `--acme-domains` | Comma-separated ACME certificate domains. |
| `--acme-email` | ACME account email. |
| `--acme-cache-dir` | ACME certificate cache directory. |
| `--playbackinfo-max-bytes` | Maximum PlaybackInfo JSON body size to buffer for URL rewriting. |
| `--allowed-clients` | Comma-separated client IP/CIDR allowlist, for example `1.2.3.4,192.168.0.0/32`. Empty means unrestricted. |
| `--trust-proxy-headers` | Use `X-Forwarded-For`/`X-Real-IP` for client IP checks when proxemby is behind a trusted proxy. |
| `--hide-client` | Do not send `X-Forwarded-*` client/proxy headers to the upstream Emby server. |
| `--help` | Show command-line help. |

Environment variables:

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `PROXEMBY_UPSTREAM_URL` | yes | | Upstream Emby server, for example `https://us.emby.com`. |
| `PROXEMBY_PUBLIC_URL` | yes | | Public URL clients use to reach proxemby. |
| `PROXEMBY_HTTP_ADDR` | no | `:8080` | HTTP listen address. |
| `PROXEMBY_TLS_ENABLE` | no | `false` | Enable built-in HTTPS with ACME. |
| `PROXEMBY_TLS_ADDR` | no | `:443` | HTTPS listen address when TLS is enabled. |
| `PROXEMBY_ACME_DOMAINS` | with TLS | | Comma-separated ACME certificate domains. |
| `PROXEMBY_ACME_EMAIL` | no | | ACME account email. |
| `PROXEMBY_ACME_CACHE_DIR` | no | `.acme-cache` | ACME certificate cache directory. |
| `PROXEMBY_ALLOWED_HOSTS` | no | | Comma-separated initial resource proxy host allowlist. |
| `PROXEMBY_PLAYBACKINFO_MAX_BYTES` | no | `8388608` | Maximum PlaybackInfo JSON body size to buffer for URL rewriting. |
| `PROXEMBY_ALLOWED_CLIENTS` | no | | Comma-separated client IP/CIDR allowlist, for example `1.2.3.4,192.168.0.0/32`. Empty means unrestricted. |
| `PROXEMBY_TRUST_PROXY_HEADERS` | no | `false` | Use `X-Forwarded-For`/`X-Real-IP` for client IP checks when proxemby is behind a trusted proxy. |
| `PROXEMBY_HIDE_CLIENT` | no | `false` | Do not send `X-Forwarded-*` client/proxy headers to the upstream Emby server. |
| `PROXEMBY_DEBUG` | no | `false` | Log request method, sanitized path/query, status, bytes, duration, client IP, and target. |

## Behavior

- Normal Emby API traffic is reverse proxied to `PROXEMBY_UPSTREAM_URL`.
- WebSocket upgrade requests use the same reverse proxy path.
- `PlaybackInfo` JSON responses are scanned for absolute `http` or `https` URL strings.
- Rewritten resource URLs use `PROXEMBY_PUBLIC_URL/_proxy/{scheme}/{host}/{path}`.
- `/_proxy/` only allows hosts discovered from rewritten `PlaybackInfo` URLs or explicitly listed in `PROXEMBY_ALLOWED_HOSTS`.
- Media/resource proxying is streamed by Go's reverse proxy; only PlaybackInfo JSON is buffered, with a size limit.
- Client IP allowlisting is disabled by default; set `PROXEMBY_ALLOWED_CLIENTS` to enable it.
- Set `PROXEMBY_HIDE_CLIENT=true` when the upstream should see requests as coming directly from the proxemby server.
- Set `PROXEMBY_DEBUG=true` to inspect requests without logging common token query values.

## Development

```sh
go test ./...
```
