use std::pin::Pin;
use std::task::{Context, Poll};

use bytes::Bytes;
use http_body::{Body, Frame, SizeHint};
use hyper::body::Incoming;

use crate::stats::Counters;

/// The body type used for both proxied requests and responses. Streaming
/// bodies are passed through without boxing or copying.
pub enum ProxyBody {
    Incoming(Incoming),
    /// A streamed body whose bytes are added to the traffic statistics.
    Metered(Incoming, Meter),
    Full(Option<Bytes>),
    Empty,
    Counted(Box<CountedBody>),
}

/// Counts streamed bytes and reports them when the body is dropped, which
/// happens once the response has been fully written or the client went away.
pub struct CountedBody {
    inner: ProxyBody,
    bytes: u64,
    on_done: Option<Box<dyn FnOnce(u64) + Send>>,
}

/// Adds streamed bytes to one route and kind. A response meter also counts
/// the response as active until the body is dropped.
pub struct Meter {
    counters: &'static Counters,
    response: bool,
}

impl Meter {
    /// Meters a request body received from a client.
    pub fn request(counters: &'static Counters) -> Meter {
        Meter {
            counters,
            response: false,
        }
    }

    /// Meters a response body sent to a client.
    pub fn response(counters: &'static Counters) -> Meter {
        counters.stream_started();
        Meter {
            counters,
            response: true,
        }
    }

    #[inline]
    fn add(&self, bytes: usize) {
        if self.response {
            self.counters.add_bytes_out(bytes as u64);
        } else {
            self.counters.add_bytes_in(bytes as u64);
        }
    }
}

impl Drop for Meter {
    fn drop(&mut self) {
        if self.response {
            self.counters.stream_finished();
        }
    }
}

impl ProxyBody {
    pub fn full(data: impl Into<Bytes>) -> ProxyBody {
        ProxyBody::Full(Some(data.into()))
    }

    pub fn counted(inner: ProxyBody, on_done: impl FnOnce(u64) + Send + 'static) -> ProxyBody {
        ProxyBody::Counted(Box::new(CountedBody {
            inner,
            bytes: 0,
            on_done: Some(Box::new(on_done)),
        }))
    }
}

impl Drop for CountedBody {
    fn drop(&mut self) {
        if let Some(on_done) = self.on_done.take() {
            on_done(self.bytes);
        }
    }
}

impl Body for ProxyBody {
    type Data = Bytes;
    type Error = hyper::Error;

    #[inline]
    fn poll_frame(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
    ) -> Poll<Option<Result<Frame<Bytes>, hyper::Error>>> {
        match self.get_mut() {
            ProxyBody::Incoming(body) => Pin::new(body).poll_frame(cx),
            ProxyBody::Metered(body, meter) => {
                let poll = Pin::new(body).poll_frame(cx);
                if let Poll::Ready(Some(Ok(frame))) = &poll {
                    if let Some(data) = frame.data_ref() {
                        meter.add(data.len());
                    }
                }
                poll
            }
            ProxyBody::Full(data) => Poll::Ready(
                data.take()
                    .filter(|d| !d.is_empty())
                    .map(|d| Ok(Frame::data(d))),
            ),
            ProxyBody::Empty => Poll::Ready(None),
            ProxyBody::Counted(counted) => {
                let poll = Pin::new(&mut counted.inner).poll_frame(cx);
                if let Poll::Ready(Some(Ok(frame))) = &poll {
                    if let Some(data) = frame.data_ref() {
                        counted.bytes += data.len() as u64;
                    }
                }
                poll
            }
        }
    }

    #[inline]
    fn is_end_stream(&self) -> bool {
        match self {
            ProxyBody::Incoming(body) | ProxyBody::Metered(body, _) => body.is_end_stream(),
            ProxyBody::Full(data) => data.as_ref().is_none_or(Bytes::is_empty),
            ProxyBody::Empty => true,
            ProxyBody::Counted(counted) => counted.inner.is_end_stream(),
        }
    }

    #[inline]
    fn size_hint(&self) -> SizeHint {
        match self {
            ProxyBody::Incoming(body) | ProxyBody::Metered(body, _) => body.size_hint(),
            ProxyBody::Full(data) => {
                SizeHint::with_exact(data.as_ref().map_or(0, |d| d.len() as u64))
            }
            ProxyBody::Empty => SizeHint::with_exact(0),
            ProxyBody::Counted(counted) => counted.inner.size_hint(),
        }
    }
}
