//! Traffic statistics per route and request kind.
//!
//! The proxy path only touches atomic counters. A background flush turns the
//! counters into per-day totals and persists them, so daily and monthly usage
//! survives restarts.

use std::collections::BTreeMap;
use std::fmt::Write as _;
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::sync::atomic::{AtomicI64, AtomicU64, AtomicUsize, Ordering::Relaxed};
use std::time::SystemTime;

use serde::{Deserialize, Serialize};

/// How many days of history the stats file keeps.
const KEEP_DAYS: usize = 400;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Kind {
    Api = 0,
    Media = 1,
    Image = 2,
}

pub const KINDS: [Kind; 3] = [Kind::Api, Kind::Media, Kind::Image];

impl Kind {
    pub fn name(self) -> &'static str {
        match self {
            Kind::Api => "api",
            Kind::Media => "media",
            Kind::Image => "image",
        }
    }
}

/// Classifies a request path without allocating. Media covers proxied
/// resources, Emby video and audio streams and downloads.
pub fn classify(path: &str) -> Kind {
    if path.starts_with("/_proxy/") {
        return Kind::Media;
    }
    let mut segments = path.split('/').filter(|s| !s.is_empty());
    let mut first = segments.next();
    if first
        .is_some_and(|s| s.eq_ignore_ascii_case("emby") || s.eq_ignore_ascii_case("mediabrowser"))
    {
        first = segments.next();
    }
    let Some(first) = first else {
        return Kind::Api;
    };
    if first.eq_ignore_ascii_case("videos") || first.eq_ignore_ascii_case("audio") {
        return Kind::Media;
    }
    for segment in segments {
        if segment.eq_ignore_ascii_case("images") {
            return Kind::Image;
        }
        if segment.eq_ignore_ascii_case("download") {
            return Kind::Media;
        }
    }
    Kind::Api
}

/// Number of counter shards. Each thread updates its own shard so concurrent
/// requests never contend on a cache line; readers sum the shards.
const SHARDS: usize = 16;

#[derive(Default)]
#[repr(align(128))]
struct Shard {
    requests: AtomicU64,
    bytes_out: AtomicU64,
    bytes_in: AtomicU64,
    /// Can go negative when a stream starts and ends on different threads.
    active: AtomicI64,
}

fn shard_index() -> usize {
    static NEXT: AtomicUsize = AtomicUsize::new(0);
    thread_local! {
        static INDEX: usize = NEXT.fetch_add(1, Relaxed) % SHARDS;
    }
    INDEX.with(|index| *index)
}

/// Live counters for one route and kind.
#[derive(Default)]
pub struct Counters {
    shards: [Shard; SHARDS],
}

impl Counters {
    #[inline]
    fn shard(&self) -> &Shard {
        &self.shards[shard_index()]
    }

    #[inline]
    pub fn add_request(&self) {
        self.shard().requests.fetch_add(1, Relaxed);
    }

    /// Adds bytes sent to clients.
    #[inline]
    pub fn add_bytes_out(&self, bytes: u64) {
        self.shard().bytes_out.fetch_add(bytes, Relaxed);
    }

    /// Adds bytes received from clients.
    #[inline]
    pub fn add_bytes_in(&self, bytes: u64) {
        self.shard().bytes_in.fetch_add(bytes, Relaxed);
    }

    #[inline]
    pub fn stream_started(&self) {
        self.shard().active.fetch_add(1, Relaxed);
    }

    #[inline]
    pub fn stream_finished(&self) {
        self.shard().active.fetch_sub(1, Relaxed);
    }

    pub fn totals(&self) -> Totals {
        let mut totals = Totals::default();
        for shard in &self.shards {
            totals.requests += shard.requests.load(Relaxed);
            totals.bytes_out += shard.bytes_out.load(Relaxed);
            totals.bytes_in += shard.bytes_in.load(Relaxed);
        }
        totals
    }

    /// Responses currently streaming to clients.
    pub fn active(&self) -> u64 {
        let active: i64 = self.shards.iter().map(|s| s.active.load(Relaxed)).sum();
        active.max(0) as u64
    }
}

pub struct RouteStats {
    name: String,
    kinds: [Counters; 3],
}

