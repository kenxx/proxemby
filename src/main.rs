use std::path::Path;
use std::process::ExitCode;
use std::sync::Arc;
use std::time::Duration;

use futures_util::StreamExt;
use rustls_acme::AcmeConfig;
use rustls_acme::caches::DirCache;

use proxemby::auth::Store;
use proxemby::config::{self, Config};
use proxemby::logging::Logger;
use proxemby::server::{self, Server, Shutdown, ShutdownSignal};
use proxemby::stats::{self, Stats};
use proxemby::{error, info, warn};

#[cfg(target_env = "musl")]
#[global_allocator]
static GLOBAL: mimalloc::MiMalloc = mimalloc::MiMalloc;

const VERSION: &str = env!("CARGO_PKG_VERSION");

/// How long open connections get to finish after SIGTERM or SIGINT.
const SHUTDOWN_GRACE: Duration = Duration::from_secs(10);

/// How often traffic totals are written to the stats file.
const STATS_FLUSH_INTERVAL: Duration = Duration::from_secs(60);

fn main() -> ExitCode {
    let mut args: Vec<String> = std::env::args().skip(1).collect();
    let stats_command = args.first().is_some_and(|a| a == "stats");
    if stats_command {
        args.remove(0);
    }
    let cfg = match config::from_sources(&args, std::env::vars()) {
        Ok(cfg) => cfg,
        Err(config::Error::Help) => {
            print!("{}", config::USAGE);
            return ExitCode::SUCCESS;
        }
        Err(config::Error::Version) => {
            println!("proxemby {VERSION}");
            return ExitCode::SUCCESS;
        }
        Err(e) => {
            eprintln!("{e}");
            return ExitCode::FAILURE;
        }
    };
    if stats_command {
        return show_stats(&cfg);
    }
    let _ = rustls::crypto::ring::default_provider().install_default();
    let logger = Logger::new(cfg.logging, Box::new(std::io::stderr()));

    let runtime = match tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
    {
        Ok(runtime) => runtime,
        Err(e) => {
            eprintln!("start runtime: {e}");
            return ExitCode::FAILURE;
        }
    };
    match runtime.block_on(run(cfg, logger.clone())) {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            error!(logger, "proxemby server failed", "error" => e);
            ExitCode::FAILURE
        }
    }
}

/// `proxemby stats`: prints the running proxy's traffic statistics.
fn show_stats(cfg: &Config) -> ExitCode {
    if !cfg.stats_enable {
        eprintln!("stats are disabled: set [stats] enable = true in the config");
        return ExitCode::FAILURE;
    }
    if cfg.stats_addr.is_empty() {
        eprintln!("the stats listener is disabled: set [stats] addr in the config");
        return ExitCode::FAILURE;
    }
    match stats::fetch_report(&stats::admin_connect_addr(&cfg.stats_addr)) {
        Ok(report) => {
            print!("{}", stats::render_table(&report));
            ExitCode::SUCCESS
        }
        Err(e) => {
            eprintln!("{e}");
            ExitCode::FAILURE
        }
    }
}

async fn run(cfg: Config, logger: Logger) -> Result<(), String> {
    info!(logger, "proxemby starting", "version" => VERSION);
    for route in &cfg.routes {
        info!(logger, "proxemby route configured",
            "public_url" => route.public_url.to_string(),
            "upstream_url" => route.upstream_url.to_string(),
            "acme_domain" => route.acme_domain.as_str(),
        );
    }

    let mut store = None;
    if !cfg.allowed_users.is_empty() {
        let path = (!cfg.auth_state_file.is_empty()).then(|| Path::new(&cfg.auth_state_file));
        store = Some(Arc::new(
            Store::open(path).map_err(|e| format!("proxemby auth state failed: {e}"))?,
        ));
        info!(logger, "proxemby allowed users configured",
            "users" => &cfg.allowed_users,
            "state_file" => cfg.auth_state_file.as_str(),
        );
    }

    let mut stats = None;
    if cfg.stats_enable {
        let path = (!cfg.stats_file.is_empty()).then(|| Path::new(&cfg.stats_file));
        stats = Some(Arc::new(Stats::open(&server::route_names(&cfg), path)?));
    }
    let proxy = Server::with_stats(&cfg, logger.clone(), store, stats.clone());
    let http_listener = server::bind(&cfg.http_addr)
        .await
        .map_err(|e| format!("listen {}: {e}", cfg.http_addr))?;
    info!(logger, "proxemby listening", "scheme" => "http", "addr" => cfg.http_addr.as_str());
    let shutdown = Shutdown::new();
    let mut servers = tokio::task::JoinSet::new();
    servers.spawn(server::serve_http(
        http_listener,
        proxy.clone(),
        shutdown.signal(),
    ));
    if let Some(stats) = &stats {
        if !cfg.stats_addr.is_empty() {
            let admin = server::bind(&cfg.stats_addr)
                .await
                .map_err(|e| format!("listen {}: {e}", cfg.stats_addr))?;
            info!(logger, "proxemby stats listening", "addr" => cfg.stats_addr.as_str(), "file" => cfg.stats_file.as_str());
            servers.spawn(server::serve_admin(admin, stats.clone(), shutdown.signal()));
        }
        tokio::spawn(flush_stats(
            stats.clone(),
            shutdown.signal(),
            logger.clone(),
        ));
    }
    if cfg.tls_enable {
        serve_tls(&cfg, proxy, &shutdown, &mut servers, &logger).await?;
    }

    let result = tokio::select! {
        signal = wait_for_signal() => {
            info!(logger, "proxemby shutting down", "signal" => signal, "grace" => SHUTDOWN_GRACE);
            Ok(())
        }
        Some(joined) = servers.join_next() => match joined {
            Ok(Ok(())) => Ok(()),
            Ok(Err(e)) => Err(e.to_string()),
            Err(e) => Err(e.to_string()),
        },
    };
    if !shutdown.shutdown(SHUTDOWN_GRACE).await {
        warn!(
            logger,
            "proxemby closed connections still open after the grace period"
        );
    }
    if let Some(Err(e)) = stats.map(|stats| stats.flush()) {
        warn!(logger, "proxemby stats flush failed", "error" => e);
    }
    result
}

