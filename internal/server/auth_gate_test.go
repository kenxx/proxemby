package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type authTestEnv struct {
	proxy       *httptest.Server
	publicHost  string
	resourceURL *url.URL
	logouts     chan string
	upstreamHit chan string
}

func newAuthTestEnv(t *testing.T) *authTestEnv {
	t.Helper()
	env := &authTestEnv{
		logouts:     make(chan string, 4),
		upstreamHit: make(chan string, 64),
	}

	resource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "movie-bytes")
	}))
	t.Cleanup(resource.Close)
	env.resourceURL, _ = url.Parse(resource.URL)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env.upstreamHit <- r.URL.Path
		switch r.URL.Path {
		case "/emby/Users/AuthenticateByName":
			var body struct{ Username string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"User":{"Name":"`+body.Username+`","Id":"id-`+body.Username+`"},"AccessToken":"tok-`+body.Username+`","ServerId":"s1"}`)
		case "/emby/Sessions/Logout":
			env.logouts <- r.Header.Get("X-Emby-Token")
			w.WriteHeader(http.StatusNoContent)
		case "/emby/Items/1/PlaybackInfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"MediaSources":[{"Path":"`+resource.URL+`/dir/movie.mp4"}]}`)
		default:
			_, _ = io.WriteString(w, "upstream-ok")
		}
	}))
	t.Cleanup(upstream.Close)

	upstreamURL, _ := url.Parse(upstream.URL)
	publicURL, _ := url.Parse("http://proxemby")
	cfg := testConfig(upstreamURL, publicURL)
	cfg.AllowedUsers = []string{"Ken"}
	env.proxy = httptest.NewServer(NewServer(cfg).Handler())
	t.Cleanup(env.proxy.Close)
	env.publicHost = publicURL.Host
	return env
}

