//! URL, query and IP helpers that mirror the Go `net/url` and `net/netip`
//! behavior proxemby relied on.

use std::net::IpAddr;

const HEX: &[u8; 16] = b"0123456789ABCDEF";

fn unhex(c: u8) -> Option<u8> {
    match c {
        b'0'..=b'9' => Some(c - b'0'),
        b'a'..=b'f' => Some(c - b'a' + 10),
        b'A'..=b'F' => Some(c - b'A' + 10),
        _ => None,
    }
}

/// Decodes percent escapes. Returns `None` for malformed escapes.
pub fn percent_decode(s: &str, plus_as_space: bool) -> Option<String> {
    if !(s.contains('%') || plus_as_space && s.contains('+')) {
        return Some(s.to_owned());
    }
    let bytes = s.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        match bytes[i] {
            b'%' => {
                let hi = unhex(*bytes.get(i + 1)?)?;
                let lo = unhex(*bytes.get(i + 2)?)?;
                out.push(hi << 4 | lo);
                i += 3;
            }
            b'+' if plus_as_space => {
                out.push(b' ');
                i += 1;
            }
            c => {
                out.push(c);
                i += 1;
            }
        }
    }
    Some(
        String::from_utf8(out)
            .unwrap_or_else(|e| String::from_utf8_lossy(e.as_bytes()).into_owned()),
    )
}

/// Parses a raw query like Go's `url.ParseQuery`. The flag reports whether any
/// pair was malformed; well-formed pairs are still returned.
pub fn parse_query(raw: &str) -> (Vec<(String, String)>, bool) {
    let mut pairs = Vec::new();
    let mut malformed = false;
    for part in raw.split('&') {
        if part.is_empty() {
            continue;
        }
        if part.contains(';') {
            malformed = true;
            continue;
        }
        let (key, value) = part.split_once('=').unwrap_or((part, ""));
        match (percent_decode(key, true), percent_decode(value, true)) {
            (Some(key), Some(value)) => pairs.push((key, value)),
            _ => malformed = true,
        }
    }
    (pairs, malformed)
}

/// Escapes like Go's `url.QueryEscape`.
pub fn query_escape(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for &b in s.as_bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(b as char)
            }
            b' ' => out.push('+'),
            _ => {
                out.push('%');
                out.push(HEX[(b >> 4) as usize] as char);
                out.push(HEX[(b & 15) as usize] as char);
            }
        }
    }
    out
}

/// Escapes a decoded path like Go's `url.URL.EscapedPath` for a path without
/// a raw form.
pub fn escape_path(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 8);
    for &b in s.as_bytes() {
        match b {
            b'A'..=b'Z'
            | b'a'..=b'z'
            | b'0'..=b'9'
            | b'-'
            | b'_'
            | b'.'
            | b'~'
            | b'$'
            | b'&'
            | b'+'
            | b','
            | b'/'
            | b':'
            | b';'
            | b'='
            | b'@' => out.push(b as char),
            _ => {
                out.push('%');
                out.push(HEX[(b >> 4) as usize] as char);
                out.push(HEX[(b & 15) as usize] as char);
            }
        }
    }
    out
}

const SENSITIVE_QUERY_KEYS: &[&str] = &[
    "api_key",
    "apikey",
    "access_token",
    "token",
    "auth",
    "password",
];

/// Replaces sensitive query values with `redacted`. Malformed queries are
/// returned unchanged, like the Go implementation.
pub fn sanitize_raw_query(raw: &str) -> String {
    if raw.is_empty() {
        return String::new();
    }
    let (mut pairs, malformed) = parse_query(raw);
    if malformed {
        return raw.to_owned();
    }
    // url.Values.Set replaces every value of a key with a single one.
    let mut seen_sensitive: Vec<String> = Vec::new();
    pairs.retain_mut(|(key, value)| {
        if SENSITIVE_QUERY_KEYS.contains(&key.to_ascii_lowercase().as_str()) {
            if seen_sensitive.contains(key) {
                return false;
            }
            seen_sensitive.push(key.clone());
            *value = "redacted".to_owned();
        }
        true
    });
    pairs.sort_by(|a, b| a.0.cmp(&b.0));
    let mut out = String::with_capacity(raw.len());
    for (key, value) in pairs {
        if !out.is_empty() {
            out.push('&');
        }
        out.push_str(&query_escape(&key));
        out.push('=');
        out.push_str(&query_escape(&value));
    }
    out
}

