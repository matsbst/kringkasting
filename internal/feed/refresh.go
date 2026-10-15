package feed

import (
	"errors"
	"log"
	"math"
	"sync"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/feedid"
	"github.com/matsbst/kringkasting/internal/nrk"
	"github.com/matsbst/kringkasting/internal/retry"
	"github.com/matsbst/kringkasting/internal/store"
)

const (
	syncIntervalHours = 3
	// metadataTTL is how often title/artwork is re-fetched from NRK.
	metadataTTL = 7 * 24 * time.Hour
	// notFoundTTL: ids NRK doesn't know are remembered a while, so
	// garbage requests are cheap.
	notFoundTTL = 10 * time.Minute
	// cheap refreshes treat the newest 50 stored episodes as the boundary
	stopBoundary = 50
)

// ErrUpstream means NRK was unavailable for a feed we have nothing stored
// for (the route answers 502).
var ErrUpstream = errors.New("NRK unavailable")

// Enqueuer admits a feed to the background archive crawl.
type Enqueuer interface{ Enqueue(feedID string) }

// Feeds decides when a feed is (re)fetched from NRK and returns it.
type Feeds struct {
	store   *store.Store
	nrk     *nrk.Client
	retry   *retry.Retry
	crawler Enqueuer
	// Videos, when set, gets a round after each fetch or refresh (not on
	// plain feed polls, which never reach fetchSeries).
	Videos Enqueuer

	mu            sync.Mutex
	notFoundUntil map[string]time.Time
	inFlight      map[string]*call
}

type call struct {
	done   chan struct{}
	series *store.Series
	err    error
}

func NewFeeds(s *store.Store, client *nrk.Client, r *retry.Retry, crawler Enqueuer) *Feeds {
	return &Feeds{store: s, nrk: client, retry: r, crawler: crawler,
		notFoundUntil: map[string]time.Time{}, inFlight: map[string]*call{}}
}

type progress struct {
	Cursor         string   `json:"cursor"`
	StopEpisodeIDs []string `json:"stopEpisodeIds"`
}

// fetchFromNrk: season feeds resolve via NRK's season endpoint, everything
// else via the series catalog.
func (f *Feeds) fetchFromNrk(feedID string, opts nrk.FetchOptions) (*store.Series, nrk.Outcome) {
	ref := feedid.Parse(feedID)
	if ref.SeasonID == "" {
		return f.nrk.GetSeries(feedID, opts)
	}
	return f.nrk.GetSeason(ref.SeriesID, ref.SeasonID, opts)
}

func (f *Feeds) onFailure(feedID string) nrk.OnEpisodeFailure {
	return func(episodeID string) { f.store.RecordEpisodeFailure(feedID, episodeID) }
}

// onClipIDs records clip references of listed episodes already stored.
func (f *Feeds) onClipIDs(feedID string) func(map[string]string) {
	return func(clipIDs map[string]string) { f.store.SetClipIDs(feedID, clipIDs) }
}

func (f *Feeds) initialFetch(id string) (*store.Series, error) {
	series, outcome := f.fetchFromNrk(id, nrk.FetchOptions{OnEpisodeFailure: f.onFailure(id), OnClipIDs: f.onClipIDs(id)})
	switch outcome {
	case nrk.Unavailable:
		// surface the outage (502) instead of letting the negative cache
		// turn a hiccup into ten minutes of 404s
		f.retry.Defer("refresh:"+id, 0)
		return nil, ErrUpstream
	case nrk.NotFound:
		f.retry.Clear("refresh:" + id)
		return nil, nil
	}
	if !f.store.WriteSeries(series) {
		// still serve the fetched data; only persisting failed
		log.Printf("Failed to store series %s", id)
		return series, nil
	}
	f.retry.Clear("refresh:" + id)
	return f.readOr(id, series), nil
}

func (f *Feeds) readOr(id string, fallback *store.Series) *store.Series {
	if stored, err := f.store.ReadSeries(id); err == nil && stored != nil {
		return stored
	}
	return fallback
}

