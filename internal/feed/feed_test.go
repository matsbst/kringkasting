package feed_test

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/feed"
	"github.com/matsbst/kringkasting/internal/nrk"
	"github.com/matsbst/kringkasting/internal/retry"
	"github.com/matsbst/kringkasting/internal/stats"
	"github.com/matsbst/kringkasting/internal/store"
	"github.com/matsbst/kringkasting/internal/testutil"
)

func ep(id, url string) store.Episode {
	return store.Episode{ID: id, Title: "T " + id, URL: url, ShareLink: "https://radio.nrk.no/x/l_" + id,
		Date: time.Date(2026, 10, 1, 4, 0, 0, 0, time.UTC), DurationInSeconds: 61}
}

func TestRSSRendering(t *testing.T) {
	s := &store.Series{ID: "show/sesong/s1", Title: `Tom & "Jerry" <live>`, Link: "https://radio.nrk.no/podkast/show/sesong/s1",
		Episodes: []store.Episode{ep("mp3", "https://podkast.nrk.no/a.mp3"), ep("hls", "https://x/master.m3u8")}}
	xml := feed.AssembleFeed(s, "https://k.example")
	for _, want := range []string{
		`<title>Tom &amp; "Jerry" &lt;live&gt;</title>`,
		`<atom:link href="https://k.example/api/feeds/show/sesong/s1" rel="self" type="application/rss+xml"/>`,
		// season feeds resolve chapters through the parent series
		`<podcast:chapters url="https://k.example/api/feeds/show/mp3/chapters" type="application/json+chapters"/>`,
		`<enclosure url="https://podkast.nrk.no/a.mp3" length="0" type="audio/mpeg"/>`,
		`<pubDate>Thu, 01 Oct 2026 04:00:00 GMT</pubDate>`,
		`<description/>`,
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("missing %s", want)
		}
	}
	// mixed shows drop stray HLS episodes
	if strings.Contains(xml, "master.m3u8") {
		t.Error("HLS episode kept in a mixed show")
	}
	// stream-only shows keep HLS, typed honestly
	s.Episodes = []store.Episode{ep("hls", "https://x/master.m3u8")}
	if !strings.Contains(feed.AssembleFeed(s, "o"), `type="application/vnd.apple.mpegurl"`) || !feed.IsStreamOnly(s) {
		t.Error("stream-only show must keep its HLS enclosures")
	}
}

