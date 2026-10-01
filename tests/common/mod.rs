#![allow(dead_code)]

use std::convert::Infallible;
use std::future::Future;
use std::io::Write;
use std::net::SocketAddr;
use std::pin::Pin;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use bytes::Bytes;
use http::{HeaderMap, Request, Response, StatusCode};
use http_body_util::{BodyExt, Full};
use hyper::body::Incoming;
use hyper::service::service_fn;
use hyper_util::client::legacy::Client;
use hyper_util::client::legacy::connect::HttpConnector;
use hyper_util::rt::{TokioExecutor, TokioIo};
use tokio::net::TcpListener;

use proxemby::config::{Config, Route};
use proxemby::logging::{self, Level, Logger};
use proxemby::server::{self, Server, Shutdown, ShutdownSignal};
use proxemby::util::HttpUrl;

pub type Handler = Arc<
    dyn Fn(Request<Incoming>) -> Pin<Box<dyn Future<Output = Response<Full<Bytes>>> + Send>>
        + Send
        + Sync,
>;

pub fn handler<F, Fut>(f: F) -> Handler
where
    F: Fn(Request<Incoming>) -> Fut + Send + Sync + 'static,
    Fut: Future<Output = Response<Full<Bytes>>> + Send + 'static,
{
    Arc::new(move |req| Box::pin(f(req)))
}

pub fn text(body: impl Into<Bytes>) -> Response<Full<Bytes>> {
    Response::new(Full::new(body.into()))
}

pub fn json(body: impl Into<Bytes>) -> Response<Full<Bytes>> {
    let mut resp = text(body);
    resp.headers_mut()
        .insert("content-type", "application/json".parse().unwrap());
    resp
}

/// Starts an HTTP/1.1 server (with upgrade support) and returns its URL.
pub async fn spawn_upstream(handler: Handler) -> String {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move {
        loop {
            let Ok((stream, _)) = listener.accept().await else {
                return;
            };
            let handler = handler.clone();
            tokio::spawn(async move {
                let service = service_fn(move |req| {
                    let handler = handler.clone();
                    async move { Ok::<_, Infallible>(handler(req).await) }
                });
                let _ = hyper::server::conn::http1::Builder::new()
                    .serve_connection(TokioIo::new(stream), service)
                    .with_upgrades()
                    .await;
            });
        }
    });
    format!("http://{addr}")
}

pub fn route(upstream: &str, public: &str) -> Route {
    Route {
        upstream_url: HttpUrl::parse(upstream).unwrap(),
        public_url: HttpUrl::parse(public).unwrap(),
        acme_domain: String::new(),
    }
}

pub fn test_config(upstream: &str, public: &str) -> Config {
    Config::with_routes(vec![route(upstream, public)])
}

#[derive(Clone, Default)]
pub struct LogBuffer(Arc<Mutex<Vec<u8>>>);

impl LogBuffer {
    pub fn text(&self) -> String {
        String::from_utf8(self.0.lock().unwrap().clone()).unwrap()
    }

    /// Waits until `needle` shows up; request logs are written after the
    /// response body finishes.
    pub async fn wait_for(&self, needle: &str) -> String {
        for _ in 0..100 {
            let text = self.text();
            if text.contains(needle) {
                return text;
            }
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
        self.text()
    }
}

impl Write for LogBuffer {
    fn write(&mut self, data: &[u8]) -> std::io::Result<usize> {
        self.0.lock().unwrap().extend_from_slice(data);
        Ok(data.len())
    }
    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

pub fn test_logger(level: Level) -> (Logger, LogBuffer) {
    let buf = LogBuffer::default();
    let logger = Logger::new(
        logging::Config {
            level,
            format: logging::Format::Text,
            time: false,
        },
        Box::new(buf.clone()),
    );
    (logger, buf)
}

/// Starts proxemby on a random local port and returns its address.
pub async fn spawn_proxy(cfg: &Config, logger: Logger) -> SocketAddr {
    let _ = rustls::crypto::ring::default_provider().install_default();
    let proxy = Server::new(cfg, logger, None);
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(server::serve_http(listener, proxy, no_shutdown()));
    addr
}

pub struct TestResponse {
    pub status: StatusCode,
    pub headers: HeaderMap,
    pub body: String,
}

/// Sends a request to the proxy with the given `Host` header.
pub async fn send(
    proxy: SocketAddr,
    method: &str,
    host: &str,
    path: &str,
    headers: &[(&str, &str)],
    body: &str,
) -> TestResponse {
    let client: Client<HttpConnector, Full<Bytes>> =
        Client::builder(TokioExecutor::new()).build(HttpConnector::new());
    let mut builder = Request::builder()
        .method(method)
        .uri(format!("http://{proxy}{path}"))
        .header("host", host);
    for (k, v) in headers {
        builder = builder.header(*k, *v);
    }
    let resp = client
        .request(
            builder
                .body(Full::new(Bytes::from(body.to_owned())))
                .unwrap(),
        )
        .await
        .unwrap();
    let status = resp.status();
    let headers = resp.headers().clone();
    let body = resp.into_body().collect().await.unwrap().to_bytes();
    TestResponse {
        status,
        headers,
        body: String::from_utf8_lossy(&body).into_owned(),
    }
}

pub async fn get(
    proxy: SocketAddr,
    host: &str,
    path: &str,
    headers: &[(&str, &str)],
) -> TestResponse {
    send(proxy, "GET", host, path, headers, "").await
}

/// Records values seen by mock servers so tests can assert on them.
#[derive(Clone, Default)]
pub struct Recorder(Arc<Mutex<Vec<String>>>);

impl Recorder {
    pub fn push(&self, value: impl Into<String>) {
        self.0.lock().unwrap().push(value.into());
    }
    pub fn values(&self) -> Vec<String> {
        self.0.lock().unwrap().clone()
    }
}

pub fn host_of(url: &str) -> String {
    url.trim_start_matches("http://").to_owned()
}

/// A shutdown signal that never fires, for servers that live as long as the test.
pub fn no_shutdown() -> ShutdownSignal {
    Box::leak(Box::new(Shutdown::new())).signal()
}