func (f *Feeds) updateFetch(existing *store.Series) *store.Series {
	id := existing.ID
	// incremental: only NEW episodes get manifest lookups, and episodes
	// under failure backoff are not re-checked every refresh
	skip := map[string]bool{}
	for _, e := range existing.Episodes {
		skip[e.ID] = true
	}
	blocked, _ := f.store.ReadBlockedEpisodeIDs(id)
	for b := range blocked {
		skip[b] = true
	}
	onFailure := f.onFailure(id)

	// metadata (title/artwork) changes rarely: most refreshes only need the
	// episode listing (one request instead of two). Umbrella shows and
	// unknown catalog kinds take the full path; season feeds do too (their
	// listing is oldest-first, so the newest-first early stop can't apply,
	// and a full season walk is small).
	refreshedAt := time.UnixMilli(0) // never: always stale
	if existing.MetadataRefreshedAt != nil {
		refreshedAt = *existing.MetadataRefreshedAt
	}
	metadataAge := clock.Now().Sub(refreshedAt)
	cheap := !existing.IsUmbrella && feedid.Parse(id).SeasonID == "" && existing.CatalogKind != nil

	if cheap {
		progressKey := "refresh-progress:" + id
		var saved progress
		hasProgress := f.store.ReadState(progressKey, &saved)
		if !hasProgress && metadataAge >= metadataTTL {
			metadata, outcome := f.nrk.GetSeries(id, nrk.FetchOptions{CatalogKind: existing.CatalogKind, MetadataOnly: true})
			if outcome != nrk.Found {
				f.retry.Defer("refresh:"+id, 0)
				return existing
			}
			metadata.LastFetchedAt = existing.LastFetchedAt
			if !f.store.WriteSeries(metadata) {
				f.retry.Defer("refresh:"+id, 0)
				return existing
			}
		}
		// keep the boundary from BEFORE this batch, so newly persisted
		// episodes can't stop a resumed refresh on its overlap page
		stopIDs := saved.StopEpisodeIDs
		if !hasProgress {
			stopIDs = []string{}
			for i, e := range existing.Episodes {
				if i == stopBoundary {
					break
				}
				stopIDs = append(stopIDs, e.ID)
			}
		}
		// save the original boundary before committing new episodes, so an
		// exit between persisting episodes and advancing the checkpoint is safe
		cursor := "/radio/catalog/" + string(*existing.CatalogKind) + "/" + id + "/episodes?page=1&pageSize=50"
		if hasProgress {
			cursor = saved.Cursor
		}
		f.store.WriteState(progressKey, progress{Cursor: cursor, StopEpisodeIDs: stopIDs})

		stop := map[string]bool{}
		for _, s := range stopIDs {
			stop[s] = true
		}
		opts := nrk.FetchOptions{SkipEpisodeIDs: skip, StopEpisodeIDs: stop, OnEpisodeFailure: onFailure,
			OnClipIDs: f.onClipIDs(id)}
		if hasProgress {
			opts.CursorHref = &saved.Cursor
		}
		result := f.nrk.GetNewEpisodes(id, *existing.CatalogKind, opts)
		if result == nil {
			f.retry.Defer("refresh:"+id, 0)
			return existing
		}
		if len(result.Episodes) > 0 && !f.store.AddEpisodes(id, result.Episodes) {
			f.retry.Defer("refresh:"+id, 0)
			return existing
		}
		if !result.SawAllPages && result.ResumeHref != nil {
			f.store.WriteState(progressKey, progress{Cursor: *result.ResumeHref, StopEpisodeIDs: stopIDs})
			f.retry.Defer("refresh:"+id, 0)
		} else {
			f.store.DeleteState(progressKey)
			f.retry.Clear("refresh:" + id)
			f.store.TouchLastFetched(id)
		}
		return f.readOr(id, existing)
	}

	update, outcome := f.fetchFromNrk(id, nrk.FetchOptions{
		SkipEpisodeIDs: skip, OnEpisodeFailure: onFailure, CatalogKind: existing.CatalogKind,
		OnClipIDs: f.onClipIDs(id),
	})
	if outcome != nrk.Found {
		// NRK outage or rate limiting: serve the stale copy rather than
		// pretending the series disappeared
		log.Printf("Failed to refresh series %s, serving stale data", id)
		f.retry.Defer("refresh:"+id, 0)
		return existing
	}
	// upserts metadata + new episodes and renews last_fetched_at; existing
	// episodes are kept, so the archive accumulates
	if !f.store.WriteSeries(update) {
		log.Printf("Failed to update series %s", id)
		f.retry.Defer("refresh:"+id, 0)
		return existing
	}
	f.retry.Clear("refresh:" + id)
	return f.readOr(id, existing)
}

