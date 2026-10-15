mod common;

use std::time::Duration;

use common::*;
use http::StatusCode;
use proxemby::logging::Logger;
use proxemby::server;
use proxemby::stats::{self, Report};
use tokio::net::TcpListener;

async fn report_eventually(server: &server::Server, done: impl Fn(&Report) -> bool) -> Report {
    // Response bodies are counted as they stream, so wait for the last bytes.
    for _ in 0..100 {
        let report = server.stats().report();
        if done(&report) {
            return report;
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    server.stats().report()
}

#[tokio::test]
async fn counts_traffic_by_kind() {
    let resource = spawn_upstream(handler(|_| async { text("movie-bytes") })).await;
    let upstream = {
        let resource = resource.clone();
        spawn_upstream(handler(move |req| {
            let resource = resource.clone();
            async move {
                match req.uri().path() {
                    "/emby/Items/1/PlaybackInfo" => {
                        json(format!(r#"{{"Path":"{resource}/movie.mkv"}}"#))
                    }
                    "/emby/Items/1/Images/Primary" => text("poster"),
                    _ => text("ok"),
                }
            }
        }))
        .await
    };
    let (proxy, server) = spawn_server(
        &test_config(&upstream, "http://proxemby"),
        Logger::discard(),
    )
    .await;

    get(proxy, "proxemby", "/emby/Items/1/PlaybackInfo", &[]).await;
    let resp = get(
        proxy,
        "proxemby",
        &format!("/_proxy/http/{}/movie.mkv", host_of(&resource)),
        &[],
    )
    .await;
    assert_eq!(resp.body, "movie-bytes");
    get(proxy, "proxemby", "/emby/Items/1/Images/Primary", &[]).await;
    let resp = send(
        proxy,
        "POST",
        "proxemby",
        "/emby/Sessions/Playing",
        &[],
        "0123456789",
    )
    .await;
    assert_eq!(resp.status, StatusCode::OK);
    get(proxy, "unknown.example.com", "/emby/System/Info", &[]).await;

    let report = report_eventually(&server, |r| {
        let k = &r.routes[0].kinds;
        k["media"].since_start.bytes_out == 11 && k["image"].since_start.bytes_out == 6
    })
    .await;
    assert_eq!(report.routes.len(), 1);
    assert_eq!(report.routes[0].route, "proxemby");
    let kinds = &report.routes[0].kinds;

    let api = &kinds["api"];
    assert_eq!(api.since_start.requests, 2);
    assert_eq!(api.since_start.bytes_in, 10);
    let playback_info = format!(
        r#"{{"Path":"http://proxemby/_proxy/http/{}/movie.mkv"}}"#,
        host_of(&resource)
    );
    assert_eq!(api.since_start.bytes_out, (playback_info.len() + 2) as u64);

    let media = &kinds["media"];
    assert_eq!(media.since_start.requests, 1);
    assert_eq!(media.since_start.bytes_out, 11);
    assert_eq!(media.active, 0);

    assert_eq!(kinds["image"].since_start.requests, 1);
    // Today's totals include counters that are not flushed yet.
    assert_eq!(media.today, media.since_start);
    assert_eq!(media.month, media.since_start);
}

#[tokio::test]
async fn admin_listener_serves_stats_and_metrics() {
    let upstream = spawn_upstream(handler(|_| async { text("ok") })).await;
    let (proxy, server) = spawn_server(
        &test_config(&upstream, "http://proxemby"),
        Logger::discard(),
    )
    .await;
    get(proxy, "proxemby", "/emby/System/Info", &[]).await;

    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let admin = listener.local_addr().unwrap();
    tokio::spawn(server::serve_admin(
        listener,
        server.stats().clone(),
        no_shutdown(),
    ));

    let resp = get(admin, "localhost", "/metrics", &[]).await;
    assert_eq!(resp.status, StatusCode::OK);
    assert!(
        resp.headers["content-type"]
            .to_str()
            .unwrap()
            .starts_with("text/plain")
    );
    assert!(
        resp.body
            .contains(r#"proxemby_requests_total{route="proxemby",kind="api"} 1"#),
        "{}",
        resp.body
    );
    assert!(
        resp.body
            .contains(r#"proxemby_response_bytes_total{route="proxemby",kind="api"} 2"#),
        "{}",
        resp.body
    );

    let resp = get(admin, "localhost", "/stats", &[]).await;
    assert_eq!(resp.status, StatusCode::OK);
    let report: Report = serde_json::from_str(&resp.body).unwrap();
    assert_eq!(report.routes[0].kinds["api"].since_start.requests, 1);

    assert_eq!(
        get(admin, "localhost", "/other", &[]).await.status,
        StatusCode::NOT_FOUND
    );

    // The `proxemby stats` client reads the same report.
    let fetched = tokio::task::spawn_blocking(move || stats::fetch_report(&admin.to_string()))
        .await
        .unwrap()
        .unwrap();
    assert_eq!(fetched.routes[0].kinds["api"].since_start.requests, 1);
    assert!(stats::render_table(&fetched).contains("proxemby"));
}
