//! Path rules for the allowed-users check.

use http::Method;

/// Returns lower-cased path segments without the optional `emby` or
/// `mediabrowser` base path.
pub(crate) fn emby_path_segments(path: &str) -> Vec<String> {
    let trimmed = path.trim_matches('/');
    if trimmed.is_empty() {
        return Vec::new();
    }
    let mut segments: Vec<String> = trimmed.split('/').map(str::to_ascii_lowercase).collect();
    if matches!(segments[0].as_str(), "emby" | "mediabrowser") {
        segments.remove(0);
    }
    segments
}

pub(crate) fn match_segments(segments: &[String], want: &[&str]) -> bool {
    segments.len() == want.len() && segments.iter().zip(want).all(|(s, w)| *w == "*" || s == w)
}

/// Reports whether the path is an Emby login endpoint, allowing for an
/// upstream base path in front of it.
pub(crate) fn is_login_path(path: &str) -> bool {
    let segments = emby_path_segments(path);
    segments.iter().enumerate().any(|(i, segment)| {
        segment == "users" && {
            let rest = &segments[i..];
            match_segments(rest, &["users", "authenticatebyname"])
                || match_segments(rest, &["users", "*", "authenticate"])
        }
    })
}

/// Endpoints clients need before they have logged in.
pub(crate) fn is_public_emby_path(method: &Method, segments: &[String]) -> bool {
    if segments.is_empty()
        || [
            &["favicon.ico"][..],
            &["system", "info", "public"],
            &["system", "ping"],
            &["users", "authenticatebyname"],
            &["users", "*", "authenticate"],
            &["branding", "configuration"],
            &["branding", "css"],
            &["branding", "css.css"],
        ]
        .iter()
        .any(|want| match_segments(segments, want))
        // Static files for the Emby web client.
        || segments[0] == "web"
    {
        return true;
    }
    if method != Method::GET && method != Method::HEAD {
        return false;
    }
    // Most clients load artwork without an access token.
    segments.iter().any(|s| s == "images")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn login_paths() {
        assert!(is_login_path("/emby/Users/AuthenticateByName"));
        assert!(is_login_path("/base/emby/Users/abc/Authenticate"));
        assert!(!is_login_path("/emby/Users/abc/Items"));
    }

    #[test]
    fn public_paths() {
        let public = |m: Method, p: &str| is_public_emby_path(&m, &emby_path_segments(p));
        assert!(public(Method::GET, "/emby/System/Info/Public"));
        assert!(public(Method::GET, "/"));
        assert!(public(Method::GET, "/web/index.html"));
        assert!(public(Method::GET, "/emby/Items/1/Images/Primary"));
        assert!(!public(Method::POST, "/emby/Items/1/Images/Primary"));
        assert!(!public(Method::GET, "/emby/Users/1/Items"));
    }
}
