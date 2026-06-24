package proxemby

import (
	"io"
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
	server := NewServer(Config{
		UpstreamURL: upstreamURL,
		PublicURL:   publicURL,
	})
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/emby/Items/1/PlaybackInfo")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	expected := "http://proxemby/_proxy/http/" + resourceURL.Host + "/movie.mp4?token=abc"
	if !strings.Contains(string(body), expected) {
		t.Fatalf("body = %s, want %s", body, expected)
	}

	req, _ := http.NewRequest(http.MethodGet, proxy.URL+"/_proxy/http/"+resourceURL.Host+"/movie.mp4?token=abc", nil)
	req.Header.Set("Range", "bytes=0-10")
	resourceResp, err := http.DefaultClient.Do(req)
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
	server := NewServer(Config{
		UpstreamURL:          upstreamURL,
		PublicURL:            publicURL,
		PlaybackInfoMaxBytes: 16,
	})
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/emby/Items/1/PlaybackInfo")
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
	server := NewServer(Config{
		UpstreamURL: upstreamURL,
		PublicURL:   publicURL,
	})

	req := httptest.NewRequest(http.MethodGet, "/_proxy/https/vod.us.emby.com/movie.mp4", nil)
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
	server := NewServer(Config{
		UpstreamURL:    upstreamURL,
		PublicURL:      publicURL,
		AllowedClients: allowedClients,
	})
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/emby/System/Info")
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
	server := NewServer(Config{
		UpstreamURL:    upstreamURL,
		PublicURL:      publicURL,
		AllowedClients: allowedClients,
	})

	req := httptest.NewRequest(http.MethodGet, "/emby/System/Info", nil)
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
	server := NewServer(Config{
		UpstreamURL:       upstreamURL,
		PublicURL:         publicURL,
		AllowedClients:    allowedClients,
		TrustProxyHeaders: true,
	})

	req := httptest.NewRequest(http.MethodGet, "/emby/System/Info", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.10, 127.0.0.1")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestServerForwardsNormalRequestsToUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emby/System/Info" {
			t.Fatalf("path = %q, want /emby/System/Info", r.URL.Path)
		}
		if r.URL.RawQuery != "api_key=secret" {
			t.Fatalf("query = %q, want api_key=secret", r.URL.RawQuery)
		}
		if r.Host == "" {
			t.Fatal("upstream host was empty")
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	publicURL, _ := url.Parse("http://proxemby")
	server := NewServer(Config{
		UpstreamURL: upstreamURL,
		PublicURL:   publicURL,
	})
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/emby/System/Info?api_key=secret")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body = %q, want ok", body)
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
	server := NewServer(Config{
		UpstreamURL: upstreamURL,
		PublicURL:   publicURL,
	})
	proxy := httptest.NewServer(server.Handler())
	defer proxy.Close()

	req, _ := http.NewRequest(http.MethodGet, proxy.URL+"/embywebsocket", nil)
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
