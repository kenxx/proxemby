package proxemby

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestServerRewritesPlaybackInfoAndProxiesAllowedResource(t *testing.T) {
	resource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=0-10" {
			t.Fatalf("Range header = %q, want bytes=0-10", r.Header.Get("Range"))
		}
		if r.URL.RawQuery != "token=abc" {
			t.Fatalf("resource query = %q, want token=abc", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("movie-bytes"))
	}))
	defer resource.Close()
	resourceURL, _ := url.Parse(resource.URL)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emby/Items/1/PlaybackInfo" {
			t.Fatalf("upstream path = %q", r.URL.Path)
		}
		if r.Header.Get("Accept-Encoding") != "" {
			t.Fatalf("Accept-Encoding = %q, want empty", r.Header.Get("Accept-Encoding"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"MediaSources":[{"Path":"`+resource.URL+`/movie.mp4?token=abc"}]}`)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	publicURL, _ := url.Parse("http://proxemby")
	server := NewServer(testConfig(upstreamURL, publicURL))
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	req, _ := http.NewRequest(http.MethodGet, proxy.URL+"/emby/Items/1/PlaybackInfo", nil)
	req.Host = publicURL.Host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	expected := "http://proxemby/_proxy/http/" + resourceURL.Host + "/movie.mp4?token=abc"
	if !strings.Contains(string(body), expected) {
		t.Fatalf("body = %s, want %s", body, expected)
	}

	resourceReq, _ := http.NewRequest(http.MethodGet, proxy.URL+"/_proxy/http/"+resourceURL.Host+"/movie.mp4?token=abc", nil)
	resourceReq.Host = publicURL.Host
	resourceReq.Header.Set("Range", "bytes=0-10")
	resourceResp, err := http.DefaultClient.Do(resourceReq)
	if err != nil {
		t.Fatal(err)
	}
	defer resourceResp.Body.Close()
	resourceBody, _ := io.ReadAll(resourceResp.Body)
	if resourceResp.StatusCode != http.StatusPartialContent {
		t.Fatalf("resource status = %d, want 206", resourceResp.StatusCode)
	}
	if string(resourceBody) != "movie-bytes" {
		t.Fatalf("resource body = %q, want movie-bytes", resourceBody)
	}
}

func TestServerRejectsOversizedPlaybackInfo(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Path":"https://vod.us.emby.com/movie.mp4","Padding":"too large"}`)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	publicURL, _ := url.Parse("http://proxemby")
	cfg := testConfig(upstreamURL, publicURL)
	cfg.PlaybackInfoMaxBytes = 16
	server := NewServer(cfg)
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	req, _ := http.NewRequest(http.MethodGet, proxy.URL+"/emby/Items/1/PlaybackInfo", nil)
	req.Host = publicURL.Host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}

