//! Sessions accepted through the allowed-users check, plus the secret used to
//! sign resource URLs. The state file format matches the Go implementation.

use std::collections::HashMap;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::sync::RwLock;
use std::time::SystemTime;

use base64::Engine;
use base64::engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD};
use hmac::{Hmac, Mac};
use http::HeaderMap;
use serde::{Deserialize, Serialize};
use sha2::Sha256;

use crate::util::parse_query;

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Session {
    #[serde(default)]
    pub route: String,
    #[serde(default)]
    pub user_id: String,
    #[serde(default)]
    pub user_name: String,
    #[serde(default)]
    pub created_at: String,
}

impl Session {
    pub fn new(route: &str, user_id: &str, user_name: &str) -> Session {
        Session {
            route: route.to_owned(),
            user_id: user_id.to_owned(),
            user_name: user_name.to_owned(),
            created_at: crate::logging::format_time(SystemTime::now()),
        }
    }
}

#[derive(Serialize, Deserialize)]
struct StoreFile {
    secret: String,
    #[serde(default)]
    sessions: Option<HashMap<String, Session>>,
}

pub struct Store {
    path: Option<PathBuf>,
    secret: Vec<u8>,
    sessions: RwLock<HashMap<String, Session>>,
}

impl Store {
    pub fn memory() -> Store {
        Store::open(None).expect("in-memory auth store")
    }

    /// Opens the state file, creating it when missing. `None` keeps everything
    /// in memory.
    pub fn open(path: Option<&Path>) -> Result<Store, String> {
        let mut secret = Vec::new();
        let mut sessions = HashMap::new();
        if let Some(path) = path {
            match std::fs::read(path) {
                Ok(data) => {
                    let file: StoreFile = serde_json::from_slice(&data)
                        .map_err(|e| format!("load auth state {}: {e}", path.display()))?;
                    secret = STANDARD.decode(file.secret).map_err(|e| {
                        format!("load auth state {}: invalid secret: {e}", path.display())
                    })?;
                    sessions = file.sessions.unwrap_or_default();
                }
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
                Err(e) => return Err(format!("load auth state {}: {e}", path.display())),
            }
        }
        let fresh = secret.is_empty();
        if fresh {
            secret = vec![0; 32];
            getrandom::fill(&mut secret).map_err(|e| format!("generate auth secret: {e}"))?;
        }
        let store = Store {
            path: path.map(Path::to_path_buf),
            secret,
            sessions: RwLock::new(sessions),
        };
        if fresh {
            store.save(&store.sessions.read().unwrap())?;
        }
        Ok(store)
    }

    pub fn add(&self, token: &str, session: Session) -> Result<(), String> {
        if token.is_empty() {
            return Err("empty access token".into());
        }
        let mut sessions = self.sessions.write().unwrap();
        sessions.insert(token.to_owned(), session);
        self.save(&sessions)
    }

    pub fn lookup(&self, token: &str) -> Option<Session> {
        if token.is_empty() {
            return None;
        }
        self.sessions.read().unwrap().get(token).cloned()
    }

    /// Reports whether `token` belongs to a session on `route`.
    pub fn is_valid(&self, token: &str, route: &str) -> bool {
        !token.is_empty()
            && self
                .sessions
                .read()
                .unwrap()
                .get(token)
                .is_some_and(|s| s.route == route)
    }

    pub fn remove(&self, token: &str) -> Result<(), String> {
        let mut sessions = self.sessions.write().unwrap();
        if sessions.remove(token).is_none() {
            return Ok(());
        }
        self.save(&sessions)
    }

    /// Returns a URL-safe signature for the given parts.
    pub fn sign(&self, parts: &[&str]) -> String {
        URL_SAFE_NO_PAD.encode(&self.mac(parts).finalize().into_bytes()[..16])
    }

