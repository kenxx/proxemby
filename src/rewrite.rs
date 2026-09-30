//! Rewrites absolute media URLs in PlaybackInfo JSON so they flow through
//! proxemby's `/_proxy/` endpoint.
//!
//! The JSON is scanned in place and only the rewritten strings are spliced
//! into the output, so the rest of the document keeps its exact bytes and no
//! intermediate tree is built.

use std::borrow::Cow;
use std::sync::Arc;

use crate::hosts::Registry;
use crate::util::{HttpUrl, escape_path, percent_decode};

pub type Signer = Arc<dyn Fn(&str, &str) -> String + Send + Sync>;

pub struct Rewriter {
    public_url: HttpUrl,
    public_path: String,
    registry: Arc<Registry>,
    sign: Option<Signer>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RewriteEvent {
    pub path: String,
    pub original: String,
    pub rewritten: String,
    pub scheme: String,
    pub host: String,
}

impl Rewriter {
    pub fn new(public_url: HttpUrl, registry: Arc<Registry>) -> Rewriter {
        let public_path =
            percent_decode(&public_url.path, false).unwrap_or_else(|| public_url.path.clone());
        Rewriter {
            public_url,
            public_path,
            registry,
            sign: None,
        }
    }

    /// Adds a signature path segment in front of the scheme so resource URLs
    /// only work when they were issued by proxemby.
    pub fn with_signer(mut self, sign: Signer) -> Rewriter {
        self.sign = Some(sign);
        self
    }

    /// Returns the rewritten body, or `None` when nothing changed.
    pub fn rewrite_playback_info(&self, body: &[u8]) -> (Option<Vec<u8>>, Vec<RewriteEvent>) {
        let Ok(text) = std::str::from_utf8(body) else {
            return (None, Vec::new());
        };
        let Some(found) = scan(text) else {
            return (None, Vec::new());
        };

        let mut out: Option<Vec<u8>> = None;
        let mut last = 0;
        let mut events = Vec::new();
        for item in found {
            let Some((rewritten, scheme, host)) = self.rewrite_url(&item.value) else {
                continue;
            };
            let buf = out.get_or_insert_with(|| Vec::with_capacity(body.len() + 256));
            buf.extend_from_slice(&body[last..item.start]);
            buf.extend_from_slice(
                serde_json::to_string(&rewritten)
                    .unwrap_or_default()
                    .as_bytes(),
            );
            last = item.end;
            events.push(RewriteEvent {
                path: item.path,
                original: item.value.into_owned(),
                rewritten,
                scheme,
                host,
            });
        }
        if let Some(buf) = out.as_mut() {
            buf.extend_from_slice(&body[last..]);
        }
        (out, events)
    }

    fn rewrite_url(&self, raw: &str) -> Option<(String, String, String)> {
        let (scheme, rest) = if let Some(rest) = raw.strip_prefix("https://") {
            ("https", rest)
        } else {
            ("http", raw.strip_prefix("http://")?)
        };
        if raw.bytes().any(|b| b < 0x20 || b == 0x7f) {
            return None;
        }
        let rest = rest.split('#').next().unwrap_or("");
        let (before_query, query) = match rest.split_once('?') {
            Some((a, q)) => (a, Some(q)),
            None => (rest, None),
        };
        let (authority, raw_path) = match before_query.find('/') {
            Some(i) => (&before_query[..i], &before_query[i..]),
            None => (before_query, ""),
        };
        let host = authority.rsplit('@').next().unwrap_or("");
        if !valid_host(host) {
            return None;
        }
        let path = percent_decode(raw_path, false)?;

        self.registry.allow(host, scheme);

        let mut parts: Vec<&str> = vec!["_proxy"];
        let signature;
        if let Some(sign) = &self.sign {
            signature = sign(scheme, host);
            parts.push(&signature);
        }
        parts.extend([scheme, host, path.trim_start_matches('/')]);

        let mut rewritten = format!(
            "{}://{}{}",
            self.public_url.scheme,
            self.public_url.host,
            escape_path(&join_url_path(&self.public_path, &parts))
        );
        if let Some(query) = query.filter(|q| !q.is_empty()) {
            rewritten.push('?');
            rewritten.push_str(query);
        }
        Some((rewritten, scheme.to_owned(), host.to_owned()))
    }
}

fn valid_host(host: &str) -> bool {
    if host.is_empty()
        || host
            .bytes()
            .any(|b| b <= b' ' || b == b'%' || b == b'"' || b == b'<' || b == b'>' || b == b'\\')
    {
        return false;
    }
    let port = if host.starts_with('[') {
        match host.find(']') {
            Some(end) => &host[end + 1..],
            None => return false,
        }
    } else {
        match host.rfind(':') {
            Some(i) => &host[i..],
            None => "",
        }
    };
    port.is_empty() || (port.starts_with(':') && port[1..].bytes().all(|b| b.is_ascii_digit()))
}

/// Joins path segments like the Go implementation: each part is trimmed of
/// slashes and empty parts are dropped.
fn join_url_path(base: &str, parts: &[&str]) -> String {
    let mut out = String::new();
    for part in std::iter::once(base).chain(parts.iter().copied()) {
        let part = part.trim_matches('/');
        if !part.is_empty() {
            out.push('/');
            out.push_str(part);
        }
    }
    if out.is_empty() {
        out.push('/');
    }
    out
}

struct FoundString<'a> {
    start: usize,
    end: usize,
    path: String,
    value: Cow<'a, str>,
}

