//! The HTTP side of proxemby: host routing, connection serving and the shared
//! upstream clients.

mod auth_gate;
mod body;
mod client_filter;
mod route;
mod shutdown;
mod tls;

use std::collections::HashMap;
use std::convert::Infallible;
use std::net::SocketAddr;
use std::sync::Arc;
use std::time::Duration;

use http::header::{self, HeaderMap, HeaderName, HeaderValue};
use http::{Request, Response, StatusCode};
use hyper::body::Incoming;
use hyper::service::service_fn;
use hyper_rustls::HttpsConnector;
use hyper_util::client::legacy::Client;
use hyper_util::client::legacy::connect::HttpConnector;
use hyper_util::rt::{TokioExecutor, TokioIo, TokioTimer};
use hyper_util::server::conn::auto;
use tokio::io::{AsyncRead, AsyncWrite};
use tokio::net::{TcpListener, TcpStream};

pub use body::ProxyBody;
pub use shutdown::{Shutdown, ShutdownSignal};
pub use tls::{client_config, serve_tls, server_config};

use crate::auth::Store;
use crate::config::Config;
use crate::logging::Logger;
use crate::util::{hostname, sanitize_request_uri, unmap};
use route::RouteProxy;

pub(crate) const RESOURCE_PREFIX: &str = "/_proxy/";

type HttpClient = Client<HttpsConnector<HttpConnector>, ProxyBody>;

/// Upstream HTTP clients shared by every route so connections are pooled.
pub(crate) struct Clients {
    /// Negotiates HTTP/2 or HTTP/1.1 via ALPN.
    pub default: HttpClient,
    /// HTTP/1.1 only, for protocol upgrades such as WebSocket.
    pub http1: HttpClient,
}

impl Clients {
    fn new() -> Clients {
        let _ = rustls::crypto::ring::default_provider().install_default();
        Clients {
            default: build_client(true),
            http1: build_client(false),
        }
    }
}

fn build_client(http2: bool) -> HttpClient {
    let mut http = HttpConnector::new();
    http.enforce_http(false);
    http.set_nodelay(true);
    http.set_keepalive(Some(Duration::from_secs(60)));
    http.set_connect_timeout(Some(Duration::from_secs(30)));

    let builder = hyper_rustls::HttpsConnectorBuilder::new()
        .with_tls_config(client_config())
        .https_or_http()
        .enable_http1();
    let connector = if http2 {
        builder.enable_http2().wrap_connector(http)
    } else {
        builder.wrap_connector(http)
    };

    Client::builder(TokioExecutor::new())
        .pool_timer(TokioTimer::new())
        .timer(TokioTimer::new())
        .pool_idle_timeout(Duration::from_secs(90))
        .http2_adaptive_window(true)
        .build(connector)
}

/// Information about the client connection a request arrived on.
#[derive(Clone, Copy, Debug)]
pub struct ConnInfo {
    pub remote: SocketAddr,
    pub tls: bool,
}

pub struct Server {
    routes: HashMap<String, Arc<RouteProxy>>,
    logger: Logger,
}

impl Server {
    /// Builds the server. `store` keeps sessions for the allowed-users check
    /// and is ignored when `cfg.allowed_users` is empty.
    pub fn new(cfg: &Config, logger: Logger, store: Option<Arc<Store>>) -> Arc<Server> {
        let store = if cfg.allowed_users.is_empty() {
            None
        } else {
            Some(store.unwrap_or_else(|| Arc::new(Store::memory())))
        };
        let cfg = Arc::new(cfg.clone());
        let clients = Arc::new(Clients::new());
        let routes = cfg
            .routes
            .iter()
            .map(|route| {
                let route_logger = logger.with(vec![
                    ("route", route.public_url.hostname().into()),
                    ("public_url", route.public_url.to_string().into()),
                    ("upstream_url", route.upstream_url.to_string().into()),
                ]);
                let proxy = RouteProxy::new(
                    cfg.clone(),
                    route.clone(),
                    clients.clone(),
                    store.clone(),
                    route_logger,
                );
                (
                    route.public_url.hostname().to_ascii_lowercase(),
                    Arc::new(proxy),
                )
            })
            .collect();
        Arc::new(Server { routes, logger })
    }

