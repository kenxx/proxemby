use std::io::Read;
use std::sync::Arc;
use std::time::{Duration, Instant};

use bytes::{Bytes, BytesMut};
use http::header::{self, HeaderValue};
use http::{Method, Request, Response, StatusCode, Uri, Version};
use http_body_util::BodyExt;
use hyper::body::Incoming;
use hyper_util::rt::TokioIo;

use super::auth_gate::{emby_path_segments, is_login_path, is_public_emby_path, match_segments};
use super::client_filter::{client_addr, header_str, is_allowed};
use super::{
    Clients, ConnInfo, ProxyBody, RESOURCE_PREFIX, empty_response, error_chain,
    prepare_outbound_headers, remove_hop_by_hop, request_host, text_response, upgrade_type,
};
use crate::auth::{Session, Store, request_token};
use crate::config::{Config, Route};
use crate::hosts::Registry;
use crate::logging::{Level, Logger};
use crate::rewrite::{RewriteEvent, Rewriter};
use crate::util::{sanitize_request_uri, sanitize_url_string};

const UPSTREAM_LOGOUT_TIMEOUT: Duration = Duration::from_secs(10);

const IDENTITY_HEADERS: &[&str] = &[
    "forwarded",
    "via",
    "x-forwarded-for",
    "x-forwarded-host",
    "x-forwarded-proto",
    "x-forwarded-protocol",
    "x-forwarded-ssl",
    "x-real-ip",
];

pub(crate) struct RouteProxy {
    cfg: Arc<Config>,
    route: Route,
    upstream_display: String,
    route_key: String,
    registry: Arc<Registry>,
    rewriter: Rewriter,
    clients: Arc<Clients>,
    /// `None` when no allowed users are configured.
    auth: Option<Arc<Store>>,
    allowed_users: Vec<String>,
    logger: Logger,
}

enum BodyError {
    TooLarge,
    Read(String),
}

impl RouteProxy {
    pub(crate) fn new(
        cfg: Arc<Config>,
        route: Route,
        clients: Arc<Clients>,
        auth: Option<Arc<Store>>,
        logger: Logger,
    ) -> RouteProxy {
        let registry = Arc::new(Registry::new(&cfg.allowed_hosts));
        let route_key = route.public_url.hostname().to_ascii_lowercase();
        let mut rewriter = Rewriter::new(route.public_url.clone(), registry.clone());
        if let Some(store) = &auth {
            let store = store.clone();
            let key = route_key.clone();
            rewriter = rewriter.with_signer(Arc::new(move |scheme, host| {
                store.sign(&[&key, scheme, host])
            }));
        }
        RouteProxy {
            allowed_users: cfg.allowed_users.iter().map(|u| u.to_lowercase()).collect(),
            upstream_display: route.upstream_url.to_string(),
            cfg,
            route,
            route_key,
            registry,
            rewriter,
            clients,
            auth,
            logger,
        }
    }

    pub(crate) async fn handle(
        self: Arc<Self>,
        req: Request<Incoming>,
        conn: ConnInfo,
    ) -> Response<ProxyBody> {
        if !self.logger.enabled(Level::Debug) {
            return self.handle_checked(req, conn).await;
        }

        let start = Instant::now();
        let method = req.method().to_string();
        let path = sanitize_request_uri(req.uri().path(), req.uri().query());
        let client = client_addr(req.headers(), &conn, self.cfg.trust_proxy_headers)
            .0
            .to_string();
        let target = self.target_for_log(req.uri().path());
        let user_agent = header_str(req.headers(), "user-agent")
            .unwrap_or("")
            .to_owned();

        let resp = self.clone().handle_checked(req, conn).await;
        let status = resp.status().as_u16();
        let logger = self.logger.clone();
        resp.map(move |body| {
            ProxyBody::counted(body, move |bytes| {
                debug!(logger, "request completed",
                    "method" => method,
                    "path" => path,
                    "status" => status,
                    "bytes" => bytes,
                    "duration" => start.elapsed(),
                    "client" => client,
                    "target" => target,
                    "user_agent" => user_agent,
                );
            })
        })
    }

