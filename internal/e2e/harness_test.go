// Package e2e compares the application against the behaviour recorded
// from the Deno version before the Go rewrite (testdata/baseline),
// replaying the recorded NRK responses (testdata/nrk) with the clock
// pinned to the capture time, as the capture tool did.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/matsbst/kringkasting/internal/app"
	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/config"
	"github.com/matsbst/kringkasting/internal/feed"
)

const (
	fixtureDir  = "../../testdata/nrk"
	scenarioDir = "../../testdata/scenarios"
	goldenDir   = "../../testdata/baseline/http"
	origin      = "https://kringkasting.example"
	adminToken  = "baseline-admin-token"
)

// fixedStart is when the fixtures were recorded.
var fixedStart = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// fixtureKey is the fixture file naming used when they were recorded.
func fixtureKey(method, rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	search := ""
	if u.RawQuery != "" {
		search = "?" + u.RawQuery
	}
	readable := regexp.MustCompile(`[^a-zA-Z0-9._-]+`).ReplaceAllString(u.Host+u.EscapedPath()+search, "_")
	if units := utf16.Encode([]rune(readable)); len(units) > 120 {
		readable = string(utf16.Decode(units[:120]))
	}
	// FNV-1a over UTF-16 code units, as JavaScript's charCodeAt
	hash := uint32(0x811c9dc5)
	for _, unit := range utf16.Encode([]rune(method + " " + rawURL)) {
		hash ^= uint32(unit)
		hash *= 0x01000193
	}
	return fmt.Sprintf("%s__%s__%08x", strings.ToLower(method), readable, hash)
}

type fixture struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// replay answers NRK requests from the recorded fixtures (overlay first).
type replay struct {
	overlay string

	mu      sync.Mutex
	missing []string
}

func (r *replay) RoundTrip(req *http.Request) (*http.Response, error) {
	name := fixtureKey(req.Method, req.URL.String()) + ".json"
	var f fixture
	found := false
	for _, dir := range []string{r.overlay, fixtureDir} {
		if dir == "" {
			continue
		}
		if raw, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			if err := json.Unmarshal(raw, &f); err != nil {
				return nil, err
			}
			found = true
			break
		}
	}
	if !found || f.URL != req.URL.String() {
		r.mu.Lock()
		r.missing = append(r.missing, req.Method+" "+req.URL.String())
		r.mu.Unlock()
		return &http.Response{StatusCode: 599, Body: io.NopCloser(strings.NewReader("fixture missing")),
			Header: http.Header{}, Request: req}, nil
	}
	header := http.Header{}
	for k, v := range f.Headers {
		header.Set(k, v)
	}
	body := f.Body
	if req.Method == http.MethodHead {
		body = ""
	}
	return &http.Response{StatusCode: f.Status, Header: header, Body: io.NopCloser(strings.NewReader(body)),
		Request: req, ContentLength: int64(len(body))}, nil
}

type instance struct {
	*app.App
	cancel context.CancelFunc
	nrk    *replay
	dbPath string
}

type options struct {
	env     map[string]string
	overlay string
	dbPath  string
	offset  time.Duration
}