    pub async fn handle(&self, req: Request<Incoming>, conn: ConnInfo) -> Response<ProxyBody> {
        let host = request_host(&req);
        let key = hostname(host).to_ascii_lowercase();
        match self.routes.get(&key) {
            Some(route) => route.clone().handle(req, conn).await,
            None => {
                debug!(self.logger, "route miss", "host" => host, "path" => sanitize_request_uri(req.uri().path(), req.uri().query()));
                text_response(StatusCode::NOT_FOUND, "404 page not found")
            }
        }
    }
}

/// Returns the request host like Go's `Request.Host`: the URI authority for
/// HTTP/2 or absolute-form requests, otherwise the `Host` header.
pub(crate) fn request_host<B>(req: &Request<B>) -> &str {
    if let Some(authority) = req.uri().authority() {
        return authority.as_str();
    }
    req.headers()
        .get(header::HOST)
        .and_then(|h| h.to_str().ok())
        .unwrap_or("")
}

/// Serves one client connection (HTTP/1.1 with upgrades, or HTTP/2).
///
/// When `signal` fires, in-flight requests finish and the connection closes:
/// HTTP/1.1 stops keep-alive and HTTP/2 sends GOAWAY.
pub async fn serve_connection<IO>(
    io: IO,
    server: Arc<Server>,
    conn: ConnInfo,
    signal: ShutdownSignal,
) where
    IO: AsyncRead + AsyncWrite + Unpin + Send + 'static,
{
    let service = service_fn(move |req| {
        let server = server.clone();
        async move { Ok::<_, Infallible>(server.handle(req, conn).await) }
    });
    let mut builder = auto::Builder::new(TokioExecutor::new());
    builder.http1().timer(TokioTimer::new()).keep_alive(true);
    builder
        .http2()
        .timer(TokioTimer::new())
        .adaptive_window(true);
    let connection = builder.serve_connection_with_upgrades(TokioIo::new(io), service);
    tokio::pin!(connection);
    tokio::select! {
        _ = connection.as_mut() => return,
        _ = signal.triggered() => {}
    }
    connection.as_mut().graceful_shutdown();
    let _ = connection.await;
}

/// Accepts plain HTTP connections until `signal` fires.
pub async fn serve_http(
    listener: TcpListener,
    server: Arc<Server>,
    signal: ShutdownSignal,
) -> std::io::Result<()> {
    while let Some((stream, remote)) = accept(&listener, &signal).await? {
        let conn = prepare_stream(&stream, remote, false);
        tokio::spawn(serve_connection(
            stream,
            server.clone(),
            conn,
            signal.clone(),
        ));
    }
    Ok(())
}

/// Waits for the next connection, or returns `None` once shutdown starts.
pub(crate) async fn accept(
    listener: &TcpListener,
    signal: &ShutdownSignal,
) -> std::io::Result<Option<(TcpStream, SocketAddr)>> {
    loop {
        tokio::select! {
            biased;
            _ = signal.triggered() => return Ok(None),
            accepted = listener.accept() => match accepted {
                Ok(accepted) => return Ok(Some(accepted)),
                Err(e) if is_transient_accept_error(&e) => {
                    tokio::time::sleep(Duration::from_millis(5)).await;
                }
                Err(e) => return Err(e),
            },
        }
    }
}

pub fn prepare_stream(stream: &TcpStream, remote: SocketAddr, tls: bool) -> ConnInfo {
    let _ = stream.set_nodelay(true);
    ConnInfo {
        remote: SocketAddr::new(unmap(remote.ip()), remote.port()),
        tls,
    }
}

fn is_transient_accept_error(e: &std::io::Error) -> bool {
    use std::io::ErrorKind::*;
    matches!(
        e.kind(),
        ConnectionAborted | ConnectionReset | Interrupted | WouldBlock
    ) || e
        .raw_os_error()
        .is_some_and(|code| code == 24 || code == 23)
}

