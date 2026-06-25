package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"proxemby/internal/config"
)

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
	server := NewServer(config.Config{Routes: []config.Route{
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
	server := NewServer(config.Config{Routes: []config.Route{
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
