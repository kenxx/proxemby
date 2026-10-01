mod common;

use std::sync::Arc;

use bytes::Bytes;
use common::*;
use http::Request;
use http_body_util::{BodyExt, Empty};
use hyper_util::rt::{TokioExecutor, TokioIo};
use proxemby::logging::Logger;
use proxemby::server::{self, Server};
use rustls::pki_types::{PrivateKeyDer, PrivatePkcs8KeyDer, ServerName};
use rustls::sign::{CertifiedKey, SingleCertAndKey};
use tokio::net::{TcpListener, TcpStream};
use tokio_rustls::TlsConnector;

#[tokio::test]
async fn serves_http2_over_tls() {
    let _ = rustls::crypto::ring::default_provider().install_default();
    let seen = Recorder::default();
    let upstream = {
        let seen = seen.clone();
        spawn_upstream(handler(move |req| {
            seen.push(format!(
                "xfp={}",
                req.headers()["x-forwarded-proto"].to_str().unwrap()
            ));
            async { text("ok") }
        }))
        .await
    };

    let cert = rcgen::generate_simple_self_signed(vec!["proxemby".to_owned()]).unwrap();
    let key = PrivateKeyDer::Pkcs8(PrivatePkcs8KeyDer::from(cert.key_pair.serialize_der()));
    let provider = rustls::crypto::ring::default_provider();
    let certified = CertifiedKey::from_der(vec![cert.cert.der().clone()], key, &provider).unwrap();
    let tls_config = Arc::new(server::server_config(Arc::new(SingleCertAndKey::from(
        certified,
    ))));

    let proxy = Server::new(
        &test_config(&upstream, "https://proxemby"),
        Logger::discard(),
        None,
    );
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(server::serve_tls(
        listener,
        proxy,
        tls_config,
        None,
        no_shutdown(),
    ));

    let mut roots = rustls::RootCertStore::empty();
    roots.add(cert.cert.der().clone()).unwrap();
    let mut client_config = rustls::ClientConfig::builder()
        .with_root_certificates(roots)
        .with_no_client_auth();
    client_config.alpn_protocols = vec![b"h2".to_vec()];
    let tcp = TcpStream::connect(addr).await.unwrap();
    let tls = TlsConnector::from(Arc::new(client_config))
        .connect(ServerName::try_from("proxemby").unwrap(), tcp)
        .await
        .unwrap();
    assert_eq!(tls.get_ref().1.alpn_protocol(), Some(&b"h2"[..]));

    let (mut sender, conn) =
        hyper::client::conn::http2::handshake(TokioExecutor::new(), TokioIo::new(tls))
            .await
            .unwrap();
    tokio::spawn(conn);
    let req = Request::builder()
        .uri("https://proxemby/emby/System/Info")
        .body(Empty::<Bytes>::new())
        .unwrap();
    let resp = sender.send_request(req).await.unwrap();
    assert_eq!(resp.status(), 200);
    assert_eq!(resp.version(), http::Version::HTTP_2);
    assert_eq!(resp.into_body().collect().await.unwrap().to_bytes(), "ok");
    assert_eq!(seen.values(), vec!["xfp=https"]);
}
