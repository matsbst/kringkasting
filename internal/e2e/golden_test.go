package e2e

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/matsbst/kringkasting/internal/feed"
	_ "modernc.org/sqlite"
)

var feeds = []string{
	"supertroett",
	"doemt",
	"musikkhistorier",
	"musikkhistorier/sesong/miles-davis--bak-mytene",
	"musikkhistorier/sesong/nick-cave--moerkets-lysmester",
	"radioteatret-nrk-p1pluss",
}

func adminEnv() map[string]string {
	return map[string]string{"APP_ORIGIN": origin, "ADMIN_TOKEN": adminToken}
}

// TestFeeds: feed XML, validators and conditional GETs must match the Deno
// version byte for byte (feed identity for subscribers).
func TestFeeds(t *testing.T) {
	app := start(t, options{env: adminEnv()})
	for _, id := range feeds {
		app.warmFeed(t, id)
	}
	for _, id := range feeds {
		slug := strings.ReplaceAll(id, "/", "_")
		path := "/api/feeds/" + id
		first := app.do(request{path: path})
		etag, lastModified := first.Header.Get("ETag"), first.Header.Get("Last-Modified")
		compare(t, "feed_"+slug, first, exact)
		compare(t, "feed_"+slug+"_304_etag", app.do(request{path: path, headers: map[string]string{"If-None-Match": etag}}), exact)
		compare(t, "feed_"+slug+"_304_ims", app.do(request{path: path, headers: map[string]string{"If-Modified-Since": lastModified}}), exact)
		compare(t, "feed_"+slug+"_etag_precedence", app.do(request{path: path,
			headers: map[string]string{"If-None-Match": `"stale"`, "If-Modified-Since": lastModified}}), headersOnly)
	}
	// browsers opening a feed get its stylesheet
	css := app.do(request{path: feed.Stylesheet})
	if css.StatusCode != http.StatusOK || !strings.HasPrefix(css.Header.Get("Content-Type"), "text/css") ||
		!strings.Contains(body(css), "podkast-strøm") {
		t.Errorf("feed stylesheet: status %d, %s", css.StatusCode, css.Header.Get("Content-Type"))
	}

	// Deno answered HEAD with content-length 0; Go sends the real length
	// (excluded from comparison). Everything else matches.
	compare(t, "feed_head", app.do(request{method: http.MethodHead, path: "/api/feeds/supertroett"}), headersOnly)
	compare(t, "feed_trailing_slash", app.do(request{path: "/api/feeds/supertroett/"}), statusOnly)
	compare(t, "feed_post", app.do(request{method: http.MethodPost, path: "/api/feeds/supertroett"}), statusOnly)
	compare(t, "feed_invalid_id", app.do(request{path: "/api/feeds/..%2Fetc"}), exact)
	compare(t, "feed_unknown", app.do(request{path: "/api/feeds/finnes-ikke-baseline"}), exact)
	compare(t, "season_unknown", app.do(request{path: "/api/feeds/musikkhistorier/sesong/finnes-ikke-baseline"}), exact)

	// chapters, by the feed's own links and by NRK episode ids
	for _, id := range []string{"supertroett", "musikkhistorier"} {
		xml := body(app.do(request{path: "/api/feeds/" + id}))
		link := regexp.MustCompile(`podcast:chapters url="([^"]+)"`).FindStringSubmatch(xml)[1]
		compare(t, "chapters_"+id+"_feed_link", app.do(request{path: strings.TrimPrefix(link, origin)}), exact)
	}
	compare(t, "chapters_supertroett_episode_id",
		app.do(request{path: "/api/feeds/supertroett/l_dc4c5213-d03c-4491-8c52-13d03cc491a1/chapters"}), exact)
	compare(t, "chapters_musikkhistorier_episode_id",
		app.do(request{path: "/api/feeds/musikkhistorier/l_6f0c81a3-4e1c-4cee-8c81-a34e1c0cee7c/chapters"}), exact)
	compare(t, "chapters_unknown", app.do(request{path: "/api/feeds/supertroett/finnes-ikke/chapters"}), exact)
	compare(t, "chapters_invalid_id", app.do(request{path: "/api/feeds/supertroett/..%2F..%2Fx/chapters"}), exact)
}

