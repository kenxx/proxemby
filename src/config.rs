//! Configuration loading: defaults < TOML config < `PROXEMBY_*` environment
//! variables < command-line flags.

use std::collections::{BTreeMap, HashMap, HashSet};

use serde::Deserialize;

use crate::logging;
use crate::util::{HttpUrl, IpPrefix};

const DEFAULT_HTTP_ADDR: &str = ":8080";
const DEFAULT_TLS_ADDR: &str = ":443";
const DEFAULT_ACME_CACHE_DIR: &str = ".acme-cache";
pub const DEFAULT_PLAYBACKINFO_MAX_BYTES: i64 = 8 << 20;
pub const DEFAULT_CONFIG_PATH: &str = "/etc/proxemby/proxemby.toml";

#[derive(Clone, Debug)]
pub struct Route {
    pub upstream_url: HttpUrl,
    pub public_url: HttpUrl,
    pub acme_domain: String,
}

#[derive(Clone, Debug)]
pub struct Config {
    pub routes: Vec<Route>,
    pub http_addr: String,
    pub tls_enable: bool,
    pub tls_addr: String,
    pub acme_domains: Vec<String>,
    pub acme_email: String,
    pub acme_cache_dir: String,
    pub allowed_hosts: Vec<String>,
    pub playbackinfo_max_bytes: i64,
    pub allowed_clients: Vec<IpPrefix>,
    pub trust_proxy_headers: bool,
    pub hide_client: bool,
    pub allowed_users: Vec<String>,
    pub auth_state_file: String,
    pub logging: logging::Config,
}

impl Config {
    /// A config with one route and defaults for everything else.
    pub fn with_routes(routes: Vec<Route>) -> Config {
        Config {
            routes,
            http_addr: DEFAULT_HTTP_ADDR.into(),
            tls_enable: false,
            tls_addr: DEFAULT_TLS_ADDR.into(),
            acme_domains: Vec::new(),
            acme_email: String::new(),
            acme_cache_dir: DEFAULT_ACME_CACHE_DIR.into(),
            allowed_hosts: Vec::new(),
            playbackinfo_max_bytes: DEFAULT_PLAYBACKINFO_MAX_BYTES,
            allowed_clients: Vec::new(),
            trust_proxy_headers: false,
            hide_client: false,
            allowed_users: Vec::new(),
            auth_state_file: String::new(),
            logging: logging::Config::default(),
        }
    }
}

#[derive(Debug)]
pub enum Error {
    /// `--help` was requested.
    Help,
    Invalid(String),
}

impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Error::Help => f.write_str("help requested"),
            Error::Invalid(msg) => f.write_str(msg),
        }
    }
}

impl From<String> for Error {
    fn from(msg: String) -> Self {
        Error::Invalid(msg)
    }
}

