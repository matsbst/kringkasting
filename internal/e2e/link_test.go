package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/matsbst/kringkasting/internal/app"
	"github.com/matsbst/kringkasting/internal/testutil"
)

// linkNRK fakes NRK for link resolution: an umbrella podcast with a
// standalone season, an ordinary podcast, a radio series and search.
func linkNRK() *testutil.FakeNRK {
	reply := func(v any) testutil.Reply {
		b, _ := json.Marshal(v)
		return testutil.Reply{Status: 200, Body: string(b)}
	}
	image := func(base string) []any {
		return []any{map[string]any{"url": base + "-300", "width": 300}, map[string]any{"url": base + "-600", "width": 600}}
	}
	return &testutil.FakeNRK{Handle: func(method, rawURL string) testutil.Reply {
		path := strings.TrimPrefix(rawURL, "https://psapi.nrk.no")
		switch path {
		case "/radio/catalog/podcast/musikkhistorier":
			return reply(map[string]any{"seriesType": "umbrella", "type": "podcast", "series": map[string]any{
				"id": "musikkhistorier", "titles": map[string]any{"title": "Musikkhistorier", "subtitle": "Historier om musikk"},
				"squareImage": image("mh")}})
		case "/radio/catalog/podcast/musikkhistorier/seasons/miles-davis--bak-mytene":
			return reply(map[string]any{"titles": map[string]any{"title": "Miles Davis – bak mytene", "subtitle": "Om Miles"},
				"squareImage": image("miles")})
		case "/radio/catalog/podcast/musikkhistorier/episodes/l_miles-1":
			return reply(map[string]any{"_links": map[string]any{
				"season": map[string]any{"href": "/radio/catalog/podcast/musikkhistorier/seasons/miles-davis--bak-mytene"}}})
		case "/radio/catalog/podcast/sentralbordet":
			return reply(map[string]any{"seriesType": "standard", "type": "podcast", "series": map[string]any{
				"id": "sentralbordet", "titles": map[string]any{"title": "Sentralbordet"}, "squareImage": image("sb")}})
		case "/radio/catalog/series/radioteatret":
			return reply(map[string]any{"seriesType": "standard", "type": "series", "series": map[string]any{
				"id": "radioteatret", "titles": map[string]any{"title": "Radioteatret"}, "image": image("rt")}})
		case "/radio/catalog/series/radioteatret/seasons/2026":
			return reply(map[string]any{"titles": map[string]any{"title": "2026"}})
		case "/radio/catalog/podcast/nede":
			return testutil.Reply{Status: 503}
		}
		if strings.HasPrefix(path, "/radio/search/search?q=") {
			return reply(map[string]any{"results": map[string]any{"series": map[string]any{"results": []any{
				map[string]any{"seriesId": "sentralbordet", "type": "podcast", "title": "Sentralbordet"}}}}})
		}
		return testutil.Reply{Status: 404}
	}}
}

func linkApp(t *testing.T) (*app.App, *testutil.FakeNRK) {
	t.Helper()
	fake := linkNRK()
	a, cancel := startWith(t, filepath.Join(t.TempDir(), "db.sqlite3"), fake, 0)
	t.Cleanup(func() { closeApp(t, a, cancel) })
	return a, fake
}

func fetch(t *testing.T, a *app.App, path string, client string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = client + ":1234"
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, req)
	return rec.Result(), rec.Body.String()
}

func fragment(t *testing.T, a *app.App, query string) (int, string) {
	t.Helper()
	resp, body := fetch(t, a, "/api/search/fragment?q="+url.QueryEscape(query), "192.0.2.10")
	return resp.StatusCode, body
}

func feedLinks(html string) []string {
	var feeds []string
	for _, part := range strings.Split(html, `data-feed-url="`)[1:] {
		feeds = append(feeds, strings.TrimPrefix(part[:strings.Index(part, `"`)], origin))
	}
	return feeds
}

