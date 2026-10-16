package nrk

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/retry"
	"github.com/matsbst/kringkasting/internal/stats"
	"github.com/matsbst/kringkasting/internal/store"
	"github.com/matsbst/kringkasting/internal/testutil"
)

func jsonReply(v any) testutil.Reply {
	b, _ := json.Marshal(v)
	return testutil.Reply{Status: 200, Body: string(b)}
}

func nrkEpisode(id string) map[string]any {
	return map[string]any{
		"id": id, "episodeId": "l_" + id, "titles": map[string]any{"title": "T " + id, "subtitle": "S " + id},
		"date": "2026-10-01T06:00:00+02:00", "durationInSeconds": 60,
		"_links": map[string]any{"share": map[string]any{"href": "https://radio.nrk.no/podkast/x/l_" + id}},
	}
}

func newClient(t *testing.T, handle func(method, url string) testutil.Reply) (*Client, *testutil.FakeNRK) {
	t.Helper()
	s, _ := testutil.OpenStore(t)
	fake := &testutil.FakeNRK{Handle: handle}
	return New(fake, retry.New(s), stats.New()), fake
}

// a podcast with episodes a (MP3), b (no manifest: gone) and c (playable=null: gone)
func podcastHandler(method, url string) testutil.Reply {
	switch {
	case method == "HEAD":
		return testutil.Reply{Status: 200, Headers: map[string]string{"content-length": "4321"}}
	case strings.HasSuffix(url, "/radio/catalog/podcast/pod/episodes"):
		return jsonReply(map[string]any{"_embedded": map[string]any{"episodes": []any{nrkEpisode("a"), nrkEpisode("b"), nrkEpisode("c")}}})
	case strings.HasSuffix(url, "/radio/catalog/podcast/pod"):
		return jsonReply(map[string]any{"seriesType": "standard", "type": "podcast",
			"series": map[string]any{"id": "pod", "titles": map[string]any{"title": "Pod"},
				"squareImage": []any{map[string]any{"url": "small"}, map[string]any{"url": "large"}}}})
	case strings.HasSuffix(url, "/playback/manifest/podcast/l_a"):
		return jsonReply(map[string]any{"playable": map[string]any{"assets": []any{
			map[string]any{"url": "https://x/a.m3u8"}, map[string]any{"url": "https://podkast.nrk.no/a.mp3"}}}})
	case strings.HasSuffix(url, "/playback/manifest/podcast/l_c"):
		return jsonReply(map[string]any{"playable": nil})
	}
	return testutil.Reply{Status: 404, Body: `"not found"`}
}

func TestGetSeriesParsesAndDropsUnavailable(t *testing.T) {
	c, fake := newClient(t, podcastHandler)
	var failed []string
	series, outcome := c.GetSeries("pod", FetchOptions{OnEpisodeFailure: func(id string) { failed = append(failed, id) }})
	if outcome != Found || series.Title != "Pod" || series.ImageURL != "large" || *series.CatalogKind != "podcast" {
		t.Fatalf("series %+v, outcome %v", series, outcome)
	}
	if len(series.Episodes) != 1 || series.Episodes[0].URL != "https://podkast.nrk.no/a.mp3" || *series.Episodes[0].Bytes != 4321 {
		t.Errorf("episodes %+v (progressive MP3 preferred, size from HEAD)", series.Episodes)
	}
	if fmt.Sprint(failed) != "[b c]" {
		t.Errorf("failures %v", failed)
	}
	if fake.Count("/catalog/series/") != 0 {
		t.Error("the podcast family answered; the series family must not be tried")
	}

	// the skip set spares manifest lookups for known episodes
	c2, fake2 := newClient(t, podcastHandler)
	c2.GetSeries("pod", FetchOptions{SkipEpisodeIDs: map[string]bool{"a": true, "b": true, "c": true}})
	if fake2.Count("/playback/manifest/") != 0 {
		t.Error("skipped episodes were looked up")
	}
}

