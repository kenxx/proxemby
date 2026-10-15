//! The admin listener: traffic statistics for local tools, kept off the
//! public routes.

use std::convert::Infallible;
use std::sync::Arc;

use http::header::{self, HeaderValue};
use http::{Method, Request, Response, StatusCode};
use hyper::body::Incoming;
use hyper::service::service_fn;
use hyper_util::rt::TokioIo;
use tokio::net::TcpListener;

use super::{ProxyBody, ShutdownSignal, accept, text_response};
use crate::stats::Stats;

/// Serves `/stats` (JSON) and `/metrics` (Prometheus) until `signal` fires.
pub async fn serve_admin(
    listener: TcpListener,
    stats: Arc<Stats>,
    signal: ShutdownSignal,
) -> std::io::Result<()> {
    while let Some((stream, _)) = accept(&listener, &signal).await? {
        let stats = stats.clone();
        let signal = signal.clone();
        tokio::spawn(async move {
            let service = service_fn(move |req| {
                let stats = stats.clone();
                async move { Ok::<_, Infallible>(admin_response(&stats, &req)) }
            });
            let connection = hyper::server::conn::http1::Builder::new()
                .serve_connection(TokioIo::new(stream), service);
            tokio::pin!(connection);
            tokio::select! {
                _ = connection.as_mut() => return,
                _ = signal.triggered() => {}
            }
            connection.as_mut().graceful_shutdown();
            let _ = connection.await;
        });
    }
    Ok(())
}

fn admin_response(stats: &Stats, req: &Request<Incoming>) -> Response<ProxyBody> {
    if req.method() != Method::GET && req.method() != Method::HEAD {
        return text_response(StatusCode::METHOD_NOT_ALLOWED, "method not allowed");
    }
    let (body, content_type) = match req.uri().path() {
        "/stats" => (
            serde_json::to_vec_pretty(&stats.report()).unwrap_or_default(),
            "application/json",
        ),
        "/metrics" => (
            stats.prometheus().into_bytes(),
            "text/plain; version=0.0.4; charset=utf-8",
        ),
        _ => return text_response(StatusCode::NOT_FOUND, "404 page not found"),
    };
    let mut resp = Response::new(ProxyBody::full(body));
    resp.headers_mut()
        .insert(header::CONTENT_TYPE, HeaderValue::from_static(content_type));
    resp
}
