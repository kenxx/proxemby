package server

import (
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"strings"

	"proxemby/internal/config"
)

var errResponseTooLarge = errors.New("response body exceeds limit")

func readResponseBody(resp *http.Response, maxBytes int64) ([]byte, error) {
	defer resp.Body.Close()
	if maxBytes <= 0 {
		maxBytes = config.DefaultPlaybackInfoMaxBytes
	}

	var reader io.Reader = resp.Body
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer gzipReader.Close()
		reader = gzipReader
	}

	limited := io.LimitReader(reader, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, errResponseTooLarge
	}
	return body, nil
}
