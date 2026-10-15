package store_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/store"
	"github.com/matsbst/kringkasting/internal/testutil"
)

func ptr[T any](v T) *T { return &v }

func episode(id string, date time.Time) store.Episode {
	return store.Episode{ID: id, Title: "Episode " + id, Subtitle: ptr("Om " + id), URL: "https://podkast.nrk.no/" + id + ".mp3",
		ShareLink: "https://radio.nrk.no/podkast/test/l_" + id, Date: date, DurationInSeconds: 60}
}

func series(id string, episodes ...store.Episode) *store.Series {
	kind := store.KindPodcast
	return &store.Series{ID: id, Title: "Serie " + id, Link: "https://radio.nrk.no/podkast/" + id,
		ImageURL: "https://gfx.nrk.no/x", LastFetchedAt: clock.Now(), CatalogKind: &kind, Episodes: episodes}
}

var day = 24 * time.Hour

func TestSeriesRoundTripNewestFirst(t *testing.T) {
	s, _ := testutil.OpenStore(t)
	now := time.Now().Truncate(time.Millisecond).UTC()
	in := series("rt", episode("old", now.Add(-2*day)), episode("new", now), episode("mid", now.Add(-day)))
	if !s.WriteSeries(in) {
		t.Fatal("write failed")
	}
	got, err := s.ReadSeries("rt")
	if err != nil || got == nil {
		t.Fatalf("read: %v %v", got, err)
	}
	if got.Title != in.Title || *got.CatalogKind != store.KindPodcast || got.MetadataRefreshedAt == nil {
		t.Errorf("metadata not round-tripped: %+v", got)
	}
	var ids []string
	for _, e := range got.Episodes {
		ids = append(ids, e.ID)
	}
	if fmt.Sprint(ids) != "[new mid old]" {
		t.Errorf("order %v, want newest first", ids)
	}
	if !got.Episodes[0].Date.Equal(now) || *got.Episodes[0].Subtitle != "Om new" {
		t.Errorf("episode fields: %+v", got.Episodes[0])
	}
	if missing, _ := s.ReadSeries("finnes-ikke"); missing != nil {
		t.Error("unknown series should read as nil")
	}
}

// Writing a series again keeps episodes missing from the update: that is
// how the archive accumulates beyond NRK's page size. Known byte sizes
// survive an update without them.
func TestArchiveAccumulatesAndKeepsBytes(t *testing.T) {
	s, _ := testutil.OpenStore(t)
	now := time.Now()
	first := episode("a", now)
	first.Bytes = ptr(int64(1234))
	s.WriteSeries(series("acc", first))
	s.WriteSeries(series("acc", episode("a", now), episode("b", now.Add(time.Hour))))
	got, _ := s.ReadSeries("acc")
	if len(got.Episodes) != 2 {
		t.Fatalf("%d episodes, want 2", len(got.Episodes))
	}
	for _, e := range got.Episodes {
		if e.ID == "a" && (e.Bytes == nil || *e.Bytes != 1234) {
			t.Errorf("byte size lost: %v", e.Bytes)
		}
	}
}

func TestWriteSeriesRenewsLastFetched(t *testing.T) {
	s, _ := testutil.OpenStore(t)
	stale := series("renew")
	stale.LastFetchedAt = time.Now().Add(-10 * day)
	s.WriteSeries(stale)
	fresh := series("renew")
	s.WriteSeries(fresh)
	got, _ := s.ReadSeries("renew")
	if time.Since(got.LastFetchedAt) > time.Minute {
		t.Errorf("lastFetchedAt not renewed: %v", got.LastFetchedAt)
	}
}

func TestBacklogStateAndEpisodeIDs(t *testing.T) {
	s, _ := testutil.OpenStore(t)
	s.WriteSeries(series("bl"))
	s.SetBacklogState("bl", ptr("/radio/catalog/podcast/bl/episodes?page=2"), false)
	got, _ := s.ReadSeries("bl")
	if got.BacklogComplete || got.BacklogCursor == nil || *got.BacklogCursor != "/radio/catalog/podcast/bl/episodes?page=2" {
		t.Errorf("cursor not saved: %+v", got)
	}
	s.SetBacklogState("bl", nil, true)
	got, _ = s.ReadSeries("bl")
	if !got.BacklogComplete || got.BacklogCursor != nil {
		t.Errorf("completion not saved: %+v", got)
	}
	s.AddEpisodes("bl", []store.Episode{episode("x", time.Now()), episode("y", time.Now())})
	ids, _ := s.ReadEpisodeIDs("bl")
	if !ids["x"] || !ids["y"] || len(ids) != 2 {
		t.Errorf("ids %v", ids)
	}
}

