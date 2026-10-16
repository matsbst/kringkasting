//go:build live

// Live checks against NRK's real API, catching changes in NRK's responses
// that recorded fixtures can't. Opt-in, never part of CI:
//
//	go test -tags live ./internal/nrk/
package nrk

import (
	"net/http"
	"strings"
	"testing"

	"github.com/matsbst/kringkasting/internal/nrklink"
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
	detail, status := c.GetEpisodeResult("sentralbordet", episodeID, nil)
	if status != 200 || detail == nil || len(detail.IndexPoints) == 0 {
		t.Errorf("chapters for %s: status %d, %+v", episodeID, status, detail)
	}
}

func TestLiveResolveLinks(t *testing.T) {
	c := liveClient(t)
	for _, tc := range []struct {
		url  string
		want []string // feed ids, in order
	}{
		{"https://radio.nrk.no/podkast/sentralbordet", []string{"sentralbordet"}},
		{"https://radio.nrk.no/podkast/musikkhistorier/sesong/miles-davis--bak-mytene",
			[]string{"musikkhistorier/sesong/miles-davis--bak-mytene", "musikkhistorier"}},
		{"https://radio.nrk.no/serie/radioteatret-nrk-p1pluss", []string{"radioteatret-nrk-p1pluss"}},
	} {
		link, result := nrklink.Parse(tc.url)
		if result != nrklink.Supported {
			t.Fatalf("%s: not supported", tc.url)
		}
		resolution, outcome := c.Resolve(link)
		var got []string
		for _, show := range resolution.Shows {
			id := show.SeriesID
			if show.SeasonID != "" {
				id += "/sesong/" + show.SeasonID
			}
			got = append(got, id)
			if show.Title == "" || show.ImageURL == "" {
				t.Errorf("%s: incomplete show %+v", tc.url, show)
			}
		}
		if outcome != Found || strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("%s: %v %v, want %v", tc.url, outcome, got, tc.want)
		}
	}

	// an episode of an umbrella podcast's season resolves to that season
	page := c.GetEpisodePage("musikkhistorier/sesong/miles-davis--bak-mytene", "", map[string]bool{}, nil, nil)
	if page == nil || len(page.Episodes) == 0 {
		t.Fatal("no Miles Davis episodes")
	}
	link, _ := nrklink.Parse(page.Episodes[0].ShareLink)
	resolution, _ := c.Resolve(link)
	if len(resolution.Shows) != 2 || resolution.Shows[0].SeasonID != "miles-davis--bak-mytene" || !resolution.Episode {
		t.Errorf("episode share link %s resolved to %+v", page.Episodes[0].ShareLink, resolution)
	}
}

// NRK's video versions of podcast episodes: found, playable, the same
// length as the audio, and the playlist URL answers.
func TestLiveClipVideo(t *testing.T) {
	c := liveClient(t)
	var listing episodesPage
	if _, ok := c.up.getJSON(API+"/radio/catalog/podcast/sentralbordet/episodes?page=1&pageSize=5", &listing); !ok {
		t.Fatal("no sentralbordet listing")
	}
	checked := 0
	for _, e := range listing.Embedded.Episodes {
		if e.ClipID == "" {
			continue
		}
		video, outcome := c.VideoForClip(e.ClipID)
		if outcome != Found {
			t.Errorf("%s (clip %s): %v", e.EpisodeID, e.ClipID, outcome)
			continue
		}
		if diff := video.DurationSeconds - float64(e.DurationInSeconds); diff > 60 || diff < -60 {
			t.Errorf("%s: video %.0f s, audio %d s", e.EpisodeID, video.DurationSeconds, e.DurationInSeconds)
		}
		resp, err := http.Head(video.URL)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Errorf("%s: playlist %s: %v %v", e.EpisodeID, video.URL, resp, err)
		}
		checked++
	}
	if checked == 0 {
		t.Error("no sentralbordet episode with a clip reference")
	}
	t.Logf("checked %d episodes with video", checked)
}