func TestSearchAndPages(t *testing.T) {
	app := start(t, options{env: adminEnv()})
	compare(t, "api_search", app.do(request{path: "/api/search?q=musikk"}), exact)
	compare(t, "api_search_empty", app.do(request{path: "/api/search?q="}), exact)
	compare(t, "api_search_post", app.do(request{method: http.MethodPost, path: "/api/search?q=musikk"}), statusOnly)
	compare(t, "page_sitemap", app.do(request{path: "/sitemap.xml"}), exact)
	compare(t, "page_not_found", app.do(request{path: "/finnes-ikke"}), statusOnly)

	home := compare(t, "page_home", app.do(request{path: "/"}), statusOnly)
	mustContain(t, "page_home", home, `<title>Kringkasting – NRK-podkaster som RSS</title>`,
		`<link rel="canonical" href="https://kringkasting.example/"/>`, `"@type":"WebSite"`, "Slik funker det")

	search := compare(t, "page_home_search", app.do(request{path: "/?query=musikk"}), statusOnly)
	mustContain(t, "page_home_search", search, `<title>Søk: musikk – Kringkasting</title>`,
		`<meta name="robots" content="noindex,follow"/>`, "10 treff for «musikk»",
		`href="podcast://kringkasting.example/api/feeds/musikklivet"`,
		`href="overcast://x-callback-url/add?url=https%3A%2F%2Fkringkasting.example%2Fapi%2Ffeeds%2Fmusikklivet"`,
		`addrssfeed=aHR0cHM6Ly9rcmluZ2thc3RpbmcuZXhhbXBsZS9hcGkvZmVlZHMvbXVzaWtrbGl2ZXQ"`,
		`href="https://radio.nrk.no/podkast/musikklivet"`)
	if got := strings.Count(search, "<article"); got != 10 {
		t.Errorf("page_home_search: %d cards, want 10", got)
	}
	none := compare(t, "page_home_search_none", app.do(request{path: "/?query=zzqqxxnotfound"}), statusOnly)
	// and points to pasting a link from NRK instead
	mustContain(t, "page_home_search_none", none, "Ingen treff for «zzqqxxnotfound»", "Kopier lenke")

	personvern := compare(t, "page_personvern", app.do(request{path: "/personvern"}), statusOnly)
	mustContain(t, "page_personvern", personvern, "<title>Personvern – Kringkasting</title>", `aria-current="page"`)

	fragment := app.do(request{path: "/api/search/fragment?q=musikk"})
	if fragment.StatusCode != http.StatusOK || strings.Count(body(fragment), "<article") != 10 {
		t.Errorf("search fragment: status %d", fragment.StatusCode)
	}
}

func TestStaticFiles(t *testing.T) {
	app := start(t, options{env: adminEnv()})
	goldens, _ := filepath.Glob(filepath.Join(goldenDir, "static_*.txt"))
	if len(goldens) == 0 {
		t.Fatal("no static golden files")
	}
	for _, golden := range goldens {
		name := strings.TrimSuffix(filepath.Base(golden), ".txt")
		path := "/" + strings.TrimPrefix(name, "static_")
		if strings.HasPrefix(path, "/fonts_") {
			path = "/fonts/" + strings.TrimPrefix(path, "/fonts_")
		}
		compare(t, name, app.do(request{path: path}), headersOnly)
	}
}

// robots.txt and llms.txt name the instance's own address, not the public
// instance's (they replaced their Deno baselines when that changed).
func TestOriginTextFiles(t *testing.T) {
	app := start(t, options{env: adminEnv()})
	for path, want := range map[string]string{
		"/robots.txt": "Sitemap: " + origin + "/sitemap.xml",
		"/llms.txt":   "[Search for a podcast](" + origin + "/)",
	} {
		resp := app.do(request{path: path})
		text := body(resp)
		if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") ||
			!strings.Contains(text, want) || strings.Contains(text, "https://kringkast.ing") {
			t.Errorf("%s: status %d, missing %q or names kringkast.ing:\n%s", path, resp.StatusCode, want, text)
		}
		etag := resp.Header.Get("ETag")
		if again := app.do(request{path: path, headers: map[string]string{"If-None-Match": etag}}); again.StatusCode != http.StatusNotModified {
			t.Errorf("%s: revalidation with its ETag: status %d", path, again.StatusCode)
		}
	}
}

func TestAdmin(t *testing.T) {
	app := start(t, options{env: adminEnv()})
	form := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	cookie := "kringkasting_admin=" + adminToken
	withCookie := map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Cookie": cookie}

	login := compare(t, "admin_login_page", app.do(request{path: "/admin"}), statusOnly)
	mustContain(t, "admin_login_page", login, "<title>Admin – Kringkasting</title>", `name="token"`)
	wrong := compare(t, "admin_login_wrong", app.do(request{method: "POST", path: "/admin", headers: form, body: "token=wrong-token-123"}), statusOnly)
	mustContain(t, "admin_login_wrong", wrong, "Feil token, prøv igjen.")
	compare(t, "admin_login_ok", app.do(request{method: "POST", path: "/admin", headers: form, body: "token=" + adminToken}), headersOnly)
	compare(t, "admin_login_ok_forwarded_https", app.do(request{method: "POST", path: "/admin",
		headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded", "X-Forwarded-Proto": "https"},
		body:    "token=" + adminToken}), headersOnly)
	dashboard := compare(t, "admin_dashboard", app.do(request{path: "/admin", headers: map[string]string{"Cookie": cookie}}), statusOnly)
	mustContain(t, "admin_dashboard", dashboard, "Siste 30 dager", "Strømmer (", "Rydd opp")
	compare(t, "admin_action_unauthenticated", app.do(request{method: "POST", path: "/admin", headers: form,
		body: "action=refresh&seriesId=supertroett"}), statusOnly)
	compare(t, "admin_action_unknown", app.do(request{method: "POST", path: "/admin", headers: withCookie,
		body: "action=explode&seriesId=supertroett"}), headersOnly)
}

