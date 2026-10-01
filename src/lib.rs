//! proxemby: a small edge proxy for Emby servers.

#[macro_use]
pub mod logging;

pub mod auth;
pub mod config;
pub mod hosts;
pub mod rewrite;
pub mod server;
pub mod util;
