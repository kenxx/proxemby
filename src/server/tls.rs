use std::sync::{Arc, OnceLock};
use std::time::Duration;

use rustls::server::{Acceptor, ResolvesServerCert};
use rustls::{ClientConfig, RootCertStore, ServerConfig};
use tokio::io::AsyncWriteExt;
use tokio::net::TcpListener;
use tokio_rustls::LazyConfigAcceptor;

use super::{Server, is_transient_accept_error, prepare_stream, serve_connection};

const TLS_HANDSHAKE_TIMEOUT: Duration = Duration::from_secs(30);

fn provider() -> Arc<rustls::crypto::CryptoProvider> {
    Arc::new(rustls::crypto::ring::default_provider())
}

/// Client TLS settings for upstream and ACME connections. System roots are
/// preferred, like Go; the bundled Mozilla roots are used when none load.
pub fn client_config() -> ClientConfig {
    static ROOTS: OnceLock<Arc<RootCertStore>> = OnceLock::new();
    let roots = ROOTS.get_or_init(|| {
        let mut roots = RootCertStore::empty();
        for cert in rustls_native_certs::load_native_certs().certs {
            let _ = roots.add(cert);
        }
        if roots.is_empty() {
            roots.extend(webpki_roots::TLS_SERVER_ROOTS.iter().cloned());
        }
        Arc::new(roots)
    });
    ClientConfig::builder_with_provider(provider())
        .with_safe_default_protocol_versions()
        .expect("ring supports the default protocol versions")
        .with_root_certificates(roots.clone())
        .with_no_client_auth()
}

/// Server TLS settings offering HTTP/2 and HTTP/1.1 with session resumption.
pub fn server_config(resolver: Arc<dyn ResolvesServerCert>) -> ServerConfig {
    let mut config = ServerConfig::builder_with_provider(provider())
        .with_safe_default_protocol_versions()
        .expect("ring supports the default protocol versions")
        .with_no_client_auth()
        .with_cert_resolver(resolver);
    config.alpn_protocols = vec![b"h2".to_vec(), b"http/1.1".to_vec()];
    // Session tickets let clients resume TLS 1.3 sessions without a full handshake.
    if let Ok(ticketer) = rustls::crypto::ring::Ticketer::new() {
        config.ticketer = ticketer;
    }
    config
}

/// Accepts TLS connections forever. ACME TLS-ALPN-01 validation handshakes
/// are answered with `challenge` when it is set.
pub async fn serve_tls(
    listener: TcpListener,
    server: Arc<Server>,
    config: Arc<ServerConfig>,
    challenge: Option<Arc<ServerConfig>>,
) -> std::io::Result<()> {
    loop {
        let (stream, remote) = match listener.accept().await {
            Ok(accepted) => accepted,
            Err(e) if is_transient_accept_error(&e) => {
                tokio::time::sleep(Duration::from_millis(5)).await;
                continue;
            }
            Err(e) => return Err(e),
        };
        let conn = prepare_stream(&stream, remote, true);
        let server = server.clone();
        let config = config.clone();
        let challenge = challenge.clone();
        tokio::spawn(async move {
            let handshake = async {
                let start = LazyConfigAcceptor::new(Acceptor::default(), stream).await?;
                if let Some(challenge) = challenge {
                    if rustls_acme::is_tls_alpn_challenge(&start.client_hello()) {
                        let mut tls = start.into_stream(challenge).await?;
                        tls.shutdown().await?;
                        return Ok(None);
                    }
                }
                start.into_stream(config).await.map(Some)
            };
            if let Ok(Ok(Some(tls))) = tokio::time::timeout(TLS_HANDSHAKE_TIMEOUT, handshake).await
            {
                serve_connection(tls, server, conn).await;
            }
        });
    }
}