func TestNotFoundVersusOutage(t *testing.T) {
	c, _ := newClient(t, func(string, string) testutil.Reply { return testutil.Reply{Status: 404} })
	if _, outcome := c.GetSeries("nope", FetchOptions{}); outcome != NotFound {
		t.Errorf("404 everywhere: %v, want NotFound", outcome)
	}
	if _, outcome := c.GetSeason("nope", "s", FetchOptions{}); outcome != NotFound {
		t.Errorf("season 404: %v, want NotFound", outcome)
	}
	c, _ = newClient(t, func(string, string) testutil.Reply { return testutil.Reply{Status: 503} })
	if _, outcome := c.GetSeries("down", FetchOptions{}); outcome != Unavailable {
		t.Errorf("503: %v, want Unavailable", outcome)
	}
}

func TestEpisodePagePaginationAndTransientFailures(t *testing.T) {
	c, _ := newClient(t, func(method, url string) testutil.Reply {
		switch {
		case strings.Contains(url, "/radio/catalog/podcast/arkiv/episodes?page=1"):
			return jsonReply(map[string]any{"_embedded": map[string]any{"episodes": []any{nrkEpisode("p1")}},
				"_links": map[string]any{"next": map[string]any{"href": "/radio/catalog/podcast/arkiv/episodes?page=2&pageSize=50"}}})
		case strings.Contains(url, "/radio/catalog/podcast/arkiv/episodes?page=2"):
			return jsonReply(map[string]any{"_embedded": map[string]any{"episodes": []any{nrkEpisode("p2"), nrkEpisode("flaky")}}})
		case strings.HasSuffix(url, "/l_flaky"):
			return testutil.Reply{Status: 503}
		case strings.Contains(url, "/playback/manifest/"):
			return jsonReply(map[string]any{"playable": map[string]any{"assets": []any{map[string]any{"url": "https://x/e.mp3"}}}})
		case method == "HEAD":
			return testutil.Reply{Status: 200}
		}
		return testutil.Reply{Status: 404}
	})
	first := c.GetEpisodePage("arkiv", "", map[string]bool{}, nil, nil)
	if first == nil || len(first.Episodes) != 1 || first.NextHref != "/radio/catalog/podcast/arkiv/episodes?page=2&pageSize=50" {
		t.Fatalf("first page %+v", first)
	}
	second := c.GetEpisodePage("arkiv", first.NextHref, map[string]bool{}, nil, nil)
	if second == nil || len(second.Episodes) != 1 || second.TransientFailures != 1 || second.NextHref != "" {
		t.Errorf("second page %+v", second)
	}
}

// A manifest NRK refuses (4xx) fails at once; one that keeps failing
// transiently fails after five real attempts, so its page can move on.
// Waiting for a retry deadline doesn't count as an attempt.
func TestManifestFailuresGiveUp(t *testing.T) {
	defer clock.Unpin()
	now := time.Now()
	clock.Pin(now)
	c, fake := newClient(t, func(method, url string) testutil.Reply {
		switch {
		case strings.HasSuffix(url, "/l_forbidden"):
			return testutil.Reply{Status: 403}
		case strings.HasSuffix(url, "/l_flaky"):
			return testutil.Reply{Status: 500}
		}
		return testutil.Reply{Status: 404}
	})
	resolve := func(id string) mediaResult {
		_, result := c.episodeWithDownloadLink(Episode{ID: id, EpisodeID: "l_" + id}, "podcast")
		return result
	}
	if resolve("forbidden") != mediaGone {
		t.Error("a 403 manifest must count as gone")
	}
	for attempt := 1; attempt < maxManifestFailures; attempt++ {
		if resolve("flaky") != mediaTransient {
			t.Fatalf("attempt %d: want transient", attempt)
		}
		// inside the URL's retry deadline: no request, no attempt counted
		before := len(fake.Requests)
		if resolve("flaky") != mediaTransient || len(fake.Requests) != before {
			t.Fatal("a pending deadline must not cost a request or an attempt")
		}
		now = now.Add(25 * time.Hour)
		clock.Pin(now)
	}
	if resolve("flaky") != mediaGone {
		t.Errorf("attempt %d: want gone", maxManifestFailures)
	}
}

