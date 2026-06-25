package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

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
