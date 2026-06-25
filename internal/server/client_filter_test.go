package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

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
