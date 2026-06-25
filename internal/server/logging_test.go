package server

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestServerDebugLogsSanitizedRequest(t *testing.T) {
	var logs bytes.Buffer
	logger := testLogger(t, &logs, slog.LevelDebug, "text", false)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	publicURL, _ := url.Parse("http://proxemby")
	cfg := testConfig(upstreamURL, publicURL)
	server := NewServerWithLogger(cfg, logger)
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	req, _ := http.NewRequest(http.MethodGet, proxy.URL+"/emby/System/Info?api_key=secret&token=abc&device=ios", nil)
	req.Host = publicURL.Host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	text := logs.String()
	for _, want := range []string{
		`level=DEBUG msg="request completed"`,
		"method=GET",
		"api_key=redacted",
		"token=redacted",
		"device=ios",
		"status=200",
		"target=upstream:" + upstream.URL,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("debug log %q missing %q", text, want)
		}
	}
	if strings.Contains(text, "secret") || strings.Contains(text, "token=abc") {
		t.Fatalf("debug log leaked sensitive query values: %q", text)
	}
}

func TestServerDebugLogsPlaybackInfoRewrite(t *testing.T) {
	var logs bytes.Buffer
	logger := testLogger(t, &logs, slog.LevelDebug, "text", false)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Path":"https://vod.us.emby.com/movie.mp4?token=secret"}`)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	publicURL, _ := url.Parse("http://proxemby")
	cfg := testConfig(upstreamURL, publicURL)
	server := NewServerWithLogger(cfg, logger)
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	req, _ := http.NewRequest(http.MethodGet, proxy.URL+"/emby/Items/1/PlaybackInfo?api_key=secret", nil)
	req.Host = publicURL.Host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	text := logs.String()
	for _, want := range []string{
		`level=DEBUG msg="playbackinfo rewrite"`,
		`path="/emby/Items/1/PlaybackInfo?api_key=redacted"`,
		"count=1",
		`level=DEBUG msg="playbackinfo rewrite item"`,
		"json_path=Path",
		"scheme=https",
		"host=vod.us.emby.com",
		`from="https://vod.us.emby.com/movie.mp4?token=redacted"`,
		`to="http://proxemby/_proxy/https/vod.us.emby.com/movie.mp4?token=redacted"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("debug log %q missing %q", text, want)
		}
	}
	if strings.Contains(text, "token=secret") || strings.Contains(text, "api_key=secret") {
		t.Fatalf("debug log leaked sensitive query values: %q", text)
	}
}

func TestServerLogsRuleDecisions(t *testing.T) {
	var logs bytes.Buffer
	logger := testLogger(t, &logs, slog.LevelDebug, "text", false)

	resource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "movie")
	}))
	defer resource.Close()
	resourceURL, _ := url.Parse(resource.URL)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Path":"`+resource.URL+`/movie.mp4"}`)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	publicURL, _ := url.Parse("http://proxemby")
	allowedClients, err := parseClientPrefixes("203.0.113.10")
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(upstreamURL, publicURL)
	cfg.AllowedClients = allowedClients
	cfg.TrustProxyHeaders = true
	server := NewServerWithLogger(cfg, logger)
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	rewriteReq, _ := http.NewRequest(http.MethodGet, proxy.URL+"/emby/Items/1/PlaybackInfo", nil)
	rewriteReq.Host = publicURL.Host
	rewriteReq.Header.Set("X-Forwarded-For", "203.0.113.10")
	rewriteResp, err := http.DefaultClient.Do(rewriteReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = rewriteResp.Body.Close()

	allowedReq, _ := http.NewRequest(http.MethodGet, proxy.URL+"/_proxy/http/"+resourceURL.Host+"/movie.mp4", nil)
	allowedReq.Host = publicURL.Host
	allowedReq.Header.Set("X-Forwarded-For", "203.0.113.10")
	allowedResp, err := http.DefaultClient.Do(allowedReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = allowedResp.Body.Close()

	blockedClientReq, _ := http.NewRequest(http.MethodGet, proxy.URL+"/emby/System/Info", nil)
	blockedClientReq.Host = publicURL.Host
	blockedClientReq.Header.Set("X-Forwarded-For", "198.51.100.20")
	blockedClientResp, err := http.DefaultClient.Do(blockedClientReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = blockedClientResp.Body.Close()

	blockedResourceReq, _ := http.NewRequest(http.MethodGet, proxy.URL+"/_proxy/https/blocked.example.com/movie.mp4", nil)
	blockedResourceReq.Host = publicURL.Host
	blockedResourceReq.Header.Set("X-Forwarded-For", "203.0.113.10")
	blockedResourceResp, err := http.DefaultClient.Do(blockedResourceReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = blockedResourceResp.Body.Close()

	text := logs.String()
	for _, want := range []string{
		`msg="client filter decision"`,
		"allowed=true",
		"client=203.0.113.10",
		"source=x_forwarded_for",
		`level=WARN msg="client filter rejected request"`,
		"reason=client_ip_not_allowed",
		"client=198.51.100.20",
		`msg="resource proxy decision"`,
		"host=" + resourceURL.Host,
		`level=WARN msg="resource proxy rejected request"`,
		"reason=host_not_allowed",
		"host=blocked.example.com",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("debug log %q missing %q", text, want)
		}
	}
}

func TestServerLogsRejectedPlaybackInfoRouteMissAndHideClient(t *testing.T) {
	var logs bytes.Buffer
	logger := testLogger(t, &logs, slog.LevelDebug, "text", false)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Path":"https://vod.us.emby.com/movie.mp4","Padding":"too large"}`)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	publicURL, _ := url.Parse("http://proxemby")
	cfg := testConfig(upstreamURL, publicURL)
	cfg.PlaybackInfoMaxBytes = 16
	cfg.HideClient = true
	server := NewServerWithLogger(cfg, logger)
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	missReq, _ := http.NewRequest(http.MethodGet, proxy.URL+"/emby/System/Info", nil)
	missReq.Host = "unknown.example.com"
	missResp, err := http.DefaultClient.Do(missReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = missResp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, proxy.URL+"/emby/Items/1/PlaybackInfo", nil)
	req.Host = publicURL.Host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	text := logs.String()
	for _, want := range []string{
		`msg="route miss"`,
		"host=unknown.example.com",
		`msg="upstream request hides client identity headers"`,
		`level=WARN msg="playbackinfo response rejected"`,
		"reason=response_too_large",
		"max_bytes=16",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("debug log %q missing %q", text, want)
		}
	}
}

func TestInfoLevelDoesNotLogDebugRequests(t *testing.T) {
	var logs bytes.Buffer
	logger := testLogger(t, &logs, slog.LevelInfo, "text", false)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	publicURL, _ := url.Parse("http://proxemby")
	server := NewServerWithLogger(testConfig(upstreamURL, publicURL), logger)
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	req, _ := http.NewRequest(http.MethodGet, proxy.URL+"/emby/System/Info", nil)
	req.Host = publicURL.Host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if strings.Contains(logs.String(), "request completed") {
		t.Fatalf("info-level logs included debug request log: %q", logs.String())
	}
}