enum Segment<'a> {
    Key(Cow<'a, str>),
    Index(usize),
}

const MAX_DEPTH: usize = 512;

/// Validates `text` as JSON and returns every string value (not object key)
/// that starts with `http://` or `https://`. Returns `None` for invalid JSON.
fn scan(text: &str) -> Option<Vec<FoundString<'_>>> {
    let mut scanner = Scanner {
        text,
        bytes: text.as_bytes(),
        pos: 0,
        stack: Vec::new(),
        found: Vec::new(),
    };
    scanner.value()?;
    scanner.skip_ws();
    if scanner.pos != scanner.bytes.len() {
        return None;
    }
    Some(scanner.found)
}

struct Scanner<'a> {
    text: &'a str,
    bytes: &'a [u8],
    pos: usize,
    stack: Vec<Segment<'a>>,
    found: Vec<FoundString<'a>>,
}

impl<'a> Scanner<'a> {
    fn skip_ws(&mut self) {
        while let Some(b' ' | b'\t' | b'\n' | b'\r') = self.bytes.get(self.pos) {
            self.pos += 1;
        }
    }

    fn value(&mut self) -> Option<()> {
        if self.stack.len() > MAX_DEPTH {
            return None;
        }
        self.skip_ws();
        match *self.bytes.get(self.pos)? {
            b'{' => self.object(),
            b'[' => self.array(),
            b'"' => {
                let start = self.pos;
                let value = self.string()?;
                if value.starts_with("http://") || value.starts_with("https://") {
                    self.found.push(FoundString {
                        start,
                        end: self.pos,
                        path: self.path(),
                        value,
                    });
                }
                Some(())
            }
            b't' => self.literal(b"true"),
            b'f' => self.literal(b"false"),
            b'n' => self.literal(b"null"),
            b'-' | b'0'..=b'9' => self.number(),
            _ => None,
        }
    }

    fn object(&mut self) -> Option<()> {
        self.pos += 1;
        self.skip_ws();
        if self.bytes.get(self.pos) == Some(&b'}') {
            self.pos += 1;
            return Some(());
        }
        loop {
            self.skip_ws();
            if self.bytes.get(self.pos) != Some(&b'"') {
                return None;
            }
            let key = self.string()?;
            self.skip_ws();
            if self.bytes.get(self.pos) != Some(&b':') {
                return None;
            }
            self.pos += 1;
            self.stack.push(Segment::Key(key));
            self.value()?;
            self.stack.pop();
            self.skip_ws();
            match self.bytes.get(self.pos)? {
                b',' => self.pos += 1,
                b'}' => {
                    self.pos += 1;
                    return Some(());
                }
                _ => return None,
            }
        }
    }