// The first page of a cheap refresh answering 404 is reported as gone
// (dead cursor or moved series), unlike an outage.
func TestNewEpisodesFirstPageGone(t *testing.T) {
	status := 404
	c, _ := newClient(t, func(string, string) testutil.Reply { return testutil.Reply{Status: status} })
	if r := c.GetNewEpisodes("show", store.KindPodcast, FetchOptions{}); r == nil || !r.Gone {
		t.Errorf("404: %+v, want gone", r)
	}
	status = 503
	if r := c.GetNewEpisodes("other", store.KindPodcast, FetchOptions{}); r != nil {
		t.Errorf("503: %+v, want nil", r)
	}
}

// An empty page that ends the listing mid-crawl is retried, not taken as
// the end of the archive.
func TestEmptyArchivePageIsNotTheEnd(t *testing.T) {
	c, _ := newClient(t, func(method, url string) testutil.Reply {
		if strings.Contains(url, "/episodes?page=7") {
			return jsonReply(map[string]any{"_embedded": map[string]any{"episodes": []any{}}})
		}
		return testutil.Reply{Status: 404}
	})
	if page := c.GetEpisodePage("arkiv", "/radio/catalog/podcast/arkiv/episodes?page=7&pageSize=50", map[string]bool{}, nil, nil); page != nil {
		t.Errorf("empty mid-crawl page: %+v, want nil", page)
	}
}

func TestSeasonPagination(t *testing.T) {
	c, _ := newClient(t, func(method, url string) testutil.Reply {
		switch {
		case strings.HasSuffix(url, "/radio/catalog/podcast/paraply/seasons/s1"):
			return jsonReply(map[string]any{"titles": map[string]any{"title": "Sesong"}, "_embedded": map[string]any{
				"episodes": map[string]any{"_embedded": map[string]any{"episodes": []any{nrkEpisode("e1")}},
					"_links": map[string]any{"next": map[string]any{"href": "/radio/catalog/podcast/paraply/seasons/s1/episodes?page=2"}}}}})
		case strings.Contains(url, "/seasons/s1/episodes?page=2"):
			return jsonReply(map[string]any{"_embedded": map[string]any{"episodes": []any{nrkEpisode("e2")}}})
		case strings.Contains(url, "/playback/manifest/"):
			return jsonReply(map[string]any{"playable": map[string]any{"assets": []any{map[string]any{"url": "https://x/e.mp3"}}}})
		case method == "HEAD":
			return testutil.Reply{Status: 200}
		}
		return testutil.Reply{Status: 404}
	})
	season, outcome := c.GetSeason("paraply", "s1", FetchOptions{})
	if outcome != Found || season.ID != "paraply/sesong/s1" || season.Link != "https://radio.nrk.no/podkast/paraply/sesong/s1" ||
		len(season.Episodes) != 2 {
		t.Errorf("season %+v %v", season, outcome)
	}
}

func TestSearch(t *testing.T) {
	var queries []string
	c, fake := newClient(t, func(method, url string) testutil.Reply {
		queries = append(queries, url)
		switch {
		case strings.Contains(url, "q=down"):
			return testutil.Reply{Status: 500}
		case strings.Contains(url, "q=ingenting"):
			return jsonReply(map[string]any{"results": map[string]any{}})
		}
		return jsonReply(map[string]any{"results": map[string]any{"series": map[string]any{"results": []any{
			map[string]any{"seriesId": "abels_taarn", "type": "podcast", "title": "Abels tårn"}}}}})
	})
	if got := c.Search(" Abels tårn & co "); len(got) != 1 || got[0].SeriesID != "abels_taarn" {
		t.Errorf("results %+v", got)
	}
	if !strings.HasSuffix(queries[0], "?q=Abels%20t%C3%A5rn%20%26%20co") {
		t.Errorf("query not encoded like encodeURIComponent: %s", queries[0])
	}
	c.Search("abels TÅRN & co")
	if fake.Count("search?q=") != 1 {
		t.Error("repeated queries are served from the micro-cache (case-insensitive)")
	}
	if got := c.Search("ingenting"); got == nil || len(got) != 0 {
		t.Errorf("no matches must be an empty slice, got %v", got)
	}
	if c.Search("down") != nil || c.Search("   ") != nil {
		t.Error("outages and empty queries must be nil")
	}
	c.Search(strings.Repeat("ø", 150))
	if last := queries[len(queries)-1]; strings.Count(last, "%C3%B8") != 100 {
		t.Errorf("query must be clamped to 100 characters, got %d", strings.Count(last, "%C3%B8"))
	}
}

