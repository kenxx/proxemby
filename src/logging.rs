//! Structured logging compatible with Go's `log/slog` text and JSON handlers.

use std::fmt::Write as _;
use std::io::Write;
use std::sync::{Arc, Mutex};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

pub const DEFAULT_LEVEL: &str = "info";
pub const DEFAULT_FORMAT: &str = "text";

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum Level {
    Debug,
    Info,
    Warn,
    Error,
}

impl Level {
    fn as_str(self) -> &'static str {
        match self {
            Level::Debug => "DEBUG",
            Level::Info => "INFO",
            Level::Warn => "WARN",
            Level::Error => "ERROR",
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Format {
    Text,
    Json,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Config {
    pub level: Level,
    pub format: Format,
    pub time: bool,
}

impl Default for Config {
    fn default() -> Self {
        Config {
            level: Level::Info,
            format: Format::Text,
            time: true,
        }
    }
}

pub fn parse_config(level: &str, format: &str, time: bool) -> Result<Config, String> {
    let level = match level.trim().to_ascii_lowercase().as_str() {
        "debug" => Level::Debug,
        "info" => Level::Info,
        "warn" | "warning" => Level::Warn,
        "error" => Level::Error,
        _ => return Err("log level must be debug, info, warn, or error".into()),
    };
    let format = match format.trim().to_ascii_lowercase().as_str() {
        "text" => Format::Text,
        "json" => Format::Json,
        _ => return Err("log format must be text or json".into()),
    };
    Ok(Config {
        level,
        format,
        time,
    })
}

/// A log attribute value.
#[derive(Clone, Debug)]
pub enum Value {
    Str(String),
    Int(i64),
    Bool(bool),
    List(Vec<String>),
    Duration(Duration),
}

impl From<&str> for Value {
    fn from(v: &str) -> Self {
        Value::Str(v.to_owned())
    }
}
impl From<String> for Value {
    fn from(v: String) -> Self {
        Value::Str(v)
    }
}
impl From<&String> for Value {
    fn from(v: &String) -> Self {
        Value::Str(v.clone())
    }
}
impl From<bool> for Value {
    fn from(v: bool) -> Self {
        Value::Bool(v)
    }
}
impl From<Duration> for Value {
    fn from(v: Duration) -> Self {
        Value::Duration(v)
    }
}
impl From<&[String]> for Value {
    fn from(v: &[String]) -> Self {
        Value::List(v.to_vec())
    }
}
impl From<&Vec<String>> for Value {
    fn from(v: &Vec<String>) -> Self {
        Value::List(v.clone())
    }
}
macro_rules! int_value {
    ($($t:ty),*) => {$(
        impl From<$t> for Value {
            fn from(v: $t) -> Self {
                Value::Int(v as i64)
            }
        }
    )*};
}
int_value!(i32, i64, u16, u32, u64, usize);

type Sink = Arc<Mutex<Box<dyn Write + Send>>>;

#[derive(Clone)]
pub struct Logger {
    config: Config,
    sink: Sink,
    attrs: Arc<Vec<(String, Value)>>,
}

impl Logger {
    pub fn new(config: Config, writer: Box<dyn Write + Send>) -> Logger {
        Logger {
            config,
            sink: Arc::new(Mutex::new(writer)),
            attrs: Arc::new(Vec::new()),
        }
    }

    /// A logger that discards everything.
    pub fn discard() -> Logger {
        Logger::new(
            Config {
                level: Level::Error,
                ..Config::default()
            },
            Box::new(std::io::sink()),
        )
    }

    pub fn with(&self, attrs: Vec<(&str, Value)>) -> Logger {
        let mut all = (*self.attrs).clone();
        all.extend(attrs.into_iter().map(|(k, v)| (k.to_owned(), v)));
        Logger {
            config: self.config,
            sink: self.sink.clone(),
            attrs: Arc::new(all),
        }
    }

    #[inline]
    pub fn enabled(&self, level: Level) -> bool {
        level >= self.config.level
    }

    pub fn log(&self, level: Level, msg: &str, attrs: &[(&str, Value)]) {
        if !self.enabled(level) {
            return;
        }
        let mut line = String::with_capacity(256);
        let time = self.config.time.then(|| format_time(SystemTime::now()));
        match self.config.format {
            Format::Text => {
                if let Some(time) = &time {
                    line.push_str("time=");
                    line.push_str(time);
                    line.push(' ');
                }
                line.push_str("level=");
                line.push_str(level.as_str());
                line.push_str(" msg=");
                push_text_string(&mut line, msg);
                for (key, value) in self
                    .attrs
                    .iter()
                    .map(|(k, v)| (k.as_str(), v))
                    .chain(attrs.iter().map(|(k, v)| (*k, v)))
                {
                    line.push(' ');
                    line.push_str(key);
                    line.push('=');
                    push_text_value(&mut line, value);
                }
            }
            Format::Json => {
                line.push('{');
                if let Some(time) = &time {
                    line.push_str("\"time\":");
                    push_json_string(&mut line, time);
                    line.push(',');
                }
                line.push_str("\"level\":");
                push_json_string(&mut line, level.as_str());
                line.push_str(",\"msg\":");
                push_json_string(&mut line, msg);
                for (key, value) in self
                    .attrs
                    .iter()
                    .map(|(k, v)| (k.as_str(), v))
                    .chain(attrs.iter().map(|(k, v)| (*k, v)))
                {
                    line.push(',');
                    push_json_string(&mut line, key);
                    line.push(':');
                    push_json_value(&mut line, value);
                }
                line.push('}');
            }
        }
        line.push('\n');
        if let Ok(mut sink) = self.sink.lock() {
            let _ = sink.write_all(line.as_bytes());
        }
    }
}

/// Logs a message with key/value attributes. Attribute values are only built
/// when the level is enabled.
#[macro_export]
macro_rules! log_at {
    ($logger:expr, $level:expr, $msg:expr $(, $key:expr => $value:expr)* $(,)?) => {{
        let logger = &$logger;
        if logger.enabled($level) {
            logger.log($level, $msg, &[$(($key, $crate::logging::Value::from($value))),*]);
        }
    }};
}

#[macro_export]
macro_rules! debug {
    ($logger:expr, $msg:expr $(, $key:expr => $value:expr)* $(,)?) => {
        $crate::log_at!($logger, $crate::logging::Level::Debug, $msg $(, $key => $value)*)
    };
}

#[macro_export]
macro_rules! info {
    ($logger:expr, $msg:expr $(, $key:expr => $value:expr)* $(,)?) => {
        $crate::log_at!($logger, $crate::logging::Level::Info, $msg $(, $key => $value)*)
    };
}

#[macro_export]
macro_rules! warn {
    ($logger:expr, $msg:expr $(, $key:expr => $value:expr)* $(,)?) => {
        $crate::log_at!($logger, $crate::logging::Level::Warn, $msg $(, $key => $value)*)
    };
}

#[macro_export]
macro_rules! error {
    ($logger:expr, $msg:expr $(, $key:expr => $value:expr)* $(,)?) => {
        $crate::log_at!($logger, $crate::logging::Level::Error, $msg $(, $key => $value)*)
    };
}

fn push_text_value(out: &mut String, value: &Value) {
    match value {
        Value::Str(s) => push_text_string(out, s),
        Value::Int(i) => {
            let _ = write!(out, "{i}");
        }
        Value::Bool(b) => {
            let _ = write!(out, "{b}");
        }
        Value::List(items) => push_text_string(out, &format!("[{}]", items.join(" "))),
        Value::Duration(d) => out.push_str(&format_duration(*d)),
    }
}

fn push_json_value(out: &mut String, value: &Value) {
    match value {
        Value::Str(s) => push_json_string(out, s),
        Value::Int(i) => {
            let _ = write!(out, "{i}");
        }
        Value::Bool(b) => {
            let _ = write!(out, "{b}");
        }
        Value::List(items) => {
            out.push('[');
            for (i, item) in items.iter().enumerate() {
                if i > 0 {
                    out.push(',');
                }
                push_json_string(out, item);
            }
            out.push(']');
        }
        Value::Duration(d) => {
            let _ = write!(out, "{}", d.as_nanos());
        }
    }
}

fn needs_quoting(s: &str) -> bool {
    s.is_empty()
        || s.chars()
            .any(|c| c == ' ' || c == '=' || c == '"' || c.is_control() || c == '\u{fffd}')
}

fn push_text_string(out: &mut String, s: &str) {
    if !needs_quoting(s) {
        out.push_str(s);
        return;
    }
    out.push('"');
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            c if c.is_control() => {
                let _ = write!(out, "\\x{:02x}", c as u32);
            }
            c => out.push(c),
        }
    }
    out.push('"');
}

fn push_json_string(out: &mut String, s: &str) {
    out.push('"');
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            c if (c as u32) < 0x20 => {
                let _ = write!(out, "\\u{:04x}", c as u32);
            }
            c => out.push(c),
        }
    }
    out.push('"');
}