    fn target_for_log(&self, path: &str) -> String {
        let Some(rest) = path.strip_prefix(RESOURCE_PREFIX) else {
            return format!("upstream:{}", self.upstream_display);
        };
        let mut split = rest.split_once('/');
        if let Some((scheme, remainder)) = split {
            if !is_http_scheme(scheme) {
                // Skip the signature segment of signed resource URLs.
                split = remainder.split_once('/');
            }
        }
        match split.and_then(|(scheme, remainder)| {
            remainder.split_once('/').map(|(host, _)| (scheme, host))
        }) {
            Some((scheme, host)) => format!("resource:{scheme}://{host}"),
            None => "resource:invalid".to_owned(),
        }
    }

    async fn handle_checked(
        self: Arc<Self>,
        req: Request<Incoming>,
        conn: ConnInfo,
    ) -> Response<ProxyBody> {
        if !self.cfg.allowed_clients.is_empty() {
            if let Some(resp) = self.filter_client(&req, &conn) {
                return resp;
            }
        }
        if self.auth.is_some() {
            return self.handle_with_auth(req, conn).await;
        }
        self.route_request(req, conn).await
    }

    async fn route_request(
        self: Arc<Self>,
        req: Request<Incoming>,
        conn: ConnInfo,
    ) -> Response<ProxyBody> {
        if req.uri().path().starts_with(RESOURCE_PREFIX) {
            self.proxy_resource(req).await
        } else {
            self.proxy_upstream(req, conn).await
        }
    }

    fn filter_client(
        &self,
        req: &Request<Incoming>,
        conn: &ConnInfo,
    ) -> Option<Response<ProxyBody>> {
        let (addr, source) = client_addr(req.headers(), conn, self.cfg.trust_proxy_headers);
        let allowed = is_allowed(&self.cfg.allowed_clients, addr);
        let level = if allowed { Level::Debug } else { Level::Warn };
        let msg = if allowed {
            "client filter decision"
        } else {
            "client filter rejected request"
        };
        log_at!(self.logger, level, msg,
            "allowed" => allowed,
            "reason" => if allowed { "" } else { "client_ip_not_allowed" },
            "client" => addr.to_string(),
            "source" => source,
            "trust_proxy_headers" => self.cfg.trust_proxy_headers,
            "path" => sanitize_request_uri(req.uri().path(), req.uri().query()),
            "remote" => conn.remote.to_string(),
            "x_forwarded_for" => header_str(req.headers(), "x-forwarded-for").unwrap_or(""),
            "x_real_ip" => header_str(req.headers(), "x-real-ip").unwrap_or(""),
        );
        (!allowed).then(|| text_response(StatusCode::FORBIDDEN, "client IP is not allowed"))
    }

    /// Only lets requests through when they carry an access token that was
    /// issued to an allowed user by a login proxied through this route.
    async fn handle_with_auth(
        self: Arc<Self>,
        req: Request<Incoming>,
        conn: ConnInfo,
    ) -> Response<ProxyBody> {
        let store = self.auth.clone().expect("auth enabled");
        if req.uri().path().starts_with(RESOURCE_PREFIX) {
            // Resource URLs are checked by proxy_resource.
            return self.proxy_resource(req).await;
        }
        let segments = emby_path_segments(req.uri().path());
        if match_segments(&segments, &["users", "public"]) {
            // Do not reveal the upstream user list.
            let mut resp = Response::new(ProxyBody::full(Bytes::from_static(b"[]")));
            resp.headers_mut().insert(
                header::CONTENT_TYPE,
                HeaderValue::from_static("application/json"),
            );
            return resp;
        }
        if is_public_emby_path(req.method(), &segments) {
            return self.proxy_upstream(req, conn).await;
        }
        let token = request_token(req.headers(), req.uri().query());
        let token = match token {
            Some(token) if store.is_valid(&token, &self.route_key) => token,
            other => {
                warn!(self.logger, "auth gate rejected request",
                    "reason" => if other.is_some() { "token_unknown" } else { "token_missing" },
                    "path" => sanitize_request_uri(req.uri().path(), req.uri().query()),
                );
                return text_response(StatusCode::UNAUTHORIZED, "unauthorized");
            }
        };
        let resp = self.clone().proxy_upstream(req, conn).await;
        if match_segments(&segments, &["sessions", "logout"]) {
            if let Err(e) = store.remove(&token) {
                error!(self.logger, "auth session remove failed", "error" => e);
            }
        }
        resp
    }

