package e2e

import (
	"regexp"
	"strings"
	"testing"
)

// The pages from the 2026-10 design review: what they promise to show.
func TestPageDetails(t *testing.T) {
	app := start(t, options{env: adminEnv()})

	home := body(app.do(request{path: "/"}))
	search := body(app.do(request{path: "/?query=musikk"}))
	if strings.Contains(home, "data-hero data-compact") || !strings.Contains(search, "data-hero data-compact") {
		t.Error("the hero compacts only once a search is made")
	}
	// several results: every subscribe button steps back
	if got := strings.Count(search, "data-quiet"); got != 10 {
		t.Errorf("%d quiet subscribe buttons of 10 results", got)
	}
	// the QR view names the podcast like the app list does
	if !regexp.MustCompile(`<h2 id="[^"]+-qr-title"[^>]*><span[^>]*>[^<]+</span> <span[^>]*data-panel-subtitle>Åpne på mobilen</span></h2>`).MatchString(search) {
		t.Error("the QR view lacks the podcast header")
	}

	missing := body(app.do(request{path: "/finnes-ikke"}))
	if !strings.Contains(missing, "data-search-form") || !strings.Contains(missing, `placeholder="Søk etter podkast"`) {
		t.Error("the 404 page uses the home page's search field")
	}

	if login := body(app.do(request{path: "/admin"})); !strings.Contains(login, `<label for="token" class="block`) {
		t.Error("the admin login field has a visible label")
	}
	app.warmFeed(t, "doemt")
	overview := app.admin(t, "")
	for _, want := range []string{"Grafen fylles etter hvert som dagene går.", "Ingen ennå"} {
		if !strings.Contains(overview, want) {
			t.Errorf("a new instance's overview: missing %q", want)
		}
	}
	feeds := app.admin(t, "/strommer")
	for _, want := range []string{">Hent alt på nytt</button>", ">Bruk</button>"} {
		if !strings.Contains(feeds, want) {
			t.Errorf("Strømmer: missing %q", want)
		}
	}
	if strings.Contains(feeds, "Re-crawl") || strings.Contains(app.admin(t, "/drift"), "200 / 304") {
		t.Error("admin labels are Norwegian, without HTTP status codes")
	}
}