func TestLinkSearch(t *testing.T) {
	a, fake := linkApp(t)
	for _, c := range []struct {
		name, query string
		feeds       []string
		contains    []string
	}{
		{"podcast", "https://radio.nrk.no/podkast/sentralbordet",
			[]string{"/api/feeds/sentralbordet"}, []string{"Fra NRK-lenken", "Sentralbordet"}},
		{"podcast without scheme", "radio.nrk.no/podkast/sentralbordet/",
			[]string{"/api/feeds/sentralbordet"}, nil},
		{"season", "https://radio.nrk.no/podkast/musikkhistorier/sesong/miles-davis--bak-mytene",
			[]string{"/api/feeds/musikkhistorier/sesong/miles-davis--bak-mytene", "/api/feeds/musikkhistorier"},
			[]string{"Sesong i «Musikkhistorier»", "Hele podkasten", "Miles Davis – bak mytene"}},
		{"episode in an umbrella season (share-link form)", "https://radio.nrk.no/podkast/musikkhistorier/l_miles-1",
			[]string{"/api/feeds/musikkhistorier/sesong/miles-davis--bak-mytene", "/api/feeds/musikkhistorier"},
			[]string{"Lenken går til én episode", "Sesong i «Musikkhistorier»"}},
		{"episode of an ordinary podcast, longer than 100 characters",
			"https://radio.nrk.no/podkast/sentralbordet/l_1de12aee-3b7a-4a14-a12a-ee3b7a8a14a7?utm_source=share&utm_medium=x",
			[]string{"/api/feeds/sentralbordet"}, []string{"Lenken går til én episode"}},
		{"radio series season", "https://radio.nrk.no/serie/radioteatret/sesong/2026",
			[]string{"/api/feeds/radioteatret/sesong/2026", "/api/feeds/radioteatret"}, []string{"Hele serien"}},
	} {
		status, html := fragment(t, a, c.query)
		if status != http.StatusOK {
			t.Errorf("%s: status %d", c.name, status)
		}
		if got := feedLinks(html); strings.Join(got, " ") != strings.Join(c.feeds, " ") {
			t.Errorf("%s: feeds %v, want %v", c.name, got, c.feeds)
		}
		for _, want := range c.contains {
			if !strings.Contains(html, want) {
				t.Errorf("%s: missing %q", c.name, want)
			}
		}
	}

	// an ordinary podcast's episode needs no episode lookup: its season
	// isn't a standalone show
	if n := fake.Count("/sentralbordet/episodes/"); n != 0 {
		t.Errorf("%d episode lookups for an ordinary podcast", n)
	}
	// the pasted URL itself is never fetched: only NRK's API
	for _, request := range fake.Requests {
		if !strings.HasPrefix(request, "GET https://psapi.nrk.no/") {
			t.Errorf("unexpected request %s", request)
		}
	}
	// repeated links are served from the cache
	before := len(fake.Requests)
	fragment(t, a, "https://radio.nrk.no/podkast/musikkhistorier/sesong/miles-davis--bak-mytene")
	if len(fake.Requests) != before {
		t.Error("a repeated link should not reach NRK again")
	}
}

func TestLinkMessages(t *testing.T) {
	a, fake := linkApp(t)
	for _, c := range []struct {
		name, query, want string
		status            int
	}{
		{"other NRK site", "https://tv.nrk.no/serie/skam", "Lenken støttes ikke", 200},
		{"other host", "https://example.com/podkast/sentralbordet", "Lenken støttes ikke", 200},
		{"malformed path", "https://radio.nrk.no/podkast/sentralbordet/sesong", "Lenken støttes ikke", 200},
		{"path traversal", "https://radio.nrk.no/podkast/..%2Fadmin", "Lenken støttes ikke", 200},
		{"unknown podcast", "https://radio.nrk.no/podkast/finnes-ikke", "Fant ikke podkasten i lenken", 200},
		{"NRK outage", "https://radio.nrk.no/podkast/nede", "Søket feilet", 503},
	} {
		status, html := fragment(t, a, c.query)
		if status != c.status || !strings.Contains(html, c.want) {
			t.Errorf("%s: status %d, want %d with %q", c.name, status, c.status, c.want)
		}
	}
	for _, request := range fake.Requests {
		if strings.Contains(request, "tv.nrk.no") || strings.Contains(request, "example.com") || strings.Contains(request, "admin") {
			t.Errorf("unsupported link reached the network: %s", request)
		}
	}
}

