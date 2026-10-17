package e2e

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func (i *instance) saveIntegrations(t *testing.T, fields map[string]string) string {
	t.Helper()
	form := url.Values{"action": {"integrations"}}
	for name, value := range fields {
		form.Set(name, value)
	}
	location := i.adminPost(t, form).Header.Get("Location")
	notice, _ := url.QueryUnescape(strings.TrimPrefix(location, "/admin/innstillinger?m="))
	return notice
}

// Analytics and error reporting set in admin reach the pages, the
// security header and the privacy page at once, and survive a restart.
func TestAdminIntegrations(t *testing.T) {
	app := start(t, options{env: adminEnv()})
	html := app.admin(t, "/innstillinger")
	for _, want := range []string{`data-status="umami"><span class="font-medium">Av</span>`, `data-status="sentry"><span class="font-medium">Av</span>`, "Ingen passnøkler ennå", "Logg ut overalt"} {
		if !strings.Contains(html, want) {
			t.Errorf("settings before saving: missing %q", want)
		}
	}

	for fields, want := range map[*map[string]string]string{
		{"umami-src": "http://stats.example.org/script.js", "umami-id": "abc"}:         "https-adresse",
		{"umami-src": "https://stats.example.org/script.js"}:                           "både skriptadresse og nettsteds-id",
		{"umami-src": "https://stats.example.org/script.js", "umami-id": `"><script>`}: "bokstaver, tall og bindestrek",
		{"sentry-dsn": "https://errors.example.org/1"}:                                 "DSN-en må se ut som",
	} {
		if notice := app.saveIntegrations(t, *fields); !strings.Contains(notice, want) {
			t.Errorf("%v: notice %q, want %q", *fields, notice, want)
		}
	}
	if html := body(app.do(request{path: "/"})); strings.Contains(html, "stats.example.org") {
		t.Fatal("a rejected setting must not be applied")
	}

	notice := app.saveIntegrations(t, map[string]string{"umami-src": "https://stats.example.org/script.js",
		"umami-id": "3f2a-1", "sentry-dsn": "https://key@errors.example.org/1"})
	if !strings.HasPrefix(notice, "Lagret") {
		t.Fatalf("save: %q", notice)
	}
	check := func(i *instance, when string) {
		t.Helper()
		home := i.do(request{path: "/"})
		if html := body(home); !strings.Contains(html, `<script defer src="https://stats.example.org/script.js" data-website-id="3f2a-1">`) {
			t.Errorf("%s: the Umami script is missing", when)
		}
		if csp := home.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self' https://stats.example.org;") {
			t.Errorf("%s: CSP doesn't allow the script: %s", when, csp)
		}
		if html := body(i.do(request{path: "/personvern", remote: "203.0.113.5"})); !strings.Contains(html, "Umami") || !strings.Contains(html, "feilovervåking") {
			t.Errorf("%s: the privacy page doesn't say what's on", when)
		}
	}
	check(app, "after saving")
	html = app.admin(t, "/innstillinger")
	for _, want := range []string{"teller besøk uten informasjonskapsler via stats.example.org", "sendes til errors.example.org"} {
		if !strings.Contains(html, want) {
			t.Errorf("settings after saving: missing status %q", want)
		}
	}

	// a restart keeps them; ten minutes on, changing them needs a new login
	dbPath := app.dbPath
	session := app.session
	app.stop(t)
	later := start(t, options{env: adminEnv(), dbPath: dbPath, offset: 11 * time.Minute})
	later.session = session
	check(later, "after a restart")
	if html := later.admin(t, "/innstillinger"); !strings.Contains(html, "Logg inn på nytt") || !strings.Contains(html, `<fieldset class="space-y-8" disabled>`) {
		t.Error("a stale session must be asked to log in again")
	}
	if notice := later.saveIntegrations(t, map[string]string{}); !strings.Contains(notice, "Logg inn på nytt") {
		t.Errorf("stale save: %q", notice)
	}
	check(later, "after a refused change")

	// logging in again allows turning them off
	reauth := later.adminPost(t, url.Values{"action": {"reauth"}})
	if reauth.StatusCode != http.StatusSeeOther || reauth.Header.Get("Location") != "/admin/innstillinger" {
		t.Errorf("reauth: %d %s", reauth.StatusCode, reauth.Header.Get("Location"))
	}
	later.session = ""
	if notice := later.saveIntegrations(t, map[string]string{}); !strings.HasPrefix(notice, "Lagret") {
		t.Fatalf("turn off: %q", notice)
	}
	home := later.do(request{path: "/"})
	if strings.Contains(body(home), "stats.example.org") || strings.Contains(home.Header.Get("Content-Security-Policy"), "stats.example.org") {
		t.Error("turned off, the script must be gone")
	}
}

// Values from the environment win and can't be changed in admin.
func TestAdminIntegrationsFromEnvironment(t *testing.T) {
	env := adminEnv()
	env["UMAMI_SRC"] = "https://env.example.org/script.js"
	env["UMAMI_WEBSITE_ID"] = "env-id"
	env["SENTRY_DSN"] = "https://secret@errors.example.org/7"
	app := start(t, options{env: env})
	html := app.admin(t, "/innstillinger")
	for _, want := range []string{"Satt i miljøet (UMAMI_SRC)", "Satt i miljøet (UMAMI_WEBSITE_ID)", "sendes til errors.example.org"} {
		if !strings.Contains(html, want) {
			t.Errorf("settings lack %q", want)
		}
	}
	if strings.Contains(html, "secret@") {
		t.Error("a DSN from the environment is shown only by its host")
	}
	app.saveIntegrations(t, map[string]string{"umami-src": "https://other.example.org/script.js", "umami-id": "other"})
	if html := body(app.do(request{path: "/"})); !strings.Contains(html, "env.example.org") || strings.Contains(html, "other.example.org") {
		t.Error("the environment's Umami must stay in effect")
	}
}
