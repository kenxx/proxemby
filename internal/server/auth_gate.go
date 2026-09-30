package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"proxemby/internal/auth"
)

const upstreamLogoutTimeout = 10 * time.Second

// authGate only lets requests through when they carry an access token that was
// issued to an allowed user by a login proxied through this route. Login and a
// few pre-login endpoints stay public so clients can still sign in.
func (s *routeProxy) authGate(next http.Handler) http.Handler {
	if s.auth == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, resourcePrefix) {
			// Resource URLs are checked by handleResourceProxy.
			next.ServeHTTP(w, req)
			return
		}
		segments := embyPathSegments(req.URL.Path)
		if matchSegments(segments, "users", "public") {
			// Do not reveal the upstream user list.
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, "[]")
			return
		}
		if isPublicEmbyPath(req.Method, segments) {
			next.ServeHTTP(w, req)
			return
		}
		token := auth.RequestToken(req)
		if _, ok := s.lookupSession(token); !ok {
			reason := "token_unknown"
			if token == "" {
				reason = "token_missing"
			}
			s.logger.Warn("auth gate rejected request", "reason", reason, "path", sanitizeRequestURI(req.URL))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, req)
		if matchSegments(segments, "sessions", "logout") {
			if err := s.auth.Remove(token); err != nil {
				s.logger.Error("auth session remove failed", "error", err)
			}
		}
	})
}

func (s *routeProxy) lookupSession(token string) (auth.Session, bool) {
	session, ok := s.auth.Lookup(token)
	if !ok || session.Route != s.routeKey {
		return auth.Session{}, false
	}
	return session, true
}

func (s *routeProxy) signResourceHost(scheme, host string) string {
	return s.auth.Sign(s.routeKey, scheme, host)
}

// authorizeResource checks the first path segment after /_proxy/. It is either
// a signature issued by the PlaybackInfo rewriter or, for unsigned URLs, the
// scheme, in which case the request needs a valid access token.
func (s *routeProxy) authorizeResource(req *http.Request, first, scheme, host string) (bool, string) {
	if isHTTPProxyScheme(first) {
		if _, ok := s.lookupSession(auth.RequestToken(req)); ok {
			return true, ""
		}
		return false, "unsigned_without_token"
	}
	if s.auth.Verify(first, s.routeKey, scheme, host) {
		return true, ""
	}
	return false, "invalid_signature"
}

// handleLoginResponse only hands the access token back to the client when the
// upstream login belongs to an allowed user.
func (s *routeProxy) handleLoginResponse(resp *http.Response) error {
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := readResponseBody(resp, s.cfg.PlaybackInfoMaxBytes)
	if err != nil {
		return err
	}
	token := gjson.GetBytes(body, "AccessToken").String()
	userName := gjson.GetBytes(body, "User.Name").String()
	userID := gjson.GetBytes(body, "User.Id").String()

	if _, allowed := s.allowedUsers[strings.ToLower(userName)]; allowed && token != "" {
		err := s.auth.Add(token, auth.Session{
			Route:     s.routeKey,
			UserID:    userID,
			UserName:  userName,
			CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			return err
		}
		s.logger.Info("login accepted", "user", userName)
		setResponseBody(resp, body)
		return nil
	}

	s.logger.Warn("login rejected", "reason", "user_not_allowed", "user", userName)
	if token != "" {
		go s.logoutUpstream(resp.Request.URL, token)
	}
	resp.StatusCode = http.StatusUnauthorized
	resp.Status = http.StatusText(http.StatusUnauthorized)
	resp.Header = http.Header{}
	resp.Header.Set("Content-Type", "text/plain; charset=utf-8")
	setResponseBody(resp, []byte("user is not allowed\n"))
	return nil
}

// logoutUpstream revokes a token for a user that is not allowed so the
// upstream session does not linger.
func (s *routeProxy) logoutUpstream(loginURL *url.URL, token string) {
	segments := strings.Split(loginURL.Path, "/")
	prefix := loginURL.Path
	for i := len(segments) - 1; i >= 0; i-- {
		if strings.EqualFold(segments[i], "users") {
			prefix = strings.Join(segments[:i], "/")
			break
		}
	}
	target := *loginURL
	target.Path = prefix + "/Sessions/Logout"
	target.RawPath = ""
	target.RawQuery = ""

	ctx, cancel := context.WithTimeout(context.Background(), upstreamLogoutTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), nil)
	if err != nil {
		return
	}
	req.Header.Set("X-Emby-Token", token)
	resp, err := s.upstreamProxy.Transport.RoundTrip(req)
	if err != nil {
		s.logger.Warn("upstream logout failed", "error", err)
		return
	}
	resp.Body.Close()
}

func setResponseBody(resp *http.Response, body []byte) {
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.Header.Del("Content-Encoding")
}

// embyPathSegments returns lower-cased path segments without the optional
// "emby" or "mediabrowser" base path.
func embyPathSegments(path string) []string {
	segments := strings.Split(strings.Trim(strings.ToLower(path), "/"), "/")
	if len(segments) > 0 && (segments[0] == "emby" || segments[0] == "mediabrowser") {
		segments = segments[1:]
	}
	if len(segments) == 1 && segments[0] == "" {
		return nil
	}
	return segments
}

func matchSegments(segments []string, want ...string) bool {
	if len(segments) != len(want) {
		return false
	}
	for i := range want {
		if want[i] != "*" && segments[i] != want[i] {
			return false
		}
	}
	return true
}

func isLoginPath(path string) bool {
	segments := embyPathSegments(path)
	for i, segment := range segments {
		if segment != "users" {
			continue
		}
		rest := segments[i:]
		if matchSegments(rest, "users", "authenticatebyname") || matchSegments(rest, "users", "*", "authenticate") {
			return true
		}
	}
	return false
}

func isPublicEmbyPath(method string, segments []string) bool {
	switch {
	case len(segments) == 0,
		matchSegments(segments, "favicon.ico"),
		matchSegments(segments, "system", "info", "public"),
		matchSegments(segments, "system", "ping"),
		matchSegments(segments, "users", "authenticatebyname"),
		matchSegments(segments, "users", "*", "authenticate"),
		matchSegments(segments, "branding", "configuration"),
		matchSegments(segments, "branding", "css"),
		matchSegments(segments, "branding", "css.css"):
		return true
	case segments[0] == "web":
		// Static files for the Emby web client.
		return true
	}
	if method != http.MethodGet && method != http.MethodHead {
		return false
	}
	// Most clients load artwork without an access token.
	for _, segment := range segments {
		if segment == "images" {
			return true
		}
	}
	return false
}
