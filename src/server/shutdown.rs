use std::time::Duration;

use tokio::sync::{mpsc, watch};

/// Coordinates a graceful shutdown: listeners stop accepting, connections
/// finish their in-flight requests, and [`Shutdown::shutdown`] waits for them.
pub struct Shutdown {
    trigger: watch::Sender<bool>,
    signal: ShutdownSignal,
    done: mpsc::Receiver<()>,
}

/// Held by listeners and connections. Shutdown completes once every clone
/// has been dropped.
#[derive(Clone)]
pub struct ShutdownSignal {
    triggered: watch::Receiver<bool>,
    _alive: mpsc::Sender<()>,
}

impl Shutdown {
    pub fn new() -> Shutdown {
        let (trigger, triggered) = watch::channel(false);
        let (alive, done) = mpsc::channel(1);
        Shutdown {
            trigger,
            signal: ShutdownSignal {
                triggered,
                _alive: alive,
            },
            done,
        }
    }

    pub fn signal(&self) -> ShutdownSignal {
        self.signal.clone()
    }

    /// Asks everything to stop and waits up to `grace` for open connections
    /// to finish. Returns whether they all finished in time.
    pub async fn shutdown(self, grace: Duration) -> bool {
        let Shutdown {
            trigger,
            signal,
            mut done,
        } = self;
        let _ = trigger.send(true);
        drop(signal);
        tokio::time::timeout(grace, done.recv()).await.is_ok()
    }
}

impl Default for Shutdown {
    fn default() -> Self {
        Shutdown::new()
    }
}

impl ShutdownSignal {
    /// Resolves once shutdown has been requested.
    pub async fn triggered(&self) {
        let mut triggered = self.triggered.clone();
        let _ = triggered.wait_for(|stop| *stop).await;
    }
}