// Characters XML 1.0 doesn't allow are dropped from text and attributes,
// so one stray control character can't break a whole feed.
func TestRSSDropsInvalidXMLCharacters(t *testing.T) {
	e := ep("a", "https://podkast.nrk.no/a.mp3")
	e.Title = "Del\x0b1\x00"
	subtitle := "tab\tline\nok\uFFFF"
	e.Subtitle = &subtitle
	e.ShareLink = "https://radio.nrk.no/x/l_a\x1f"
	s := &store.Series{ID: "show", Title: "Show\x08", Link: "https://radio.nrk.no/podkast/show", Episodes: []store.Episode{e}}
	out := feed.AssembleFeed(s, "https://k.example")
	if err := xml.Unmarshal([]byte(out), new(any)); err != nil {
		t.Fatalf("feed is not valid XML: %v", err)
	}
	for _, want := range []string{"<title>Del1</title>", "<title>Show</title>", "tab\tline\nok", "l_a</link>"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

// A render cached under a pre-write version heals on the next request.
func TestCacheSelfHeals(t *testing.T) {
	c := feed.NewCache()
	s := &store.Series{ID: "c", Title: "Før", Episodes: []store.Episode{}, Version: 1}
	first := c.Render(s, "o")
	if c.Peek("c", "o", 1) == nil || c.Peek("c", "o", 2) != nil {
		t.Fatal("peek must match the version exactly")
	}
	s.Title, s.Version = "Etter", 2
	second := c.Render(s, "o")
	if second.ETag == first.ETag || !strings.Contains(second.XML, "Etter") {
		t.Error("new version must re-render")
	}
	// identical XML under a new version keeps Last-Modified
	s.Version = 3
	third := c.Render(s, "o")
	if !third.LastModified.Equal(second.LastModified) {
		t.Error("unchanged XML must keep its Last-Modified")
	}
	// a slower request holding an older read never replaces a newer render
	old := &store.Series{ID: "c", Title: "Før", Episodes: []store.Episode{}, Version: 2}
	if got := c.Render(old, "o"); got.ETag != third.ETag || c.Peek("c", "o", 3) == nil {
		t.Error("an older read replaced a newer render")
	}
	// a series that didn't come from the store is served but never cached
	fetched := &store.Series{ID: "d", Title: "Ny", Episodes: []store.Episode{}}
	if got := c.Render(fetched, "o"); !strings.Contains(got.XML, "Ny") || c.Peek("d", "o", 0) != nil || c.Peek("d", "o", 1) != nil {
		t.Error("an unversioned series must not be cached")
	}
	if feed.ETagFor("x") != `"11f6ad8ec52a2984abaafd7c3b516503785c2072"` {
		t.Errorf("ETag must be the quoted SHA-1: %s", feed.ETagFor("x"))
	}
}

// Request-derived origins must not multiply cached copies of a feed.
func TestCacheKeepsOneOriginPerFeed(t *testing.T) {
	c := feed.NewCache()
	s := &store.Series{ID: "c", Title: "Serie", Episodes: []store.Episode{}, Version: 1}
	c.Render(s, "https://a.example")
	second := c.Render(s, "https://b.example")
	if !strings.Contains(second.XML, "https://b.example") {
		t.Error("each origin renders its own URLs")
	}
	if c.Peek("c", "https://a.example", 1) != nil || c.Peek("c", "https://b.example", 1) == nil {
		t.Error("a feed keeps only its most recent origin")
	}
}

func TestPollingTiers(t *testing.T) {
	defer clock.Unpin()
	now := time.Now()
	clock.Pin(now)
	for days, want := range map[int]float64{0: 1, 2: 12, 30: 72, 180: 168} {
		newest := now.Add(-time.Duration(days) * 24 * time.Hour)
		if got := feed.IntervalHoursFor(&newest); got != want {
			t.Errorf("%d days old: %v hours, want %v", days, got, want)
		}
	}
	if feed.IntervalHoursFor(nil) != 3 {
		t.Error("empty shows refresh every 3 hours")
	}
}

type stubCrawler struct{ enqueued []string }

func (s *stubCrawler) Enqueue(id string) { s.enqueued = append(s.enqueued, id) }

func env(t *testing.T, handle func(method, url string) testutil.Reply) (*feed.Feeds, *store.Store, *testutil.FakeNRK, *stubCrawler) {
	t.Helper()
	s, _ := testutil.OpenStore(t)
	fake := &testutil.FakeNRK{Handle: handle}
	r := retry.New(s)
	crawler := &stubCrawler{}
	return feed.NewFeeds(s, nrk.New(fake, r, stats.New()), r, crawler), s, fake, crawler
}

func reply(v any) testutil.Reply {
	b, _ := json.Marshal(v)
	return testutil.Reply{Status: 200, Body: string(b)}
}

func nrkEp(id string, daysAgo int) map[string]any {
	return map[string]any{"id": id, "episodeId": "l_" + id, "titles": map[string]any{"title": id},
		"date": clock.Now().Add(-time.Duration(daysAgo) * 24 * time.Hour).Format(time.RFC3339), "durationInSeconds": 60,
		"_links": map[string]any{"share": map[string]any{"href": "https://radio.nrk.no/podkast/s/l_" + id}}}
}

func manifestOK(url string) (testutil.Reply, bool) {
	if strings.Contains(url, "/playback/manifest/") {
		if strings.HasSuffix(url, "/l_gone") {
			return testutil.Reply{Status: 404}, true
		}
		return reply(map[string]any{"playable": map[string]any{"assets": []any{map[string]any{"url": "https://x/e.m3u8"}}}}), true
	}
	return testutil.Reply{}, false
}

// The first request stores the feed, puts unplayable episodes under
// backoff (not re-checked on refresh) and admits the archive crawl.
func TestFirstFetchStoresAndBlocksFailures(t *testing.T) {
	feeds, s, fake, crawler := env(t, func(method, url string) testutil.Reply {
		if r, ok := manifestOK(url); ok {
			return r
		}
		switch {
		case strings.HasSuffix(url, "/radio/catalog/podcast/show/episodes"):
			return reply(map[string]any{"_embedded": map[string]any{"episodes": []any{nrkEp("ok", 1), nrkEp("gone", 2)}}})
		case strings.HasSuffix(url, "/radio/catalog/podcast/show"):
			return reply(map[string]any{"type": "podcast", "series": map[string]any{"id": "show", "titles": map[string]any{"title": "Show"}}})
		}
		return testutil.Reply{Status: 404}
	})
	got, err := feeds.GetSeries("show")
	if err != nil || got == nil || len(got.Episodes) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if blocked, _ := s.ReadBlockedEpisodeIDs("show"); !blocked["gone"] {
		t.Error("unplayable episode must be under backoff")
	}
	if fmt.Sprint(crawler.enqueued) != "[show]" {
		t.Errorf("archive crawl not admitted: %v", crawler.enqueued)
	}
	// unknown feeds are not-found and negatively cached
	if missing, err := feeds.GetSeries("finnes-ikke"); missing != nil || err != nil {
		t.Errorf("unknown feed: %v, %v", missing, err)
	}
	before := len(fake.Requests)
	feeds.GetSeries("finnes-ikke")
	if len(fake.Requests) != before {
		t.Error("not-found feeds must be cached for a while")
	}
}

// A cheap refresh that breaks off mid-listing saves a checkpoint with the
// ORIGINAL stop boundary, and the next refresh resumes from it with one
// page of overlap instead of rescanning.
func TestPartialRefreshResumes(t *testing.T) {
	defer clock.Unpin()
	start := time.Now()
	clock.Pin(start)
	page2Fails := true
	feeds, s, fake, _ := env(t, func(method, url string) testutil.Reply {
		if r, ok := manifestOK(url); ok {
			return r
		}
		switch {
		case strings.Contains(url, "/episodes?page=1"):
			return reply(map[string]any{"_embedded": map[string]any{"episodes": []any{nrkEp("n1", 0)}},
				"_links": map[string]any{"next": map[string]any{"href": "/radio/catalog/podcast/show/episodes?page=2&pageSize=50"}}})
		case strings.Contains(url, "/episodes?page=2"):
			if page2Fails {
				return testutil.Reply{Status: 503}
			}
			return reply(map[string]any{"_embedded": map[string]any{"episodes": []any{nrkEp("n2", 0), nrkEp("old", 30)}}})
		}
		return testutil.Reply{Status: 404}
	})
	kind := store.KindPodcast
	s.WriteSeries(&store.Series{ID: "show", Title: "Show", Link: "l", LastFetchedAt: start.Add(-30 * 24 * time.Hour),
		CatalogKind: &kind, Episodes: []store.Episode{{ID: "old", Title: "old", URL: "https://x/o.m3u8", ShareLink: "s",
			Date: start.Add(-30 * 24 * time.Hour), DurationInSeconds: 1}}})
	s.TouchLastFetched("show")
	s.ExpireSeries("show")

	got, _ := feeds.GetSeries("show")
	var progress struct {
		Cursor         string   `json:"cursor"`
		StopEpisodeIDs []string `json:"stopEpisodeIds"`
	}
	if !s.ReadState("refresh-progress:show", &progress) || progress.Cursor != "/radio/catalog/podcast/show/episodes?page=1&pageSize=50" ||
		fmt.Sprint(progress.StopEpisodeIDs) != "[old]" {
		t.Fatalf("checkpoint %+v", progress)
	}
	if len(got.Episodes) != 2 {
		t.Errorf("page 1's new episode must be kept: %d episodes", len(got.Episodes))
	}

	page2Fails = false
	clock.Pin(start.Add(time.Hour)) // past the refresh retry deadline
	fake.Requests = nil
	got, _ = feeds.GetSeries("show")
	if s.HasState("refresh-progress:show") || len(got.Episodes) != 3 {
		t.Errorf("resume did not finish: progress kept=%v, %d episodes", s.HasState("refresh-progress:show"), len(got.Episodes))
	}
	if fake.Count("/episodes?page=1") != 1 || fake.Count("/episodes?page=2") != 1 {
		t.Errorf("resume must revisit page 1 once for overlap, then page 2: %v", fake.Requests)
	}
}

// A saved refresh cursor that NRK answers 404 for (here: the show moved
// to the series catalog) is dropped, and the full fetch finds the show
// in the other catalog instead of retrying the dead cursor forever.
func TestDeadRefreshCursorFallsBackToFullFetch(t *testing.T) {
	feeds, s, _, _ := env(t, func(method, url string) testutil.Reply {
		if r, ok := manifestOK(url); ok {
			return r
		}
		switch {
		case strings.HasSuffix(url, "/radio/catalog/series/show/episodes"):
			return reply(map[string]any{"_embedded": map[string]any{"episodes": []any{nrkEp("new", 0)}}})
		case strings.HasSuffix(url, "/radio/catalog/series/show"):
			return reply(map[string]any{"type": "series", "series": map[string]any{"id": "show", "titles": map[string]any{"title": "Show"}}})
		}
		return testutil.Reply{Status: 404}
	})
	kind := store.KindPodcast
	s.WriteSeries(&store.Series{ID: "show", Title: "Show", Link: "l", LastFetchedAt: clock.Now(),
		CatalogKind: &kind, Episodes: []store.Episode{{ID: "old", Title: "old", URL: "https://x/o.m3u8", ShareLink: "s",
			Date: clock.Now().Add(-30 * 24 * time.Hour), DurationInSeconds: 1}}})
	s.WriteState("refresh-progress:show", map[string]any{"cursor": "/radio/catalog/podcast/show/episodes?page=3&pageSize=50",
		"stopEpisodeIds": []string{"old"}})
	s.ExpireSeries("show")

	got, err := feeds.GetSeries("show")
	if err != nil || got == nil || len(got.Episodes) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if s.HasState("refresh-progress:show") {
		t.Error("the dead cursor must be dropped")
	}
	if got.CatalogKind == nil || *got.CatalogKind != store.KindSeries {
		t.Errorf("catalog kind %v, want series", got.CatalogKind)
	}
}

// Simultaneous requests for the same feed share one NRK fetch, also when
// that fetch fails.
func TestConcurrentRequestsShareOneFetch(t *testing.T) {
	for _, outage := range []bool{false, true} {
		gate := make(chan struct{})
		feeds, _, fake, _ := env(t, func(method, url string) testutil.Reply {
			if outage {
				return testutil.Reply{Status: 503}
			}
			if r, ok := manifestOK(url); ok {
				return r
			}
			switch {
			case strings.HasSuffix(url, "/radio/catalog/podcast/delt/episodes"):
				return reply(map[string]any{"_embedded": map[string]any{"episodes": []any{nrkEp("a", 1)}}})
			case strings.HasSuffix(url, "/radio/catalog/podcast/delt"):
				return reply(map[string]any{"type": "podcast", "series": map[string]any{"id": "delt", "titles": map[string]any{"title": "Delt"}}})
			}
			return testutil.Reply{Status: 404}
		})
		fake.Gate = gate
		var wg sync.WaitGroup
		errs := make(chan error, 20)
		for range 20 {
			wg.Go(func() {
				_, err := feeds.GetSeries("delt")
				errs <- err
			})
		}
		time.Sleep(100 * time.Millisecond)
		close(gate)
		wg.Wait()
		close(errs)
		failures := 0
		for err := range errs {
			if err != nil {
				failures++
			}
		}
		// one listing + one series request per catalog family, for all 20
		// callers together; an outage also tries the radio-series family
		seriesFamily := 0
		if outage {
			seriesFamily = 2
		}
		if got := fake.Count("/radio/catalog/podcast/delt"); got != 2 {
			t.Errorf("outage=%v: %d podcast-family requests, want 2", outage, got)
		}
		if got := fake.Count("/radio/catalog/series/delt"); got != seriesFamily {
			t.Errorf("outage=%v: %d series-family requests, want %d", outage, got, seriesFamily)
		}
		if outage && failures != 20 || !outage && failures != 0 {
			t.Errorf("outage=%v: %d callers failed", outage, failures)
		}
	}
}

// Simultaneous chapter lookups share one NRK request, also when it fails.
func TestConcurrentChapterLookupsShareOneFetch(t *testing.T) {
	for _, status := range []int{200, 503} {
		s, _ := testutil.OpenStore(t)
		gate := make(chan struct{})
		fake := &testutil.FakeNRK{Gate: gate, Handle: func(string, string) testutil.Reply {
			if status != 200 {
				return testutil.Reply{Status: status}
			}
			return reply(map[string]any{"indexPoints": []any{map[string]any{"title": "Intro", "startPoint": "PT10S"}}})
		}}
		r := retry.New(s)
		chapters := feed.NewChapters(s, nrk.New(fake, r, stats.New()), r)
		var wg sync.WaitGroup
		results := make(chan int, 20)
		for range 20 {
			wg.Go(func() { results <- chapters.Get("serie", "l_ep").Status })
		}
		time.Sleep(100 * time.Millisecond)
		close(gate)
		wg.Wait()
		close(results)
		want := 1
		if status != 200 {
			want = 2 // a failure in the podcast family also tries the series family
		}
		if got := len(fake.Requests); got != want {
			t.Errorf("status %d: %d NRK requests for 20 callers, want %d", status, got, want)
		}
		for got := range results {
			if got != status {
				t.Errorf("status %d: a caller got %d", status, got)
			}
		}
	}
}

// A lookup that panics answers 503 and leaves nothing behind: the next
// request for the same chapters fetches again instead of waiting forever.
func TestChapterLookupPanicIsCleanedUp(t *testing.T) {
	s, _ := testutil.OpenStore(t)
	calls := 0
	fake := &testutil.FakeNRK{Handle: func(string, string) testutil.Reply {
		calls++
		if calls == 1 {
			panic("boom")
		}
		return reply(map[string]any{"indexPoints": []any{map[string]any{"title": "Intro", "startPoint": "PT10S"}}})
	}}
	r := retry.New(s)
	chapters := feed.NewChapters(s, nrk.New(fake, r, stats.New()), r)
	if got := chapters.Get("serie", "l_ep").Status; got != 503 {
		t.Fatalf("panicking lookup: %d, want 503", got)
	}
	done := make(chan int, 1)
	go func() { done <- chapters.Get("serie", "l_ep").Status }()
	select {
	case got := <-done:
		if got != 200 {
			t.Errorf("next lookup: %d, want 200", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the next lookup waited on the panicked one")
	}
}

// Chapters for a show stored as a radio series ask NRK's series catalog
// first, instead of spending a request on the podcast catalog.
func TestChapterLookupUsesStoredCatalog(t *testing.T) {
	s, _ := testutil.OpenStore(t)
	kind := store.KindSeries
	s.WriteSeries(&store.Series{ID: "radio", Title: "Radio", Link: "l", LastFetchedAt: clock.Now(), CatalogKind: &kind})
	fake := &testutil.FakeNRK{Handle: func(string, string) testutil.Reply {
		return reply(map[string]any{"indexPoints": []any{map[string]any{"title": "Intro", "startPoint": "PT10S"}}})
	}}
	r := retry.New(s)
	chapters := feed.NewChapters(s, nrk.New(fake, r, stats.New()), r)
	if got := chapters.Get("radio", "l_ep").Status; got != 200 {
		t.Fatalf("status %d", got)
	}
	if len(fake.Requests) != 1 || !strings.Contains(fake.Requests[0], "/radio/catalog/series/radio/episodes/l_ep") {
		t.Errorf("requests %v, want one to the series catalog", fake.Requests)
	}
}

// An umbrella show whose season listing fails is refreshed again once the
// retry deadline passes, not a full refresh interval later.
func TestUmbrellaSeasonFailureRetriesSoon(t *testing.T) {
	defer clock.Unpin()
	start := time.Now()
	clock.Pin(start)
	seasonFails := true
	feeds, _, _, _ := env(t, func(method, url string) testutil.Reply {
		if r, ok := manifestOK(url); ok {
			return r
		}
		switch {
		case strings.HasSuffix(url, "/radio/catalog/podcast/paraply/episodes"):
			// umbrella shows list episodes per season; this page only has to exist
			return reply(map[string]any{"_embedded": map[string]any{"episodes": []any{nrkEp("a1", 1)}}})
		case strings.HasSuffix(url, "/radio/catalog/podcast/paraply"):
			return reply(map[string]any{"type": "podcast", "seriesType": "umbrella",
				"series": map[string]any{"id": "paraply", "titles": map[string]any{"title": "Paraply"}},
				"_links": map[string]any{"seasons": []any{
					map[string]any{"href": "/radio/catalog/podcast/paraply/seasons/a"},
					map[string]any{"href": "/radio/catalog/podcast/paraply/seasons/b"}}}})
		case strings.HasSuffix(url, "/seasons/a"):
			return reply(map[string]any{"_embedded": map[string]any{"episodes": map[string]any{
				"_embedded": map[string]any{"episodes": []any{nrkEp("a1", 1)}}}}})
		case strings.HasSuffix(url, "/seasons/b"):
			if seasonFails {
				return testutil.Reply{Status: 503}
			}
			return reply(map[string]any{"_embedded": map[string]any{"episodes": map[string]any{
				"_embedded": map[string]any{"episodes": []any{nrkEp("b1", 2)}}}}})
		}
		return testutil.Reply{Status: 404}
	})
	got, err := feeds.GetSeries("paraply")
	if err != nil || got == nil || len(got.Episodes) != 1 {
		t.Fatalf("first fetch: %+v, %v", got, err)
	}
	seasonFails = false
	clock.Pin(start.Add(time.Hour)) // past the retry deadline, well inside any refresh interval
	got, _ = feeds.GetSeries("paraply")
	if len(got.Episodes) != 2 {
		t.Errorf("the failed season wasn't fetched again: %d episodes", len(got.Episodes))
	}
}