impl From<&str> for Error {
    fn from(msg: &str) -> Self {
        Error::Invalid(msg.to_owned())
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
struct RouteValues {
    upstream_url: String,
    public_url: String,
    acme_domain: String,
}

#[derive(Clone, Debug)]
struct Values {
    routes: Vec<RouteValues>,
    http_addr: String,
    tls_enable: bool,
    tls_addr: String,
    acme_email: String,
    acme_cache_dir: String,
    allowed_hosts: Vec<String>,
    playbackinfo_max_bytes: i64,
    allowed_clients: Vec<String>,
    trust_proxy_headers: bool,
    hide_client: bool,
    allowed_users: Vec<String>,
    auth_state_file: String,
    log_level: String,
    log_format: String,
    log_time: bool,
}

#[derive(Clone, Debug, Default)]
struct Raw {
    routes: Option<Vec<RouteValues>>,
    http_addr: Option<String>,
    tls_enable: Option<bool>,
    tls_addr: Option<String>,
    acme_email: Option<String>,
    acme_cache_dir: Option<String>,
    allowed_hosts: Option<Vec<String>>,
    playbackinfo_max_bytes: Option<i64>,
    allowed_clients: Option<Vec<String>>,
    trust_proxy_headers: Option<bool>,
    hide_client: Option<bool>,
    allowed_users: Option<Vec<String>>,
    auth_state_file: Option<String>,
    debug: Option<bool>,
    log_level: Option<String>,
    log_format: Option<String>,
    log_time: Option<bool>,
}

impl Default for Values {
    fn default() -> Self {
        Values {
            routes: Vec::new(),
            http_addr: DEFAULT_HTTP_ADDR.into(),
            tls_enable: false,
            tls_addr: DEFAULT_TLS_ADDR.into(),
            acme_email: String::new(),
            acme_cache_dir: DEFAULT_ACME_CACHE_DIR.into(),
            allowed_hosts: Vec::new(),
            playbackinfo_max_bytes: DEFAULT_PLAYBACKINFO_MAX_BYTES,
            allowed_clients: Vec::new(),
            trust_proxy_headers: false,
            hide_client: false,
            allowed_users: Vec::new(),
            auth_state_file: String::new(),
            log_level: logging::DEFAULT_LEVEL.into(),
            log_format: logging::DEFAULT_FORMAT.into(),
            log_time: true,
        }
    }
}

fn value_or_default(value: &str, fallback: &str) -> String {
    let value = value.trim();
    if value.is_empty() {
        fallback.to_owned()
    } else {
        value.to_owned()
    }
}

fn clean_strings(values: &[String]) -> Vec<String> {
    values
        .iter()
        .map(|v| v.trim())
        .filter(|v| !v.is_empty())
        .map(str::to_owned)
        .collect()
}

fn split_csv(raw: &str) -> Vec<String> {
    raw.split(',')
        .map(str::trim)
        .filter(|v| !v.is_empty())
        .map(str::to_owned)
        .collect()
}

fn parse_bool(raw: &str) -> bool {
    matches!(
        raw.trim().to_ascii_lowercase().as_str(),
        "1" | "t" | "true" | "y" | "yes" | "on"
    )
}

fn parse_positive_int(raw: &str, name: &str) -> Result<i64, Error> {
    match raw.trim().parse::<i64>() {
        Ok(value) if value > 0 => Ok(value),
        _ => Err(format!("{name} must be a positive integer").into()),
    }
}

impl Values {
    fn apply_raw(&mut self, raw: Raw) {
        if let Some(routes) = raw.routes {
            self.routes = routes;
        }
        if let Some(v) = raw.http_addr {
            self.http_addr = value_or_default(&v, DEFAULT_HTTP_ADDR);
        }
        if let Some(v) = raw.tls_enable {
            self.tls_enable = v;
        }
        if let Some(v) = raw.tls_addr {
            self.tls_addr = value_or_default(&v, DEFAULT_TLS_ADDR);
        }
        if let Some(v) = raw.acme_email {
            self.acme_email = v.trim().to_owned();
        }
        if let Some(v) = raw.acme_cache_dir {
            self.acme_cache_dir = value_or_default(&v, DEFAULT_ACME_CACHE_DIR);
        }
        if let Some(v) = raw.allowed_hosts {
            self.allowed_hosts = clean_strings(&v);
        }
        if let Some(v) = raw.playbackinfo_max_bytes {
            self.playbackinfo_max_bytes = v;
        }
        if let Some(v) = raw.allowed_clients {
            self.allowed_clients = clean_strings(&v);
        }
        if let Some(v) = raw.trust_proxy_headers {
            self.trust_proxy_headers = v;
        }
        if let Some(v) = raw.hide_client {
            self.hide_client = v;
        }
        if let Some(v) = raw.allowed_users {
            self.allowed_users = clean_strings(&v);
        }
        if let Some(v) = raw.auth_state_file {
            self.auth_state_file = v.trim().to_owned();
        }
        if let Some(debug) = raw.debug {
            self.log_level = if debug {
                "debug".into()
            } else {
                logging::DEFAULT_LEVEL.into()
            };
        }
        if let Some(v) = raw.log_level {
            self.log_level = v.trim().to_owned();
        }
        if let Some(v) = raw.log_format {
            self.log_format = v.trim().to_owned();
        }
        if let Some(v) = raw.log_time {
            self.log_time = v;
        }
    }

    fn apply_env(&mut self, env: &HashMap<String, String>) -> Result<(), Error> {
        let non_empty = |name: &str| {
            env.get(name)
                .map(|v| v.trim().to_owned())
                .filter(|v| !v.is_empty())
        };
        let boolean = |name: &str| env.get(name).map(|v| parse_bool(v));
        let mut raw = Raw::default();
        if let Some(v) = non_empty("PROXEMBY_ROUTE") {
            raw.routes = Some(parse_route_values(&v)?);
        }
        raw.http_addr = non_empty("PROXEMBY_HTTP_ADDR");
        raw.tls_enable = boolean("PROXEMBY_TLS_ENABLE");
        raw.tls_addr = non_empty("PROXEMBY_TLS_ADDR");
        raw.acme_email = non_empty("PROXEMBY_ACME_EMAIL");
        raw.acme_cache_dir = non_empty("PROXEMBY_ACME_CACHE_DIR");
        raw.allowed_hosts = non_empty("PROXEMBY_ALLOWED_HOSTS").map(|v| split_csv(&v));
        if let Some(v) = non_empty("PROXEMBY_PLAYBACKINFO_MAX_BYTES") {
            raw.playbackinfo_max_bytes =
                Some(parse_positive_int(&v, "PROXEMBY_PLAYBACKINFO_MAX_BYTES")?);
        }
        raw.allowed_clients = non_empty("PROXEMBY_ALLOWED_CLIENTS").map(|v| split_csv(&v));
        raw.trust_proxy_headers = boolean("PROXEMBY_TRUST_PROXY_HEADERS");
        raw.hide_client = boolean("PROXEMBY_HIDE_CLIENT");
        raw.allowed_users = non_empty("PROXEMBY_ALLOWED_USERS").map(|v| split_csv(&v));
        raw.auth_state_file = non_empty("PROXEMBY_AUTH_STATE_FILE");
        raw.debug = boolean("PROXEMBY_DEBUG");
        raw.log_level = non_empty("PROXEMBY_LOG_LEVEL");
        raw.log_format = non_empty("PROXEMBY_LOG_FORMAT");
        raw.log_time = boolean("PROXEMBY_LOG_TIME");
        self.apply_raw(raw);
        Ok(())
    }

    fn config(self) -> Result<Config, Error> {
        if self.playbackinfo_max_bytes <= 0 {
            return Err("playbackinfo max bytes must be a positive integer".into());
        }
        let allowed_clients = self
            .allowed_clients
            .iter()
            .map(|v| {
                IpPrefix::parse(v)
                    .map_err(|e| format!("allowed clients contains invalid value {v:?}: {e}"))
            })
            .collect::<Result<Vec<_>, _>>()?;
        let (routes, acme_domains) = parse_routes(&self.routes)?;
        if self.tls_enable && acme_domains.is_empty() {
            return Err("ACME domains are required when TLS is enabled".into());
        }
        let logging = logging::parse_config(&self.log_level, &self.log_format, self.log_time)?;
        Ok(Config {
            routes,
            http_addr: self.http_addr,
            tls_enable: self.tls_enable,
            tls_addr: self.tls_addr,
            acme_domains,
            acme_email: self.acme_email.trim().to_owned(),
            acme_cache_dir: self.acme_cache_dir,
            allowed_hosts: clean_strings(&self.allowed_hosts),
            playbackinfo_max_bytes: self.playbackinfo_max_bytes,
            allowed_clients,
            trust_proxy_headers: self.trust_proxy_headers,
            hide_client: self.hide_client,
            allowed_users: clean_strings(&self.allowed_users),
            auth_state_file: self.auth_state_file.trim().to_owned(),
            logging,
        })
    }
}

fn parse_required_url(raw: &str, name: &str) -> Result<HttpUrl, Error> {
    let raw = raw.trim();
    if raw.is_empty() {
        return Err(format!("{name} is required").into());
    }
    let url = HttpUrl::parse(raw).map_err(|e| format!("{name} is invalid: {e}"))?;
    if url.scheme != "http" && url.scheme != "https" {
        return Err(format!("{name} must use http or https").into());
    }
    if url.host.is_empty() {
        return Err(format!("{name} must include a host").into());
    }
    Ok(url)
}

fn parse_routes(values: &[RouteValues]) -> Result<(Vec<Route>, Vec<String>), Error> {
    if values.is_empty() {
        return Err("at least one route is required".into());
    }
    let mut routes = Vec::with_capacity(values.len());
    let mut acme_domains = Vec::with_capacity(values.len());
    let mut seen_public_hosts = HashSet::new();
    let mut seen_acme_domains = HashSet::new();
    for (i, value) in values.iter().enumerate() {
        let name = format!("route {}", i + 1);
        let upstream_url =
            parse_required_url(&value.upstream_url, &format!("{name} upstream URL"))?;
        let public_url = parse_required_url(&value.public_url, &format!("{name} public URL"))?;
        let public_host = public_url.hostname().to_ascii_lowercase();
        if !seen_public_hosts.insert(public_host.clone()) {
            return Err(format!("duplicate route public host {public_host:?}").into());
        }
        let mut acme_domain = value.acme_domain.trim().to_owned();
        if acme_domain.is_empty() {
            acme_domain = public_host;
        }
        let acme_domain = acme_domain.to_ascii_lowercase();
        if seen_acme_domains.insert(acme_domain.clone()) {
            acme_domains.push(acme_domain.clone());
        }
        routes.push(Route {
            upstream_url,
            public_url,
            acme_domain,
        });
    }
    Ok((routes, acme_domains))
}

fn parse_route_values(raw: &str) -> Result<Vec<RouteValues>, Error> {
    let mut routes = Vec::new();
    for entry in raw.split(';').map(str::trim).filter(|e| !e.is_empty()) {
        let parts: Vec<&str> = entry.split(',').map(str::trim).collect();
        if parts.len() != 2 && parts.len() != 3 {
            return Err(
                format!("route {entry:?} must have upstream_url,public_url[,acme_domain]").into(),
            );
        }
        if parts[0].is_empty() || parts[1].is_empty() {
            return Err(format!("route {entry:?} must include upstream_url and public_url").into());
        }
        let mut route = RouteValues {
            upstream_url: parts[0].to_owned(),
            public_url: parts[1].to_owned(),
            acme_domain: String::new(),
        };
        if parts.len() == 3 {
            if parts[2].is_empty() {
                return Err(format!("route {entry:?} has empty acme_domain").into());
            }
            route.acme_domain = parts[2].to_owned();
        }
        routes.push(route);
    }
    if routes.is_empty() {
        return Err("at least one route is required".into());
    }
    Ok(routes)
}

#[derive(Deserialize, Default)]
struct TomlConfig {
    routes: Option<Vec<TomlRoute>>,
    #[serde(default)]
    server: TomlServer,
    #[serde(default)]
    tls: TomlTls,
    #[serde(default)]
    proxy: TomlProxy,
    #[serde(default)]
    clients: TomlClients,
    #[serde(default)]
    auth: TomlAuth,
    #[serde(default)]
    logging: TomlLogging,
}

#[derive(Deserialize, Default)]
struct TomlRoute {
    #[serde(default)]
    upstream_url: String,
    #[serde(default)]
    public_url: String,
    #[serde(default)]
    acme_domain: String,
}

#[derive(Deserialize, Default)]
struct TomlServer {
    http_addr: Option<String>,
}

#[derive(Deserialize, Default)]
struct TomlTls {
    enable: Option<bool>,
    addr: Option<String>,
    acme_email: Option<String>,
    acme_cache_dir: Option<String>,
}

#[derive(Deserialize, Default)]
struct TomlProxy {
    allowed_hosts: Option<Vec<String>>,
    playbackinfo_max_bytes: Option<i64>,
    hide_client: Option<bool>,
}

#[derive(Deserialize, Default)]
struct TomlClients {
    allowed: Option<Vec<String>>,
    trust_proxy_headers: Option<bool>,
}

#[derive(Deserialize, Default)]
struct TomlAuth {
    allowed_users: Option<Vec<String>>,
    state_file: Option<String>,
}

#[derive(Deserialize, Default)]
struct TomlLogging {
    debug: Option<bool>,
    level: Option<String>,
    format: Option<String>,
    time: Option<bool>,
}

fn raw_from_toml_file(path: &str) -> Result<Raw, std::io::Error> {
    let text = std::fs::read_to_string(path)?;
    let cfg: TomlConfig = toml::from_str(&text)
        .map_err(|e| std::io::Error::new(std::io::ErrorKind::InvalidData, e.to_string()))?;
    Ok(Raw {
        routes: cfg.routes.map(|routes| {
            routes
                .into_iter()
                .map(|r| RouteValues {
                    upstream_url: r.upstream_url,
                    public_url: r.public_url,
                    acme_domain: r.acme_domain,
                })
                .collect()
        }),
        http_addr: cfg.server.http_addr,
        tls_enable: cfg.tls.enable,
        tls_addr: cfg.tls.addr,
        acme_email: cfg.tls.acme_email,
        acme_cache_dir: cfg.tls.acme_cache_dir,
        allowed_hosts: cfg.proxy.allowed_hosts,
        playbackinfo_max_bytes: cfg.proxy.playbackinfo_max_bytes,
        allowed_clients: cfg.clients.allowed,
        trust_proxy_headers: cfg.clients.trust_proxy_headers,
        hide_client: cfg.proxy.hide_client,
        allowed_users: cfg.auth.allowed_users,
        auth_state_file: cfg.auth.state_file,
        debug: cfg.logging.debug,
        log_level: cfg.logging.level,
        log_format: cfg.logging.format,
        log_time: cfg.logging.time,
    })
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum FlagKind {
    Str,
    Bool,
    Int,
}

/// (flag names, canonical key, kind)
const FLAGS: &[(&[&str], &str, FlagKind)] = &[
    (&["c", "config"], "config", FlagKind::Str),
    (&["route"], "route", FlagKind::Str),
    (&["h", "http-addr"], "http-addr", FlagKind::Str),
    (&["tls-enable"], "tls-enable", FlagKind::Bool),
    (&["tls-addr"], "tls-addr", FlagKind::Str),
    (&["acme-email"], "acme-email", FlagKind::Str),
    (&["acme-cache-dir"], "acme-cache-dir", FlagKind::Str),
    (&["a", "allowed-hosts"], "allowed-hosts", FlagKind::Str),
    (
        &["playbackinfo-max-bytes"],
        "playbackinfo-max-bytes",
        FlagKind::Int,
    ),
    (&["allowed-clients"], "allowed-clients", FlagKind::Str),
    (
        &["trust-proxy-headers"],
        "trust-proxy-headers",
        FlagKind::Bool,
    ),
    (&["hide-client"], "hide-client", FlagKind::Bool),
    (&["allowed-users"], "allowed-users", FlagKind::Str),
    (&["auth-state-file"], "auth-state-file", FlagKind::Str),
    (&["d", "debug"], "debug", FlagKind::Bool),
    (&["log-level"], "log-level", FlagKind::Str),
    (&["log-format"], "log-format", FlagKind::Str),
    (&["log-time"], "log-time", FlagKind::Bool),
    (&["help"], "help", FlagKind::Bool),
];

fn parse_go_bool(raw: &str) -> Option<bool> {
    match raw {
        "1" | "t" | "T" | "TRUE" | "true" | "True" => Some(true),
        "0" | "f" | "F" | "FALSE" | "false" | "False" => Some(false),
        _ => None,
    }
}

struct Cli {
    raw: Raw,
    config_path: Option<String>,
    help: bool,
}

/// Parses flags with the same rules as Go's `flag` package: `-name` and
/// `--name` are equivalent, values follow `=` or the next argument, and
/// parsing stops at the first non-flag argument.
fn parse_flags(args: &[String]) -> Result<Cli, Error> {
    if args.iter().any(|a| a == "-help" || a.starts_with("-help=")) {
        return Err("use --help for help; -h is --http-addr".into());
    }
    let mut values: BTreeMap<&str, String> = BTreeMap::new();
    let mut routes: Vec<String> = Vec::new();
    let mut i = 0;
    while i < args.len() {
        let arg = &args[i];
        if arg.len() < 2 || !arg.starts_with('-') {
            break;
        }
        i += 1;
        let name = if let Some(rest) = arg.strip_prefix("--") {
            if rest.is_empty() {
                break;
            }
            rest
        } else {
            &arg[1..]
        };
        if name.starts_with('-') || name.starts_with('=') {
            return Err(format!("bad flag syntax: {arg}").into());
        }
        let (name, inline) = match name.split_once('=') {
            Some((n, v)) => (n, Some(v.to_owned())),
            None => (name, None),
        };
        let Some((_, key, kind)) = FLAGS.iter().find(|(names, _, _)| names.contains(&name)) else {
            return Err(format!("flag provided but not defined: -{name}").into());
        };
        let value = match (kind, inline) {
            (FlagKind::Bool, Some(v)) => {
                parse_go_bool(&v).ok_or_else(|| {
                    format!("invalid boolean value {v:?} for -{name}: parse error")
                })?;
                v
            }
            (FlagKind::Bool, None) => "true".to_owned(),
            (_, Some(v)) => v,
            (_, None) => {
                let v = args
                    .get(i)
                    .ok_or_else(|| format!("flag needs an argument: -{name}"))?
                    .clone();
                i += 1;
                v
            }
        };
        if *kind == FlagKind::Int && value.trim().parse::<i64>().is_err() {
            return Err(format!("invalid value {value:?} for flag -{name}: parse error").into());
        }
        if *key == "route" {
            routes.push(value.clone());
        }
        values.insert(key, value);
    }
    if let Some(extra) = args.get(i) {
        let extra = if extra == "--" {
            args.get(i + 1)
        } else {
            Some(extra)
        };
        if let Some(extra) = extra {
            return Err(format!("unexpected argument {extra:?}").into());
        }
    }

    let mut cli = Cli {
        raw: Raw::default(),
        config_path: None,
        help: false,
    };
    let as_bool = |v: &String| parse_go_bool(v).unwrap_or(false);
    for (key, value) in values {
        let raw = &mut cli.raw;
        match key {
            "config" => cli.config_path = Some(value),
            "route" => raw.routes = Some(parse_route_values(&routes.join(";"))?),
            "http-addr" => raw.http_addr = Some(value),
            "tls-enable" => raw.tls_enable = Some(as_bool(&value)),
            "tls-addr" => raw.tls_addr = Some(value),
            "acme-email" => raw.acme_email = Some(value),
            "acme-cache-dir" => raw.acme_cache_dir = Some(value),
            "allowed-hosts" => raw.allowed_hosts = Some(split_csv(&value)),
            "playbackinfo-max-bytes" => raw.playbackinfo_max_bytes = value.trim().parse().ok(),
            "allowed-clients" => raw.allowed_clients = Some(split_csv(&value)),
            "trust-proxy-headers" => raw.trust_proxy_headers = Some(as_bool(&value)),
            "hide-client" => raw.hide_client = Some(as_bool(&value)),
            "allowed-users" => raw.allowed_users = Some(split_csv(&value)),
            "auth-state-file" => raw.auth_state_file = Some(value),
            "debug" => raw.debug = Some(as_bool(&value)),
            "log-level" => raw.log_level = Some(value),
            "log-format" => raw.log_format = Some(value),
            "log-time" => raw.log_time = Some(as_bool(&value)),
            "help" => cli.help = as_bool(&value),
            _ => {}
        }
    }
    Ok(cli)
}

fn env_map<I, K, V>(env: I) -> HashMap<String, String>
where
    I: IntoIterator<Item = (K, V)>,
    K: Into<String>,
    V: Into<String>,
{
    env.into_iter().map(|(k, v)| (k.into(), v.into())).collect()
}

/// Builds a config from environment variables only.
pub fn from_env_map<I, K, V>(env: I) -> Result<Config, Error>
where
    I: IntoIterator<Item = (K, V)>,
    K: Into<String>,
    V: Into<String>,
{
    let mut values = Values::default();
    values.apply_env(&env_map(env))?;
    values.config()
}

/// Builds a config from command-line arguments (without the program name),
/// environment variables and the TOML config file.
pub fn from_sources<I, K, V>(args: &[String], env: I) -> Result<Config, Error>
where
    I: IntoIterator<Item = (K, V)>,
    K: Into<String>,
    V: Into<String>,
{
    from_sources_with_default(args, env, DEFAULT_CONFIG_PATH)
}

fn from_sources_with_default<I, K, V>(
    args: &[String],
    env: I,
    default_path: &str,
) -> Result<Config, Error>
where
    I: IntoIterator<Item = (K, V)>,
    K: Into<String>,
    V: Into<String>,
{
    let cli = parse_flags(args)?;
    if cli.help {
        return Err(Error::Help);
    }
    let mut values = Values::default();
    let explicit = cli.config_path.is_some();
    let path = cli
        .config_path
        .as_deref()
        .map(str::trim)
        .unwrap_or(default_path);
    if !path.is_empty() {
        match raw_from_toml_file(path) {
            Ok(raw) => values.apply_raw(raw),
            // The default config file is optional so env-only and CLI-only runs keep working.
            Err(e) if !explicit && e.kind() == std::io::ErrorKind::NotFound => {}
            Err(e) => return Err(format!("load config {path}: {e}").into()),
        }
    }
    values.apply_env(&env_map(env))?;
    values.apply_raw(cli.raw);
    values.config()
}

pub const USAGE: &str = "Usage:
  proxemby [options]

Options:
  -c, --config PATH                  Config file path (default /etc/proxemby/proxemby.toml)
      --route ROUTE                  Route as upstream_url,public_url[,acme_domain]; may be repeated
  -h, --http-addr ADDR               HTTP listen address
  -a, --allowed-hosts HOSTS          Comma-separated resource proxy host allowlist
  -d, --debug                        Enable debug logging (same as --log-level debug)
      --tls-enable                   Enable built-in HTTPS with ACME
      --tls-addr ADDR                HTTPS listen address
      --acme-email EMAIL             ACME account email
      --acme-cache-dir DIR           ACME certificate cache directory
      --playbackinfo-max-bytes N     Maximum PlaybackInfo JSON body size
      --allowed-clients CLIENTS      Comma-separated client IP/CIDR allowlist
      --trust-proxy-headers          Trust X-Forwarded-For/X-Real-IP for client checks
      --hide-client                  Hide client identity headers from upstream
      --allowed-users USERS          Comma-separated upstream Emby usernames allowed to log in
      --auth-state-file PATH         File used to persist login sessions
      --log-level LEVEL              Log level: debug, info, warn, or error
      --log-format FORMAT            Log format: text or json
      --log-time                     Include time in log output
      --help                         Show this help
";

#[cfg(test)]
mod tests {
    use super::*;
    use crate::logging::{Format, Level};

    fn args(list: &[&str]) -> Vec<String> {
        list.iter().map(|s| s.to_string()).collect()
    }

    const NO_ENV: [(&str, &str); 0] = [];

    fn write_config(content: &str) -> String {
        let mut suffix = [0u8; 8];
        getrandom::fill(&mut suffix).unwrap();
        let path =
            std::env::temp_dir().join(format!("proxemby-test-{}.toml", u64::from_le_bytes(suffix)));
        std::fs::write(&path, content).unwrap();
        path.to_string_lossy().into_owned()
    }

    fn sources(list: &[&str], env: &[(&str, &str)]) -> Result<Config, Error> {
        from_sources_with_default(&args(list), env.iter().copied(), "")
    }

    #[test]
    fn env_requires_route() {
        assert!(from_env_map(NO_ENV).is_err());
    }

    #[test]
    fn env_defaults_and_allowed_hosts() {
        let cfg = from_env_map([
            ("PROXEMBY_ROUTE", "https://us.emby.com,http://proxemby"),
            (
                "PROXEMBY_ALLOWED_HOSTS",
                "vod.us.emby.com, cdn.example.com ",
            ),
        ])
        .unwrap();
        assert_eq!(cfg.routes.len(), 1);
        assert_eq!(
            cfg.routes[0].upstream_url.to_string(),
            "https://us.emby.com"
        );
        assert_eq!(cfg.routes[0].public_url.to_string(), "http://proxemby");
        assert_eq!(cfg.routes[0].acme_domain, "proxemby");
        assert_eq!(cfg.http_addr, ":8080");
        assert_eq!(cfg.tls_addr, ":443");
        assert_eq!(cfg.acme_cache_dir, ".acme-cache");
        assert_eq!(cfg.playbackinfo_max_bytes, DEFAULT_PLAYBACKINFO_MAX_BYTES);
        assert!(cfg.allowed_clients.is_empty());
        assert!(!cfg.hide_client);
        assert_eq!(cfg.logging, crate::logging::Config::default());
        assert_eq!(
            cfg.allowed_hosts,
            vec!["vod.us.emby.com", "cdn.example.com"]
        );
    }

    #[test]
    fn env_multiple_routes() {
        let cfg = from_env_map([("PROXEMBY_ROUTE", "https://us.emby.com,https://proxemby.jp,cdn.proxemby.jp;https://us2.emby.com,https://proxemby2.jp")]).unwrap();
        assert_eq!(cfg.routes.len(), 2);
        assert_eq!(cfg.routes[0].acme_domain, "cdn.proxemby.jp");
        assert_eq!(cfg.routes[1].acme_domain, "proxemby2.jp");
        assert_eq!(cfg.acme_domains, vec!["cdn.proxemby.jp", "proxemby2.jp"]);
    }

    #[test]
    fn env_logging() {
        let cfg = from_env_map([
            ("PROXEMBY_ROUTE", "https://us.emby.com,http://proxemby"),
            ("PROXEMBY_DEBUG", "true"),
            ("PROXEMBY_LOG_LEVEL", "warn"),
            ("PROXEMBY_LOG_FORMAT", "json"),
            ("PROXEMBY_LOG_TIME", "false"),
        ])
        .unwrap();
        assert_eq!(cfg.logging.level, Level::Warn);
        assert_eq!(cfg.logging.format, Format::Json);
        assert!(!cfg.logging.time);

        let cfg = from_env_map([
            ("PROXEMBY_ROUTE", "https://us.emby.com,http://proxemby"),
            ("PROXEMBY_DEBUG", "true"),
        ])
        .unwrap();
        assert_eq!(cfg.logging.level, Level::Debug);
    }

    #[test]
    fn env_hide_client_allowed_clients_and_users() {
        let cfg = from_env_map([
            ("PROXEMBY_ROUTE", "https://us.emby.com,http://proxemby"),
            ("PROXEMBY_HIDE_CLIENT", "true"),
        ])
        .unwrap();
        assert!(cfg.hide_client);

        let cfg = from_env_map([
            ("PROXEMBY_ROUTE", "https://us.emby.com,http://proxemby"),
            ("PROXEMBY_ALLOWED_CLIENTS", "1.2.3.4, 192.168.0.0/24"),
            ("PROXEMBY_TRUST_PROXY_HEADERS", "true"),
        ])
        .unwrap();
        assert_eq!(cfg.allowed_clients.len(), 2);
        assert!(cfg.trust_proxy_headers);
        assert!(
            from_env_map([
                ("PROXEMBY_ROUTE", "https://us.emby.com,http://proxemby"),
                ("PROXEMBY_ALLOWED_CLIENTS", "not-an-ip")
            ])
            .is_err()
        );

        let cfg = from_env_map([
            ("PROXEMBY_ROUTE", "https://us.emby.com,http://proxemby"),
            ("PROXEMBY_ALLOWED_USERS", "ken, alice"),
            ("PROXEMBY_AUTH_STATE_FILE", "/tmp/auth.json"),
        ])
        .unwrap();
        assert_eq!(cfg.allowed_users, vec!["ken", "alice"]);
        assert_eq!(cfg.auth_state_file, "/tmp/auth.json");
    }

    #[test]
    fn explicit_missing_config_errors() {
        assert!(
            sources(
                &[
                    "-c",
                    "/nonexistent/missing.toml",
                    "--route",
                    "https://cli.emby.com,http://proxemby"
                ],
                &[]
            )
            .is_err()
        );
    }

    #[test]
    fn loads_sectioned_toml() {
        let path = write_config(
            r#"
[[routes]]
upstream_url = "https://toml.emby.com"
public_url = "https://proxemby.example.com"
acme_domain = "cert.example.com"

[[routes]]
upstream_url = "https://toml2.emby.com"
public_url = "https://proxemby2.example.com"

[server]
http_addr = ":9090"

[tls]
enable = true
addr = ":9443"
acme_email = "ops@example.com"
acme_cache_dir = "/tmp/proxemby-acme"

[proxy]
allowed_hosts = ["vod.example.com", "cdn.example.com"]
playbackinfo_max_bytes = 2048
hide_client = true

[clients]
allowed = ["1.2.3.4", "192.168.0.0/24"]
trust_proxy_headers = true

[auth]
allowed_users = ["ken", " "]
state_file = "/var/lib/proxemby/auth.json"

[logging]
debug = true
level = "error"
format = "json"
time = false
"#,
        );
        let cfg = sources(&["--config", &path], &[]).unwrap();
        std::fs::remove_file(&path).unwrap();
        assert_eq!(cfg.routes.len(), 2);
        assert_eq!(
            cfg.routes[0].upstream_url.to_string(),
            "https://toml.emby.com"
        );
        assert_eq!(cfg.routes[0].acme_domain, "cert.example.com");
        assert_eq!(cfg.routes[1].acme_domain, "proxemby2.example.com");
        assert_eq!(cfg.http_addr, ":9090");
        assert!(cfg.tls_enable);
        assert_eq!(cfg.tls_addr, ":9443");
        assert_eq!(cfg.acme_domains.len(), 2);
        assert_eq!(cfg.acme_email, "ops@example.com");
        assert_eq!(cfg.acme_cache_dir, "/tmp/proxemby-acme");
        assert_eq!(cfg.allowed_hosts.len(), 2);
        assert_eq!(cfg.playbackinfo_max_bytes, 2048);
        assert_eq!(cfg.allowed_clients.len(), 2);
        assert!(cfg.trust_proxy_headers);
        assert_eq!(cfg.allowed_users, vec!["ken"]);
        assert_eq!(cfg.auth_state_file, "/var/lib/proxemby/auth.json");
        assert!(cfg.hide_client);
        assert_eq!(cfg.logging.level, Level::Error);
        assert_eq!(cfg.logging.format, Format::Json);
        assert!(!cfg.logging.time);
    }

    #[test]
    fn precedence() {
        let path = write_config(
            r#"
[[routes]]
upstream_url = "https://toml.emby.com"
public_url = "http://toml-public"

[server]
http_addr = ":8081"

[logging]
level = "warn"
"#,
        );
        let cfg = sources(
            &[
                "--config",
                &path,
                "--route",
                "https://cli.emby.com,http://cli-public",
                "--http-addr",
                ":9090",
                "--debug",
                "--log-level",
                "error",
                "--log-format",
                "json",
                "--log-time=false",
            ],
            &[
                ("PROXEMBY_ROUTE", "https://env.emby.com,http://env-public"),
                ("PROXEMBY_HTTP_ADDR", ":8082"),
                ("PROXEMBY_DEBUG", "false"),
            ],
        )
        .unwrap();
        assert_eq!(
            cfg.routes[0].upstream_url.to_string(),
            "https://cli.emby.com"
        );
        assert_eq!(cfg.routes[0].public_url.to_string(), "http://cli-public");
        assert_eq!(cfg.http_addr, ":9090");
        assert_eq!(cfg.logging.level, Level::Error);
        assert_eq!(cfg.logging.format, Format::Json);
        assert!(!cfg.logging.time);

        // Environment overrides the TOML file when no flag is set.
        let cfg = sources(&["--config", &path], &[("PROXEMBY_HTTP_ADDR", ":8082")]).unwrap();
        std::fs::remove_file(&path).unwrap();
        assert_eq!(cfg.http_addr, ":8082");
        assert_eq!(
            cfg.routes[0].upstream_url.to_string(),
            "https://toml.emby.com"
        );
        assert_eq!(cfg.logging.level, Level::Warn);
    }

    #[test]
    fn short_flags() {
        let path = write_config(
            "[[routes]]\nupstream_url = \"https://toml.emby.com\"\npublic_url = \"http://toml-public\"\n",
        );
        let cfg = sources(
            &[
                "-c",
                &path,
                "--route",
                "https://short.emby.com,http://short-public",
                "-h",
                ":9091",
                "-a",
                "vod.example.com, cdn.example.com",
                "-d",
            ],
            &[],
        )
        .unwrap();
        std::fs::remove_file(&path).unwrap();
        assert_eq!(
            cfg.routes[0].upstream_url.to_string(),
            "https://short.emby.com"
        );
        assert_eq!(cfg.routes[0].public_url.to_string(), "http://short-public");
        assert_eq!(cfg.http_addr, ":9091");
        assert_eq!(cfg.allowed_hosts.len(), 2);
        assert_eq!(cfg.logging.level, Level::Debug);
    }

    #[test]
    fn repeated_route_flags() {
        let cfg = sources(
            &[
                "--route",
                "https://one.emby.com,http://one.example.com",
                "--route",
                "https://two.emby.com,http://two.example.com,two-cert.example.com",
            ],
            &[],
        )
        .unwrap();
        assert_eq!(cfg.routes.len(), 2);
        assert_eq!(cfg.routes[1].acme_domain, "two-cert.example.com");
    }

    #[test]
    fn validation_errors() {
        let invalid = write_config("[[[");
        assert!(sources(&["--config", &invalid], &[]).is_err());
        std::fs::remove_file(&invalid).unwrap();

        for list in [
            &["--route", "ftp://example.com,http://proxemby"][..],
            &["--route", "https://us.emby.com,ftp://proxemby"],
            &["--route", "https://us.emby.com"],
            &["--route", "https://us.emby.com,http://proxemby,"],
            &[
                "--route",
                "https://us.emby.com,http://same.example.com;https://us2.emby.com,http://same.example.com",
            ],
            &[
                "--route",
                "https://us.emby.com,http://proxemby",
                "--allowed-clients",
                "not-an-ip",
            ],
            &[
                "--route",
                "https://us.emby.com,http://proxemby",
                "--playbackinfo-max-bytes",
                "0",
            ],
            &[
                "--route",
                "https://us.emby.com,http://proxemby",
                "--log-level",
                "verbose",
            ],
            &[
                "--route",
                "https://us.emby.com,http://proxemby",
                "--log-format",
                "plain",
            ],
            &["-u", "https://us.emby.com", "-p", "http://proxemby"],
            &["--route", "https://us.emby.com,http://proxemby", "extra"],
            &["--route"],
        ] {
            assert!(sources(list, &[]).is_err(), "{list:?}");
        }
    }

    #[test]
    fn help() {
        assert!(matches!(sources(&["--help"], &[]), Err(Error::Help)));
        assert!(matches!(sources(&["-help"], &[]), Err(Error::Invalid(_))));
    }
}