    async fn proxy_upstream(
        self: Arc<Self>,
        mut req: Request<Incoming>,
        conn: ConnInfo,
    ) -> Response<ProxyBody> {
        let in_path = req.uri().path().to_owned();
        let login = self.auth.is_some() && is_login_path(&in_path);
        let buffer_response = login || is_playback_info_path(&in_path);

        let upstream = &self.route.upstream_url;
        let mut target =
            String::with_capacity(upstream.scheme.len() + upstream.host.len() + in_path.len() + 64);
        target.push_str(&upstream.scheme);
        target.push_str("://");
        target.push_str(&upstream.host);
        target.push_str(&join_path(&upstream.path, &in_path));
        let query = join_query(&upstream.query, req.uri().query().unwrap_or(""));
        if !query.is_empty() {
            target.push('?');
            target.push_str(&query);
        }
        let uri: Uri = match target.parse() {
            Ok(uri) => uri,
            Err(e) => {
                error!(self.logger, &format!("http: proxy error: {e}"));
                return empty_response(StatusCode::BAD_GATEWAY);
            }
        };

        let request_upgrade = upgrade_type(req.headers());
        let client_upgrade = request_upgrade
            .as_ref()
            .map(|_| hyper::upgrade::on(&mut req));
        let in_host = request_host(&req).to_owned();
        let (parts, body) = req.into_parts();
        let mut headers = parts.headers;
        prepare_outbound_headers(&mut headers, request_upgrade.as_ref());
        if self.cfg.hide_client {
            debug!(self.logger, "upstream request hides client identity headers",
                "path" => sanitize_request_uri(&in_path, parts.uri.query()),
                "target" => self.upstream_display.as_str(),
            );
            for name in IDENTITY_HEADERS {
                headers.remove(*name);
            }
        } else {
            let ip = conn.remote.ip().to_string();
            if let Ok(v) = HeaderValue::from_str(&ip) {
                headers.insert("x-forwarded-for", v);
            }
            if let Ok(v) = HeaderValue::from_str(&in_host) {
                headers.insert("x-forwarded-host", v);
            }
            headers.insert(
                "x-forwarded-proto",
                HeaderValue::from_static(if conn.tls { "https" } else { "http" }),
            );
        }
        if buffer_response {
            headers.remove(header::ACCEPT_ENCODING);
        }

        let mut out = Request::new(ProxyBody::Incoming(body));
        *out.method_mut() = parts.method;
        *out.uri_mut() = uri;
        *out.version_mut() = Version::HTTP_11;
        *out.headers_mut() = headers;
        let out_path = out.uri().path().to_owned();

        let client = if request_upgrade.is_some() {
            &self.clients.http1
        } else {
            &self.clients.default
        };
        let mut resp = match client.request(out).await {
            Ok(resp) => resp,
            Err(e) => {
                error!(
                    self.logger,
                    &format!("http: proxy error: {}", error_chain(&e))
                );
                return empty_response(StatusCode::BAD_GATEWAY);
            }
        };

        if resp.status() == StatusCode::SWITCHING_PROTOCOLS {
            return self.handle_upgrade_response(resp, request_upgrade, client_upgrade);
        }

        remove_hop_by_hop(resp.headers_mut());
        if login {
            return self.handle_login_response(resp, &out_path).await;
        }
        if buffer_response && is_json_content_type(resp.headers()) {
            return self
                .rewrite_playback_info(resp, &in_path, parts.uri.query())
                .await;
        }
        resp.map(ProxyBody::Incoming)
    }

    fn handle_upgrade_response(
        &self,
        mut resp: Response<Incoming>,
        request_upgrade: Option<HeaderValue>,
        client_upgrade: Option<hyper::upgrade::OnUpgrade>,
    ) -> Response<ProxyBody> {
        let response_upgrade = upgrade_type(resp.headers());
        let (Some(requested), Some(client_upgrade)) = (request_upgrade, client_upgrade) else {
            error!(
                self.logger,
                "http: proxy error: backend tried to switch protocol when no upgrade was requested"
            );
            return empty_response(StatusCode::BAD_GATEWAY);
        };
        if !response_upgrade
            .is_some_and(|r| r.as_bytes().eq_ignore_ascii_case(requested.as_bytes()))
        {
            error!(
                self.logger,
                "http: proxy error: backend tried to switch to a different protocol than requested"
            );
            return empty_response(StatusCode::BAD_GATEWAY);
        }
        let upstream_upgrade = hyper::upgrade::on(&mut resp);
        let logger = self.logger.clone();
        tokio::spawn(async move {
            let (client, upstream) = match tokio::try_join!(client_upgrade, upstream_upgrade) {
                Ok(pair) => pair,
                Err(e) => {
                    error!(
                        logger,
                        &format!("http: proxy error: upgrade failed: {}", error_chain(&e))
                    );
                    return;
                }
            };
            let mut client = TokioIo::new(client);
            let mut upstream = TokioIo::new(upstream);
            let _ = tokio::io::copy_bidirectional(&mut client, &mut upstream).await;
        });
        let (parts, _) = resp.into_parts();
        Response::from_parts(parts, ProxyBody::Empty)
    }

