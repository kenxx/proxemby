package server

import (
	"bytes"
	"errors"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"strconv"

	"proxemby/internal/logging"
	"proxemby/internal/rewrite"
)

func (s *routeProxy) newUpstreamProxy() *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{
		Transport: noCompressionTransport(),
		Rewrite: func(req *httputil.ProxyRequest) {
			playbackInfo := isPlaybackInfoPath(req.In.URL.Path)
			req.SetURL(s.upstreamTarget)
			req.Out.Host = s.upstreamTarget.Host
			if s.cfg.HideClient {
				s.logger.Debug("upstream request hides client identity headers", "path", sanitizeRequestURI(req.In.URL), "target", s.upstreamTarget.String())
				deleteProxyIdentityHeaders(req.Out.Header)
			} else {
				req.SetXForwarded()
			}
			if playbackInfo {
				req.Out.Header.Del("Accept-Encoding")
			}
		},
		ModifyResponse: s.modifyUpstreamResponse,
		ErrorLog:       log.New(logging.NewSlogWriter(s.logger, slog.LevelError), "", 0),
	}
	return proxy
}

func deleteProxyIdentityHeaders(header http.Header) {
	for _, name := range []string{
		"Forwarded",
		"Via",
		"X-Forwarded-For",
		"X-Forwarded-Host",
		"X-Forwarded-Proto",
		"X-Forwarded-Protocol",
		"X-Forwarded-Ssl",
		"X-Real-IP",
	} {
		header.Del(name)
	}
}

func (s *routeProxy) modifyUpstreamResponse(resp *http.Response) error {
	if !isPlaybackInfoPath(resp.Request.URL.Path) || !isJSONContentType(resp.Header.Get("Content-Type")) {
		return nil
	}

	body, err := readResponseBody(resp, s.cfg.PlaybackInfoMaxBytes)
	if err != nil {
		if errors.Is(err, errResponseTooLarge) {
			s.logger.Warn("playbackinfo response rejected", "reason", "response_too_large", "max_bytes", s.cfg.PlaybackInfoMaxBytes, "path", sanitizeRequestURI(resp.Request.URL))
		}
		return err
	}
	rewritten, events, err := s.rewriter.RewritePlaybackInfo(body)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		rewritten = body
	}
	s.logRewriteEvents(resp.Request, events)

	resp.Body = io.NopCloser(bytes.NewReader(rewritten))
	resp.ContentLength = int64(len(rewritten))
	resp.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
	resp.Header.Del("Content-Encoding")
	return nil
}

func (s *routeProxy) logRewriteEvents(req *http.Request, events []rewrite.RewriteEvent) {
	s.logger.Debug("playbackinfo rewrite", "path", sanitizeRequestURI(req.URL), "count", len(events))
	for _, event := range events {
		s.logger.Debug(
			"playbackinfo rewrite item",
			"json_path", event.Path,
			"scheme", event.Scheme,
			"host", event.Host,
			"from", sanitizeURLString(event.Original),
			"to", sanitizeURLString(event.Rewritten),
		)
	}
}
