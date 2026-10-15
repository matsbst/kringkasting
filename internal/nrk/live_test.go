//go:build live

// Live checks against NRK's real API, catching changes in NRK's responses
// that recorded fixtures can't. Opt-in, never part of CI:
//
//	go test -tags live ./internal/nrk/
package nrk

import (
	"net/http"
	"testing"

	"github.com/matsbst/kringkasting/internal/retry"
	"github.com/matsbst/kringkasting/internal/stats"
	"github.com/matsbst/kringkasting/internal/testutil"
)

func liveClient(t *testing.T) *Client {
	s, _ := testutil.OpenStore(t)
	return New(http.DefaultTransport, retry.New(s), stats.New())
}

func TestLiveSearch(t *testing.T) {
	c := liveClient(t)
	found := false
	for _, r := range c.Search("trygd") {
		found = found || r.SeriesID == "trygdekontoret"
	}
	if !found {
		t.Error(`searching "trygd" should find trygdekontoret`)
	}
	if c.Search("") != nil {
		t.Error("empty query must be nil")
	}
}

func TestLiveSeries(t *testing.T) {
	c := liveClient(t)
	series, outcome := c.GetSeries("trygdekontoret", FetchOptions{})
	if outcome != Found || series.Title != "Trygdekontoret" || len(series.Episodes) == 0 {
		t.Fatalf("trygdekontoret: %v, %+v", outcome, series)
	}
	for _, e := range series.Episodes {
		if e.ID == "" || e.URL == "" || e.ShareLink == "" || e.Date.IsZero() || e.DurationInSeconds <= 0 {
			t.Errorf("incomplete episode %+v", e)
		}
	}
	if _, outcome := c.GetSeries("trygd", FetchOptions{}); outcome != NotFound {
		t.Errorf(`"trygd" is not a series: %v`, outcome)
	}
	umbrella, outcome := c.GetSeries("seriesnakk", FetchOptions{MetadataOnly: true})
	if outcome != Found || !umbrella.IsUmbrella {
		t.Errorf("seriesnakk should be an umbrella: %v %+v", outcome, umbrella)
	}
}

func TestLiveChapters(t *testing.T) {
	c := liveClient(t)
	page := c.GetEpisodePage("sentralbordet", "", map[string]bool{}, nil, nil)
	if page == nil || len(page.Episodes) == 0 {
		t.Fatal("no sentralbordet episodes")
	}
	share := page.Episodes[0].ShareLink
	episodeID := share[len(share)-len("l_00000000-0000-0000-0000-000000000000"):]
	detail, status := c.GetEpisodeResult("sentralbordet", episodeID)
	if status != 200 || detail == nil || len(detail.IndexPoints) == 0 {
		t.Errorf("chapters for %s: status %d, %+v", episodeID, status, detail)
	}
}