// 429/5xx with Retry-After defer the URL and its whole origin.
func TestRetryAfterBlocksOrigin(t *testing.T) {
	c, fake := newClient(t, func(string, string) testutil.Reply {
		return testutil.Reply{Status: 429, Headers: map[string]string{"retry-after": "120"}}
	})
	var v any
	if status, _ := c.GetJSON(API+"/a", &v); status != 429 {
		t.Fatalf("status %d", status)
	}
	if status, _ := c.GetJSON(API+"/other", &v); status != 503 || len(fake.Requests) != 1 {
		t.Errorf("origin not blocked: status %d after %d requests", status, len(fake.Requests))
	}
}

// A burst of 429s pauses the origin only as long as NRK asked: the
// escalating backoff applies to each URL, not to all of NRK.
func TestRetryAfterBurstDoesNotEscalateOrigin(t *testing.T) {
	s, _ := testutil.OpenStore(t)
	r := retry.New(s)
	gate := make(chan struct{})
	fake := &testutil.FakeNRK{Gate: gate, Handle: func(string, string) testutil.Reply {
		return testutil.Reply{Status: 429, Headers: map[string]string{"retry-after": "30"}}
	}}
	c := New(fake, r, stats.New())
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			var v any
			c.GetJSON(fmt.Sprintf("%s/burst%d", API, i), &v)
		})
	}
	time.Sleep(200 * time.Millisecond)
	close(gate)
	wg.Wait()
	if len(fake.Requests) != 10 {
		t.Fatalf("%d requests, want 10 in flight together", len(fake.Requests))
	}
	wait := time.Duration(r.At("origin:"+originOf(API))-time.Now().UnixMilli()) * time.Millisecond
	if wait <= 0 || wait > 31*time.Second {
		t.Errorf("origin paused for %v, want about 30s", wait)
	}
}

// At most 12 NRK requests run at once across all operations, and the
// queue behind them is capped at 64.
func TestGlobalConcurrencyCap(t *testing.T) {
	gate := make(chan struct{})
	c, fake := newClient(t, func(string, string) testutil.Reply { return testutil.Reply{Status: 200, Body: "{}"} })
	fake.Gate = gate
	var wg sync.WaitGroup
	var failed atomic.Int32
	for i := range 12 + 64 + 5 {
		wg.Go(func() {
			var v any
			if status, _ := c.GetJSON(fmt.Sprintf("%s/r%d", API, i), &v); status == 0 {
				failed.Add(1)
			}
		})
	}
	time.Sleep(200 * time.Millisecond)
	close(gate)
	wg.Wait()
	if max := fake.MaxInFlight.Load(); max != maxConcurrentUpstream {
		t.Errorf("max in flight %d, want %d", max, maxConcurrentUpstream)
	}
	if failed.Load() != 5 {
		t.Errorf("%d requests failed fast, want 5 beyond the queue cap", failed.Load())
	}
}

func TestMapConcurrent(t *testing.T) {
	var inFlight, peak atomic.Int32
	items := []int{5, 3, 8, 1, 9, 2, 7}
	got := mapConcurrent(items, 3, func(n int) int {
		current := inFlight.Add(1)
		for {
			p := peak.Load()
			if current <= p || peak.CompareAndSwap(p, current) {
				break
			}
		}
		time.Sleep(time.Duration(n) * time.Millisecond)
		inFlight.Add(-1)
		return n * 10
	})
	if fmt.Sprint(got) != "[50 30 80 10 90 20 70]" || peak.Load() > 3 {
		t.Errorf("results %v, peak %d", got, peak.Load())
	}
}