func TestServerRejectsUnknownResourceHost(t *testing.T) {
	upstreamURL, _ := url.Parse("https://us.emby.com")
	publicURL, _ := url.Parse("http://proxemby")
	server := NewServer(testConfig(upstreamURL, publicURL))

	req := httptest.NewRequest(http.MethodGet, "/_proxy/https/vod.us.emby.com/movie.mp4", nil)
	req.Host = publicURL.Host
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestServerAllowsConfiguredClientIP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	publicURL, _ := url.Parse("http://proxemby")
	allowedClients, err := parseClientPrefixes("127.0.0.1,10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(upstreamURL, publicURL)
	cfg.AllowedClients = allowedClients
	server := NewServer(cfg)
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	req, _ := http.NewRequest(http.MethodGet, proxy.URL+"/emby/System/Info", nil)
	req.Host = publicURL.Host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestServerRejectsDisallowedClientIP(t *testing.T) {
	upstreamURL, _ := url.Parse("https://us.emby.com")
	publicURL, _ := url.Parse("http://proxemby")
	allowedClients, err := parseClientPrefixes("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(upstreamURL, publicURL)
	cfg.AllowedClients = allowedClients
	server := NewServer(cfg)

	req := httptest.NewRequest(http.MethodGet, "/emby/System/Info", nil)
	req.Host = publicURL.Host
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestServerUsesForwardedForWhenTrusted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
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
	server := NewServer(cfg)

	req := httptest.NewRequest(http.MethodGet, "/emby/System/Info", nil)
	req.Host = publicURL.Host
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.10, 127.0.0.1")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestServerForwardsNormalRequestsToUpstream(t *testing.T) {
	var upstreamHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emby/System/Info" {
			t.Fatalf("path = %q, want /emby/System/Info", r.URL.Path)
		}
		if r.URL.RawQuery != "api_key=secret" {
			t.Fatalf("query = %q, want api_key=secret", r.URL.RawQuery)
		}
		if r.Host != upstreamHost {
			t.Fatalf("Host = %q, want %q", r.Host, upstreamHost)
		}
		if r.Header.Get("X-Forwarded-Host") == "" {
			t.Fatal("X-Forwarded-Host was empty")
		}
		if r.Header.Get("X-Forwarded-For") == "spoofed" {
			t.Fatal("spoofed X-Forwarded-For was preserved")
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	upstreamHost = upstreamURL.Host
	publicURL, _ := url.Parse("http://proxemby")
	server := NewServer(testConfig(upstreamURL, publicURL))
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	req, _ := http.NewRequest(http.MethodGet, proxy.URL+"/emby/System/Info?api_key=secret", nil)
	req.Host = publicURL.Host
	req.Header.Set("Connection", "X-Forwarded-Host")
	req.Header.Set("X-Forwarded-For", "spoofed")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body = %q, want ok", body)
	}
}

func TestServerHidesClientForwardingHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, header := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "Forwarded", "Via"} {
			if got := r.Header.Get(header); got != "" {
				t.Fatalf("%s = %q, want empty", header, got)
			}
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	publicURL, _ := url.Parse("http://proxemby")
	cfg := testConfig(upstreamURL, publicURL)
	cfg.HideClient = true
	server := NewServer(cfg)
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	req, _ := http.NewRequest(http.MethodGet, proxy.URL+"/emby/System/Info", nil)
	req.Host = publicURL.Host
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	req.Header.Set("X-Forwarded-Host", "client.example.com")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Forwarded", "for=203.0.113.10")
	req.Header.Set("Via", "1.1 old-proxy")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestServerDebugLogsSanitizedRequest(t *testing.T) {
	var logs bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousOutput)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	publicURL, _ := url.Parse("http://proxemby")
	cfg := testConfig(upstreamURL, publicURL)
	cfg.Debug = true
	server := NewServer(cfg)
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
		"debug request method=GET",
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
	previousOutput := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousOutput)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Path":"https://vod.us.emby.com/movie.mp4?token=secret"}`)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	publicURL, _ := url.Parse("http://proxemby")
	cfg := testConfig(upstreamURL, publicURL)
	cfg.Debug = true
	server := NewServer(cfg)
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
		"debug playbackinfo rewrite path=/emby/Items/1/PlaybackInfo?api_key=redacted count=1",
		"debug playbackinfo rewrite item json_path=Path scheme=https host=vod.us.emby.com",
		"from=https://vod.us.emby.com/movie.mp4?token=redacted",
		"to=http://proxemby/_proxy/https/vod.us.emby.com/movie.mp4?token=redacted",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("debug log %q missing %q", text, want)
		}
	}
	if strings.Contains(text, "token=secret") || strings.Contains(text, "api_key=secret") {
		t.Fatalf("debug log leaked sensitive query values: %q", text)
	}
}

func TestServerForwardsWebSocketUpgrade(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Connection"), "upgrade") {
			t.Fatalf("Connection = %q, want upgrade", r.Header.Get("Connection"))
		}
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			t.Fatalf("Upgrade = %q, want websocket", r.Header.Get("Upgrade"))
		}
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Upgrade", "websocket")
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	publicURL, _ := url.Parse("http://proxemby")
	server := NewServer(testConfig(upstreamURL, publicURL))
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	req, _ := http.NewRequest(http.MethodGet, proxy.URL+"/embywebsocket", nil)
	req.Host = publicURL.Host
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
}