func (env *authTestEnv) do(t *testing.T, method, target string, body string, header map[string]string) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, env.proxy.URL+target, reader)
	req.Host = env.publicHost
	for key, value := range header {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func (env *authTestEnv) login(t *testing.T, user string) (int, string) {
	t.Helper()
	return env.do(t, http.MethodPost, "/emby/Users/AuthenticateByName", `{"Username":"`+user+`","Pw":"x"}`, map[string]string{
		"Content-Type": "application/json",
	})
}

func TestAuthGateAcceptsAllowedUserToken(t *testing.T) {
	env := newAuthTestEnv(t)

	status, body := env.login(t, "ken")
	if status != http.StatusOK || !strings.Contains(body, `"AccessToken":"tok-ken"`) {
		t.Fatalf("login = %d %s, want 200 with token", status, body)
	}

	status, body = env.do(t, http.MethodGet, "/emby/Users/id-ken/Items", "", map[string]string{"X-Emby-Token": "tok-ken"})
	if status != http.StatusOK || body != "upstream-ok" {
		t.Fatalf("authorized request = %d %s, want 200 upstream-ok", status, body)
	}
	status, _ = env.do(t, http.MethodGet, "/emby/Videos/1/stream?api_key=tok-ken", "", nil)
	if status != http.StatusOK {
		t.Fatalf("api_key request = %d, want 200", status)
	}
}

func TestAuthGateRejectsMissingAndUnknownTokens(t *testing.T) {
	env := newAuthTestEnv(t)

	for _, header := range []map[string]string{nil, {"X-Emby-Token": "tok-someone-else"}} {
		status, _ := env.do(t, http.MethodGet, "/emby/Users/id-ken/Items", "", header)
		if status != http.StatusUnauthorized {
			t.Fatalf("request with header %v = %d, want 401", header, status)
		}
	}
	select {
	case path := <-env.upstreamHit:
		t.Fatalf("rejected request reached upstream: %s", path)
	default:
	}
}

func TestAuthGateRejectsLoginForOtherUsers(t *testing.T) {
	env := newAuthTestEnv(t)

	status, body := env.login(t, "stranger")
	if status != http.StatusUnauthorized || strings.Contains(body, "tok-stranger") {
		t.Fatalf("login = %d %s, want 401 without token", status, body)
	}
	select {
	case token := <-env.logouts:
		if token != "tok-stranger" {
			t.Fatalf("upstream logout token = %q, want tok-stranger", token)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("rejected login was not logged out upstream")
	}

	status, _ = env.do(t, http.MethodGet, "/emby/Users/id-stranger/Items", "", map[string]string{"X-Emby-Token": "tok-stranger"})
	if status != http.StatusUnauthorized {
		t.Fatalf("stranger request = %d, want 401", status)
	}
}

func TestAuthGatePublicEndpoints(t *testing.T) {
	env := newAuthTestEnv(t)

	status, body := env.do(t, http.MethodGet, "/emby/Users/Public", "", nil)
	if status != http.StatusOK || body != "[]" {
		t.Fatalf("users public = %d %s, want 200 []", status, body)
	}
	for _, path := range []string{
		"/emby/System/Info/Public",
		"/System/Info/Public",
		"/emby/Items/1/Images/Primary?maxWidth=300",
		"/web/index.html",
	} {
		status, _ := env.do(t, http.MethodGet, path, "", nil)
		if status != http.StatusOK {
			t.Fatalf("%s = %d, want 200", path, status)
		}
	}
	status, _ = env.do(t, http.MethodPost, "/emby/Items/1/Images/Primary", "x", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("image upload without token = %d, want 401", status)
	}
}

func TestAuthGateLogoutRemovesSession(t *testing.T) {
	env := newAuthTestEnv(t)
	env.login(t, "ken")

	header := map[string]string{"X-Emby-Authorization": `MediaBrowser Client="test", DeviceId="d1", Token="tok-ken"`}
	status, _ := env.do(t, http.MethodPost, "/emby/Sessions/Logout", "", header)
	if status != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", status)
	}
	status, _ = env.do(t, http.MethodGet, "/emby/Users/id-ken/Items", "", header)
	if status != http.StatusUnauthorized {
		t.Fatalf("request after logout = %d, want 401", status)
	}
}

func TestAuthGateSignsResourceURLs(t *testing.T) {
	env := newAuthTestEnv(t)
	env.login(t, "ken")

	status, body := env.do(t, http.MethodGet, "/emby/Items/1/PlaybackInfo", "", map[string]string{"X-Emby-Token": "tok-ken"})
	if status != http.StatusOK {
		t.Fatalf("playbackinfo = %d, want 200", status)
	}
	var info struct {
		MediaSources []struct{ Path string }
	}
	if err := json.Unmarshal([]byte(body), &info); err != nil || len(info.MediaSources) != 1 {
		t.Fatalf("playbackinfo body = %s", body)
	}
	rewritten, _ := url.Parse(info.MediaSources[0].Path)
	unsigned := "/_proxy/http/" + env.resourceURL.Host + "/dir/movie.mp4"
	if strings.HasPrefix(rewritten.Path, "/_proxy/http/") || !strings.HasSuffix(rewritten.Path, unsigned[len("/_proxy"):]) {
		t.Fatalf("rewritten path = %q, want signed resource path", rewritten.Path)
	}

	// Signed URLs work without a token, including relative paths such as HLS segments.
	signedDir := strings.TrimSuffix(rewritten.Path, "movie.mp4")
	for _, path := range []string{rewritten.Path, signedDir + "segment1.ts"} {
		status, body := env.do(t, http.MethodGet, path, "", nil)
		if status != http.StatusOK || body != "movie-bytes" {
			t.Fatalf("signed resource %s = %d %s, want 200 movie-bytes", path, status, body)
		}
	}

	tampered := "/_proxy/AAAAAAAAAAAAAAAAAAAAAA/http/" + env.resourceURL.Host + "/dir/movie.mp4"
	if status, _ := env.do(t, http.MethodGet, tampered, "", nil); status != http.StatusForbidden {
		t.Fatalf("tampered signature = %d, want 403", status)
	}
	if status, _ := env.do(t, http.MethodGet, unsigned, "", nil); status != http.StatusForbidden {
		t.Fatalf("unsigned without token = %d, want 403", status)
	}
	if status, _ := env.do(t, http.MethodGet, unsigned, "", map[string]string{"X-Emby-Token": "tok-ken"}); status != http.StatusOK {
		t.Fatalf("unsigned with token = %d, want 200", status)
	}
}
