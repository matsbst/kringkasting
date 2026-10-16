package e2e

import (
	"regexp"
	"strings"
	"testing"
)

// Pages link assets with a content version; only that version is cached
// forever, never a made-up one or the origin-dependent text files.
func TestAssetVersions(t *testing.T) {
	app := start(t, options{})
	page := body(app.do(request{path: "/"}))
	match := regexp.MustCompile(`/app\.js\?v=([0-9a-f]{12})"`).FindStringSubmatch(page)
	if match == nil {
		t.Fatal("app.js isn't linked with a content version")
	}
	version := match[1]
	immutable := func(path string) bool {
		return strings.Contains(app.do(request{path: path}).Header.Get("Cache-Control"), "immutable")
	}
	if !immutable("/app.js?v=" + version) {
		t.Error("the current version must be cached forever")
	}
	for _, path := range []string{"/app.js?v=made-up", "/app.js", "/robots.txt?v=" + version} {
		if immutable(path) {
			t.Errorf("%s must be revalidated", path)
		}
	}
}

// HTML pages carry a Content-Security-Policy that allows Umami's host only
// when it is configured.
func TestContentSecurityPolicy(t *testing.T) {
	bare := start(t, options{})
	csp := bare.do(request{path: "/"}).Header.Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self';", "object-src 'none'", "frame-ancestors 'none'", "base-uri 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("missing %q in %q", want, csp)
		}
	}
	withUmami := start(t, options{env: map[string]string{"UMAMI_SRC": "https://stats.example.org/script.js", "UMAMI_WEBSITE_ID": "x"}})
	csp = withUmami.do(request{path: "/personvern"}).Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self' https://stats.example.org;") || !strings.Contains(csp, "connect-src 'self' https://stats.example.org;") {
		t.Errorf("Umami's host not allowed: %q", csp)
	}
}
