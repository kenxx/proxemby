package proxemby

import (
	"bufio"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var sensitiveQueryKeys = map[string]struct{}{
	"api_key":      {},
	"apikey":       {},
	"access_token": {},
	"token":        {},
	"auth":         {},
	"password":     {},
}

type debugLogger struct {
	upstreamTarget string
	next           http.Handler
}

func newDebugLogger(enabled bool, upstreamTarget *url.URL, next http.Handler) http.Handler {
	if !enabled {
		return next
	}
	return &debugLogger{
		upstreamTarget: upstreamTarget.String(),
		next:           next,
	}
}

func (l *debugLogger) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	rec := &statusRecorder{
		ResponseWriter: w,
		status:         http.StatusOK,
	}

	l.next.ServeHTTP(rec, req)

	log.Printf(
		"debug request method=%s path=%s status=%d bytes=%d duration=%s client=%s target=%s ua=%q",
		req.Method,
		sanitizeRequestURI(req.URL),
		rec.status,
		rec.bytes,
		time.Since(start).Round(time.Millisecond),
		clientAddrForLog(req),
		l.targetForLog(req),
		req.UserAgent(),
	)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	n, err := r.ResponseWriter.Write(body)
	r.bytes += int64(n)
	return n, err
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func sanitizeRequestURI(u *url.URL) string {
	out := *u
	out.RawQuery = sanitizeRawQuery(out.RawQuery)
	return out.RequestURI()
}

func sanitizeURLString(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = sanitizeRawQuery(u.RawQuery)
	return u.String()
}

func sanitizeRawQuery(raw string) string {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	for key := range query {
		if _, ok := sensitiveQueryKeys[strings.ToLower(key)]; ok {
			query.Set(key, "redacted")
		}
	}
	return query.Encode()
}

func clientAddrForLog(req *http.Request) string {
	if addr, ok := parseRemoteAddr(req.RemoteAddr); ok {
		return addr.String()
	}
	return req.RemoteAddr
}

func (l *debugLogger) targetForLog(req *http.Request) string {
	if strings.HasPrefix(req.URL.Path, resourcePrefix) {
		scheme, remainder, ok := strings.Cut(strings.TrimPrefix(req.URL.Path, resourcePrefix), "/")
		if !ok {
			return "resource:invalid"
		}
		host, _, ok := strings.Cut(remainder, "/")
		if !ok {
			return "resource:invalid"
		}
		return "resource:" + scheme + "://" + host
	}
	return "upstream:" + l.upstreamTarget
}
