mod common;

use std::time::{Duration, Instant};

use common::*;
use http::StatusCode;
use proxemby::logging::Logger;
use proxemby::server::{self, Server, Shutdown};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;

fn quiet() -> Logger {
    Logger::discard()
}

/// An upstream that waits `header_delay` before sending headers, then sends
/// the body in two halves `body_delay` apart.
async fn spawn_slow_upstream(header_delay: Duration, body_delay: Duration) -> String {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move {
        loop {
            let Ok((mut stream, _)) = listener.accept().await else {
                return;
            };
            tokio::spawn(async move {
                let mut buf = [0u8; 4096];
                let _ = stream.read(&mut buf).await;
                tokio::time::sleep(header_delay).await;
                let _ = stream
                    .write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nhello")
                    .await;
                tokio::time::sleep(body_delay).await;
                let _ = stream.write_all(b"world").await;
            });
        }
    });
    format!("http://{addr}")
}

#[tokio::test]
async fn response_header_timeout_returns_gateway_timeout() {
    let upstream = spawn_slow_upstream(Duration::from_secs(5), Duration::ZERO).await;
    let mut cfg = test_config(&upstream, "http://proxemby");
    cfg.response_header_timeout = Some(Duration::from_millis(200));
    let proxy = spawn_proxy(&cfg, quiet()).await;

    let start = Instant::now();
    let resp = get(proxy, "proxemby", "/emby/System/Info", &[]).await;
    assert_eq!(resp.status, StatusCode::GATEWAY_TIMEOUT);
    assert!(
        start.elapsed() < Duration::from_secs(2),
        "{:?}",
        start.elapsed()
    );
}

#[tokio::test]
async fn response_header_timeout_does_not_limit_the_body() {
    // Headers arrive at once; the body takes longer than the timeout.
    let upstream = spawn_slow_upstream(Duration::ZERO, Duration::from_millis(500)).await;
    let mut cfg = test_config(&upstream, "http://proxemby");
    cfg.response_header_timeout = Some(Duration::from_millis(200));
    let proxy = spawn_proxy(&cfg, quiet()).await;

    let resp = get(proxy, "proxemby", "/emby/Videos/1/stream", &[]).await;
    assert_eq!(resp.status, StatusCode::OK);
    assert_eq!(resp.body, "helloworld");
}

#[tokio::test]
async fn graceful_shutdown_finishes_in_flight_requests() {
    let upstream = spawn_slow_upstream(Duration::from_millis(300), Duration::ZERO).await;
    let cfg = test_config(&upstream, "http://proxemby");
    let _ = rustls::crypto::ring::default_provider().install_default();
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let shutdown = Shutdown::new();
    let serving = tokio::spawn(server::serve_http(
        listener,
        Server::new(&cfg, quiet(), None),
        shutdown.signal(),
    ));

    let in_flight =
        tokio::spawn(async move { get(addr, "proxemby", "/emby/System/Info", &[]).await });
    tokio::time::sleep(Duration::from_millis(100)).await;

    assert!(
        shutdown.shutdown(Duration::from_secs(5)).await,
        "connections did not finish"
    );
    let resp = in_flight.await.unwrap();
    assert_eq!(
        (resp.status, resp.body.as_str()),
        (StatusCode::OK, "helloworld")
    );

    // The listener is closed once shutdown starts.
    serving.await.unwrap().unwrap();
    assert!(tokio::net::TcpStream::connect(addr).await.is_err());
}

#[tokio::test]
async fn graceful_shutdown_gives_up_after_grace_period() {
    let upstream = spawn_slow_upstream(Duration::from_secs(30), Duration::ZERO).await;
    let mut cfg = test_config(&upstream, "http://proxemby");
    cfg.response_header_timeout = None;
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let shutdown = Shutdown::new();
    tokio::spawn(server::serve_http(
        listener,
        Server::new(&cfg, quiet(), None),
        shutdown.signal(),
    ));

    tokio::spawn(async move { get(addr, "proxemby", "/emby/System/Info", &[]).await });
    tokio::time::sleep(Duration::from_millis(100)).await;

    let start = Instant::now();
    assert!(!shutdown.shutdown(Duration::from_millis(300)).await);
    assert!(
        start.elapsed() < Duration::from_secs(2),
        "{:?}",
        start.elapsed()
    );
}
