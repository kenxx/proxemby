use std::net::IpAddr;

use http::HeaderMap;

use super::ConnInfo;
use crate::util::{IpPrefix, first_forwarded_for, parse_client_addr};

/// Returns the client address and where it came from.
pub(crate) fn client_addr(
    headers: &HeaderMap,
    conn: &ConnInfo,
    trust_proxy_headers: bool,
) -> (IpAddr, &'static str) {
    if trust_proxy_headers {
        if let Some(addr) = header_str(headers, "x-forwarded-for")
            .map(first_forwarded_for)
            .and_then(parse_client_addr)
        {
            return (addr, "x_forwarded_for");
        }
        if let Some(addr) = header_str(headers, "x-real-ip").and_then(parse_client_addr) {
            return (addr, "x_real_ip");
        }
    }
    (conn.remote.ip(), "remote_addr")
}

pub(crate) fn is_allowed(allowed: &[IpPrefix], addr: IpAddr) -> bool {
    allowed.iter().any(|prefix| prefix.contains(addr))
}

pub(crate) fn header_str<'a>(headers: &'a HeaderMap, name: &str) -> Option<&'a str> {
    headers.get(name).and_then(|v| v.to_str().ok())
}