    fn array(&mut self) -> Option<()> {
        self.pos += 1;
        self.skip_ws();
        if self.bytes.get(self.pos) == Some(&b']') {
            self.pos += 1;
            return Some(());
        }
        let mut index = 0;
        loop {
            self.stack.push(Segment::Index(index));
            self.value()?;
            self.stack.pop();
            index += 1;
            self.skip_ws();
            match self.bytes.get(self.pos)? {
                b',' => self.pos += 1,
                b']' => {
                    self.pos += 1;
                    return Some(());
                }
                _ => return None,
            }
        }
    }

    fn literal(&mut self, word: &[u8]) -> Option<()> {
        if self.bytes[self.pos..].starts_with(word) {
            self.pos += word.len();
            Some(())
        } else {
            None
        }
    }

    fn number(&mut self) -> Option<()> {
        let start = self.pos;
        if self.bytes.get(self.pos) == Some(&b'-') {
            self.pos += 1;
        }
        let digits = |s: &mut Self| {
            let begin = s.pos;
            while s.bytes.get(s.pos).is_some_and(u8::is_ascii_digit) {
                s.pos += 1;
            }
            s.pos > begin
        };
        match self.bytes.get(self.pos) {
            Some(b'0') => self.pos += 1,
            Some(b'1'..=b'9') => {
                digits(self);
            }
            _ => return None,
        }
        if self.bytes.get(self.pos) == Some(&b'.') {
            self.pos += 1;
            if !digits(self) {
                return None;
            }
        }
        if let Some(b'e' | b'E') = self.bytes.get(self.pos) {
            self.pos += 1;
            if let Some(b'+' | b'-') = self.bytes.get(self.pos) {
                self.pos += 1;
            }
            if !digits(self) {
                return None;
            }
        }
        (self.pos > start).then_some(())
    }

