package web

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/store"
)

// writeJSON sends a JSON body as application/json (no charset, as before).
func writeJSON(w http.ResponseWriter, status int, body any) {
	writeJSONType(w, status, body, "application/json")
}

// writeJSONType supports structured JSON formats such as Podcasting 2.0 chapters.
func writeJSONType(w http.ResponseWriter, status int, body any, contentType string) {
	encoded, err := store.MarshalJSON(body)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = io.WriteString(w, encoded)
}

type message struct {
	Message string `json:"message"`
}

// tooManyRequests is the shared 429 answer for rate-limited requests.
func tooManyRequests(w http.ResponseWriter, retryAfterSeconds int) {
	w.Header().Set("Retry-After", fmt.Sprint(retryAfterSeconds))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusTooManyRequests, message{"Too many requests"})
}

// setCacheHeaders: `public` + `s-maxage` is what authorizes CDNs
// (Cloudflare) to cache the response at the edge.
func setCacheHeaders(w http.ResponseWriter, maxAge, sMaxAge int) {
	directives := []string{"public", fmt.Sprintf("max-age=%d", maxAge)}
	if sMaxAge >= 0 {
		directives = append(directives, fmt.Sprintf("s-maxage=%d", sMaxAge))
	}
	w.Header().Set("Cache-Control", strings.Join(directives, ", "))
	w.Header().Set("Expires", clock.Now().Add(time.Duration(maxAge)*time.Second).UTC().Format(http.TimeFormat))
}

// etagMatches reports whether an If-None-Match value matches the ETag
// (weak comparison, lists and "*").
func etagMatches(ifNoneMatch, etag string) bool {
	if ifNoneMatch == "" {
		return false
	}
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == etag || candidate == "*" {
			return true
		}
	}
	return false
}

// notModifiedSince reports whether an If-Modified-Since date says the
// client's copy is fresh (HTTP dates have second precision).
func notModifiedSince(ifModifiedSince string, lastModified time.Time) bool {
	if ifModifiedSince == "" {
		return false
	}
	since, err := http.ParseTime(ifModifiedSince)
	if err != nil {
		return false
	}
	return !lastModified.Truncate(time.Second).After(since)
}