    pub fn verify(&self, signature: &str, parts: &[&str]) -> bool {
        match URL_SAFE_NO_PAD.decode(signature) {
            Ok(decoded) if decoded.len() == 16 => {
                self.mac(parts).verify_truncated_left(&decoded).is_ok()
            }
            _ => false,
        }
    }

    fn mac(&self, parts: &[&str]) -> Hmac<Sha256> {
        let mut mac =
            Hmac::<Sha256>::new_from_slice(&self.secret).expect("hmac accepts any key length");
        for part in parts {
            mac.update(part.as_bytes());
            mac.update(&[0]);
        }
        mac
    }

    fn save(&self, sessions: &HashMap<String, Session>) -> Result<(), String> {
        let Some(path) = &self.path else {
            return Ok(());
        };
        let data = serde_json::to_vec_pretty(&StoreFile {
            secret: STANDARD.encode(&self.secret),
            sessions: Some(sessions.clone()),
        })
        .map_err(|e| e.to_string())?;
        let dir = path
            .parent()
            .filter(|d| !d.as_os_str().is_empty())
            .unwrap_or(Path::new("."));
        let file_name = path.file_name().and_then(|n| n.to_str()).unwrap_or("auth");
        let mut suffix = [0u8; 8];
        let _ = getrandom::fill(&mut suffix);
        let tmp = dir.join(format!("{file_name}.tmp{}", u64::from_le_bytes(suffix)));
        let result = (|| {
            let mut options = std::fs::OpenOptions::new();
            options.write(true).create_new(true);
            #[cfg(unix)]
            std::os::unix::fs::OpenOptionsExt::mode(&mut options, 0o600);
            let mut file = options.open(&tmp)?;
            file.write_all(&data)?;
            file.sync_all()?;
            std::fs::rename(&tmp, path)
        })();
        if result.is_err() {
            let _ = std::fs::remove_file(&tmp);
        }
        result.map_err(|e| format!("save auth state: {e}"))
    }
}

const TOKEN_HEADERS: &[&str] = &["x-emby-token", "x-mediabrowser-token"];
const AUTHORIZATION_HEADERS: &[&str] = &["x-emby-authorization", "authorization"];
const TOKEN_QUERY_KEYS: &[&str] = &["api_key", "apikey", "x-emby-token", "x-mediabrowser-token"];

/// Returns the Emby access token sent by a client, if any.
pub fn request_token(headers: &HeaderMap, query: Option<&str>) -> Option<String> {
    for name in TOKEN_HEADERS {
        if let Some(token) = headers
            .get(*name)
            .and_then(|v| v.to_str().ok())
            .map(str::trim)
            .filter(|t| !t.is_empty())
        {
            return Some(token.to_owned());
        }
    }
    for name in AUTHORIZATION_HEADERS {
        if let Some(token) = headers
            .get(*name)
            .and_then(|v| v.to_str().ok())
            .and_then(authorization_token)
        {
            return Some(token.to_owned());
        }
    }
    let query = query?;
    if query.is_empty() {
        return None;
    }
    let (pairs, _) = parse_query(query);
    pairs
        .into_iter()
        .find(|(key, value)| {
            TOKEN_QUERY_KEYS.contains(&key.to_ascii_lowercase().as_str())
                && !value.trim().is_empty()
        })
        .map(|(_, value)| value.trim().to_owned())
}