func start(t *testing.T, opts options) *instance {
	t.Helper()
	clock.Pin(fixedStart.Add(opts.offset))
	clock.Seed(42)
	dbPath := opts.dbPath
	if dbPath == "" {
		dbPath = filepath.Join(t.TempDir(), "kringkasting.sqlite3")
	}
	env := map[string]string{"KRINGKASTING_DB_PATH": dbPath, "APP_BUILD_ID": "test"}
	for k, v := range opts.env {
		env[k] = v
	}
	ctx, cancel := context.WithCancel(context.Background())
	nrk := &replay{overlay: opts.overlay}
	a, err := app.New(ctx, config.FromLookup(func(k string) string { return env[k] }),
		app.Options{Transport: nrk, PageDelay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	inst := &instance{App: a, cancel: cancel, nrk: nrk, dbPath: dbPath}
	t.Cleanup(func() { inst.stop(t) })
	return inst
}

func (i *instance) stop(t *testing.T) {
	if i.cancel == nil {
		return
	}
	i.cancel()
	i.cancel = nil
	ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if err := i.Close(ctx); err != nil {
		t.Errorf("close: %v", err)
	}
	if len(i.nrk.missing) > 0 {
		t.Errorf("requests without a recorded fixture:\n%s", strings.Join(i.nrk.missing, "\n"))
	}
}

type request struct {
	method  string
	path    string
	headers map[string]string
	body    string
	host    string
}

func (i *instance) do(r request) *http.Response {
	method := r.method
	if method == "" {
		method = http.MethodGet
	}
	req := httptest.NewRequest(method, r.path, strings.NewReader(r.body))
	req.RequestURI = r.path
	if r.host != "" {
		req.Host = r.host
	}
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	i.Handler.ServeHTTP(rec, req)
	return rec.Result()
}

func body(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// warmFeed requests a feed until its archive crawl is done and the XML is
// stable, as the capture tool did.
func (i *instance) warmFeed(t *testing.T, id string) {
	t.Helper()
	previous := ""
	for attempt := 0; attempt < 20; attempt++ {
		resp := i.do(request{path: "/api/feeds/" + id})
		current := body(resp)
		ctx, done := context.WithTimeout(context.Background(), 30*time.Second)
		if err := i.Crawler.WaitIdle(ctx); err != nil {
			t.Fatalf("crawler for %s: %v", id, err)
		}
		done()
		if resp.StatusCode == http.StatusOK && current == previous {
			return
		}
		previous = current
	}
	t.Fatalf("feed %s never stabilised", id)
}

// golden is a parsed testdata/baseline/http file.
type golden struct {
	status  int
	headers map[string]string
	body    string
}

func readGolden(t *testing.T, name string) golden {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(goldenDir, name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	i := strings.Index(text, "\nHTTP ")
	if i < 0 {
		t.Fatalf("%s: no status line", name)
	}
	rest := text[i+len("\nHTTP "):]
	statusLine, rest, _ := strings.Cut(rest, "\n")
	status, _ := strconv.Atoi(statusLine)
	headerBlock, bodyText, _ := strings.Cut(rest, "\n\n")
	g := golden{status: status, headers: map[string]string{}, body: bodyText}
	for _, line := range strings.Split(headerBlock, "\n") {
		if name, value, ok := strings.Cut(line, ": "); ok {
			g.headers[name] = value
		}
	}
	return g
}

// Intentional correction to the Deno baseline: feeds link a stylesheet for
// browsers right after the XML declaration, which changes their bodies and
// so their ETags. correctedETags maps a baseline ETag to the corrected one,
// for the conditional-GET goldens that follow each feed's 200.
var correctedETags = map[string]string{}

const xmlDeclaration = `<?xml version="1.0" encoding="UTF-8"?>`

func correctFeed(g *golden) {
	if etag, ok := correctedETags[g.headers["etag"]]; ok {
		g.headers["etag"] = etag
	}
	if g.status != http.StatusOK || !strings.HasPrefix(g.body, xmlDeclaration+"<rss") {
		return
	}
	g.body = xmlDeclaration + `<?xml-stylesheet type="text/css" href="` + feed.Stylesheet + `"?>` +
		strings.TrimPrefix(g.body, xmlDeclaration)
	corrected := feed.ETagFor(g.body)
	correctedETags[g.headers["etag"]] = corrected
	g.headers["etag"] = corrected
}

// headersExcept are never compared: transport details that legitimately
// differ between the Deno and Go servers.
var headersExcept = map[string]bool{"date": true, "content-length": true}

type check int

const (
	exact       check = iota // status, headers and body
	headersOnly              // status and headers
	statusOnly               // status, content-type and security headers
)

func compare(t *testing.T, name string, resp *http.Response, mode check) string {
	t.Helper()
	g := readGolden(t, name)
	// Intentional correction to the Deno baseline: successful chapter documents
	// use the media type advertised by the RSS tag. Error responses stay JSON.
	if strings.HasPrefix(name, "chapters_") && g.status == http.StatusOK {
		g.headers["content-type"] = "application/json+chapters"
	}
	correctFeed(&g)
	got := body(resp)
	if resp.StatusCode != g.status {
		t.Errorf("%s: status %d, want %d", name, resp.StatusCode, g.status)
	}
	for header, want := range g.headers {
		if headersExcept[header] {
			continue
		}
		if mode == statusOnly && header != "content-type" && !strings.HasPrefix(header, "x-") && header != "referrer-policy" {
			continue
		}
		value := resp.Header.Get(header)
		switch {
		case want == "<dynamic>":
			if value == "" {
				t.Errorf("%s: missing header %s", name, header)
			}
		case header == "content-type" && strings.HasPrefix(want, "text/html"):
			if !strings.HasPrefix(value, "text/html") {
				t.Errorf("%s: content-type %q, want %q", name, value, want)
			}
		case value != want:
			t.Errorf("%s: header %s = %q, want %q", name, header, value, want)
		}
	}
	if mode == exact && got != g.body {
		t.Errorf("%s: body differs\n got: %s\nwant: %s", name, truncate(got), truncate(g.body))
	}
	return got
}

func truncate(s string) string {
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}