    async fn rewrite_playback_info(
        &self,
        resp: Response<Incoming>,
        in_path: &str,
        query: Option<&str>,
    ) -> Response<ProxyBody> {
        let (mut parts, body) = resp.into_parts();
        let body = match self.read_body(body, &parts.headers).await {
            Ok(body) => body,
            Err(BodyError::TooLarge) => {
                warn!(self.logger, "playbackinfo response rejected",
                    "reason" => "response_too_large",
                    "max_bytes" => self.cfg.playbackinfo_max_bytes,
                    "path" => sanitize_request_uri(in_path, query),
                );
                error!(
                    self.logger,
                    "http: proxy error: response body exceeds limit"
                );
                return empty_response(StatusCode::BAD_GATEWAY);
            }
            Err(BodyError::Read(e)) => {
                error!(self.logger, &format!("http: proxy error: {e}"));
                return empty_response(StatusCode::BAD_GATEWAY);
            }
        };
        let (rewritten, events) = self.rewriter.rewrite_playback_info(&body);
        self.log_rewrite_events(in_path, query, &events);
        let body = rewritten.map(Bytes::from).unwrap_or(body);
        set_full_body(&mut parts.headers, body.len());
        Response::from_parts(parts, ProxyBody::full(body))
    }

    fn log_rewrite_events(&self, path: &str, query: Option<&str>, events: &[RewriteEvent]) {
        if !self.logger.enabled(Level::Debug) {
            return;
        }
        debug!(self.logger, "playbackinfo rewrite", "path" => sanitize_request_uri(path, query), "count" => events.len());
        for event in events {
            debug!(self.logger, "playbackinfo rewrite item",
                "json_path" => event.path.as_str(),
                "scheme" => event.scheme.as_str(),
                "host" => event.host.as_str(),
                "from" => sanitize_url_string(&event.original),
                "to" => sanitize_url_string(&event.rewritten),
            );
        }
    }

    /// Reads a response body up to the PlaybackInfo size limit, decoding gzip.
    async fn read_body(
        &self,
        mut body: Incoming,
        headers: &http::HeaderMap,
    ) -> Result<Bytes, BodyError> {
        let max = self.cfg.playbackinfo_max_bytes.max(1) as usize;
        let mut buf = BytesMut::new();
        while let Some(frame) = body.frame().await {
            let frame = frame.map_err(|e| BodyError::Read(error_chain(&e)))?;
            if let Some(data) = frame.data_ref() {
                if buf.len() + data.len() > max {
                    return Err(BodyError::TooLarge);
                }
                buf.extend_from_slice(data);
            }
        }
        let gzip = headers
            .get(header::CONTENT_ENCODING)
            .is_some_and(|v| v.as_bytes().eq_ignore_ascii_case(b"gzip"));
        if !gzip {
            return Ok(buf.freeze());
        }
        let mut decoded = Vec::new();
        let read = flate2::read::GzDecoder::new(&buf[..])
            .take(max as u64 + 1)
            .read_to_end(&mut decoded)
            .map_err(|e| BodyError::Read(e.to_string()))?;
        if read > max {
            return Err(BodyError::TooLarge);
        }
        Ok(Bytes::from(decoded))
    }