/// Formats a duration like Go's `time.Duration.String`, rounded to milliseconds.
pub fn format_duration(d: Duration) -> String {
    let ms = (d.as_micros() + 500) / 1000;
    if ms == 0 {
        return "0s".to_owned();
    }
    if ms < 1000 {
        return format!("{ms}ms");
    }
    let total_secs = ms / 1000;
    let frac = ms % 1000;
    let mut out = String::new();
    let hours = total_secs / 3600;
    let minutes = (total_secs % 3600) / 60;
    let secs = total_secs % 60;
    if hours > 0 {
        let _ = write!(out, "{hours}h");
    }
    if hours > 0 || minutes > 0 {
        let _ = write!(out, "{minutes}m");
    }
    if frac == 0 {
        let _ = write!(out, "{secs}s");
    } else {
        let frac = format!("{frac:03}");
        let _ = write!(out, "{secs}.{}s", frac.trim_end_matches('0'));
    }
    out
}

/// Formats a UTC timestamp as RFC 3339 with milliseconds.
pub fn format_time(time: SystemTime) -> String {
    let since = time.duration_since(UNIX_EPOCH).unwrap_or_default();
    let secs = since.as_secs() as i64;
    let millis = since.subsec_millis();
    let days = secs.div_euclid(86_400);
    let rem = secs.rem_euclid(86_400);
    let (year, month, day) = civil_from_days(days);
    format!(
        "{year:04}-{month:02}-{day:02}T{:02}:{:02}:{:02}.{millis:03}Z",
        rem / 3600,
        (rem % 3600) / 60,
        rem % 60
    )
}

