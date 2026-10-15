package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matsbst/kringkasting/internal/app"
	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/testutil"
)

const playlist = "https://nrk-od-world-77.akamaized.net/open/potion/aa/aa/clip.smil/muxed.m3u8?adap=clip&s=0&aco=aac"

// videoNRK fakes a podcast "vid" whose episodes cover every video case,
// plus an umbrella podcast "paraply" whose season shares an episode.
// Episode durations are 1305 s (21:45).
type videoNRK struct {
	*testutil.FakeNRK
	mu        sync.Mutex
	transient bool // clip "c-flaky" answers 503 while true
}

func newVideoNRK() *videoNRK {
	v := &videoNRK{transient: true}
	episode := func(id, clip string, daysAgo int) map[string]any {
		e := map[string]any{"id": id, "episodeId": "l_" + id, "titles": map[string]any{"title": id},
			"date": clock.Now().Add(-time.Duration(daysAgo) * 24 * time.Hour).UTC().Format(time.RFC3339), "durationInSeconds": 1305,
			"_links": map[string]any{"share": map[string]any{"href": "https://radio.nrk.no/podkast/vid/l_" + id}}}
		if clip != "" {
			e["clipId"] = clip
		}
		return e
	}
	reply := func(body any) testutil.Reply {
		b, _ := json.Marshal(body)
		return testutil.Reply{Status: 200, Body: string(b)}
	}
	clip := func(medium, duration string, encrypted bool) testutil.Reply {
		return reply(map[string]any{"sourceMedium": medium, "playability": "playable", "playable": map[string]any{
			"duration": duration, "assets": []any{map[string]any{"url": playlist, "format": "HLS", "encrypted": encrypted}}}})
	}
	v.FakeNRK = &testutil.FakeNRK{Handle: func(method, rawURL string) testutil.Reply {
		path := strings.TrimPrefix(rawURL, "https://psapi.nrk.no")
		switch {
		case method == "HEAD":
			return testutil.Reply{Status: 200, Headers: map[string]string{"content-length": "1000"}}
		case strings.HasPrefix(path, "/playback/manifest/podcast/"):
			return reply(map[string]any{"playable": map[string]any{"assets": []any{
				map[string]any{"url": "https://podkast.nrk.no/" + strings.TrimPrefix(path, "/playback/manifest/podcast/") + ".mp3"}}}})
		case path == "/playback/manifest/clip/c-video", path == "/playback/manifest/clip/c-season":
			return clip("video", "PT21M45S", false)
		case path == "/playback/manifest/clip/c-promo":
			return clip("video", "PT1M", false) // a trailer, not the episode
		case path == "/playback/manifest/clip/c-audio":
			return clip("audio", "PT21M45S", false)
		case path == "/playback/manifest/clip/c-drm":
			return clip("video", "PT21M45S", true)
		case path == "/playback/manifest/clip/c-flaky":
			v.mu.Lock()
			defer v.mu.Unlock()
			if v.transient {
				return testutil.Reply{Status: 503}
			}
			return clip("video", "PT21M50S", false)
		case strings.HasSuffix(path, "/radio/catalog/podcast/vid/episodes"),
			strings.HasSuffix(path, "/radio/catalog/podcast/vid/episodes?page=1&pageSize=50"):
			return reply(map[string]any{"_embedded": map[string]any{"episodes": []any{
				episode("a", "c-video", 1), episode("b", "c-promo", 2), episode("c", "c-audio", 3),
				episode("d", "c-gone", 4), episode("e", "", 5), episode("f", "c-flaky", 6), episode("g", "c-drm", 7)}}})
		case strings.HasSuffix(path, "/radio/catalog/podcast/vid"):
			return reply(map[string]any{"seriesType": "standard", "type": "podcast",
				"series": map[string]any{"id": "vid", "titles": map[string]any{"title": "Vid"}}})
		// umbrella podcast with one season sharing episode "s1"
		case strings.HasSuffix(path, "/radio/catalog/podcast/paraply/episodes"),
			strings.Contains(path, "/radio/catalog/podcast/paraply/episodes?page=1"):
			return reply(map[string]any{"_embedded": map[string]any{"episodes": []any{episode("s1", "c-season", 1)}}})
		case strings.HasSuffix(path, "/radio/catalog/podcast/paraply"):
			return reply(map[string]any{"seriesType": "umbrella", "type": "podcast",
				"series": map[string]any{"id": "paraply", "titles": map[string]any{"title": "Paraply"}},
				"_links": map[string]any{"seasons": []any{map[string]any{"href": "/radio/catalog/podcast/paraply/seasons/s"}}}})
		case strings.HasSuffix(path, "/radio/catalog/podcast/paraply/seasons/s"):
			return reply(map[string]any{"titles": map[string]any{"title": "Sesong"}, "_embedded": map[string]any{
				"episodes": map[string]any{"_embedded": map[string]any{"episodes": []any{episode("s1", "c-season", 1)}}}}})
		case strings.Contains(path, "/radio/catalog/podcast/paraply/seasons/s/episodes"):
			return reply(map[string]any{"_embedded": map[string]any{"episodes": []any{episode("s1", "c-season", 1)}}})
		}
		return testutil.Reply{Status: 404}
	}}
	return v
}

