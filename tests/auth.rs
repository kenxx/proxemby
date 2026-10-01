mod common;

use std::net::SocketAddr;

use common::*;
use http::StatusCode;
use http_body_util::BodyExt;

struct Env {
    proxy: SocketAddr,
    resource: String,
    logouts: Recorder,
    upstream_hits: Recorder,
}

async fn env() -> Env {
    let logouts = Recorder::default();
    let upstream_hits = Recorder::default();
    let resource = spawn_upstream(handler(|_| async { text("movie-bytes") })).await;
    let upstream = {
        let resource = resource.clone();
        let logouts = logouts.clone();
        let hits = upstream_hits.clone();
        spawn_upstream(handler(move |req| {
            let resource = resource.clone();
            let logouts = logouts.clone();
            let hits = hits.clone();
            async move {
                let path = req.uri().path().to_owned();
                hits.push(path.clone());
                match path.as_str() {
                    "/emby/Users/AuthenticateByName" => {
                        let body = req.into_body().collect().await.unwrap().to_bytes();
                        let json: serde_json::Value = serde_json::from_slice(&body).unwrap();
                        let user = json["Username"].as_str().unwrap();
                        common::json(format!(r#"{{"User":{{"Name":"{user}","Id":"id-{user}"}},"AccessToken":"tok-{user}","ServerId":"s1"}}"#))
                    }
                    "/emby/Sessions/Logout" => {
                        logouts.push(req.headers().get("x-emby-token").map(|v| v.to_str().unwrap()).unwrap_or(""));
                        let mut resp = text("");
                        *resp.status_mut() = StatusCode::NO_CONTENT;
                        resp
                    }
                    "/emby/Items/1/PlaybackInfo" => common::json(format!(r#"{{"MediaSources":[{{"Path":"{resource}/dir/movie.mp4"}}]}}"#)),
                    _ => text("upstream-ok"),
                }
            }
        }))
        .await
    };
    let mut cfg = test_config(&upstream, "http://proxemby");
    cfg.allowed_users = vec!["Ken".into()];
    let proxy = spawn_proxy(&cfg, proxemby::logging::Logger::discard()).await;
    Env {
        proxy,
        resource: host_of(&resource),
        logouts,
        upstream_hits,
    }
}

impl Env {
    async fn get(&self, path: &str, headers: &[(&str, &str)]) -> TestResponse {
        get(self.proxy, "proxemby", path, headers).await
    }

    async fn login(&self, user: &str) -> TestResponse {
        send(
            self.proxy,
            "POST",
            "proxemby",
            "/emby/Users/AuthenticateByName",
            &[("content-type", "application/json")],
            &format!(r#"{{"Username":"{user}","Pw":"x"}}"#),
        )
        .await
    }
}

#[tokio::test]
async fn accepts_allowed_user_token() {
    let env = env().await;
    let resp = env.login("ken").await;
    assert_eq!(resp.status, StatusCode::OK);
    assert!(
        resp.body.contains(r#""AccessToken":"tok-ken""#),
        "{}",
        resp.body
    );

    let resp = env
        .get("/emby/Users/id-ken/Items", &[("x-emby-token", "tok-ken")])
        .await;
    assert_eq!(
        (resp.status, resp.body.as_str()),
        (StatusCode::OK, "upstream-ok")
    );
    assert_eq!(
        env.get("/emby/Videos/1/stream?api_key=tok-ken", &[])
            .await
            .status,
        StatusCode::OK
    );
}

#[tokio::test]
async fn rejects_missing_and_unknown_tokens() {
    let env = env().await;
    for headers in [&[][..], &[("x-emby-token", "tok-someone-else")]] {
        assert_eq!(
            env.get("/emby/Users/id-ken/Items", headers).await.status,
            StatusCode::UNAUTHORIZED,
            "{headers:?}"
        );
    }
    assert!(
        env.upstream_hits.values().is_empty(),
        "{:?}",
        env.upstream_hits.values()
    );
}

#[tokio::test]
async fn rejects_login_for_other_users() {
    let env = env().await;
    let resp = env.login("stranger").await;
    assert_eq!(resp.status, StatusCode::UNAUTHORIZED);
    assert!(!resp.body.contains("tok-stranger"), "{}", resp.body);

    for _ in 0..200 {
        if !env.logouts.values().is_empty() {
            break;
        }
        tokio::time::sleep(std::time::Duration::from_millis(10)).await;
    }
    assert_eq!(env.logouts.values(), vec!["tok-stranger"]);

    let resp = env
        .get(
            "/emby/Users/id-stranger/Items",
            &[("x-emby-token", "tok-stranger")],
        )
        .await;
    assert_eq!(resp.status, StatusCode::UNAUTHORIZED);
}

#[tokio::test]
async fn public_endpoints() {
    let env = env().await;
    let resp = env.get("/emby/Users/Public", &[]).await;
    assert_eq!((resp.status, resp.body.as_str()), (StatusCode::OK, "[]"));
    for path in [
        "/emby/System/Info/Public",
        "/System/Info/Public",
        "/emby/Items/1/Images/Primary?maxWidth=300",
        "/web/index.html",
    ] {
        assert_eq!(env.get(path, &[]).await.status, StatusCode::OK, "{path}");
    }
    let resp = send(
        env.proxy,
        "POST",
        "proxemby",
        "/emby/Items/1/Images/Primary",
        &[],
        "x",
    )
    .await;
    assert_eq!(resp.status, StatusCode::UNAUTHORIZED);
}

#[tokio::test]
async fn logout_removes_session() {
    let env = env().await;
    env.login("ken").await;
    let header = [(
        "x-emby-authorization",
        r#"MediaBrowser Client="test", DeviceId="d1", Token="tok-ken""#,
    )];
    let resp = send(
        env.proxy,
        "POST",
        "proxemby",
        "/emby/Sessions/Logout",
        &header,
        "",
    )
    .await;
    assert_eq!(resp.status, StatusCode::NO_CONTENT);
    assert_eq!(
        env.get("/emby/Users/id-ken/Items", &header).await.status,
        StatusCode::UNAUTHORIZED
    );
}

#[tokio::test]
async fn signs_resource_urls() {
    let env = env().await;
    env.login("ken").await;

    let resp = env
        .get("/emby/Items/1/PlaybackInfo", &[("x-emby-token", "tok-ken")])
        .await;
    assert_eq!(resp.status, StatusCode::OK);
    let json: serde_json::Value = serde_json::from_str(&resp.body).unwrap();
    let rewritten = json["MediaSources"][0]["Path"].as_str().unwrap();
    let path = rewritten.strip_prefix("http://proxemby").unwrap();
    let unsigned = format!("/_proxy/http/{}/dir/movie.mp4", env.resource);
    assert!(
        !path.starts_with("/_proxy/http/") && path.ends_with(&unsigned["/_proxy".len()..]),
        "{path}"
    );

    // Signed URLs work without a token, including relative paths such as HLS segments.
    let signed_dir = path.trim_end_matches("movie.mp4");
    for p in [path.to_owned(), format!("{signed_dir}segment1.ts")] {
        let resp = env.get(&p, &[]).await;
        assert_eq!(
            (resp.status, resp.body.as_str()),
            (StatusCode::OK, "movie-bytes"),
            "{p}"
        );
    }

    let tampered = format!(
        "/_proxy/AAAAAAAAAAAAAAAAAAAAAA/http/{}/dir/movie.mp4",
        env.resource
    );
    assert_eq!(env.get(&tampered, &[]).await.status, StatusCode::FORBIDDEN);
    assert_eq!(env.get(&unsigned, &[]).await.status, StatusCode::FORBIDDEN);
    assert_eq!(
        env.get(&unsigned, &[("x-emby-token", "tok-ken")])
            .await
            .status,
        StatusCode::OK
    );
}