/// Extracts `Token="..."` from an Emby authorization header.
fn authorization_token(header: &str) -> Option<&str> {
    let bytes = header.as_bytes();
    let mut i = 0;
    while i + 5 <= bytes.len() {
        let at_boundary = i == 0 || matches!(bytes[i - 1], b' ' | b'\t' | b',');
        if at_boundary && bytes[i..i + 5].eq_ignore_ascii_case(b"token") {
            let mut j = i + 5;
            while j < bytes.len() && bytes[j].is_ascii_whitespace() {
                j += 1;
            }
            if bytes.get(j) == Some(&b'=') {
                j += 1;
                while j < bytes.len() && bytes[j].is_ascii_whitespace() {
                    j += 1;
                }
                if bytes.get(j) == Some(&b'"') {
                    j += 1;
                }
                let start = j;
                while j < bytes.len()
                    && !matches!(bytes[j], b'"' | b',')
                    && !bytes[j].is_ascii_whitespace()
                {
                    j += 1;
                }
                if j > start {
                    return Some(&header[start..j]);
                }
            }
        }
        i += 1;
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    fn headers(pairs: &[(&str, &str)]) -> HeaderMap {
        let mut map = HeaderMap::new();
        for (k, v) in pairs {
            map.insert(
                http::HeaderName::from_bytes(k.as_bytes()).unwrap(),
                v.parse().unwrap(),
            );
        }
        map
    }

    #[test]
    fn request_token_sources() {
        type Case<'a> = (&'a [(&'a str, &'a str)], Option<&'a str>, Option<&'a str>);
        let cases: &[Case] = &[
            (&[], None, None),
            (&[("X-Emby-Token", "abc")], None, Some("abc")),
            (&[("X-MediaBrowser-Token", "abc")], None, Some("abc")),
            (
                &[(
                    "X-Emby-Authorization",
                    r#"MediaBrowser Client="Emby Web", Device="Chrome, Mac", DeviceId="d1", Version="4.8", Token="abc""#,
                )],
                None,
                Some("abc"),
            ),
            (
                &[(
                    "Authorization",
                    r#"MediaBrowser Client="Emby Web", DeviceId="d1""#,
                )],
                None,
                None,
            ),
            (
                &[("Authorization", r#"MediaBrowser AccessToken="nope""#)],
                None,
                None,
            ),
            (&[], Some("api_key=abc"), Some("abc")),
            (&[], Some("X-Emby-Token=abc&deviceId=d1"), Some("abc")),
        ];
        for (h, q, want) in cases {
            assert_eq!(
                request_token(&headers(h), *q).as_deref(),
                *want,
                "{h:?} {q:?}"
            );
        }
    }

    #[test]
    fn store_persists_sessions_and_secret() {
        let dir = std::env::temp_dir().join(format!("proxemby-auth-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("auth.json");
        let _ = std::fs::remove_file(&path);

        let store = Store::open(Some(&path)).unwrap();
        store
            .add("tok", Session::new("proxemby", "id", "ken"))
            .unwrap();
        let signature = store.sign(&["proxemby", "https", "cdn.example.com"]);

        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(
                std::fs::metadata(&path).unwrap().permissions().mode() & 0o777,
                0o600
            );
        }

        let reopened = Store::open(Some(&path)).unwrap();
        assert_eq!(reopened.lookup("tok").unwrap().user_name, "ken");
        assert!(reopened.is_valid("tok", "proxemby"));
        assert!(!reopened.is_valid("tok", "other"));
        assert!(reopened.verify(&signature, &["proxemby", "https", "cdn.example.com"]));
        assert!(!reopened.verify(&signature, &["proxemby", "https", "other.example.com"]));
        assert!(!reopened.verify("AAAA", &["proxemby", "https", "cdn.example.com"]));

        reopened.remove("tok").unwrap();
        assert!(Store::open(Some(&path)).unwrap().lookup("tok").is_none());
        std::fs::remove_dir_all(&dir).unwrap();
    }

    #[test]
    fn signature_matches_go_implementation() {
        // HMAC-SHA256 over NUL-terminated parts, first 16 bytes, base64url.
        let store = Store {
            path: None,
            secret: b"0123456789abcdef0123456789abcdef".to_vec(),
            sessions: RwLock::new(HashMap::new()),
        };
        let mut mac = Hmac::<Sha256>::new_from_slice(&store.secret).unwrap();
        mac.update(b"r\0https\0h\0");
        let expected = URL_SAFE_NO_PAD.encode(&mac.finalize().into_bytes()[..16]);
        assert_eq!(store.sign(&["r", "https", "h"]), expected);
        assert_eq!(expected.len(), 22);
    }
}
