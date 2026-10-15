package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matsbst/kringkasting/internal/app"
	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/config"
	"github.com/matsbst/kringkasting/internal/testutil"
)

// archiveNRK fakes a podcast "arkiv" with `pages` archive pages of 3
// episodes each; the first listing (no page parameter) is page 1.
func archiveNRK(pages int) *testutil.FakeNRK {
	episode := func(id string, n int) map[string]any {
		return map[string]any{"id": id, "episodeId": "l_" + id, "titles": map[string]any{"title": id},
			"date":              time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Duration(n) * time.Hour).Format(time.RFC3339),
			"durationInSeconds": 60, "_links": map[string]any{"share": map[string]any{"href": "https://radio.nrk.no/podkast/arkiv/l_" + id}}}
	}
	page := func(p int) testutil.Reply {
		var eps []any
		for i := range 3 {
			n := (p-1)*3 + i
			eps = append(eps, episode(fmt.Sprintf("e%02d", n), n))
		}
		body := map[string]any{"_embedded": map[string]any{"episodes": eps}}
		if p < pages {
			body["_links"] = map[string]any{"next": map[string]any{"href": fmt.Sprintf("/radio/catalog/podcast/arkiv/episodes?page=%d&pageSize=50", p+1)}}
		}
		b, _ := json.Marshal(body)
		return testutil.Reply{Status: 200, Body: string(b)}
	}
	return &testutil.FakeNRK{Handle: func(method, url string) testutil.Reply {
		switch {
		case strings.Contains(url, "/playback/manifest/"):
			return testutil.Reply{Status: 200, Body: `{"playable":{"assets":[{"url":"https://x/e.m3u8"}]}}`}
		case strings.HasSuffix(url, "/radio/catalog/podcast/arkiv/episodes"):
			return page(1)
		case strings.Contains(url, "/radio/catalog/podcast/arkiv/episodes?page="):
			var p int
			fmt.Sscanf(url[strings.Index(url, "page=")+5:], "%d", &p)
			return page(p)
		case strings.HasSuffix(url, "/radio/catalog/podcast/arkiv"):
			return testutil.Reply{Status: 200, Body: `{"type":"podcast","series":{"id":"arkiv","titles":{"title":"Arkiv"}}}`}
		}
		return testutil.Reply{Status: 404}
	}}
}

func startWith(t *testing.T, dbPath string, nrk http.RoundTripper, pageDelay time.Duration) (*app.App, context.CancelFunc) {
	t.Helper()
	clock.Unpin()
	ctx, cancel := context.WithCancel(context.Background())
	cfg := config.FromLookup(func(k string) string {
		return map[string]string{"KRINGKASTING_DB_PATH": dbPath, "APP_ORIGIN": origin}[k]
	})
	a, err := app.New(ctx, cfg, app.Options{Transport: nrk, PageDelay: pageDelay})
	if err != nil {
		t.Fatal(err)
	}
	return a, cancel
}

func closeApp(t *testing.T, a *app.App, cancel context.CancelFunc) {
	t.Helper()
	cancel()
	ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if err := a.Close(ctx); err != nil {
		t.Errorf("close: %v", err)
	}
}

func get(a *app.App, ctx context.Context, path string) *http.Response {
	req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, req)
	return rec.Result()
}

func episodeCount(t *testing.T, a *app.App) int {
	t.Helper()
	ids, err := a.Store.ReadEpisodeIDs("arkiv")
	if err != nil {
		t.Fatal(err)
	}
	return len(ids)
}

// The crawl belongs to the process: it continues after the response, and
// after the client has gone away.
func TestCrawlOutlivesRequestAndClient(t *testing.T) {
	a, cancel := startWith(t, filepath.Join(t.TempDir(), "db.sqlite3"), archiveNRK(4), time.Millisecond)
	defer closeApp(t, a, cancel)

	gone, disconnect := context.WithCancel(context.Background())
	disconnect() // the client disconnected before the response
	if resp := get(a, gone, "/api/feeds/arkiv"); resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	wait, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if err := a.Crawler.WaitIdle(wait); err != nil {
		t.Fatal(err)
	}
	if n := episodeCount(t, a); n != 12 {
		t.Errorf("%d episodes after the crawl, want all 12", n)
	}
	if s, _ := a.Store.ReadSeries("arkiv"); !s.BacklogComplete {
		t.Error("archive should be complete")
	}
}

// Shutdown mid-crawl leaves a consistent checkpoint; after a restart the
// next request resumes it, without gaps or duplicates.
func TestShutdownCheckpointResumes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db.sqlite3")
	nrk := archiveNRK(5)
	a, cancel := startWith(t, dbPath, nrk, 300*time.Millisecond)
	get(a, context.Background(), "/api/feeds/arkiv")
	// let page 2 land, then shut down during the pause before page 3
	for deadline := time.Now().Add(5 * time.Second); episodeCount(t, a) < 6 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	closeApp(t, a, cancel)

	b, cancel := startWith(t, dbPath, nrk, time.Millisecond)
	defer closeApp(t, b, cancel)
	s, _ := b.Store.ReadSeries("arkiv")
	if s.BacklogComplete || len(s.Episodes) < 6 || len(s.Episodes) == 15 {
		t.Fatalf("expected a partial archive after shutdown: complete=%v, %d episodes", s.BacklogComplete, len(s.Episodes))
	}
	get(b, context.Background(), "/api/feeds/arkiv")
	wait, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if err := b.Crawler.WaitIdle(wait); err != nil {
		t.Fatal(err)
	}
	s, _ = b.Store.ReadSeries("arkiv")
	if !s.BacklogComplete || len(s.Episodes) != 15 {
		t.Errorf("after resume: complete=%v, %d episodes, want 15", s.BacklogComplete, len(s.Episodes))
	}
}

// Many concurrent feed requests while the crawler writes (run with -race).
func TestConcurrentRequestsDuringCrawl(t *testing.T) {
	a, cancel := startWith(t, filepath.Join(t.TempDir(), "db.sqlite3"), archiveNRK(6), 5*time.Millisecond)
	defer closeApp(t, a, cancel)
	// the first request stores the feed (unknown feeds are rate-limited as
	// cold fetches) and starts the archive crawl the others race against
	if resp := get(a, context.Background(), "/api/feeds/arkiv"); resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var wg sync.WaitGroup
	statuses := make(chan int, 200)
	for i := range 200 {
		wg.Go(func() {
			path := "/api/feeds/arkiv"
			if i%5 == 0 {
				path = "/sitemap.xml"
			}
			statuses <- get(a, context.Background(), path).StatusCode
		})
	}
	wg.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusOK {
			t.Errorf("status %d under concurrency", status)
		}
	}
	wait, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if err := a.Crawler.WaitIdle(wait); err != nil {
		t.Fatal(err)
	}
	if n := episodeCount(t, a); n != 18 {
		t.Errorf("%d episodes, want 18", n)
	}
}