    /// Only hands the access token back to the client when the upstream login
    /// belongs to an allowed user.
    async fn handle_login_response(
        self: Arc<Self>,
        resp: Response<Incoming>,
        out_path: &str,
    ) -> Response<ProxyBody> {
        let store = self.auth.clone().expect("auth enabled");
        if resp.status() != StatusCode::OK {
            return resp.map(ProxyBody::Incoming);
        }
        let (mut parts, body) = resp.into_parts();
        let body = match self.read_body(body, &parts.headers).await {
            Ok(body) => body,
            Err(e) => {
                let msg = match e {
                    BodyError::TooLarge => "response body exceeds limit".to_owned(),
                    BodyError::Read(e) => e,
                };
                error!(self.logger, &format!("http: proxy error: {msg}"));
                return empty_response(StatusCode::BAD_GATEWAY);
            }
        };
        let json: serde_json::Value = serde_json::from_slice(&body).unwrap_or_default();
        let token = json
            .get("AccessToken")
            .and_then(|v| v.as_str())
            .unwrap_or("");
        let user = json.get("User");
        let user_name = user
            .and_then(|u| u.get("Name"))
            .and_then(|v| v.as_str())
            .unwrap_or("");
        let user_id = user
            .and_then(|u| u.get("Id"))
            .and_then(|v| v.as_str())
            .unwrap_or("");

        if !token.is_empty() && self.allowed_users.contains(&user_name.to_lowercase()) {
            if let Err(e) = store.add(token, Session::new(&self.route_key, user_id, user_name)) {
                error!(self.logger, &format!("http: proxy error: {e}"));
                return empty_response(StatusCode::BAD_GATEWAY);
            }
            info!(self.logger, "login accepted", "user" => user_name);
            set_full_body(&mut parts.headers, body.len());
            return Response::from_parts(parts, ProxyBody::full(body));
        }

        warn!(self.logger, "login rejected", "reason" => "user_not_allowed", "user" => user_name);
        if !token.is_empty() {
            let this = self.clone();
            let token = token.to_owned();
            let out_path = out_path.to_owned();
            tokio::spawn(async move { this.logout_upstream(&out_path, &token).await });
        }
        text_response(StatusCode::UNAUTHORIZED, "user is not allowed")
    }

    /// Revokes a token for a user that is not allowed so the upstream session
    /// does not linger.
    async fn logout_upstream(&self, login_path: &str, token: &str) {
        let segments: Vec<&str> = login_path.split('/').collect();
        let prefix = segments
            .iter()
            .rposition(|s| s.eq_ignore_ascii_case("users"))
            .map(|i| segments[..i].join("/"))
            .unwrap_or_else(|| login_path.to_owned());
        let upstream = &self.route.upstream_url;
        let target = format!(
            "{}://{}{}/Sessions/Logout",
            upstream.scheme, upstream.host, prefix
        );
        let Ok(uri) = target.parse::<Uri>() else {
            return;
        };
        let mut req = Request::new(ProxyBody::Empty);
        *req.method_mut() = Method::POST;
        *req.uri_mut() = uri;
        if let Ok(v) = HeaderValue::from_str(token) {
            req.headers_mut().insert("x-emby-token", v);
        }
        match tokio::time::timeout(UPSTREAM_LOGOUT_TIMEOUT, self.clients.default.request(req)).await
        {
            Ok(Ok(_)) => {}
            Ok(Err(e)) => warn!(self.logger, "upstream logout failed", "error" => error_chain(&e)),
            Err(_) => warn!(self.logger, "upstream logout failed", "error" => "timeout"),
        }
    }