// Name search, the JSON API and the no-JS page all keep working, and links
// share the search rate limit.
func TestLinkSearchEntryPoints(t *testing.T) {
	a, _ := linkApp(t)
	if _, html := fragment(t, a, "sentralbordet"); !strings.Contains(html, "1 treff for «sentralbordet»") {
		t.Error("name search changed")
	}
	_, body := fetch(t, a, "/api/search?q="+url.QueryEscape("https://radio.nrk.no/podkast/musikkhistorier/sesong/miles-davis--bak-mytene"), "192.0.2.11")
	var api struct {
		Results []struct {
			SeriesID, SeasonID, Title string
		}
	}
	if err := json.Unmarshal([]byte(body), &api); err != nil || len(api.Results) != 2 || api.Results[0].SeasonID != "miles-davis--bak-mytene" {
		t.Errorf("JSON API for a link: %s", body)
	}
	resp, html := fetch(t, a, "/?query="+url.QueryEscape("https://radio.nrk.no/podkast/sentralbordet"), "192.0.2.12")
	if resp.StatusCode != 200 || !strings.Contains(html, `data-feed-url="`+origin+`/api/feeds/sentralbordet"`) {
		t.Error("the no-JS page should render link results")
	}

	limited := 0
	for range 40 {
		resp, _ := fetch(t, a, "/api/search/fragment?q="+url.QueryEscape("https://radio.nrk.no/podkast/sentralbordet"), "192.0.2.13")
		if resp.StatusCode == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Error("links must count against the search rate limit")
	}
}

// The subscribe dialog's QR code: only for valid feed ids, and the address
// it carries renders the very dialog it opens on the phone.
func TestQRHandoff(t *testing.T) {
	a, _ := linkApp(t)
	for _, c := range []struct{ feedID, dialogID string }{
		{"sentralbordet", "subscribe-sentralbordet"},
		{"musikkhistorier/sesong/miles-davis--bak-mytene", "subscribe-musikkhistorier-sesong-miles-davis--bak-mytene"},
	} {
		resp, svg := fetch(t, a, "/api/qr?feed="+url.QueryEscape(c.feedID), "qr")
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/svg+xml" ||
			!strings.HasPrefix(svg, "<svg") || !strings.Contains(resp.Header.Get("Cache-Control"), "max-age=86400") {
			t.Errorf("%s: status %d, %s", c.feedID, resp.StatusCode, resp.Header)
		}

		// the scanned address, as the phone requests it (no fragment)
		resp, html := fetch(t, a, "/?query=radio.nrk.no/podkast/"+c.feedID, "phone")
		if resp.StatusCode != http.StatusOK || !strings.Contains(html, `<dialog id="`+c.dialogID+`"`) {
			t.Errorf("%s: the handoff page lacks dialog %s", c.feedID, c.dialogID)
		}
		// "Del" shares the same address
		if !strings.Contains(html, `data-url="`+origin+`/?query=radio.nrk.no/podkast/`+c.feedID+`#`+c.dialogID+`"`) {
			t.Errorf("%s: the share link isn't the handoff address", c.feedID)
		}
		// the dialog's QR code, followed as the browser would
		dialog := html[strings.Index(html, `<dialog id="`+c.dialogID+`"`):]
		src := dialog[strings.Index(dialog, `data-qr-src="`)+len(`data-qr-src="`):]
		src = strings.ReplaceAll(src[:strings.Index(src, `"`)], "&amp;", "&")
		if resp, _ := fetch(t, a, src, "qr"); resp.StatusCode != http.StatusOK {
			t.Errorf("%s: the dialog's QR code %s: status %d", c.feedID, src, resp.StatusCode)
		}
	}
	for _, bad := range []string{"", "a b", "../admin", "a/b", "https://example.com"} {
		if resp, _ := fetch(t, a, "/api/qr?feed="+url.QueryEscape(bad), "qr"); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%q: status %d, want 404", bad, resp.StatusCode)
		}
	}
}