// Howard Hinnant's days-to-civil algorithm.
fn civil_from_days(days: i64) -> (i64, u32, u32) {
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let y = yoe + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = (doy - (153 * mp + 2) / 5 + 1) as u32;
    let m = if mp < 10 { mp + 3 } else { mp - 9 } as u32;
    (if m <= 2 { y + 1 } else { y }, m, d)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[derive(Clone, Default)]
    struct Buf(Arc<Mutex<Vec<u8>>>);
    impl Write for Buf {
        fn write(&mut self, data: &[u8]) -> std::io::Result<usize> {
            self.0.lock().unwrap().extend_from_slice(data);
            Ok(data.len())
        }
        fn flush(&mut self) -> std::io::Result<()> {
            Ok(())
        }
    }

    #[test]
    fn parse_config_validates_values() {
        let cfg = parse_config("WARNING", " JSON ", false).unwrap();
        assert_eq!(cfg.level, Level::Warn);
        assert_eq!(cfg.format, Format::Json);
        assert!(parse_config("verbose", "text", true).is_err());
        assert!(parse_config("info", "plain", true).is_err());
    }

    #[test]
    fn text_and_json_output() {
        let buf = Buf::default();
        let config = Config {
            level: Level::Debug,
            format: Format::Text,
            time: false,
        };
        let logger =
            Logger::new(config, Box::new(buf.clone())).with(vec![("route", "proxemby".into())]);
        crate::debug!(logger, "request completed", "path" => "/a b", "status" => 200u16, "ok" => true);
        let text = String::from_utf8(buf.0.lock().unwrap().clone()).unwrap();
        assert_eq!(
            text,
            "level=DEBUG msg=\"request completed\" route=proxemby path=\"/a b\" status=200 ok=true\n"
        );

        let buf = Buf::default();
        let logger = Logger::new(
            Config {
                format: Format::Json,
                ..config
            },
            Box::new(buf.clone()),
        );
        crate::warn!(logger, "x", "users" => &vec!["a".to_owned()]);
        let text = String::from_utf8(buf.0.lock().unwrap().clone()).unwrap();
        assert_eq!(
            text,
            "{\"level\":\"WARN\",\"msg\":\"x\",\"users\":[\"a\"]}\n"
        );
    }

    #[test]
    fn info_level_skips_debug() {
        let buf = Buf::default();
        let logger = Logger::new(Config::default(), Box::new(buf.clone()));
        crate::debug!(logger, "hidden");
        assert!(buf.0.lock().unwrap().is_empty());
    }

    #[test]
    fn formats_like_go() {
        assert_eq!(format_duration(Duration::from_micros(400)), "0s");
        assert_eq!(format_duration(Duration::from_millis(12)), "12ms");
        assert_eq!(format_duration(Duration::from_millis(1500)), "1.5s");
        assert_eq!(format_duration(Duration::from_millis(125_004)), "2m5.004s");
        assert_eq!(
            format_time(UNIX_EPOCH + Duration::from_millis(1_790_000_000_123)),
            "2026-09-21T14:13:20.123Z"
        );
    }
}
