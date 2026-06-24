package proxemby

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type Rewriter struct {
	publicURL *url.URL
	registry  *HostRegistry
}

type RewriteEvent struct {
	Path      string
	Original  string
	Rewritten string
	Scheme    string
	Host      string
}

func NewRewriter(publicURL *url.URL, registry *HostRegistry) *Rewriter {
	return &Rewriter{
		publicURL: publicURL,
		registry:  registry,
	}
}

func (r *Rewriter) RewritePlaybackInfo(body []byte) ([]byte, []RewriteEvent, error) {
	if !gjson.ValidBytes(body) {
		return body, nil, nil
	}

	root := gjson.ParseBytes(body)
	out := append([]byte(nil), body...)
	paths := collectURLStrings(root, "")
	events := make([]RewriteEvent, 0, len(paths))
	for _, item := range paths {
		rewritten, event, ok := r.rewriteURL(item.value)
		if !ok {
			continue
		}
		event.Path = item.path
		var err error
		out, err = sjson.SetBytes(out, item.path, rewritten)
		if err != nil {
			return nil, nil, err
		}
		events = append(events, event)
	}

	return out, events, nil
}

type jsonStringPath struct {
	path  string
	value string
}

func collectURLStrings(value gjson.Result, path string) []jsonStringPath {
	if value.Type == gjson.String {
		if isAbsoluteHTTPURL(value.String()) {
			return []jsonStringPath{{path: path, value: value.String()}}
		}
		return nil
	}

	var paths []jsonStringPath
	if value.IsArray() {
		index := 0
		value.ForEach(func(_, child gjson.Result) bool {
			childPath := joinJSONPath(path, strconv.Itoa(index))
			paths = append(paths, collectURLStrings(child, childPath)...)
			index++
			return true
		})
		return paths
	}

	if value.IsObject() {
		value.ForEach(func(key, child gjson.Result) bool {
			childPath := joinJSONPath(path, escapeJSONPathKey(key.String()))
			paths = append(paths, collectURLStrings(child, childPath)...)
			return true
		})
	}
	return paths
}

func (r *Rewriter) rewriteURL(raw string) (string, RewriteEvent, bool) {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", RewriteEvent{}, false
	}

	r.registry.Allow(u.Host, u.Scheme)

	out := *r.publicURL
	out.Path = joinURLPath(out.Path, "_proxy", u.Scheme, u.Host, strings.TrimPrefix(u.Path, "/"))
	out.RawQuery = u.RawQuery
	out.Fragment = ""
	rewritten := out.String()
	return rewritten, RewriteEvent{
		Original:  raw,
		Rewritten: rewritten,
		Scheme:    u.Scheme,
		Host:      u.Host,
	}, true
}

func isAbsoluteHTTPURL(raw string) bool {
	return strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://")
}

func joinJSONPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func escapeJSONPathKey(key string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`.`, `\.`,
		`*`, `\*`,
		`?`, `\?`,
	)
	return replacer.Replace(key)
}

func joinURLPath(base string, parts ...string) string {
	segments := make([]string, 0, len(parts)+1)
	if strings.Trim(base, "/") != "" {
		segments = append(segments, strings.Trim(base, "/"))
	}
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part != "" {
			segments = append(segments, part)
		}
	}
	return "/" + strings.Join(segments, "/")
}
