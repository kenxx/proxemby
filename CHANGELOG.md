# proxemby

## 0.4.0

### Minor Changes

- 5ed5b6a: Add optional traffic statistics per route and kind (API, media, images), enabled with `[stats] enable = true`. Run `proxemby stats` for today's, this month's and since-start traffic, or read `/stats` (JSON) and `/metrics` (Prometheus) from the admin listener on `127.0.0.1:9180`. Set `[stats] file` to keep daily totals across restarts.

## 0.3.0

### Minor Changes

- 9c516fd: Make proxemby easier to operate:
  
  - Add `--version` and log the version at startup.
  - Add `response_header_timeout` (default 60 seconds): requests to an upstream that sends no response headers in time get `504` instead of hanging. Streaming bodies are not limited.
  - Shut down gracefully on `SIGTERM` and `SIGINT`, giving open requests up to 10 seconds to finish.
  - Upgrading the Debian package now reloads systemd and restarts a running service.

## 0.2.0

### Minor Changes

- c8ddbee: Rewrite proxemby in Rust for lower latency, lower memory use and higher throughput. Existing configuration keeps working. The first start requests a new TLS certificate because the ACME cache format changed, and certificates are now validated with TLS-ALPN-01 only, so port 443 must be reachable.
  
  Add the `allowed_users` option, which restricts the proxy to logins by the listed upstream Emby users and signs proxied media URLs.
