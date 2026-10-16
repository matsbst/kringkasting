package e2e

import (
	"strings"
	"testing"
)

// The privacy page describes this instance's own setup: analytics, CDN
// and error reports only where they are actually in use.
func TestPrivacyPageDescribesThisInstance(t *testing.T) {
	bare := start(t, options{})
	html := body(bare.do(request{path: "/personvern", remote: "203.0.113.5"}))
	for _, want := range []string{"ingen besøksstatistikk", "fra denne tjenesten"} {
		if !strings.Contains(html, want) {
			t.Errorf("bare instance: missing %q", want)
		}
	}
	for _, unwanted := range []string{"Umami", "Cloudflare", "feilrapport", "kringkast.ing, mens"} {
		if strings.Contains(html, unwanted) {
			t.Errorf("bare instance: mentions %q", unwanted)
		}
	}

	public := start(t, options{env: map[string]string{
		"UMAMI_SRC": "https://stats.example.org/script.js", "UMAMI_WEBSITE_ID": "x",
		"SENTRY_DSN": "https://key@errors.example.org/1",
	}})
	// Cloudflare's edge, through a local proxy
	html = body(public.do(request{path: "/personvern", remote: "172.18.0.1",
		headers: map[string]string{"X-Forwarded-For": "203.0.113.5, 172.70.1.1", "CF-Connecting-IP": "203.0.113.5"}}))
	for _, want := range []string{"en selvdriftet installasjon av", "Dataene forlater aldri", "via Cloudflare", "vår egen, selvdriftede feilovervåking"} {
		if !strings.Contains(html, want) {
			t.Errorf("public instance: missing %q", want)
		}
	}

	cloud := start(t, options{env: map[string]string{
		"UMAMI_SRC": "https://cloud.umami.is/script.js", "UMAMI_WEBSITE_ID": "x",
		"SENTRY_DSN": "https://key@o1.ingest.sentry.io/1",
	}})
	html = body(cloud.do(request{path: "/personvern", remote: "203.0.113.5"}))
	if strings.Contains(html, "selvdrift") || strings.Contains(html, "Dataene forlater aldri") || !strings.Contains(html, "feilovervåkingen Sentry") {
		t.Error("hosted Umami and Sentry must not be called self-hosted")
	}
}