/// Binds a Go-style listen address such as `:8080` (all interfaces).
pub async fn bind(addr: &str) -> std::io::Result<TcpListener> {
    if let Some(port) = addr.strip_prefix(':') {
        return match TcpListener::bind(format!("[::]:{port}")).await {
            Ok(listener) => Ok(listener),
            Err(_) => TcpListener::bind(format!("0.0.0.0:{port}")).await,
        };
    }
    TcpListener::bind(addr).await
}

/// A plain-text error response like Go's `http.Error`.
pub(crate) fn text_response(status: StatusCode, msg: &str) -> Response<ProxyBody> {
    let mut resp = Response::new(ProxyBody::full(format!("{msg}\n")));
    *resp.status_mut() = status;
    let headers = resp.headers_mut();
    headers.insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("text/plain; charset=utf-8"),
    );
    headers.insert(
        header::X_CONTENT_TYPE_OPTIONS,
        HeaderValue::from_static("nosniff"),
    );
    resp
}

pub(crate) fn empty_response(status: StatusCode) -> Response<ProxyBody> {
    let mut resp = Response::new(ProxyBody::Empty);
    *resp.status_mut() = status;
    resp
}

const HOP_BY_HOP: &[&str] = &[
    "connection",
    "proxy-connection",
    "keep-alive",
    "proxy-authenticate",
    "proxy-authorization",
    "te",
    "trailer",
    "transfer-encoding",
    "upgrade",
];

fn header_values_contain_token(headers: &HeaderMap, name: &HeaderName, token: &str) -> bool {
    headers
        .get_all(name)
        .iter()
        .filter_map(|v| v.to_str().ok())
        .flat_map(|v| v.split(','))
        .any(|t| t.trim().eq_ignore_ascii_case(token))
}

/// Returns the requested upgrade protocol, if any.
pub(crate) fn upgrade_type(headers: &HeaderMap) -> Option<HeaderValue> {
    if !header_values_contain_token(headers, &header::CONNECTION, "upgrade") {
        return None;
    }
    headers.get(header::UPGRADE).cloned()
}

/// Removes hop-by-hop headers, including the ones named in `Connection`.
pub(crate) fn remove_hop_by_hop(headers: &mut HeaderMap) {
    let named: Vec<HeaderName> = headers
        .get_all(header::CONNECTION)
        .iter()
        .filter_map(|v| v.to_str().ok())
        .flat_map(|v| v.split(','))
        .filter_map(|t| HeaderName::from_bytes(t.trim().as_bytes()).ok())
        .collect();
    for name in named {
        headers.remove(name);
    }
    for name in HOP_BY_HOP {
        headers.remove(*name);
    }
}

/// Prepares client request headers for forwarding like Go's
/// `httputil.ReverseProxy`: drops hop-by-hop and forwarding headers, keeps
/// `Te: trailers` and re-adds upgrade headers.
pub(crate) fn prepare_outbound_headers(headers: &mut HeaderMap, upgrade: Option<&HeaderValue>) {
    let te_trailers = header_values_contain_token(headers, &header::TE, "trailers");
    remove_hop_by_hop(headers);
    if te_trailers {
        headers.insert(header::TE, HeaderValue::from_static("trailers"));
    }
    if let Some(upgrade) = upgrade {
        headers.insert(header::CONNECTION, HeaderValue::from_static("Upgrade"));
        headers.insert(header::UPGRADE, upgrade.clone());
    }
    for name in [
        "forwarded",
        "x-forwarded-for",
        "x-forwarded-host",
        "x-forwarded-proto",
    ] {
        headers.remove(name);
    }
    headers.remove(header::HOST);
}

/// Formats an error with its source chain.
pub(crate) fn error_chain(err: &(dyn std::error::Error + 'static)) -> String {
    let mut out = err.to_string();
    let mut source = err.source();
    while let Some(e) = source {
        out.push_str(": ");
        out.push_str(&e.to_string());
        source = e.source();
    }
    out
}
