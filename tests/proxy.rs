mod common;

use common::*;
use http::StatusCode;
use proxemby::logging::Level;
use proxemby::util::IpPrefix;
use tokio::io::{AsyncReadExt, AsyncWriteExt};

fn quiet() -> proxemby::logging::Logger {
    proxemby::logging::Logger::discard()
}

#[tokio::test]
async fn rewrites_playback_info_and_proxies_allowed_resource() {
    let seen = Recorder::default();
    let resource = {
        let seen = seen.clone();
        spawn_upstream(handler(move |req| {
            seen.push(format!(
                "range={}",
                req.headers()
                    .get("range")
                    .map(|v| v.to_str().unwrap())
                    .unwrap_or("")
            ));
            seen.push(format!("query={}", req.uri().query().unwrap_or("")));
            async move {
                let mut resp = text("movie-bytes");
                *resp.status_mut() = StatusCode::PARTIAL_CONTENT;
                resp
            }
        }))
        .await
    };
    let upstream = {
        let resource = resource.clone();
        let seen = seen.clone();
        spawn_upstream(handler(move |req| {
            seen.push(format!("path={}", req.uri().path()));
            seen.push(format!(
                "accept-encoding={}",
                req.headers()
                    .get("accept-encoding")
                    .map(|v| v.to_str().unwrap())
                    .unwrap_or("")
            ));
            let body =
                format!(r#"{{"MediaSources":[{{"Path":"{resource}/movie.mp4?token=abc"}}]}}"#);
            async move { json(body) }
        }))
        .await
    };
    let proxy = spawn_proxy(&test_config(&upstream, "http://proxemby"), quiet()).await;

    let resp = get(
        proxy,
        "proxemby",
        "/emby/Items/1/PlaybackInfo",
        &[("accept-encoding", "gzip")],
    )
    .await;
    let expected = format!(
        "http://proxemby/_proxy/http/{}/movie.mp4?token=abc",
        host_of(&resource)
    );
    assert!(resp.body.contains(&expected), "body = {}", resp.body);
    assert_eq!(resp.headers["content-length"], resp.body.len().to_string());

    let resp = get(
        proxy,
        "proxemby",
        &format!("/_proxy/http/{}/movie.mp4?token=abc", host_of(&resource)),
        &[("range", "bytes=0-10")],
    )
    .await;
    assert_eq!(resp.status, StatusCode::PARTIAL_CONTENT);
    assert_eq!(resp.body, "movie-bytes");

    let seen = seen.values();
    assert!(
        seen.contains(&"path=/emby/Items/1/PlaybackInfo".to_owned()),
        "{seen:?}"
    );
    assert!(seen.contains(&"accept-encoding=".to_owned()), "{seen:?}");
    assert!(seen.contains(&"range=bytes=0-10".to_owned()), "{seen:?}");
    assert!(seen.contains(&"query=token=abc".to_owned()), "{seen:?}");
}

#[tokio::test]
async fn rejects_oversized_playback_info() {
    let upstream = spawn_upstream(handler(|_| async {
        json(r#"{"Path":"https://vod.us.emby.com/movie.mp4","Padding":"too large"}"#)
    }))
    .await;
    let mut cfg = test_config(&upstream, "http://proxemby");
    cfg.playbackinfo_max_bytes = 16;
    let proxy = spawn_proxy(&cfg, quiet()).await;
    let resp = get(proxy, "proxemby", "/emby/Items/1/PlaybackInfo", &[]).await;
    assert_eq!(resp.status, StatusCode::BAD_GATEWAY);
}

#[tokio::test]
async fn rejects_unknown_resource_host() {
    let proxy = spawn_proxy(
        &test_config("https://us.emby.com", "http://proxemby"),
        quiet(),
    )
    .await;
    let resp = get(
        proxy,
        "proxemby",
        "/_proxy/https/vod.us.emby.com/movie.mp4",
        &[],
    )
    .await;
    assert_eq!(resp.status, StatusCode::FORBIDDEN);
}

#[tokio::test]
async fn forwards_websocket_upgrade() {
    let seen = Recorder::default();
    let upstream = {
        let seen = seen.clone();
        spawn_upstream(handler(move |mut req| {
            seen.push(format!(
                "connection={} upgrade={}",
                req.headers()["connection"].to_str().unwrap(),
                req.headers()["upgrade"].to_str().unwrap()
            ));
            let on_upgrade = hyper::upgrade::on(&mut req);
            tokio::spawn(async move {
                // Echo whatever the client sends after the upgrade.
                let mut io = hyper_util::rt::TokioIo::new(on_upgrade.await.unwrap());
                let mut buf = [0u8; 5];
                io.read_exact(&mut buf).await.unwrap();
                io.write_all(&buf).await.unwrap();
            });
            async move {
                let mut resp = text("");
                *resp.status_mut() = StatusCode::SWITCHING_PROTOCOLS;
                resp.headers_mut()
                    .insert("connection", "Upgrade".parse().unwrap());
                resp.headers_mut()
                    .insert("upgrade", "websocket".parse().unwrap());
                resp
            }
        }))
        .await
    };
    let proxy = spawn_proxy(&test_config(&upstream, "http://proxemby"), quiet()).await;

    let mut stream = tokio::net::TcpStream::connect(proxy).await.unwrap();
    stream
        .write_all(b"GET /embywebsocket HTTP/1.1\r\nHost: proxemby\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
        .await
        .unwrap();
    let mut head = Vec::new();
    while !head.ends_with(b"\r\n\r\n") {
        let mut byte = [0u8; 1];
        stream.read_exact(&mut byte).await.unwrap();
        head.push(byte[0]);
    }
    let head = String::from_utf8(head).unwrap();
    assert!(head.starts_with("HTTP/1.1 101"), "{head}");
    stream.write_all(b"hello").await.unwrap();
    let mut echo = [0u8; 5];
    stream.read_exact(&mut echo).await.unwrap();
    assert_eq!(&echo, b"hello");
    assert_eq!(seen.values(), vec!["connection=Upgrade upgrade=websocket"]);
}

#[tokio::test]
async fn routes_by_host() {
    let one = spawn_upstream(handler(|_| async { text("one") })).await;
    let two = spawn_upstream(handler(|_| async { text("two") })).await;
    let cfg = proxemby::config::Config::with_routes(vec![
        route(&one, "http://one.example.com"),
        route(&two, "http://two.example.com"),
    ]);
    let proxy = spawn_proxy(&cfg, quiet()).await;
    for (host, body) in [
        ("one.example.com", "one"),
        ("two.example.com:443", "two"),
        ("TWO.EXAMPLE.COM", "two"),
    ] {
        assert_eq!(
            get(proxy, host, "/emby/System/Info", &[]).await.body,
            body,
            "{host}"
        );
    }
}

#[tokio::test]
async fn returns_not_found_for_unknown_host() {
    let proxy = spawn_proxy(
        &test_config("https://us.emby.com", "http://proxemby"),
        quiet(),
    )
    .await;
    let resp = get(proxy, "unknown.example.com", "/emby/System/Info", &[]).await;
    assert_eq!(resp.status, StatusCode::NOT_FOUND);
}

#[tokio::test]
async fn keeps_resource_registry_per_route() {
    let resource = spawn_upstream(handler(|_| async { text("movie") })).await;
    let one = {
        let resource = resource.clone();
        spawn_upstream(handler(move |_| {
            let body = format!(r#"{{"Path":"{resource}/movie.mp4"}}"#);
            async move { json(body) }
        }))
        .await
    };
    let two = spawn_upstream(handler(|_| async { text("ok") })).await;
    let cfg = proxemby::config::Config::with_routes(vec![
        route(&one, "http://one.example.com"),
        route(&two, "http://two.example.com"),
    ]);
    let proxy = spawn_proxy(&cfg, quiet()).await;

    get(proxy, "one.example.com", "/emby/Items/1/PlaybackInfo", &[]).await;
    let path = format!("/_proxy/http/{}/movie.mp4", host_of(&resource));
    assert_eq!(
        get(proxy, "one.example.com", &path, &[]).await.status,
        StatusCode::OK
    );
    assert_eq!(
        get(proxy, "two.example.com", &path, &[]).await.status,
        StatusCode::FORBIDDEN
    );
}

#[tokio::test]
async fn forwards_normal_requests_to_upstream() {
    let seen = Recorder::default();
    let upstream = {
        let seen = seen.clone();
        spawn_upstream(handler(move |req| {
            let h = req.headers();
            let get = |name: &str| {
                h.get(name)
                    .map(|v| v.to_str().unwrap().to_owned())
                    .unwrap_or_default()
            };
            seen.push(format!("path={}", req.uri().path()));
            seen.push(format!("query={}", req.uri().query().unwrap_or("")));
            seen.push(format!("host={}", get("host")));
            seen.push(format!("xfh={}", get("x-forwarded-host")));
            seen.push(format!("xff={}", get("x-forwarded-for")));
            seen.push(format!("xfp={}", get("x-forwarded-proto")));
            async { text("ok") }
        }))
        .await
    };
    let proxy = spawn_proxy(&test_config(&upstream, "http://proxemby"), quiet()).await;
    let resp = get(
        proxy,
        "proxemby",
        "/emby/System/Info?api_key=secret",
        &[
            ("connection", "X-Forwarded-Host"),
            ("x-forwarded-for", "spoofed"),
        ],
    )
    .await;
    assert_eq!(resp.body, "ok");
    let seen = seen.values();
    for want in [
        "path=/emby/System/Info".to_owned(),
        "query=api_key=secret".to_owned(),
        format!("host={}", host_of(&upstream)),
        "xfh=proxemby".to_owned(),
        "xff=127.0.0.1".to_owned(),
        "xfp=http".to_owned(),
    ] {
        assert!(seen.contains(&want), "missing {want} in {seen:?}");
    }
}

#[tokio::test]
async fn hides_client_forwarding_headers() {
    let seen = Recorder::default();
    let upstream = {
        let seen = seen.clone();
        spawn_upstream(handler(move |req| {
            for header in [
                "x-forwarded-for",
                "x-forwarded-host",
                "x-forwarded-proto",
                "forwarded",
                "via",
            ] {
                if let Some(value) = req.headers().get(header) {
                    seen.push(format!("{header}={}", value.to_str().unwrap()));
                }
            }
            async { text("ok") }
        }))
        .await
    };
    let mut cfg = test_config(&upstream, "http://proxemby");
    cfg.hide_client = true;
    let proxy = spawn_proxy(&cfg, quiet()).await;
    let resp = get(
        proxy,
        "proxemby",
        "/emby/System/Info",
        &[
            ("x-forwarded-for", "203.0.113.10"),
            ("x-forwarded-host", "client.example.com"),
            ("x-forwarded-proto", "https"),
            ("forwarded", "for=203.0.113.10"),
            ("via", "1.1 old-proxy"),
        ],
    )
    .await;
    assert_eq!(resp.status, StatusCode::OK);
    assert!(seen.values().is_empty(), "{:?}", seen.values());
}

fn prefixes(values: &[&str]) -> Vec<IpPrefix> {
    values.iter().map(|v| IpPrefix::parse(v).unwrap()).collect()
}

#[tokio::test]
async fn client_ip_allowlist() {
    let upstream = spawn_upstream(handler(|_| async { text("ok") })).await;

    let mut cfg = test_config(&upstream, "http://proxemby");
    cfg.allowed_clients = prefixes(&["127.0.0.1", "10.0.0.0/8"]);
    let proxy = spawn_proxy(&cfg, quiet()).await;
    assert_eq!(
        get(proxy, "proxemby", "/emby/System/Info", &[])
            .await
            .status,
        StatusCode::OK
    );

    cfg.allowed_clients = prefixes(&["10.0.0.0/8"]);
    let proxy = spawn_proxy(&cfg, quiet()).await;
    assert_eq!(
        get(proxy, "proxemby", "/emby/System/Info", &[])
            .await
            .status,
        StatusCode::FORBIDDEN
    );

    cfg.allowed_clients = prefixes(&["203.0.113.10"]);
    cfg.trust_proxy_headers = true;
    let proxy = spawn_proxy(&cfg, quiet()).await;
    let resp = get(
        proxy,
        "proxemby",
        "/emby/System/Info",
        &[("x-forwarded-for", "203.0.113.10, 127.0.0.1")],
    )
    .await;
    assert_eq!(resp.status, StatusCode::OK);
}

#[tokio::test]
async fn debug_logs_sanitized_request() {
    let upstream = spawn_upstream(handler(|_| async { text("ok") })).await;
    let (logger, logs) = test_logger(Level::Debug);
    let proxy = spawn_proxy(&test_config(&upstream, "http://proxemby"), logger).await;
    let resp = get(
        proxy,
        "proxemby",
        "/emby/System/Info?api_key=secret&token=abc&device=ios",
        &[],
    )
    .await;
    assert_eq!(resp.status, StatusCode::OK);

    let text = logs.wait_for("request completed").await;
    for want in [
        r#"level=DEBUG msg="request completed""#.to_owned(),
        "method=GET".to_owned(),
        "api_key=redacted".to_owned(),
        "token=redacted".to_owned(),
        "device=ios".to_owned(),
        "status=200".to_owned(),
        "bytes=2".to_owned(),
        format!("target=upstream:{upstream}"),
    ] {
        assert!(text.contains(&want), "log {text:?} missing {want:?}");
    }
    assert!(
        !text.contains("secret") && !text.contains("token=abc"),
        "{text}"
    );
}

#[tokio::test]
async fn debug_logs_playback_info_rewrite() {
    let upstream = spawn_upstream(handler(|_| async {
        json(r#"{"Path":"https://vod.us.emby.com/movie.mp4?token=secret"}"#)
    }))
    .await;
    let (logger, logs) = test_logger(Level::Debug);
    let proxy = spawn_proxy(&test_config(&upstream, "http://proxemby"), logger).await;
    let resp = get(
        proxy,
        "proxemby",
        "/emby/Items/1/PlaybackInfo?api_key=secret",
        &[],
    )
    .await;
    assert_eq!(resp.status, StatusCode::OK);

    let text = logs.wait_for("request completed").await;
    for want in [
        r#"level=DEBUG msg="playbackinfo rewrite""#,
        r#"path="/emby/Items/1/PlaybackInfo?api_key=redacted""#,
        "count=1",
        r#"level=DEBUG msg="playbackinfo rewrite item""#,
        "json_path=Path",
        "scheme=https",
        "host=vod.us.emby.com",
        r#"from="https://vod.us.emby.com/movie.mp4?token=redacted""#,
        r#"to="http://proxemby/_proxy/https/vod.us.emby.com/movie.mp4?token=redacted""#,
    ] {
        assert!(text.contains(want), "log {text:?} missing {want:?}");
    }
    assert!(
        !text.contains("token=secret") && !text.contains("api_key=secret"),
        "{text}"
    );
}

#[tokio::test]
async fn logs_rule_decisions() {
    let resource = spawn_upstream(handler(|_| async { text("movie") })).await;
    let upstream = {
        let resource = resource.clone();
        spawn_upstream(handler(move |_| {
            let body = format!(r#"{{"Path":"{resource}/movie.mp4"}}"#);
            async move { json(body) }
        }))
        .await
    };
    let (logger, logs) = test_logger(Level::Debug);
    let mut cfg = test_config(&upstream, "http://proxemby");
    cfg.allowed_clients = prefixes(&["203.0.113.10"]);
    cfg.trust_proxy_headers = true;
    let proxy = spawn_proxy(&cfg, logger).await;

    let allowed = [("x-forwarded-for", "203.0.113.10")];
    get(proxy, "proxemby", "/emby/Items/1/PlaybackInfo", &allowed).await;
    get(
        proxy,
        "proxemby",
        &format!("/_proxy/http/{}/movie.mp4", host_of(&resource)),
        &allowed,
    )
    .await;
    get(
        proxy,
        "proxemby",
        "/emby/System/Info",
        &[("x-forwarded-for", "198.51.100.20")],
    )
    .await;
    get(
        proxy,
        "proxemby",
        "/_proxy/https/blocked.example.com/movie.mp4",
        &allowed,
    )
    .await;

    let text = logs.text();
    for want in [
        r#"msg="client filter decision""#.to_owned(),
        "allowed=true".to_owned(),
        "client=203.0.113.10".to_owned(),
        "source=x_forwarded_for".to_owned(),
        r#"level=WARN msg="client filter rejected request""#.to_owned(),
        "reason=client_ip_not_allowed".to_owned(),
        "client=198.51.100.20".to_owned(),
        r#"msg="resource proxy decision""#.to_owned(),
        format!("host={}", host_of(&resource)),
        r#"level=WARN msg="resource proxy rejected request""#.to_owned(),
        "reason=host_not_allowed".to_owned(),
        "host=blocked.example.com".to_owned(),
    ] {
        assert!(text.contains(&want), "log {text:?} missing {want:?}");
    }
}

#[tokio::test]
async fn logs_rejected_playback_info_route_miss_and_hide_client() {
    let upstream = spawn_upstream(handler(|_| async {
        json(r#"{"Path":"https://vod.us.emby.com/movie.mp4","Padding":"too large"}"#)
    }))
    .await;
    let (logger, logs) = test_logger(Level::Debug);
    let mut cfg = test_config(&upstream, "http://proxemby");
    cfg.playbackinfo_max_bytes = 16;
    cfg.hide_client = true;
    let proxy = spawn_proxy(&cfg, logger).await;

    get(proxy, "unknown.example.com", "/emby/System/Info", &[]).await;
    get(proxy, "proxemby", "/emby/Items/1/PlaybackInfo", &[]).await;

    let text = logs.text();
    for want in [
        r#"msg="route miss""#,
        "host=unknown.example.com",
        r#"msg="upstream request hides client identity headers""#,
        r#"level=WARN msg="playbackinfo response rejected""#,
        "reason=response_too_large",
        "max_bytes=16",
    ] {
        assert!(text.contains(want), "log {text:?} missing {want:?}");
    }
}

#[tokio::test]
async fn info_level_does_not_log_debug_requests() {
    let upstream = spawn_upstream(handler(|_| async { text("ok") })).await;
    let (logger, logs) = test_logger(Level::Info);
    let proxy = spawn_proxy(&test_config(&upstream, "http://proxemby"), logger).await;
    get(proxy, "proxemby", "/emby/System/Info", &[]).await;
    tokio::time::sleep(std::time::Duration::from_millis(50)).await;
    assert!(
        !logs.text().contains("request completed"),
        "{}",
        logs.text()
    );
}

#[tokio::test]
async fn gzip_playback_info_is_decoded_and_rewritten() {
    use std::io::Write;
    let mut encoder = flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::default());
    encoder
        .write_all(br#"{"Path":"https://vod.us.emby.com/movie.mp4"}"#)
        .unwrap();
    let gz = bytes::Bytes::from(encoder.finish().unwrap());
    let upstream = spawn_upstream(handler(move |_| {
        let gz = gz.clone();
        async move {
            let mut resp = json(gz);
            resp.headers_mut()
                .insert("content-encoding", "gzip".parse().unwrap());
            resp
        }
    }))
    .await;
    let proxy = spawn_proxy(&test_config(&upstream, "http://proxemby"), quiet()).await;
    let resp = get(proxy, "proxemby", "/emby/Items/1/PlaybackInfo", &[]).await;
    assert!(resp.headers.get("content-encoding").is_none());
    assert_eq!(
        resp.body,
        r#"{"Path":"http://proxemby/_proxy/https/vod.us.emby.com/movie.mp4"}"#
    );
}

#[tokio::test]
async fn upstream_connection_error_is_bad_gateway() {
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let dead = format!("http://{}", listener.local_addr().unwrap());
    drop(listener);
    let proxy = spawn_proxy(&test_config(&dead, "http://proxemby"), quiet()).await;
    assert_eq!(
        get(proxy, "proxemby", "/emby/System/Info", &[])
            .await
            .status,
        StatusCode::BAD_GATEWAY
    );
}
