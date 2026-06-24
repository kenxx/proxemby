package proxemby

import (
	"net/url"
	"strings"
	"testing"
)

func TestRewritePlaybackInfoRewritesNestedURLStrings(t *testing.T) {
	publicURL, _ := url.Parse("http://proxemby")
	registry := NewHostRegistry(nil)
	rewriter := NewRewriter(publicURL, registry)

	body := []byte(`{
		"MediaSources": [{
			"Path": "https://vod.us.emby.com/movie file.mp4?token=abc",
			"Nested": {"Url": "http://cdn.example.com/subtitle.srt"}
		}],
		"Name": "http-ish but not absolute",
		"ImageTags": {"Primary": "abc"}
	}`)

	out, events, err := rewriter.RewritePlaybackInfo(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events length = %d, want 2", len(events))
	}

	text := string(out)
	if !strings.Contains(text, `"http://proxemby/_proxy/https/vod.us.emby.com/movie%20file.mp4?token=abc"`) {
		t.Fatalf("rewritten movie URL missing in %s", text)
	}
	if !strings.Contains(text, `"http://proxemby/_proxy/http/cdn.example.com/subtitle.srt"`) {
		t.Fatalf("rewritten subtitle URL missing in %s", text)
	}
	if _, ok := registry.Lookup("vod.us.emby.com"); !ok {
		t.Fatal("vod.us.emby.com was not allowed")
	}
	scheme, ok := registry.Lookup("cdn.example.com")
	if !ok || scheme != "http" {
		t.Fatalf("cdn.example.com scheme = %q, ok = %v, want http/true", scheme, ok)
	}
}

func TestRewritePlaybackInfoIgnoresInvalidJSONAndNonURLs(t *testing.T) {
	publicURL, _ := url.Parse("http://proxemby")
	rewriter := NewRewriter(publicURL, NewHostRegistry(nil))

	out, events, err := rewriter.RewritePlaybackInfo([]byte(`not-json`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("events length = %d, want 0", len(events))
	}
	if string(out) != "not-json" {
		t.Fatalf("out = %q, want not-json", out)
	}

	out, events, err = rewriter.RewritePlaybackInfo([]byte(`{"Text":"ftp://example.com/file","Name":"plain"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("events length = %d, want 0", len(events))
	}
	if string(out) != `{"Text":"ftp://example.com/file","Name":"plain"}` {
		t.Fatalf("unexpected rewrite: %s", out)
	}
}
