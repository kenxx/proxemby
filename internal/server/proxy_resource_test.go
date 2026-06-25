package server

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