/// Formats a request path and query for logs with sensitive values redacted.
pub fn sanitize_request_uri(path: &str, query: Option<&str>) -> String {
    let path = if path.is_empty() { "/" } else { path };
    match query.map(sanitize_raw_query) {
        Some(q) if !q.is_empty() => format!("{path}?{q}"),
        _ => path.to_owned(),
    }
}

/// Redacts sensitive query values in an absolute URL string.
pub fn sanitize_url_string(raw: &str) -> String {
    let (without_fragment, fragment) = match raw.split_once('#') {
        Some((a, f)) => (a, Some(f)),
        None => (raw, None),
    };
    let mut out = match without_fragment.split_once('?') {
        Some((base, query)) => {
            let query = sanitize_raw_query(query);
            if query.is_empty() {
                base.to_owned()
            } else {
                format!("{base}?{query}")
            }
        }
        None => without_fragment.to_owned(),
    };
    if let Some(fragment) = fragment {
        out.push('#');
        out.push_str(fragment);
    }
    out
}

/// An absolute http or https URL from configuration.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct HttpUrl {
    pub scheme: String,
    /// Host with optional port, as written.
    pub host: String,
    /// Path as written, possibly empty.
    pub path: String,
    /// Raw query without `?`, possibly empty.
    pub query: String,
}

impl HttpUrl {
    pub fn parse(raw: &str) -> Result<HttpUrl, String> {
        let (scheme, rest) = raw.split_once("://").ok_or("missing scheme")?;
        let scheme = scheme.to_ascii_lowercase();
        let rest = rest.split('#').next().unwrap_or("");
        let (before_query, query) = rest.split_once('?').unwrap_or((rest, ""));
        let (authority, path) = match before_query.find('/') {
            Some(i) => (&before_query[..i], &before_query[i..]),
            None => (before_query, ""),
        };
        let host = authority.rsplit('@').next().unwrap_or("");
        if host.chars().any(|c| c.is_whitespace() || c.is_control())
            || path.chars().any(|c| c.is_control())
        {
            return Err("invalid character in URL".into());
        }
        Ok(HttpUrl {
            scheme,
            host: host.to_owned(),
            path: path.to_owned(),
            query: query.to_owned(),
        })
    }

    pub fn hostname(&self) -> &str {
        hostname(&self.host)
    }
}

impl std::fmt::Display for HttpUrl {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}://{}{}", self.scheme, self.host, self.path)?;
        if !self.query.is_empty() {
            write!(f, "?{}", self.query)?;
        }
        Ok(())
    }
}

/// Returns the hostname part of a `Host` value: no port, no IPv6 brackets.
pub fn hostname(host: &str) -> &str {
    let host = host.trim();
    if let Some(rest) = host.strip_prefix('[') {
        return match rest.find(']') {
            Some(end) => &rest[..end],
            None => rest,
        };
    }
    match host.rfind(':') {
        // Only one colon means host:port; more means a bare IPv6 address.
        Some(i) if host[..i].find(':').is_none() => &host[..i],
        _ => host,
    }
}

/// An IP network such as `192.168.0.0/24` or a single address.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct IpPrefix {
    addr: IpAddr,
    bits: u8,
}

impl IpPrefix {
    pub fn parse(value: &str) -> Result<IpPrefix, String> {
        match value.split_once('/') {
            Some((addr, bits)) => {
                let addr: IpAddr = addr
                    .parse()
                    .map_err(|_| format!("ParseAddr({addr:?}): invalid IP address"))?;
                let max = if addr.is_ipv4() { 32 } else { 128 };
                let bits: u8 =
                    bits.parse().ok().filter(|b| *b <= max).ok_or_else(|| {
                        format!("netip.ParsePrefix({value:?}): bad bits after slash")
                    })?;
                Ok(IpPrefix {
                    addr: mask(addr, bits),
                    bits,
                })
            }
            None => {
                let addr = unmap(
                    value
                        .parse()
                        .map_err(|_| format!("ParseAddr({value:?}): invalid IP address"))?,
                );
                let bits = if addr.is_ipv4() { 32 } else { 128 };
                Ok(IpPrefix { addr, bits })
            }
        }
    }