func (v *videoNRK) recover() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.transient = false
}

func settle(t *testing.T, a *app.App) {
	t.Helper()
	ctx, done := context.WithTimeout(context.Background(), 20*time.Second)
	defer done()
	for range 3 {
		if err := a.Crawler.WaitIdle(ctx); err != nil {
			t.Fatal(err)
		}
		if err := a.Videos.WaitIdle(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

var alternate = regexp.MustCompile(`<item><title>([^<]+)</title>.*?</item>`)

// videoItems returns the items that carry an alternate enclosure.
func videoItems(xml string) []string {
	var titles []string
	for _, m := range alternate.FindAllStringSubmatch(xml, -1) {
		if strings.Contains(m[0], "podcast:alternateEnclosure") {
			titles = append(titles, m[1])
		}
	}
	return titles
}

func withoutVideo(xml string) string {
	return regexp.MustCompile(`<podcast:alternateEnclosure.*?</podcast:alternateEnclosure>`).ReplaceAllString(xml, "")
}

func TestVideoPassThrough(t *testing.T) {
	nrk := newVideoNRK()
	dbPath := filepath.Join(t.TempDir(), "db.sqlite3")
	a, cancel := startWith(t, dbPath, nrk, time.Millisecond)

	first := get(a, context.Background(), "/api/feeds/vid")
	before := body(first)
	settle(t, a)
	after := get(a, context.Background(), "/api/feeds/vid")
	xml := body(after)

	// only the full-length, playable, unencrypted video is attached
	if got := videoItems(xml); fmt.Sprint(got) != "[a]" {
		t.Errorf("episodes with video: %v, want [a]", got)
	}
	want := `<podcast:alternateEnclosure type="application/x-mpegURL" length="0" lang="no" title="Video">` +
		`<podcast:source uri="` + strings.ReplaceAll(playlist, "&", "&amp;") + `"/></podcast:alternateEnclosure></item>`
	if !strings.Contains(xml, want) {
		t.Errorf("alternate enclosure markup:\n%s", xml)
	}
	// audio identity unchanged: everything else in the feed is as before
	if withoutVideo(xml) != before {
		t.Error("adding video changed more than the alternate enclosure")
	}
	// the cached feed was invalidated: validators moved
	if after.Header.Get("ETag") == first.Header.Get("ETag") {
		t.Error("ETag must change when a video is added")
	}
	resp := app304(a, "/api/feeds/vid", first.Header.Get("ETag"))
	if resp.StatusCode != http.StatusOK {
		t.Errorf("old ETag should no longer match: %d", resp.StatusCode)
	}

	// a transient failure is retried after its deadline and then found
	nrk.recover()
	clock.Pin(time.Now().Add(2 * time.Hour))
	a.Videos.Enqueue("vid")
	settle(t, a)
	if got := videoItems(body(get(a, context.Background(), "/api/feeds/vid"))); fmt.Sprint(got) != "[a f]" {
		t.Errorf("after recovery: %v, want [a f]", got)
	}

	// a recent episode without video is checked again after a few hours;
	// a lookup happens only when due
	calls := nrk.Count("/manifest/clip/c-audio")
	a.Videos.Enqueue("vid")
	settle(t, a)
	if nrk.Count("/manifest/clip/c-audio") != calls {
		t.Error("an episode without video must not be re-checked before it is due")
	}
	clock.Pin(time.Now().Add(9 * time.Hour))
	a.Videos.Enqueue("vid")
	settle(t, a)
	if nrk.Count("/manifest/clip/c-audio") != calls+1 {
		t.Error("a recent episode without video should be re-checked after a few hours")
	}
	closeApp(t, a, cancel)

	// persisted: a restart serves the video without asking NRK again
	clips := nrk.Count("/manifest/clip/")
	b, cancel := startWith(t, dbPath, nrk, time.Millisecond)
	defer closeApp(t, b, cancel)
	if got := videoItems(body(get(b, context.Background(), "/api/feeds/vid"))); fmt.Sprint(got) != "[a f]" {
		t.Errorf("after restart: %v", got)
	}
	settle(t, b)
	if nrk.Count("/manifest/clip/") != clips {
		t.Error("a restart must not repeat video lookups")
	}
}

func app304(a *app.App, path, etag string) *http.Response {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, req)
	return rec.Result()
}

func rawExec(dbPath, query string) (sql.Result, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.Exec(query)
}

func rawCount(dbPath, query string) (int, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var n int
	err = db.QueryRow(query).Scan(&n)
	return n, err
}

// A podcast and its season feed carry the same episode: both get the
// video, from one lookup, also under concurrent requests.
func TestVideoSharedAcrossFeedsAndRequests(t *testing.T) {
	nrk := newVideoNRK()
	a, cancel := startWith(t, filepath.Join(t.TempDir(), "db.sqlite3"), nrk, time.Millisecond)
	defer closeApp(t, a, cancel)

	var wg sync.WaitGroup
	for i := range 20 {
		path := "/api/feeds/paraply"
		if i%2 == 0 {
			path = "/api/feeds/paraply/sesong/s"
		}
		wg.Go(func() { get(a, context.Background(), path) })
	}
	wg.Wait()
	settle(t, a)
	for _, path := range []string{"/api/feeds/paraply", "/api/feeds/paraply/sesong/s"} {
		if got := videoItems(body(get(a, context.Background(), path))); fmt.Sprint(got) != "[s1]" {
			t.Errorf("%s: videos %v, want [s1]", path, got)
		}
	}
	if n := nrk.Count("/manifest/clip/c-season"); n != 1 {
		t.Errorf("%d lookups of a clip shared by two feeds and 20 requests, want 1", n)
	}
}

// Older episodes stored before clip references were captured get them
// back gradually: one listing page per round, with no playback lookups.
func TestVideoBackfill(t *testing.T) {
	nrk := newVideoNRK()
	dbPath := filepath.Join(t.TempDir(), "db.sqlite3")
	a, cancel := startWith(t, dbPath, nrk, time.Millisecond)
	defer closeApp(t, a, cancel)
	get(a, context.Background(), "/api/feeds/vid")
	settle(t, a)

	// forget the clip references, as for an archive stored by an older
	// version, and restart the backfill (it may have finished during setup)
	if _, err := rawExec(dbPath, "UPDATE episodes SET clip_id = NULL WHERE id IN ('b','c')"); err != nil {
		t.Fatal(err)
	}
	a.Store.DeleteState("video-backfill:vid")
	manifests := nrk.Count("/playback/manifest/podcast/")
	listings := nrk.Count("/episodes?page=1&pageSize=50")
	a.Videos.Enqueue("vid")
	settle(t, a)
	if nrk.Count("/episodes?page=1&pageSize=50") != listings+1 {
		t.Error("backfill should fetch exactly one listing page per round")
	}
	if nrk.Count("/playback/manifest/podcast/") != manifests {
		t.Error("backfill must not look up audio manifests")
	}
	if n, _ := rawCount(dbPath, "SELECT count(*) FROM episodes WHERE id IN ('b','c') AND clip_id IS NOT NULL"); n != 2 {
		t.Errorf("clip references recovered for %d of 2 episodes", n)
	}
	// the single page was the last: the backfill is done and stays done
	a.Videos.Enqueue("vid")
	settle(t, a)
	if nrk.Count("/episodes?page=1&pageSize=50") != listings+1 {
		t.Error("a finished backfill must not list again")
	}
}
