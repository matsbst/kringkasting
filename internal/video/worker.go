// Package video finds NRK's video versions of podcast episodes, so feeds
// can offer them as an alternate enclosure.
//
// It runs in the background, one feed at a time, after a feed is fetched
// or refreshed — never on plain feed polls. Each round checks a bounded
// number of due episodes and, for shows that have video, recovers clip
// references for older episodes one listing page at a time. All NRK calls
// go through the shared client (concurrency cap, retry deadlines); a
// failed lookup never affects the audio feed.
package video

import (
	"context"
	"fmt"
	"log"
	"math"
	"runtime/debug"
	"sync"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/nrk"
	"github.com/matsbst/kringkasting/internal/retry"
	"github.com/matsbst/kringkasting/internal/store"
)

const (
	// perRound bounds the clip lookups per feed per round.
	perRound = 20
	// concurrency bounds simultaneous clip lookups (beneath the global cap).
	concurrency = 2
	// recheckFound re-checks a confirmed video now and then; it also keeps
	// the playlist URL current if NRK ever changes it.
	recheckFound = 30 * 24 * time.Hour
	// recheckMissingRecent: NRK may publish the video after the audio.
	recheckMissingRecent = 6 * time.Hour
	recentEpisode        = 7 * 24 * time.Hour
	recheckMissing       = 30 * 24 * time.Hour
	// a video counts as the same episode when its length matches the audio
	// within max(60 s, 5 %), which keeps trailers and promos out
	minTolerance      = 60.0
	relativeTolerance = 0.05
)

// Matches reports whether a video's length matches the audio episode's.
func Matches(videoSeconds float64, audioSeconds int64) bool {
	tolerance := math.Max(minTolerance, relativeTolerance*float64(audioSeconds))
	return math.Abs(videoSeconds-float64(audioSeconds)) <= tolerance
}

// nextCheckWithoutVideo: recent episodes again soon, older ones monthly.
func nextCheckWithoutVideo(episodeDate time.Time) time.Time {
	if clock.Now().Sub(episodeDate) < recentEpisode {
		return clock.Now().Add(recheckMissingRecent)
	}
	return clock.Now().Add(recheckMissing)
}

type Worker struct {
	ctx   context.Context // owned by the process, never a request
	store *store.Store
	nrk   *nrk.Client
	retry *retry.Retry
	// OnPanic reports a crashed round (wired to error reporting).
	OnPanic func(err error)

	mu      sync.Mutex
	queue   []string
	queued  map[string]bool
	running bool
	idle    chan struct{}

	lookupMu sync.Mutex
	lookups  map[string]*lookup // coalesces concurrent lookups per clip
}

type lookup struct {
	done    chan struct{}
	video   *nrk.ClipVideo
	outcome nrk.Outcome
}

func New(ctx context.Context, s *store.Store, client *nrk.Client, r *retry.Retry) *Worker {
	idle := make(chan struct{})
	close(idle)
	return &Worker{ctx: ctx, store: s, nrk: client, retry: r, queued: map[string]bool{}, idle: idle,
		lookups: map[string]*lookup{}}
}

// Enqueue schedules a round for a feed (no-op when already queued).
func (w *Worker) Enqueue(feedID string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.queued[feedID] || w.ctx.Err() != nil {
		return
	}
	w.queued[feedID] = true
	w.queue = append(w.queue, feedID)
	if !w.running {
		w.running = true
		w.idle = make(chan struct{})
		go w.process(w.idle)
	}
}

// WaitIdle blocks until the queue has drained (tests, shutdown).
func (w *Worker) WaitIdle(ctx context.Context) error {
	w.mu.Lock()
	idle := w.idle
	w.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker) process(idle chan struct{}) {
	for {
		w.mu.Lock()
		if len(w.queue) == 0 || w.ctx.Err() != nil {
			w.queue = nil
			w.queued = map[string]bool{}
			w.running = false
			close(idle)
			w.mu.Unlock()
			return
		}
		feedID := w.queue[0]
		w.queue = w.queue[1:]
		// leave the queued set now, so a request during this round (which
		// may make more work due) schedules another round
		delete(w.queued, feedID)
		w.mu.Unlock()

		w.roundSafely(feedID)
	}
}