    pub fn contains(&self, addr: IpAddr) -> bool {
        let addr = unmap(addr);
        if addr.is_ipv4() != self.addr.is_ipv4() {
            return false;
        }
        mask(addr, self.bits) == self.addr
    }
}

impl std::fmt::Display for IpPrefix {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}/{}", self.addr, self.bits)
    }
}

pub fn unmap(addr: IpAddr) -> IpAddr {
    match addr {
        IpAddr::V6(v6) => v6.to_ipv4_mapped().map(IpAddr::V4).unwrap_or(addr),
        v4 => v4,
    }
}

fn mask(addr: IpAddr, bits: u8) -> IpAddr {
    match addr {
        IpAddr::V4(v4) => {
            let raw = u32::from(v4);
            let m = if bits == 0 {
                0
            } else {
                u32::MAX << (32 - bits as u32)
            };
            IpAddr::V4((raw & m).into())
        }
        IpAddr::V6(v6) => {
            let raw = u128::from(v6);
            let m = if bits == 0 {
                0
            } else {
                u128::MAX << (128 - bits as u32)
            };
            IpAddr::V6((raw & m).into())
        }
    }
}

/// Parses a client address, unmapping IPv4-mapped IPv6 addresses.
pub fn parse_client_addr(raw: &str) -> Option<IpAddr> {
    let raw = raw.trim();
    if raw.is_empty() {
        return None;
    }
    raw.parse().ok().map(unmap)
}

pub fn first_forwarded_for(raw: &str) -> &str {
    raw.split(',').next().unwrap_or("").trim()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sanitizes_queries_like_go() {
        assert_eq!(
            sanitize_raw_query("api_key=secret&token=abc&device=ios"),
            "api_key=redacted&device=ios&token=redacted"
        );
        assert_eq!(sanitize_raw_query("a=1;b=2"), "a=1;b=2");
        assert_eq!(
            sanitize_request_uri("/emby/Items/1/PlaybackInfo", Some("api_key=secret")),
            "/emby/Items/1/PlaybackInfo?api_key=redacted"
        );
        assert_eq!(
            sanitize_url_string("https://vod.us.emby.com/movie.mp4?token=secret"),
            "https://vod.us.emby.com/movie.mp4?token=redacted"
        );
    }

    #[test]
    fn escapes_paths_like_go() {
        assert_eq!(
            escape_path("/movie file(1).mp4"),
            "/movie%20file%281%29.mp4"
        );
        assert_eq!(query_escape("a b/c"), "a+b%2Fc");
        assert_eq!(percent_decode("a%20b+c", true).unwrap(), "a b c");
        assert!(percent_decode("a%zz", false).is_none());
    }

    #[test]
    fn hostnames() {
        assert_eq!(hostname("two.example.com:443"), "two.example.com");
        assert_eq!(hostname("[::1]:8080"), "::1");
        assert_eq!(hostname("::1"), "::1");
        assert_eq!(hostname("proxemby"), "proxemby");
    }

    #[test]
    fn prefixes() {
        let p = IpPrefix::parse("192.168.0.1/24").unwrap();
        assert_eq!(p.to_string(), "192.168.0.0/24");
        assert!(p.contains("192.168.0.77".parse().unwrap()));
        assert!(p.contains("::ffff:192.168.0.77".parse().unwrap()));
        assert!(!p.contains("192.168.1.1".parse().unwrap()));
        assert!(
            IpPrefix::parse("::1")
                .unwrap()
                .contains("::1".parse().unwrap())
        );
        assert!(IpPrefix::parse("not-an-ip").is_err());
        assert!(IpPrefix::parse("10.0.0.0/33").is_err());
    }
}