// Every app says which phones it exists on, so app.js can hide the rest.
func TestAppPlatforms(t *testing.T) {
	a, _ := linkApp(t)
	_, html := fetch(t, a, "/?query=radio.nrk.no/podkast/sentralbordet", "platforms")
	for app, platforms := range map[string]string{
		"apple": "ios", "overcast": "ios", "pocketcasts": "ios android", "castro": "ios",
		"downcast": "ios", "podcastaddict": "android", "antennapod": "android", "anytime": "ios android",
		"youtubemusic": "ios android",
	} {
		if !strings.Contains(html, `data-app="`+app+`" data-app-name="`) ||
			!regexp.MustCompile(`data-app="`+app+`" data-app-name="[^"]+" data-platforms="`+platforms+`"`).MatchString(html) {
			t.Errorf("%s: want data-platforms=%q", app, platforms)
		}
	}
}

// On a computer, each app says what works there: Apple Podcasts and
// Pocket Casts take a pasted link, Downcast (Mac) and YouTube Music (web)
// open, and apps only on phones use the shared QR handoff. And app.js finds each
// card's dialog right after its subscribe button.
func TestAppDesktopActions(t *testing.T) {
	a, _ := linkApp(t)
	_, html := fetch(t, a, "/?query=radio.nrk.no/podkast/sentralbordet", "desktop")
	for app, desktop := range map[string]string{
		"apple": "mac:copy-open windows:copy", "pocketcasts": "*:copy-open", "downcast": "mac:open", "youtubemusic": "*:open",
		"overcast": "", "castro": "", "podcastaddict": "", "antennapod": "", "anytime": "",
	} {
		row := regexp.MustCompile(`<a [^>]*data-app="` + app + `"[^>]*>`).FindString(html)
		got := regexp.MustCompile(`data-desktop="([^"]+)"`).FindStringSubmatch(row)
		if (desktop == "" && got != nil) || (desktop != "" && (got == nil || got[1] != desktop)) {
			t.Errorf("%s: row %q, want data-desktop=%q", app, row, desktop)
		}
	}
	if !strings.Contains(html, `data-desktop-href="https://play.pocketcasts.com/discover"`) {
		t.Error("Pocket Casts opens its web player on a computer")
	}
	if !regexp.MustCompile(`data-subscribe data-feed-url="[^"]+">\s*<button[^>]*>.*?</button>\s*</span>\s*<dialog `).MatchString(html) {
		t.Error("the subscribe dialog must directly follow its button")
	}
}

// The app sheet covers the page on a phone, so it names the podcast itself.
func TestDialogNamesPodcast(t *testing.T) {
	a, _ := linkApp(t)
	_, html := fetch(t, a, "/?query=radio.nrk.no/podkast/sentralbordet", "desktop")
	card := regexp.MustCompile(`<h3[^>]*>([^<]+)</h3>`).FindStringSubmatch(html)
	heading := regexp.MustCompile(`<h2 id="[^"]+-title"[^>]*><span[^>]*>([^<]+)</span> <span[^>]*>Velg hvordan du vil abonnere</span></h2>`).FindStringSubmatch(html)
	if card == nil || heading == nil || heading[1] != card[1] {
		t.Fatalf("dialog heading %q, want the card title %q", heading, card)
	}
	if !regexp.MustCompile(`<dialog [^>]*>\s*<div[^>]*data-dialog-main>\s*<div[^>]*>\s*<img src="[^"]+" alt=""`).MatchString(html) {
		t.Error("the dialog heading shows the podcast image")
	}
}