func TestDataVersionBumpsOnContentWritesOnly(t *testing.T) {
	s, _ := testutil.OpenStore(t)
	v0 := s.DataVersion("dv")
	s.WriteSeries(series("dv"))
	v1 := s.DataVersion("dv")
	s.SetBacklogState("dv", nil, true)
	s.TouchLastFetched("dv")
	if s.DataVersion("dv") != v1 || v1 == v0 {
		t.Errorf("versions %d → %d → %d", v0, v1, s.DataVersion("dv"))
	}
	s.AddEpisodes("dv", []store.Episode{episode("z", time.Now())})
	if s.DataVersion("dv") == v1 {
		t.Error("adding episodes must bump the version")
	}
}

// Failed episodes back off 1, 2, 4 … days, capped at 30, and are blocked
// until their window passes.
func TestFailedEpisodeBackoff(t *testing.T) {
	s, _ := testutil.OpenStore(t)
	defer clock.Unpin()
	start := time.Now()
	for attempt, wantDays := range []int{1, 2, 4, 8, 16, 30, 30} {
		clock.Pin(start)
		s.RecordEpisodeFailure("fe", "ep")
		failed, _ := s.ListFailedEpisodes(10)
		if len(failed) != 1 || failed[0].Attempts != attempt+1 {
			t.Fatalf("attempt %d: %+v", attempt+1, failed)
		}
		if got := failed[0].NextRetryAt.Sub(start).Round(time.Hour); got != time.Duration(wantDays)*day {
			t.Errorf("attempt %d: backoff %v, want %d days", attempt+1, got, wantDays)
		}
	}
	clock.Pin(start)
	if blocked, _ := s.ReadBlockedEpisodeIDs("fe"); !blocked["ep"] {
		t.Error("episode should be blocked inside its window")
	}
	clock.Pin(start.Add(31 * day))
	if blocked, _ := s.ReadBlockedEpisodeIDs("fe"); blocked["ep"] {
		t.Error("episode should be retried after its window")
	}
}

func TestGarbageCollection(t *testing.T) {
	s, _ := testutil.OpenStore(t)
	old := series("gammel", episode("e", time.Now()))
	old.LastFetchedAt = time.Now().Add(-100 * day)
	s.WriteSeries(old)
	s.WriteSeries(series("fersk"))
	if n := s.DeleteStaleSeries(90 * day); n != 1 {
		t.Errorf("deleted %d, want 1", n)
	}
	if s.HasSeries("gammel") || !s.HasSeries("fersk") {
		t.Error("wrong series collected")
	}
	if ids, _ := s.ReadEpisodeIDs("gammel"); len(ids) != 0 {
		t.Error("episodes of a collected series must go too")
	}
}

func TestStreamOnlySeries(t *testing.T) {
	s, _ := testutil.OpenStore(t)
	hls := func(id string) store.Episode {
		e := episode(id, time.Now())
		e.URL = "https://nrk-od.akamaized.net/" + id + "/master.m3u8"
		return e
	}
	s.WriteSeries(series("hls", hls("a"), hls("b")))
	s.WriteSeries(series("mixed", hls("c"), episode("d", time.Now())))
	s.WriteSeries(series("mp3", episode("e", time.Now())))
	got := s.StreamOnlySeriesIDs([]string{"hls", "mixed", "mp3", "unknown"})
	if !got["hls"] || got["mixed"] || got["mp3"] || got["unknown"] || len(got) != 1 {
		t.Errorf("stream-only %v", got)
	}
}

// Feeds link chapters by NRK's hash id; the chapters endpoint needs NRK's
// episodeId, which every stored share link ends with (season episodes are
// stored under the season feed id).
func TestReadNrkEpisodeID(t *testing.T) {
	s, _ := testutil.OpenStore(t)
	s.WriteSeries(series("show", episode("hash1", time.Now())))
	s.WriteSeries(series("show/sesong/s1", episode("hash2", time.Now())))
	s.WriteSeries(series("showx", episode("hash3", time.Now())))
	for id, want := range map[string]string{"hash1": "l_hash1", "hash2": "l_hash2", "hash3": "", "nope": ""} {
		if got := s.ReadNrkEpisodeID("show", id); got != want {
			t.Errorf("ReadNrkEpisodeID(show, %s) = %q, want %q", id, got, want)
		}
	}
}

// Durable state survives reopening the database (a process restart).
func TestStateSurvivesReopen(t *testing.T) {
	s, path := testutil.OpenStore(t)
	s.WriteState("retry:x", map[string]int64{"attempts": 2, "nextRetryAt": 99})
	s.Close()
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var got map[string]int64
	if !reopened.ReadState("retry:x", &got) || got["attempts"] != 2 || got["nextRetryAt"] != 99 {
		t.Errorf("state after reopen: %v", got)
	}
}
