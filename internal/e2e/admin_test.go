package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

var adminCookie = map[string]string{"Cookie": "kringkasting_admin=" + adminToken}

func (i *instance) admin(t *testing.T, query string) string {
	t.Helper()
	resp := i.do(request{path: "/admin" + query, headers: adminCookie})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/admin%s: status %d", query, resp.StatusCode)
	}
	return body(resp)
}

func (i *instance) adminPost(t *testing.T, form url.Values) *http.Response {
	t.Helper()
	return i.do(request{method: http.MethodPost, path: "/admin", body: form.Encode(), headers: map[string]string{
		"Content-Type": "application/x-www-form-urlencoded", "Cookie": adminCookie["Cookie"]}})
}

// feedTitles are the feed list's titles, in order.
func feedTitles(html string) []string {
	var titles []string
	for _, m := range regexp.MustCompile(`<a href="/api/feeds/[^"]+" class="font-medium[^"]*" title="[^"]+">([^<]+)</a>`).FindAllStringSubmatch(html, -1) {
		titles = append(titles, m[1])
	}
	return titles
}

// Activity per feed, subscribers and daily history reach the dashboard,
// and survive a restart.
func TestAdminActivity(t *testing.T) {
	app := start(t, options{env: adminEnv()})
	for _, id := range []string{"supertroett", "doemt", "musikkhistorier/sesong/miles-davis--bak-mytene"} {
		app.warmFeed(t, id)
	}
	poll := func(id, userAgent string) {
		if resp := app.do(request{path: "/api/feeds/" + id, headers: map[string]string{"User-Agent": userAgent}}); resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", id, resp.StatusCode)
		}
	}
	poll("supertroett", "Overcast/1.0 Podcast Sync (12 subscribers; feed-id=1)")
	poll("supertroett", "Feedly/1.0 (+http://www.feedly.com/fetcher.html; 4 subscribers)")
	poll("supertroett", "Overcast/1.0 Podcast Sync (15 subscribers; feed-id=1)")
	poll("doemt", "AntennaPod/3.5.0")
	app.Flush()

	html := app.admin(t, "")
	for _, want := range []string{"Overcast", "Feedly", "≈ 19", "AntennaPod", "Sesong i «musikkhistorier»", "Siste 30 dager"} {
		if !strings.Contains(html, want) {
			t.Errorf("dashboard lacks %q", want)
		}
	}
	if strings.Contains(html, "Mozilla") || strings.Contains(html, "feed-id") {
		t.Error("raw user agents must never reach the page")
	}
	// the most recently used feed first (warming counted as use too)
	if titles := feedTitles(html); len(titles) != 3 || titles[0] != "Dømt" {
		t.Errorf("default order: %v", titles)
	}

	// restart on the same database: history and activity are still there
	dbPath := app.dbPath
	app.stop(t)
	again := start(t, options{env: adminEnv(), dbPath: dbPath})
	html = again.admin(t, "")
	if !strings.Contains(html, "≈ 19") || !regexp.MustCompile(`Strømmer levert</span><span[^>]*>\d+ i dag`).MatchString(html) {
		t.Error("activity or history lost over a restart")
	}
	if strings.Contains(html, " 0 siste 30 dager") || !regexp.MustCompile(`[1-9]\d* siste 30 dager`).MatchString(html) {
		t.Error("the 30-day totals should count the feeds served before the restart")
	}
}

func TestAdminListFilters(t *testing.T) {
	app := start(t, options{env: adminEnv()})
	for _, id := range []string{"supertroett", "doemt", "musikkhistorier", "musikkhistorier/sesong/miles-davis--bak-mytene"} {
		app.warmFeed(t, id)
	}
	for query, want := range map[string][]string{
		"?sorter=tittel":                 {"Dømt", "Miles Davis – bak mytene", "Musikkhistorier", "Supertrøtt"},
		"?vis=sesonger":                  {"Miles Davis – bak mytene"},
		"?q=TRØTT":                       {"Supertrøtt"},
		"?q=doemt":                       {"Dømt"},
		"?vis=henter":                    nil,
		"?vis=finnes-ikke&sorter=tittel": {"Dømt", "Miles Davis – bak mytene", "Musikkhistorier", "Supertrøtt"},
	} {
		html := app.admin(t, query)
		if got := feedTitles(html); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s: %v, want %v", query, got, want)
		}
		if want == nil && !strings.Contains(html, "Ingen strømmer passer") {
			t.Errorf("%s: empty result should say so", query)
		}
	}
	if html := app.admin(t, "?vis=sesonger"); !strings.Contains(html, "Strømmer (1 av 4)") {
		t.Error("a filtered list should say how many of all it shows")
	}
}