// IntervalHoursFor adapts the refresh cadence to how actively a show
// publishes: hourly for shows active in the last two days, weekly once
// long dormant.
func IntervalHoursFor(newestEpisodeAt *time.Time) float64 {
	if newestEpisodeAt == nil {
		return syncIntervalHours
	}
	ageDays := float64(clock.Now().Sub(*newestEpisodeAt)) / float64(24*time.Hour)
	switch {
	case ageDays < 2:
		return 1
	case ageDays < 30:
		return 12
	case ageDays < 180:
		return 72
	}
	return 168
}

func refreshIntervalHours(series *store.Series) float64 {
	if len(series.Episodes) == 0 {
		return IntervalHoursFor(nil)
	}
	return IntervalHoursFor(&series.Episodes[0].Date)
}

// MetaIsFresh is the cheap freshness probe for the feed fast path: true
// only when the feed is comfortably inside its refresh window (the
// conservative end of the jitter range), so the fast path never serves
// anything the slow path would have refreshed.
func (f *Feeds) MetaIsFresh(id string) bool {
	meta, err := f.store.ReadSeriesMeta(id)
	if err != nil || meta == nil {
		return false
	}
	if f.store.HasState("refresh-progress:"+id) && f.retry.Can("refresh:"+id) {
		return false
	}
	if !meta.BacklogComplete && f.retry.Can("backlog:"+id) {
		return false
	}
	if !f.retry.Can("refresh:" + id) {
		return true
	}
	ageHours := float64(clock.Now().Sub(meta.LastFetchedAt)) / float64(time.Hour)
	return ageHours <= IntervalHoursFor(meta.NewestEpisodeAt)*0.85
}

// hoursSince is whole hours between t and now, floored and absolute (as
// @std/datetime's difference).
func hoursSince(t time.Time) float64 {
	d := clock.Now().Sub(t)
	if d < 0 {
		d = -d
	}
	return math.Floor(float64(d) / float64(time.Hour))
}

func (f *Feeds) fetchSeries(id string) (*store.Series, error) {
	stored, err := f.store.ReadSeries(id)
	if err != nil {
		return nil, err
	}

	var series *store.Series
	switch {
	case !f.retry.Can("refresh:" + id):
		if stored == nil {
			return nil, errors.New("refresh is waiting for its retry deadline")
		}
		series = stored
	case stored == nil:
		// first time we see this feed
		if series, err = f.initialFetch(id); err != nil {
			return nil, err
		}
	case !f.store.HasState("refresh-progress:"+id) &&
		hoursSince(stored.LastFetchedAt) <= refreshIntervalHours(stored)*(0.85+clock.Float64()*0.3):
		// cached and fresh (a small random jitter keeps refreshes from
		// clustering at the top of the hour)
		series = stored
	default:
		series = f.updateFetch(stored)
	}

	// make sure the full archive gets (or resumes getting) crawled
	if series != nil && !series.BacklogComplete {
		f.crawler.Enqueue(series.ID)
	}
	if series != nil && f.Videos != nil {
		f.Videos.Enqueue(series.ID)
	}
	return series, nil
}

// GetSeries returns the feed, fetching or refreshing it from NRK as
// needed. Concurrent requests for the same feed share one fetch. (nil,
// nil) means NRK doesn't know the feed.
func (f *Feeds) GetSeries(id string) (*store.Series, error) {
	f.mu.Lock()
	if until, ok := f.notFoundUntil[id]; ok {
		if until.After(clock.Now()) {
			f.mu.Unlock()
			return nil, nil
		}
		delete(f.notFoundUntil, id)
	}
	c := f.inFlight[id]
	if c == nil {
		c = &call{done: make(chan struct{})}
		f.inFlight[id] = c
		f.mu.Unlock()
		func() {
			defer func() {
				if r := recover(); r != nil {
					c.err = errors.New("feed fetch panicked")
					log.Printf("feed fetch %s panicked: %v", id, r)
				}
				f.mu.Lock()
				delete(f.inFlight, id)
				f.mu.Unlock()
				close(c.done)
			}()
			c.series, c.err = f.fetchSeries(id)
		}()
	} else {
		f.mu.Unlock()
		<-c.done
	}

	if c.err == nil && c.series == nil {
		f.mu.Lock()
		if len(f.notFoundUntil) >= 500 {
			now := clock.Now()
			for k, until := range f.notFoundUntil {
				if !until.After(now) {
					delete(f.notFoundUntil, k)
				}
			}
		}
		f.notFoundUntil[id] = clock.Now().Add(notFoundTTL)
		f.mu.Unlock()
	}
	return c.series, c.err
}
