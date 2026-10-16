// Package report is a minimal Sentry-protocol error reporter, compatible
// with Bugsink. Written directly against the protocol (no SDK) to keep
// full control over what leaves the process: no IP, no request bodies, no
// query strings, only the error and a few non-identifying tags.
//
// Enabled by SENTRY_DSN or the admin settings; a no-op when unset.
package report

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
)

const reportTimeout = 5 * time.Second

type dsn struct{ endpoint, publicKey string }

// parseDSN parses https://<publicKey>@<host>[/<pathPrefix>]/<projectId>.
func parseDSN(raw string) *dsn {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil || u.User.Username() == "" || u.Host == "" {
		return nil
	}
	var segments []string
	for _, s := range strings.Split(u.Path, "/") {
		if s != "" {
			segments = append(segments, s)
		}
	}
	if len(segments) == 0 {
		return nil
	}
	project := segments[len(segments)-1]
	// keep any reverse-proxy subpath in front of the project id
	prefix := ""
	if len(segments) > 1 {
		prefix = "/" + strings.Join(segments[:len(segments)-1], "/")
	}
	return &dsn{
		endpoint:  fmt.Sprintf("%s://%s%s/api/%s/store/", u.Scheme, u.Host, prefix, project),
		publicKey: u.User.Username(),
	}
}

var urlWithQuery = regexp.MustCompile(`(https?://[^\s?#]+)[?#]\S*`)

// scrub strips query strings and fragments from URLs in reported text.
func scrub(text string) string {
	text = urlWithQuery.ReplaceAllString(text, "$1")
	if len(text) > 2000 {
		text = text[:2000]
	}
	return text
}

// ValidDSN reports whether raw is a DSN the reporter can send to.
func ValidDSN(raw string) bool { return parseDSN(raw) != nil }

type Reporter struct {
	// target can change while running (the admin settings); nil is off
	target      atomic.Pointer[dsn]
	release     string
	environment string
	client      *http.Client
}

// New returns a reporter; with an empty or unparseable DSN it does nothing.
func New(rawDSN, release string, production bool) *Reporter {
	r := &Reporter{release: release, environment: "development", client: &http.Client{Timeout: reportTimeout}}
	if production {
		r.environment = "production"
	}
	if rawDSN != "" {
		r.target.Store(parseDSN(rawDSN))
		if !r.Enabled() {
			log.Print("SENTRY_DSN is set but could not be parsed; error reporting disabled")
		}
	}
	return r
}

// SetDSN points the reporter somewhere else; an empty DSN turns it off.
func (r *Reporter) SetDSN(rawDSN string) {
	r.target.Store(parseDSN(rawDSN))
}

func (r *Reporter) Enabled() bool { return r != nil && r.target.Load() != nil }

type frame struct {
	Function string `json:"function"`
	Filename string `json:"filename"`
	Lineno   int    `json:"lineno"`
}

// Capture reports an error, fire-and-forget: it never blocks or fails the
// caller.
func (r *Reporter) Capture(err error, tags map[string]string) {
	if r == nil || err == nil {
		return
	}
	target := r.target.Load()
	if target == nil {
		return
	}
	frames := stack(3)
	go func() {
		if sendErr := r.send(target, err, tags, frames); sendErr != nil {
			log.Printf("Error reporting failed: %v", sendErr)
		}
	}()
}

// stack returns the caller's frames, oldest first as Sentry expects.
func stack(skip int) []frame {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(skip, pcs)
	iter := runtime.CallersFrames(pcs[:n])
	var frames []frame
	for {
		f, more := iter.Next()
		frames = append(frames, frame{Function: f.Function, Filename: scrub(f.File), Lineno: f.Line})
		if !more {
			break
		}
	}
	for i, j := 0, len(frames)-1; i < j; i, j = i+1, j-1 {
		frames[i], frames[j] = frames[j], frames[i]
	}
	return frames
}

func (r *Reporter) send(target *dsn, err error, tags map[string]string, frames []frame) error {
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	event := map[string]any{
		"event_id":    hex.EncodeToString(id),
		"timestamp":   clock.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"platform":    "go",
		"level":       "error",
		"logger":      "kringkasting",
		"release":     r.release,
		"environment": r.environment,
		"exception": map[string]any{"values": []any{map[string]any{
			"type":       fmt.Sprintf("%T", err),
			"value":      scrub(err.Error()),
			"stacktrace": map[string]any{"frames": frames},
		}}},
		"tags": tags,
	}
	body, _ := json.Marshal(event)
	ctx, cancel := context.WithTimeout(context.Background(), reportTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, target.endpoint, bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-sentry-auth", "Sentry sentry_version=7, sentry_client=kringkasting/1.0, sentry_key="+target.publicKey)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