impl RouteStats {
    #[inline]
    pub fn kind(&self, kind: Kind) -> &Counters {
        &self.kinds[kind as usize]
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Totals {
    #[serde(default)]
    pub requests: u64,
    #[serde(default)]
    pub bytes_out: u64,
    #[serde(default)]
    pub bytes_in: u64,
}

impl Totals {
    fn add(&mut self, other: Totals) {
        self.requests += other.requests;
        self.bytes_out += other.bytes_out;
        self.bytes_in += other.bytes_in;
    }

    fn minus(self, other: Totals) -> Totals {
        Totals {
            requests: self.requests.saturating_sub(other.requests),
            bytes_out: self.bytes_out.saturating_sub(other.bytes_out),
            bytes_in: self.bytes_in.saturating_sub(other.bytes_in),
        }
    }
}

/// date -> route -> kind -> totals
type Days = BTreeMap<String, BTreeMap<String, BTreeMap<String, Totals>>>;

#[derive(Default, Serialize, Deserialize)]
struct StatsFile {
    #[serde(default)]
    days: Days,
}

struct History {
    days: Days,
    /// Counter values already added to `days`, per route and kind.
    flushed: Vec<[Totals; 3]>,
}

pub struct Stats {
    /// Leaked so the proxy path can hold plain references instead of
    /// reference-counted pointers. Stats live for the whole process.
    routes: Vec<&'static RouteStats>,
    path: Option<PathBuf>,
    history: Mutex<History>,
    started_at: String,
}

/// Usage for one route and kind, as reported by `/stats`.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct KindReport {
    pub today: Totals,
    pub month: Totals,
    pub since_start: Totals,
    pub active: u64,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct RouteReport {
    pub route: String,
    pub kinds: BTreeMap<String, KindReport>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Report {
    pub version: String,
    pub started_at: String,
    pub today: String,
    pub routes: Vec<RouteReport>,
}

impl Stats {
    /// In-memory stats for the given route names.
    pub fn memory(routes: &[String]) -> Stats {
        Stats::open(routes, None).expect("in-memory stats")
    }

    /// Loads per-day history from `path` if it exists. `None` keeps history
    /// in memory only.
    pub fn open(routes: &[String], path: Option<&Path>) -> Result<Stats, String> {
        let mut file = StatsFile::default();
        if let Some(path) = path {
            match std::fs::read(path) {
                Ok(data) => {
                    file = serde_json::from_slice(&data)
                        .map_err(|e| format!("load stats {}: {e}", path.display()))?;
                }
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
                Err(e) => return Err(format!("load stats {}: {e}", path.display())),
            }
        }
        Ok(Stats {
            routes: routes
                .iter()
                .map(|name| {
                    &*Box::leak(Box::new(RouteStats {
                        name: name.clone(),
                        kinds: Default::default(),
                    }))
                })
                .collect(),
            path: path.map(Path::to_path_buf),
            history: Mutex::new(History {
                days: file.days,
                flushed: vec![Default::default(); routes.len()],
            }),
            started_at: crate::logging::format_time(SystemTime::now()),
        })
    }

    /// The counters for a route, or `None` for an unknown route.
    pub fn route(&self, name: &str) -> Option<&'static RouteStats> {
        self.routes.iter().find(|r| r.name == name).copied()
    }

    /// Adds counters gathered since the last flush to today's totals and
    /// writes the stats file.
    pub fn flush(&self) -> Result<(), String> {
        self.flush_on(&local_date(SystemTime::now()))
    }

    fn flush_on(&self, today: &str) -> Result<(), String> {
        let mut history = self.history.lock().unwrap();
        let History { days, flushed } = &mut *history;
        for (route, flushed) in self.routes.iter().zip(flushed.iter_mut()) {
            for kind in KINDS {
                let current = route.kind(kind).totals();
                let delta = current.minus(flushed[kind as usize]);
                flushed[kind as usize] = current;
                if delta == Totals::default() {
                    continue;
                }
                days.entry(today.to_owned())
                    .or_default()
                    .entry(route.name.clone())
                    .or_default()
                    .entry(kind.name().to_owned())
                    .or_default()
                    .add(delta);
            }
        }
        while days.len() > KEEP_DAYS {
            days.pop_first();
        }
        let Some(path) = &self.path else {
            return Ok(());
        };
        let data = serde_json::to_vec_pretty(&StatsFile { days: days.clone() })
            .map_err(|e| e.to_string())?;
        crate::util::write_private_file(path, &data)
            .map_err(|e| format!("save stats {}: {e}", path.display()))
    }

    /// Current usage per route and kind.
    pub fn report(&self) -> Report {
        self.report_on(&local_date(SystemTime::now()))
    }

    fn report_on(&self, today: &str) -> Report {
        let history = self.history.lock().unwrap();
        let month = &today[..7];
        let routes = self
            .routes
            .iter()
            .zip(&history.flushed)
            .map(|(route, flushed)| {
                let kinds = KINDS
                    .iter()
                    .map(|&kind| {
                        let counters = route.kind(kind);
                        let since_start = counters.totals();
                        let pending = since_start.minus(flushed[kind as usize]);
                        let stored = |date: &str| {
                            history
                                .days
                                .get(date)
                                .and_then(|routes| routes.get(&route.name))
                                .and_then(|kinds| kinds.get(kind.name()))
                                .copied()
                                .unwrap_or_default()
                        };
                        let mut today_totals = stored(today);
                        today_totals.add(pending);
                        let mut month_totals = pending;
                        for date in history.days.keys().filter(|d| d.starts_with(month)) {
                            month_totals.add(stored(date));
                        }
                        let report = KindReport {
                            today: today_totals,
                            month: month_totals,
                            since_start,
                            active: counters.active(),
                        };
                        (kind.name().to_owned(), report)
                    })
                    .collect();
                RouteReport {
                    route: route.name.clone(),
                    kinds,
                }
            })
            .collect();
        Report {
            version: env!("CARGO_PKG_VERSION").to_owned(),
            started_at: self.started_at.clone(),
            today: today.to_owned(),
            routes,
        }
    }

    /// Counters in the Prometheus text exposition format.
    pub fn prometheus(&self) -> String {
        let mut out = String::with_capacity(2048);
        for Metric {
            name,
            metric_type,
            help,
            value,
        } in METRICS
        {
            let _ = writeln!(out, "# HELP {name} {help}");
            let _ = writeln!(out, "# TYPE {name} {metric_type}");
            for route in &self.routes {
                for kind in KINDS {
                    let _ = writeln!(
                        out,
                        "{name}{{route=\"{}\",kind=\"{}\"}} {}",
                        route.name,
                        kind.name(),
                        value(route.kind(kind))
                    );
                }
            }
        }
        let _ = writeln!(out, "# HELP proxemby_build_info Build information.");
        let _ = writeln!(out, "# TYPE proxemby_build_info gauge");
        let _ = writeln!(
            out,
            "proxemby_build_info{{version=\"{}\"}} 1",
            env!("CARGO_PKG_VERSION")
        );
        out
    }
}

struct Metric {
    name: &'static str,
    metric_type: &'static str,
    help: &'static str,
    value: fn(&Counters) -> u64,
}

const METRICS: [Metric; 4] = [
    Metric {
        name: "proxemby_requests_total",
        metric_type: "counter",
        help: "Requests handled.",
        value: |c| c.totals().requests,
    },
    Metric {
        name: "proxemby_response_bytes_total",
        metric_type: "counter",
        help: "Bytes sent to clients.",
        value: |c| c.totals().bytes_out,
    },
    Metric {
        name: "proxemby_request_bytes_total",
        metric_type: "counter",
        help: "Bytes received from clients.",
        value: |c| c.totals().bytes_in,
    },
    Metric {
        name: "proxemby_active_streams",
        metric_type: "gauge",
        help: "Responses currently streaming to clients.",
        value: |c| c.active(),
    },
];

/// The local calendar date as `YYYY-MM-DD`, so days follow the server's
/// time zone.
pub fn local_date(time: SystemTime) -> String {
    let secs = time
        .duration_since(SystemTime::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0) as _;
    // SAFETY: localtime_r only writes to the provided tm struct.
    let mut tm: libc::tm = unsafe { std::mem::zeroed() };
    let converted = unsafe { !libc::localtime_r(&secs, &mut tm).is_null() };
    if !converted {
        return crate::logging::format_time(time)[..10].to_owned();
    }
    format!(
        "{:04}-{:02}-{:02}",
        tm.tm_year + 1900,
        tm.tm_mon + 1,
        tm.tm_mday
    )
}

/// Formats a byte count with binary units, such as `12.43 GiB`.
pub fn format_bytes(bytes: u64) -> String {
    const UNITS: [&str; 6] = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
    if bytes < 1024 {
        return format!("{bytes} B");
    }
    let mut value = bytes as f64;
    let mut unit = 0;
    while value >= 1024.0 && unit < UNITS.len() - 1 {
        value /= 1024.0;
        unit += 1;
    }
    format!("{value:.2} {}", UNITS[unit])
}

/// The address `proxemby stats` connects to for a configured listen
/// address: wildcard hosts become loopback.
pub fn admin_connect_addr(listen: &str) -> String {
    if let Some(port) = listen.strip_prefix(':') {
        return format!("127.0.0.1:{port}");
    }
    if let Some(port) = listen.strip_prefix("0.0.0.0:") {
        return format!("127.0.0.1:{port}");
    }
    if let Some(port) = listen.strip_prefix("[::]:") {
        return format!("[::1]:{port}");
    }
    listen.to_owned()
}

/// Fetches the report from a running proxy's admin listener.
pub fn fetch_report(addr: &str) -> Result<Report, String> {
    use std::io::{Read, Write};
    use std::time::Duration;

    let mut stream = std::net::TcpStream::connect(addr)
        .map_err(|e| format!("connect to {addr}: {e} (is proxemby running with stats enabled?)"))?;
    let _ = stream.set_read_timeout(Some(Duration::from_secs(10)));
    stream
        .write_all(b"GET /stats HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
        .map_err(|e| format!("request stats: {e}"))?;
    let mut response = Vec::new();
    stream
        .read_to_end(&mut response)
        .map_err(|e| format!("read stats: {e}"))?;
    let split = response
        .windows(4)
        .position(|w| w == b"\r\n\r\n")
        .ok_or("invalid stats response")?;
    let head = String::from_utf8_lossy(&response[..split]);
    if !head.starts_with("HTTP/1.1 200") {
        let status = head.lines().next().unwrap_or("");
        return Err(format!("stats request failed: {status}"));
    }
    serde_json::from_slice(&response[split + 4..]).map_err(|e| format!("invalid stats: {e}"))
}

/// Renders bytes sent to clients per route and kind as a table.
pub fn render_table(report: &Report) -> String {
    let mut out = String::new();
    let row = |out: &mut String, name: &str, kinds: &[&KindReport]| {
        let sum = |pick: fn(&KindReport) -> u64| kinds.iter().map(|k| pick(k)).sum::<u64>();
        let _ = writeln!(
            out,
            "{name:<34} {:>12} {:>12} {:>12} {:>6}",
            format_bytes(sum(|k| k.today.bytes_out)),
            format_bytes(sum(|k| k.month.bytes_out)),
            format_bytes(sum(|k| k.since_start.bytes_out)),
            sum(|k| k.active),
        );
    };
    let _ = writeln!(
        out,
        "{:<34} {:>12} {:>12} {:>12} {:>6}",
        "ROUTE / KIND", "TODAY", "THIS MONTH", "SINCE START", "ACTIVE"
    );
    let mut all = Vec::new();
    for route in &report.routes {
        let kinds: Vec<&KindReport> = route.kinds.values().collect();
        row(&mut out, &route.route, &kinds);
        for kind in KINDS {
            if let Some(k) = route.kinds.get(kind.name()) {
                row(&mut out, &format!("  {}", kind.name()), &[k]);
            }
        }
        all.extend(kinds);
    }
    if report.routes.len() > 1 {
        row(&mut out, "TOTAL", &all);
    }
    let _ = writeln!(
        out,
        "\nBytes sent to clients. Today is {}; running since {} (proxemby {}).",
        report.today, report.started_at, report.version
    );
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn classifies_paths() {
        assert_eq!(classify("/_proxy/https/cdn.example.com/a.mkv"), Kind::Media);
        assert_eq!(classify("/emby/Videos/1/stream.mkv"), Kind::Media);
        assert_eq!(classify("/videos/1/hls1/main/0.ts"), Kind::Media);
        assert_eq!(classify("/emby/Audio/1/universal"), Kind::Media);
        assert_eq!(classify("/emby/Items/1/Download"), Kind::Media);
        assert_eq!(classify("/emby/Items/1/Images/Primary"), Kind::Image);
        assert_eq!(classify("/emby/Users/1/Items"), Kind::Api);
        assert_eq!(classify("/emby/Items/1/PlaybackInfo"), Kind::Api);
        assert_eq!(classify("/"), Kind::Api);
    }

    fn bump(stats: &Stats, route: &str, kind: Kind, out: u64) {
        let counters = stats.route(route).unwrap();
        let c = counters.kind(kind);
        c.add_request();
        c.add_bytes_out(out);
        c.add_bytes_in(1);
    }

    #[test]
    fn reports_today_month_and_since_start() {
        let stats = Stats::memory(&["a".into(), "b".into()]);
        bump(&stats, "a", Kind::Media, 100);
        stats.flush_on("2026-09-30").unwrap();
        bump(&stats, "a", Kind::Media, 10);
        stats.flush_on("2026-10-01").unwrap();
        bump(&stats, "a", Kind::Media, 1); // not flushed yet
        bump(&stats, "b", Kind::Api, 5);

        let report = stats.report_on("2026-10-01");
        let media = &report.routes[0].kinds["media"];
        assert_eq!(media.today.bytes_out, 11);
        assert_eq!(media.month.bytes_out, 11);
        assert_eq!(media.since_start.bytes_out, 111);
        assert_eq!(media.since_start.requests, 3);
        assert_eq!(report.routes[1].kinds["api"].today.bytes_out, 5);
        assert_eq!(report.routes[1].kinds["media"], KindReport::default());
    }

    #[test]
    fn persists_days_across_restarts() {
        let dir = std::env::temp_dir().join(format!("proxemby-stats-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("stats.json");
        let _ = std::fs::remove_file(&path);
        let routes = vec!["a".to_owned()];

        let stats = Stats::open(&routes, Some(&path)).unwrap();
        bump(&stats, "a", Kind::Image, 42);
        stats.flush_on("2026-10-05").unwrap();
        stats.flush_on("2026-10-05").unwrap(); // nothing new, no double counting

        let reopened = Stats::open(&routes, Some(&path)).unwrap();
        bump(&reopened, "a", Kind::Image, 8);
        let report = reopened.report_on("2026-10-05");
        let image = &report.routes[0].kinds["image"];
        assert_eq!(image.today.bytes_out, 50);
        assert_eq!(image.since_start.bytes_out, 8);
        std::fs::remove_dir_all(&dir).unwrap();
    }

    #[test]
    fn keeps_limited_history() {
        let stats = Stats::memory(&["a".into()]);
        for day in 0..(KEEP_DAYS + 5) {
            bump(&stats, "a", Kind::Api, 1);
            stats.flush_on(&format!("d{day:05}")).unwrap();
        }
        assert_eq!(stats.history.lock().unwrap().days.len(), KEEP_DAYS);
    }

    #[test]
    fn prometheus_output() {
        let stats = Stats::memory(&["a.example.com".into()]);
        bump(&stats, "a.example.com", Kind::Media, 7);
        let text = stats.prometheus();
        assert!(text.contains("# TYPE proxemby_response_bytes_total counter"));
        assert!(
            text.contains(
                "proxemby_response_bytes_total{route=\"a.example.com\",kind=\"media\"} 7"
            )
        );
        assert!(text.contains("proxemby_active_streams{route=\"a.example.com\",kind=\"api\"} 0"));
        assert!(text.contains("proxemby_build_info{version=\""));
    }

    #[test]
    fn renders_table() {
        let stats = Stats::memory(&["a.example.com".into(), "b.example.com".into()]);
        bump(&stats, "a.example.com", Kind::Media, 2048);
        bump(&stats, "b.example.com", Kind::Api, 100);
        let table = render_table(&stats.report_on("2026-10-05"));
        let lines: Vec<&str> = table.lines().collect();
        assert!(lines[0].starts_with("ROUTE / KIND"));
        assert!(
            lines[1].starts_with("a.example.com") && lines[1].contains("2.00 KiB"),
            "{table}"
        );
        assert!(
            lines[3].starts_with("  media") && lines[3].contains("2.00 KiB"),
            "{table}"
        );
        assert!(
            lines
                .iter()
                .any(|l| l.starts_with("TOTAL") && l.contains("2.10 KiB")),
            "{table}"
        );
        assert!(table.contains("Today is 2026-10-05"));
    }

    #[test]
    fn admin_addresses() {
        assert_eq!(admin_connect_addr(":9180"), "127.0.0.1:9180");
        assert_eq!(admin_connect_addr("0.0.0.0:9180"), "127.0.0.1:9180");
        assert_eq!(admin_connect_addr("[::]:9180"), "[::1]:9180");
        assert_eq!(admin_connect_addr("127.0.0.1:9180"), "127.0.0.1:9180");
    }

    #[test]
    fn formats_bytes_and_dates() {
        assert_eq!(format_bytes(512), "512 B");
        assert_eq!(format_bytes(1536), "1.50 KiB");
        assert_eq!(format_bytes(13_346_383_872), "12.43 GiB");
        let date = local_date(SystemTime::now());
        assert_eq!(date.len(), 10);
        assert_eq!(&date[4..5], "-");
    }
}