/// Writes traffic totals to the stats file once a minute until shutdown.
async fn flush_stats(stats: Arc<Stats>, signal: ShutdownSignal, logger: Logger) {
    let mut interval = tokio::time::interval(STATS_FLUSH_INTERVAL);
    interval.tick().await;
    loop {
        tokio::select! {
            _ = interval.tick() => {}
            _ = signal.triggered() => return,
        }
        // Writing the file is blocking I/O; keep it off the proxy's threads.
        let stats = stats.clone();
        match tokio::task::spawn_blocking(move || stats.flush()).await {
            Ok(Ok(())) => {}
            Ok(Err(e)) => warn!(logger, "proxemby stats flush failed", "error" => e),
            Err(e) => warn!(logger, "proxemby stats flush failed", "error" => e.to_string()),
        }
    }
}

async fn serve_tls(
    cfg: &Config,
    proxy: Arc<Server>,
    shutdown: &Shutdown,
    servers: &mut tokio::task::JoinSet<std::io::Result<()>>,
    logger: &Logger,
) -> Result<(), String> {
    let mut acme =
        AcmeConfig::new_with_client_config(&cfg.acme_domains, Arc::new(server::client_config()))
            .cache(DirCache::new(cfg.acme_cache_dir.clone()))
            .directory_lets_encrypt(true);
    if !cfg.acme_email.is_empty() {
        acme = acme.contact_push(format!("mailto:{}", cfg.acme_email));
    }
    let mut state = acme.state();
    let challenge = state.challenge_rustls_config();
    let tls_config = Arc::new(server::server_config(state.resolver()));
    info!(logger, "proxemby tls acme configured",
        "domains" => &cfg.acme_domains,
        "cache_dir" => cfg.acme_cache_dir.as_str(),
    );
    let acme_logger = logger.clone();
    tokio::spawn(async move {
        while let Some(event) = state.next().await {
            match event {
                Ok(ok) => info!(acme_logger, "acme event", "event" => format!("{ok:?}")),
                Err(err) => error!(acme_logger, "acme error", "error" => format!("{err:?}")),
            }
        }
    });

    let tls_listener = server::bind(&cfg.tls_addr)
        .await
        .map_err(|e| format!("listen {}: {e}", cfg.tls_addr))?;
    info!(logger, "proxemby listening", "scheme" => "https", "addr" => cfg.tls_addr.as_str());
    servers.spawn(server::serve_tls(
        tls_listener,
        proxy,
        tls_config,
        Some(challenge),
        shutdown.signal(),
    ));
    Ok(())
}

/// Waits for SIGTERM (systemd, Docker) or SIGINT (Ctrl-C).
async fn wait_for_signal() -> &'static str {
    #[cfg(unix)]
    {
        use tokio::signal::unix::{SignalKind, signal};
        if let Ok(mut terminate) = signal(SignalKind::terminate()) {
            return tokio::select! {
                _ = terminate.recv() => "SIGTERM",
                _ = tokio::signal::ctrl_c() => "SIGINT",
            };
        }
    }
    let _ = tokio::signal::ctrl_c().await;
    "SIGINT"
}