func (w *Worker) roundSafely(feedID string) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("video round failed for %s: %v", feedID, r)
			log.Printf("%v\n%s", err, debug.Stack())
			if w.OnPanic != nil {
				w.OnPanic(err)
			}
		}
	}()
	w.checkDue(feedID)
	w.backfillClipIDs(feedID)
}

// checkDue looks up the videos of up to perRound due episodes.
func (w *Worker) checkDue(feedID string) {
	due, err := w.store.VideosDue(feedID, perRound)
	if err != nil {
		log.Printf("video candidates for %s: %v", feedID, err)
		return
	}
	work := make(chan store.VideoCandidate)
	var wg sync.WaitGroup
	for range concurrency {
		wg.Go(func() {
			for candidate := range work {
				w.check(candidate)
			}
		})
	}
	for _, candidate := range due {
		if w.ctx.Err() != nil {
			break
		}
		work <- candidate
	}
	close(work)
	wg.Wait()
}

func (w *Worker) check(c store.VideoCandidate) {
	// a podcast and its season feed share episodes: reuse a fresh result
	if url, next, ok := w.store.KnownVideo(c.ClipID, clock.Now()); ok {
		w.store.SetVideo(c.SeriesID, c.EpisodeID, url, next)
		return
	}
	key := "video:" + c.ClipID
	if !w.retry.Can(key) {
		return
	}
	video, outcome := w.lookup(c.ClipID)
	switch {
	case outcome == nrk.Unavailable:
		// transient: keep what we have, try again after the retry deadline
		w.retry.Defer(key, 0)
	case outcome == nrk.Found && Matches(video.DurationSeconds, c.DurationInSeconds):
		url := video.URL
		w.store.SetVideo(c.SeriesID, c.EpisodeID, &url, clock.Now().Add(recheckFound))
		w.retry.Clear(key)
	default:
		// no usable video, or one that isn't this episode (a trailer, say)
		w.store.SetVideo(c.SeriesID, c.EpisodeID, nil, nextCheckWithoutVideo(c.Date))
		w.retry.Clear(key)
	}
}

// lookup coalesces concurrent lookups of the same clip into one request.
func (w *Worker) lookup(clipID string) (video *nrk.ClipVideo, outcome nrk.Outcome) {
	w.lookupMu.Lock()
	l := w.lookups[clipID]
	if l != nil {
		w.lookupMu.Unlock()
		<-l.done
		return l.video, l.outcome
	}
	l = &lookup{done: make(chan struct{})}
	w.lookups[clipID] = l
	w.lookupMu.Unlock()

	// a panic must not leave the entry behind, or later lookups of the
	// clip (and the video round) would wait forever
	defer func() {
		if r := recover(); r != nil {
			l.video, l.outcome = nil, nrk.Unavailable
			video, outcome = l.video, l.outcome
			log.Printf("video lookup %s panicked: %v", clipID, r)
		}
		w.lookupMu.Lock()
		delete(w.lookups, clipID)
		w.lookupMu.Unlock()
		close(l.done)
	}()
	l.video, l.outcome = w.nrk.VideoForClip(clipID)
	return l.video, l.outcome
}

// backfill is the per-feed progress of recovering older clip references.
type backfill struct {
	Cursor string `json:"cursor"`
	Done   bool   `json:"done"`
}

// backfillClipIDs walks a video show's archive listing one page per round,
// recording clip references for episodes stored before they were captured.
// Only shows with at least one video, and only once their archive crawl is
// complete (the crawler records clip references itself until then).
func (w *Worker) backfillClipIDs(feedID string) {
	if w.ctx.Err() != nil || !w.store.HasVideo(feedID) {
		return
	}
	series, err := w.store.ReadSeriesMeta(feedID)
	if err != nil || series == nil || !series.BacklogComplete {
		return
	}
	key := "video-backfill:" + feedID
	var progress backfill
	w.store.ReadState(key, &progress)
	if progress.Done || !w.retry.Can(key) {
		return
	}
	clipIDs, next, ok := w.nrk.ListClipIDs(feedID, progress.Cursor, w.store.CatalogKindOf(feedID))
	if !ok {
		w.retry.Defer(key, 0)
		return
	}
	w.store.SetClipIDs(feedID, clipIDs)
	w.retry.Clear(key)
	w.store.WriteState(key, backfill{Cursor: next, Done: next == ""})
}
