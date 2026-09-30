use std::collections::HashMap;
use std::sync::RwLock;

/// Hosts that the `/_proxy/` resource endpoint may forward to.
#[derive(Default)]
pub struct Registry {
    schemes: RwLock<HashMap<String, String>>,
}

impl Registry {
    pub fn new(initial: &[String]) -> Registry {
        let registry = Registry::default();
        for host in initial {
            registry.allow(host, "https");
        }
        registry
    }

    pub fn allow(&self, host: &str, scheme: &str) {
        if host.is_empty() {
            return;
        }
        let scheme = if scheme == "http" || scheme == "https" {
            scheme
        } else {
            "https"
        };
        if let Ok(schemes) = self.schemes.read() {
            if schemes.get(host).is_some_and(|s| s == scheme) {
                return;
            }
        }
        if let Ok(mut schemes) = self.schemes.write() {
            schemes.insert(host.to_owned(), scheme.to_owned());
        }
    }

    pub fn lookup(&self, host: &str) -> Option<String> {
        self.schemes.read().ok()?.get(host).cloned()
    }
}