    /// Parses a JSON string starting at the opening quote.
    fn string(&mut self) -> Option<Cow<'a, str>> {
        self.pos += 1;
        let start = self.pos;
        loop {
            match *self.bytes.get(self.pos)? {
                b'"' => {
                    let s = &self.text[start..self.pos];
                    self.pos += 1;
                    return Some(Cow::Borrowed(s));
                }
                b'\\' => break,
                b if b < 0x20 => return None,
                _ => self.pos += 1,
            }
        }
        // Slow path for strings with escapes.
        let mut out = String::from(&self.text[start..self.pos]);
        loop {
            match *self.bytes.get(self.pos)? {
                b'"' => {
                    self.pos += 1;
                    return Some(Cow::Owned(out));
                }
                b'\\' => {
                    self.pos += 1;
                    match *self.bytes.get(self.pos)? {
                        b'"' => out.push('"'),
                        b'\\' => out.push('\\'),
                        b'/' => out.push('/'),
                        b'b' => out.push('\u{8}'),
                        b'f' => out.push('\u{c}'),
                        b'n' => out.push('\n'),
                        b'r' => out.push('\r'),
                        b't' => out.push('\t'),
                        b'u' => {
                            let hi = self.hex4()?;
                            let c = if (0xD800..0xDC00).contains(&hi)
                                && self.bytes[self.pos + 1..].starts_with(b"\\u")
                            {
                                self.pos += 2;
                                let lo = self.hex4()?;
                                if (0xDC00..0xE000).contains(&lo) {
                                    char::from_u32(0x10000 + ((hi - 0xD800) << 10) + (lo - 0xDC00))
                                } else {
                                    None
                                }
                            } else {
                                char::from_u32(hi)
                            };
                            out.push(c.unwrap_or('\u{fffd}'));
                        }
                        _ => return None,
                    }
                    self.pos += 1;
                }
                b if b < 0x20 => return None,
                _ => {
                    let c = self.text[self.pos..].chars().next()?;
                    out.push(c);
                    self.pos += c.len_utf8();
                }
            }
        }
    }

    /// Reads the four hex digits after `\u`, leaving `pos` on the last digit.
    fn hex4(&mut self) -> Option<u32> {
        let digits = self.text.get(self.pos + 1..self.pos + 5)?;
        let value = u32::from_str_radix(digits, 16).ok()?;
        self.pos += 4;
        Some(value)
    }

    /// Builds a gjson-style path such as `MediaSources.0.Path`.
    fn path(&self) -> String {
        let mut out = String::new();
        for segment in &self.stack {
            if !out.is_empty() {
                out.push('.');
            }
            match segment {
                Segment::Index(i) => out.push_str(&i.to_string()),
                Segment::Key(key) => {
                    for c in key.chars() {
                        if matches!(c, '\\' | '.' | '*' | '?') {
                            out.push('\\');
                        }
                        out.push(c);
                    }
                }
            }
        }
        out
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn rewriter(registry: Arc<Registry>) -> Rewriter {
        Rewriter::new(HttpUrl::parse("http://proxemby").unwrap(), registry)
    }

    #[test]
    fn rewrites_nested_url_strings() {
        let registry = Arc::new(Registry::new(&[]));
        let body = br#"{
		"MediaSources": [{
			"Path": "https://vod.us.emby.com/movie file.mp4?token=abc",
			"Nested": {"Url": "http://cdn.example.com/subtitle.srt"}
		}],
		"Name": "http-ish but not absolute",
		"ImageTags": {"Primary": "abc"}
	}"#;
        let (out, events) = rewriter(registry.clone()).rewrite_playback_info(body);
        assert_eq!(events.len(), 2);
        assert_eq!(events[0].path, "MediaSources.0.Path");
        assert_eq!(events[1].path, "MediaSources.0.Nested.Url");
        let text = String::from_utf8(out.unwrap()).unwrap();
        assert!(
            text.contains(
                r#""http://proxemby/_proxy/https/vod.us.emby.com/movie%20file.mp4?token=abc""#
            ),
            "{text}"
        );
        assert!(
            text.contains(r#""http://proxemby/_proxy/http/cdn.example.com/subtitle.srt""#),
            "{text}"
        );
        assert!(text.contains(r#""Name": "http-ish but not absolute""#));
        assert!(registry.lookup("vod.us.emby.com").is_some());
        assert_eq!(registry.lookup("cdn.example.com").as_deref(), Some("http"));
    }

    #[test]
    fn ignores_invalid_json_and_non_urls() {
        let r = rewriter(Arc::new(Registry::new(&[])));
        let (out, events) = r.rewrite_playback_info(b"not-json");
        assert!(out.is_none() && events.is_empty());
        let (out, events) =
            r.rewrite_playback_info(br#"{"Text":"ftp://example.com/file","Name":"plain"}"#);
        assert!(out.is_none() && events.is_empty());
        let (out, _) = r.rewrite_playback_info(br#"{"Path":"https://x.example.com/a"} trailing"#);
        assert!(out.is_none());
    }

    #[test]
    fn handles_escapes_keys_and_signatures() {
        let r = rewriter(Arc::new(Registry::new(&[])))
            .with_signer(Arc::new(|scheme, host| format!("sig-{scheme}-{host}")));
        let (out, events) = r.rewrite_playback_info(br#"{"a.b":["x",{"U":"https:\/\/h.example.com:8443\/d\/f.mkv#frag"}],"https://key.example.com":1}"#);
        assert_eq!(events.len(), 1);
        assert_eq!(events[0].path, r"a\.b.1.U");
        assert_eq!(events[0].host, "h.example.com:8443");
        assert_eq!(
            String::from_utf8(out.unwrap()).unwrap(),
            r#"{"a.b":["x",{"U":"http://proxemby/_proxy/sig-https-h.example.com:8443/https/h.example.com:8443/d/f.mkv"}],"https://key.example.com":1}"#
        );
    }

    #[test]
    fn scanner_rejects_malformed_json() {
        for bad in [
            "{",
            r#"{"a":}"#,
            r#"{"a":1,}"#,
            "[1,]",
            "01",
            r#""\x""#,
            "-",
            "1.",
            "tru",
        ] {
            assert!(scan(bad).is_none(), "{bad}");
        }
        for good in [
            "1",
            "-0.5e+3",
            "[]",
            "{}",
            r#"{"a":[true,false,null]}"#,
            r#""😀""#,
        ] {
            assert!(scan(good).is_some(), "{good}");
        }
    }
}
