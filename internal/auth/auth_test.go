package auth

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRequestToken(t *testing.T) {
	tests := []struct {
		name   string
		target string
		header map[string]string
		want   string
	}{
		{name: "none", target: "/emby/Items", want: ""},
		{name: "x-emby-token", target: "/emby/Items", header: map[string]string{"X-Emby-Token": "abc"}, want: "abc"},
		{name: "mediabrowser token", target: "/emby/Items", header: map[string]string{"X-MediaBrowser-Token": "abc"}, want: "abc"},
		{
			name:   "authorization header",
			target: "/emby/Items",
			header: map[string]string{"X-Emby-Authorization": `MediaBrowser Client="Emby Web", Device="Chrome, Mac", DeviceId="d1", Version="4.8", Token="abc"`},
			want:   "abc",
		},
		{
			name:   "authorization without token",
			target: "/emby/Items",
			header: map[string]string{"Authorization": `MediaBrowser Client="Emby Web", DeviceId="d1"`},
			want:   "",
		},
		{name: "api_key query", target: "/emby/Videos/1/stream?api_key=abc", want: "abc"},
		{name: "token query", target: "/embywebsocket?X-Emby-Token=abc&deviceId=d1", want: "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.target, nil)
			for key, value := range tt.header {
				req.Header.Set(key, value)
			}
			if got := RequestToken(req); got != tt.want {
				t.Fatalf("RequestToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStorePersistsSessionsAndSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add("tok", Session{Route: "proxemby", UserName: "ken"}); err != nil {
		t.Fatal(err)
	}
	signature := store.Sign("proxemby", "https", "cdn.example.com")

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v, want 0600", info.Mode().Perm())
	}

	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session, ok := reopened.Lookup("tok")
	if !ok || session.UserName != "ken" {
		t.Fatalf("Lookup() = %+v/%v, want ken session", session, ok)
	}
	if !reopened.Verify(signature, "proxemby", "https", "cdn.example.com") {
		t.Fatal("signature from previous store is not valid after reopening")
	}
	if reopened.Verify(signature, "proxemby", "https", "other.example.com") {
		t.Fatal("signature is valid for a different host")
	}

	if err := reopened.Remove("tok"); err != nil {
		t.Fatal(err)
	}
	again, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Lookup("tok"); ok {
		t.Fatal("removed session is still present after reopening")
	}
}
