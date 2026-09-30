package auth

import (
	"net/http"
	"regexp"
	"strings"
)

var tokenHeaders = []string{"X-Emby-Token", "X-MediaBrowser-Token"}

var authorizationHeaders = []string{"X-Emby-Authorization", "Authorization"}

var tokenQueryKeys = map[string]struct{}{
	"api_key":              {},
	"apikey":               {},
	"x-emby-token":         {},
	"x-mediabrowser-token": {},
}

var authorizationTokenPattern = regexp.MustCompile(`(?i)(?:^|[\s,])token\s*=\s*"?([^",\s]+)`)

// RequestToken returns the Emby access token sent by a client, if any.
func RequestToken(req *http.Request) string {
	for _, name := range tokenHeaders {
		if token := strings.TrimSpace(req.Header.Get(name)); token != "" {
			return token
		}
	}
	for _, name := range authorizationHeaders {
		if match := authorizationTokenPattern.FindStringSubmatch(req.Header.Get(name)); match != nil {
			return match[1]
		}
	}
	for key, values := range req.URL.Query() {
		if _, ok := tokenQueryKeys[strings.ToLower(key)]; !ok {
			continue
		}
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}