    async fn proxy_resource(&self, req: Request<Incoming>) -> Response<ProxyBody> {
        let path = req.uri().path();
        let rest = &path[RESOURCE_PREFIX.len()..];
        let (first, mut remainder) = split_first_segment(rest);
        let mut scheme = first;
        if self.auth.is_some() && !is_http_scheme(first) {
            // Signed URLs carry the signature in front of the scheme.
            if let Some(rest) = remainder {
                (scheme, remainder) = split_first_segment(rest);
            }
        }
        let Some(remainder) = remainder.filter(|_| is_http_scheme(scheme)) else {
            self.log_resource_decision(&req, false, "invalid_scheme", scheme, "");
            return text_response(
                StatusCode::BAD_REQUEST,
                "missing or invalid proxied resource scheme",
            );
        };
        let (host, rest) = match remainder.split_once('/') {
            Some((host, rest)) if !host.is_empty() => (host, rest),
            _ => {
                let host = remainder.split('/').next().unwrap_or("");
                self.log_resource_decision(&req, false, "missing_host", scheme, host);
                return text_response(StatusCode::BAD_REQUEST, "missing proxied resource host");
            }
        };
        if let Some(store) = &self.auth {
            let reason = if is_http_scheme(first) {
                let token = request_token(req.headers(), req.uri().query()).unwrap_or_default();
                (!store.is_valid(&token, &self.route_key)).then_some("unsigned_without_token")
            } else {
                (!store.verify(first, &[&self.route_key, scheme, host]))
                    .then_some("invalid_signature")
            };
            if let Some(reason) = reason {
                self.log_resource_decision(&req, false, reason, scheme, host);
                return text_response(StatusCode::FORBIDDEN, "proxied resource is not authorized");
            }
        }
        if self.registry.lookup(host).is_none() {
            self.log_resource_decision(&req, false, "host_not_allowed", scheme, host);
            return text_response(
                StatusCode::FORBIDDEN,
                "proxied resource host is not allowed",
            );
        }
        self.log_resource_decision(&req, true, "", scheme, host);

        let mut target = format!("{scheme}://{host}/{rest}");
        if let Some(query) = req.uri().query() {
            target.push('?');
            target.push_str(query);
        }
        let uri: Uri = match target.parse() {
            Ok(uri) => uri,
            Err(_) => {
                return text_response(StatusCode::BAD_REQUEST, "invalid proxied resource URL");
            }
        };

        let (parts, body) = req.into_parts();
        let mut headers = parts.headers;
        prepare_outbound_headers(&mut headers, None);
        let mut out = Request::new(ProxyBody::Incoming(body));
        *out.method_mut() = parts.method;
        *out.uri_mut() = uri;
        *out.version_mut() = Version::HTTP_11;
        *out.headers_mut() = headers;

        match self.clients.default.request(out).await {
            Ok(mut resp) => {
                remove_hop_by_hop(resp.headers_mut());
                resp.map(ProxyBody::Incoming)
            }
            Err(e) => {
                error!(
                    self.logger,
                    &format!("http: proxy error: {}", error_chain(&e))
                );
                empty_response(StatusCode::BAD_GATEWAY)
            }
        }
    }

    fn log_resource_decision(
        &self,
        req: &Request<Incoming>,
        allowed: bool,
        reason: &str,
        scheme: &str,
        host: &str,
    ) {
        let level = if allowed { Level::Debug } else { Level::Warn };
        let msg = if allowed {
            "resource proxy decision"
        } else {
            "resource proxy rejected request"
        };
        log_at!(self.logger, level, msg,
            "allowed" => allowed,
            "reason" => reason,
            "scheme" => scheme,
            "host" => host,
            "path" => sanitize_request_uri(req.uri().path(), req.uri().query()),
        );
    }
}

fn split_first_segment(path: &str) -> (&str, Option<&str>) {
    match path.split_once('/') {
        Some((first, rest)) => (first, Some(rest)),
        None => (path, None),
    }
}

fn is_http_scheme(scheme: &str) -> bool {
    scheme == "http" || scheme == "https"
}

fn is_playback_info_path(path: &str) -> bool {
    path.split('/')
        .any(|part| part.eq_ignore_ascii_case("PlaybackInfo"))
}

fn is_json_content_type(headers: &http::HeaderMap) -> bool {
    headers
        .get(header::CONTENT_TYPE)
        .and_then(|v| v.to_str().ok())
        .is_some_and(|ct| {
            let ct = ct.to_ascii_lowercase();
            ct.contains("application/json") || ct.contains("+json")
        })
}

fn set_full_body(headers: &mut http::HeaderMap, len: usize) {
    headers.insert(header::CONTENT_LENGTH, HeaderValue::from(len));
    headers.remove(header::CONTENT_ENCODING);
}

/// Joins the upstream base path and the request path with a single slash,
/// like Go's `httputil` `singleJoiningSlash`.
fn join_path(base: &str, path: &str) -> String {
    match (base.ends_with('/'), path.starts_with('/')) {
        (true, true) => format!("{base}{}", &path[1..]),
        (false, false) => format!("{base}/{path}"),
        _ => format!("{base}{path}"),
    }
}

fn join_query(base: &str, query: &str) -> String {
    match (base.is_empty(), query.is_empty()) {
        (true, _) => query.to_owned(),
        (_, true) => base.to_owned(),
        _ => format!("{base}&{query}"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn joins_like_go() {
        assert_eq!(join_path("", "/emby/Items"), "/emby/Items");
        assert_eq!(join_path("/base/", "/emby"), "/base/emby");
        assert_eq!(join_path("/base", "emby"), "/base/emby");
        assert_eq!(join_query("", "a=1"), "a=1");
        assert_eq!(join_query("k=v", "a=1"), "k=v&a=1");
        assert!(is_playback_info_path("/emby/Items/1/playbackinfo"));
    }
}