func TestEncodeURIComponent(t *testing.T) {
	for in, want := range map[string]string{
		"a b": "a%20b", "æøå": "%C3%A6%C3%B8%C3%A5", "-_.!~*'()": "-_.!~*'()", "a/b?c=d&e": "a%2Fb%3Fc%3Dd%26e",
	} {
		if got := EncodeURIComponent(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

// NRK load limits must keep their values (docs/nrk-api-usage.md).
func TestLoadLimits(t *testing.T) {
	if maxConcurrentUpstream != 12 || maxQueuedUpstream != 64 || fetchConcurrency != 6 || backlogConcurrency != 3 ||
		requestTimeout != 15*time.Second || searchCacheTTL != 5*time.Minute {
		t.Error("NRK load limits changed")
	}
}

// An undecodable 200 counts once, as a failure; an error page's length is
// never taken as a file size.
func TestUpstreamCountsAndSizes(t *testing.T) {
	s, _ := testutil.OpenStore(t)
	counters := stats.New()
	fake := &testutil.FakeNRK{Handle: func(method, url string) testutil.Reply {
		if method == "HEAD" {
			return testutil.Reply{Status: 404, Headers: map[string]string{"content-length": "512"}}
		}
		return testutil.Reply{Status: 200, Body: "<html>not json"}
	}}
	c := New(fake, retry.New(s), counters)
	var v any
	if status, ok := c.GetJSON(API+"/broken", &v); status != 0 || ok {
		t.Errorf("undecodable body: %d %v", status, ok)
	}
	if snap := counters.Snapshot(); snap.UpstreamTotal != 1 || snap.UpstreamErrors != 1 {
		t.Errorf("counted %d requests, %d errors; want 1 and 1", snap.UpstreamTotal, snap.UpstreamErrors)
	}
	if status, size := c.up.head("https://podkast.nrk.no/missing.mp3"); status != 404 || size != nil {
		t.Errorf("HEAD 404: status %d, size %v; want no size", status, size)
	}
}

// Stop cancels requests in flight without recording NRK failures or
// backoff, and fails later ones at once.
func TestStopCancelsRequests(t *testing.T) {
	s, _ := testutil.OpenStore(t)
	r := retry.New(s)
	counters := stats.New()
	gate := make(chan struct{})
	fake := &testutil.FakeNRK{Gate: gate, Handle: func(string, string) testutil.Reply { return jsonReply(map[string]any{}) }}
	c := New(&cancellable{fake}, r, counters)
	done := make(chan int, 1)
	go func() {
		var v any
		status, _ := c.GetJSON(API+"/slow", &v)
		done <- status
	}()
	time.Sleep(100 * time.Millisecond)
	c.Stop()
	select {
	case status := <-done:
		if status != 0 {
			t.Errorf("cancelled request: status %d", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop didn't cancel the request in flight")
	}
	close(gate)
	if !r.Can("GET:"+API+"/slow") || counters.Snapshot().UpstreamTotal != 0 {
		t.Error("a cancelled request must not count as an NRK failure")
	}
}

// cancellable makes a gated fake give up when the request's context ends,
// as a real transport does.
type cancellable struct{ fake *testutil.FakeNRK }

func (c *cancellable) RoundTrip(req *http.Request) (*http.Response, error) {
	type result struct {
		resp *http.Response
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := c.fake.RoundTrip(req)
		ch <- result{resp, err}
	}()
	select {
	case r := <-ch:
		return r.resp, r.err
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
}

// Identical searches arriving together share one NRK request.
func TestConcurrentSearchesShareOneRequest(t *testing.T) {
	gate := make(chan struct{})
	c, fake := newClient(t, func(string, string) testutil.Reply {
		return jsonReply(map[string]any{"results": map[string]any{"series": map[string]any{"results": []any{}}}})
	})
	fake.Gate = gate
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() { c.Search("Dagsnytt") })
	}
	time.Sleep(100 * time.Millisecond)
	close(gate)
	wg.Wait()
	if len(fake.Requests) != 1 {
		t.Errorf("%d NRK requests for 10 identical searches, want 1", len(fake.Requests))
	}
}