func TestAdminDisabledAndOrigin(t *testing.T) {
	app := start(t, options{})
	compare(t, "admin_disabled", app.do(request{path: "/admin"}), statusOnly)
	compare(t, "admin_disabled_post", app.do(request{method: http.MethodPost, path: "/admin"}), statusOnly)
	app.warmFeed(t, "supertroett")
	compare(t, "origin_from_host", app.do(request{path: "/api/feeds/supertroett", host: "127.0.0.1:8765"}), exact)
	compare(t, "origin_forwarded", app.do(request{path: "/api/feeds/supertroett", host: "127.0.0.1:8765",
		headers: map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "proxy.example"}}), exact)
}

// TestFailedEpisode: a definitive manifest 404 skips the episode and puts it
// under backoff.
func TestFailedEpisode(t *testing.T) {
	app := start(t, options{env: map[string]string{"APP_ORIGIN": origin}, overlay: filepath.Join(scenarioDir, "failed-episode")})
	app.warmFeed(t, "supertroett")
	compare(t, "scenario_failed_episode_feed", app.do(request{path: "/api/feeds/supertroett"}), exact)
	app.stop(t)
	compareState(t, app.dbPath, "scenario_failed_episode_state")
}

// TestInterruptedCrawl: archive page 2 fails, the crawl pauses with its
// checkpoint; a restart two days later resumes and completes it.
func TestInterruptedCrawl(t *testing.T) {
	id := "radioteatret-nrk-p1pluss"
	first := start(t, options{env: map[string]string{"APP_ORIGIN": origin}, overlay: filepath.Join(scenarioDir, "interrupted-crawl")})
	first.warmFeed(t, id)
	compare(t, "scenario_interrupted_crawl_paused_feed", first.do(request{path: "/api/feeds/" + id}), exact)
	first.stop(t)
	compareState(t, first.dbPath, "scenario_interrupted_crawl_paused_state")

	second := start(t, options{env: map[string]string{"APP_ORIGIN": origin}, dbPath: first.dbPath, offset: 48 * time.Hour})
	second.warmFeed(t, id)
	compare(t, "scenario_interrupted_crawl_resumed_feed", second.do(request{path: "/api/feeds/" + id}), exact)
	second.stop(t)
	compareState(t, second.dbPath, "scenario_interrupted_crawl_resumed_state")
}

func mustContain(t *testing.T, name, html string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(html, part) {
			t.Errorf("%s: missing %q", name, part)
		}
	}
}

// compareState dumps durable state the way the capture tool did.
func compareState(t *testing.T, dbPath, name string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var lines []string
	dump := func(header, query string, columns int) {
		lines = append(lines, header)
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			values := make([]any, columns)
			pointers := make([]any, columns)
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			fields := make([]string, columns)
			for i, v := range values {
				switch v := v.(type) {
				case nil:
					fields[i] = ""
				case []byte:
					fields[i] = string(v)
				default:
					fields[i] = fmt.Sprint(v)
				}
			}
			lines = append(lines, strings.Join(fields, " | "))
		}
	}
	dump("# series: id | backlog_complete | backlog_cursor | catalog_kind | is_umbrella",
		"SELECT id, backlog_complete, backlog_cursor, catalog_kind, is_umbrella FROM series ORDER BY id", 5)
	dump("# episodes: series_id | id | url | date | duration_seconds | bytes",
		"SELECT series_id, id, url, date, duration_seconds, bytes FROM episodes ORDER BY series_id, id", 6)
	dump("# failed_episodes: series_id | id | attempts",
		"SELECT series_id, id, attempts FROM failed_episodes ORDER BY series_id, id", 3)
	// activity-since (when dashboard activity was first recorded) came
	// after the Deno baseline
	dump("# app_state keys", "SELECT key FROM app_state WHERE key <> 'activity-since' ORDER BY key", 1)
	got := strings.Join(lines, "\n") + "\n"
	want, err := os.ReadFile(filepath.Join(goldenDir, name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs:\n got:\n%s\nwant:\n%s", name, got, want)
	}
}