func TestAdminDeleteNeedsConfirmation(t *testing.T) {
	app := start(t, options{env: adminEnv()})
	app.warmFeed(t, "doemt")

	// a delete without confirmation only leads to the confirmation
	resp := app.adminPost(t, url.Values{"action": {"delete"}, "seriesId": {"doemt"}, "back": {"vis=brukt"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin?slett=doemt&vis=brukt" {
		t.Fatalf("unconfirmed delete: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if !app.Store.HasSeries("doemt") {
		t.Fatal("deleted without confirmation")
	}
	html := app.admin(t, "?slett=doemt")
	if !strings.Contains(html, "Slette «Dømt»?") || !strings.Contains(html, `name="confirm" value="doemt"`) {
		t.Fatal("confirmation missing")
	}
	// a confirmation for another feed doesn't count
	app.adminPost(t, url.Values{"action": {"delete"}, "seriesId": {"doemt"}, "confirm": {"supertroett"}})
	if !app.Store.HasSeries("doemt") {
		t.Fatal("deleted with another feed's confirmation")
	}
	resp = app.adminPost(t, url.Values{"action": {"delete"}, "seriesId": {"doemt"}, "confirm": {"doemt"}})
	if resp.StatusCode != http.StatusSeeOther || app.Store.HasSeries("doemt") {
		t.Fatalf("confirmed delete: %d, still there: %v", resp.StatusCode, app.Store.HasSeries("doemt"))
	}
}

func TestAdminCleanup(t *testing.T) {
	app := start(t, options{env: adminEnv()})
	app.warmFeed(t, "doemt")
	app.warmFeed(t, "supertroett")
	app.Flush()
	if html := app.admin(t, ""); !strings.Contains(html, "Tilgjengelig fra") || strings.Contains(html, `value="cleanup"`) {
		t.Fatal("cleanup must wait until use has been measured for 180 days")
	}
	if resp := app.adminPost(t, url.Values{"action": {"cleanup"}, "count": {"0"}, "hash": {"x"}}); !strings.Contains(resp.Header.Get("Location"), "ikke%20tilgjengelig") {
		t.Errorf("early cleanup: %s", resp.Header.Get("Location"))
	}

	// 200 days later, supertroett was requested again recently, doemt not
	dbPath := app.dbPath
	app.stop(t)
	later := start(t, options{env: adminEnv(), dbPath: dbPath, offset: 200 * 24 * time.Hour})
	later.do(request{path: "/api/feeds/supertroett"})
	later.Flush()
	html := later.admin(t, "")
	m := regexp.MustCompile(`name="count" value="(\d+)"/><input type="hidden" name="hash" value="([0-9a-f]+)"`).FindStringSubmatch(html)
	if m == nil || m[1] != "1" || !strings.Contains(html, "<li>Dømt</li>") {
		t.Fatalf("cleanup preview: %v", m)
	}
	// a stale preview is refused
	if resp := later.adminPost(t, url.Values{"action": {"cleanup"}, "count": {"1"}, "hash": {"0000"}}); !strings.Contains(resp.Header.Get("Location"), "endret") || !later.Store.HasSeries("doemt") {
		t.Error("a cleanup with a stale hash must delete nothing")
	}
	later.adminPost(t, url.Values{"action": {"cleanup"}, "count": {m[1]}, "hash": {m[2]}})
	if later.Store.HasSeries("doemt") || !later.Store.HasSeries("supertroett") {
		t.Error("cleanup must delete exactly the previewed feeds")
	}
}

// Failed logins are limited per client address, which headers can't
// forge: one address guessing can't lock the admin out elsewhere, and
// a correct token is never refused for earlier successes.
func TestAdminLoginLimit(t *testing.T) {
	app := start(t, options{env: adminEnv()})
	login := func(remote, token string, headers map[string]string) string {
		h := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
		for k, v := range headers {
			h[k] = v
		}
		resp := app.do(request{method: http.MethodPost, path: "/admin", remote: remote, headers: h,
			body: url.Values{"token": {token}}.Encode()})
		if resp.StatusCode == http.StatusSeeOther {
			return "ok"
		}
		html := body(resp)
		switch {
		case strings.Contains(html, "For mange forsøk"):
			return "limited"
		case strings.Contains(html, "Feil token"):
			return "wrong"
		}
		return "?"
	}
	for range 10 {
		if got := login("203.0.113.1", adminToken, nil); got != "ok" {
			t.Fatalf("correct token: %s", got)
		}
	}
	for i := range 5 {
		// a forged header doesn't give the guesser fresh identities
		forged := map[string]string{"CF-Connecting-IP": fmt.Sprintf("198.51.100.%d", i), "X-Forwarded-For": fmt.Sprintf("198.51.100.%d", i)}
		if got := login("203.0.113.66", "gjett", forged); got != "wrong" {
			t.Fatalf("attempt %d: %s, want wrong", i, got)
		}
	}
	if got := login("203.0.113.66", adminToken, nil); got != "limited" {
		t.Errorf("sixth attempt from the guessing address: %s, want limited", got)
	}
	if got := login("203.0.113.1", adminToken, nil); got != "ok" {
		t.Errorf("admin elsewhere: %s, want ok", got)
	}
}
