---
"proxemby": minor
---

Rewrite proxemby in Rust for lower latency, lower memory use and higher throughput. Existing configuration keeps working. The first start requests a new TLS certificate because the ACME cache format changed, and certificates are now validated with TLS-ALPN-01 only, so port 443 must be reachable.

Add the `allowed_users` option, which restricts the proxy to logins by the listed upstream Emby users and signs proxied media URLs.