func TestServerRoutesByHost(t *testing.T) {
	upstreamOne := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "one")
	}))
	defer upstreamOne.Close()
	upstreamTwo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "two")
	}))
	defer upstreamTwo.Close()

	upstreamOneURL, _ := url.Parse(upstreamOne.URL)
	upstreamTwoURL, _ := url.Parse(upstreamTwo.URL)
	publicOneURL, _ := url.Parse("http://one.example.com")
	publicTwoURL, _ := url.Parse("http://two.example.com")
	server := NewServer(Config{Routes: []Route{
		{UpstreamURL: upstreamOneURL, PublicURL: publicOneURL},
		{UpstreamURL: upstreamTwoURL, PublicURL: publicTwoURL},
	}})
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	for _, tc := range []struct {
		host string
		body string
	}{
		{host: "one.example.com", body: "one"},
		{host: "two.example.com:443", body: "two"},
		{host: "TWO.EXAMPLE.COM", body: "two"},
	} {
		req, _ := http.NewRequest(http.MethodGet, proxy.URL+"/emby/System/Info", nil)
		req.Host = tc.host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if string(body) != tc.body {
			t.Fatalf("host %q body = %q, want %q", tc.host, body, tc.body)
		}
	}
}

func TestServerReturnsNotFoundForUnknownHost(t *testing.T) {
	upstreamURL, _ := url.Parse("https://us.emby.com")
	publicURL, _ := url.Parse("http://proxemby")
	server := NewServer(testConfig(upstreamURL, publicURL))

	req := httptest.NewRequest(http.MethodGet, "/emby/System/Info", nil)
	req.Host = "unknown.example.com"
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestServerKeepsResourceRegistryPerRoute(t *testing.T) {
	resource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "movie")
	}))
	defer resource.Close()
	resourceURL, _ := url.Parse(resource.URL)

	upstreamOne := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Path":"`+resource.URL+`/movie.mp4"}`)
	}))
	defer upstreamOne.Close()
	upstreamTwo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstreamTwo.Close()

	upstreamOneURL, _ := url.Parse(upstreamOne.URL)
	upstreamTwoURL, _ := url.Parse(upstreamTwo.URL)
	publicOneURL, _ := url.Parse("http://one.example.com")
	publicTwoURL, _ := url.Parse("http://two.example.com")
	server := NewServer(Config{Routes: []Route{
		{UpstreamURL: upstreamOneURL, PublicURL: publicOneURL},
		{UpstreamURL: upstreamTwoURL, PublicURL: publicTwoURL},
	}})
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	rewriteReq, _ := http.NewRequest(http.MethodGet, proxy.URL+"/emby/Items/1/PlaybackInfo", nil)
	rewriteReq.Host = publicOneURL.Host
	rewriteResp, err := http.DefaultClient.Do(rewriteReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = rewriteResp.Body.Close()

	resourcePath := "/_proxy/http/" + resourceURL.Host + "/movie.mp4"
	allowedReq, _ := http.NewRequest(http.MethodGet, proxy.URL+resourcePath, nil)
	allowedReq.Host = publicOneURL.Host
	allowedResp, err := http.DefaultClient.Do(allowedReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = allowedResp.Body.Close()
	if allowedResp.StatusCode != http.StatusOK {
		t.Fatalf("allowed route status = %d, want 200", allowedResp.StatusCode)
	}

	blockedReq, _ := http.NewRequest(http.MethodGet, proxy.URL+resourcePath, nil)
	blockedReq.Host = publicTwoURL.Host
	blockedResp, err := http.DefaultClient.Do(blockedReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = blockedResp.Body.Close()
	if blockedResp.StatusCode != http.StatusForbidden {
		t.Fatalf("other route status = %d, want 403", blockedResp.StatusCode)
	}
}

func testConfig(upstreamURL, publicURL *url.URL) Config {
	return Config{
		Routes: []Route{{
			UpstreamURL: upstreamURL,
			PublicURL:   publicURL,
		}},
	}
}
