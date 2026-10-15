// Package crawl is the background crawler that pages through a feed's
// complete episode archive after it is first requested, so feeds grow
// beyond the ~50 episodes NRK returns per catalog page.
//
// Deliberately gentle with NRK: one feed at a time, sequential pages with
// a pause in between, and progress checkpointed per page in the
// backlog_cursor column so a restart resumes where it left off. The queue
// itself is in memory: an unfinished crawl is re-admitted by the next
// request for that feed, not scheduled at startup.
package crawl

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/nrk"
	"github.com/matsbst/kringkasting/internal/retry"
	"github.com/matsbst/kringkasting/internal/store"
)

const (
	// DefaultPageDelay is the pause between archive pages.
	DefaultPageDelay = time.Second
	// maxPagesPerRun is a hard stop per feed per crawl round.
	maxPagesPerRun = 200
	maxEvents      = 20
)

// Event is a recent crawl event for the admin dashboard.
type Event struct {
	At      time.Time
	Message string
}

// Status is the crawler state shown on the admin dashboard.
type Status struct {
	QueueLength int
	Running     bool
	Events      []Event // newest first
}

type Crawler struct {
	ctx       context.Context // owned by the process, never a request
	store     *store.Store
	nrk       *nrk.Client
	retry     *retry.Retry
	pageDelay time.Duration
	// OnPanic reports a crashed crawl (wired to error reporting).
	OnPanic func(err error)
	// AfterPage is told when a page added episodes (video lookups).
	AfterPage func(feedID string)

	mu      sync.Mutex
	queue   []string
	queued  map[string]bool
	running bool
	idle    chan struct{} // closed when the queue drains
	events  []Event
}

// New returns a crawler whose work stops when ctx is cancelled.
func New(ctx context.Context, s *store.Store, client *nrk.Client, r *retry.Retry, pageDelay time.Duration) *Crawler {
	idle := make(chan struct{})
	close(idle)
	return &Crawler{ctx: ctx, store: s, nrk: client, retry: r, pageDelay: pageDelay, queued: map[string]bool{}, idle: idle}
}

func (c *Crawler) recordEvent(message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, Event{At: clock.Now(), Message: message})
	if len(c.events) > maxEvents {
		c.events = c.events[1:]
	}
}

// Status returns the queue state and recent events, newest first.
func (c *Crawler) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	events := make([]Event, len(c.events))
	for i, e := range c.events {
		events[len(c.events)-1-i] = e
	}
	return Status{QueueLength: len(c.queue), Running: c.running, Events: events}
}

// Enqueue admits a feed for crawling unless it is queued already or
// waiting for its retry deadline.
func (c *Crawler) Enqueue(feedID string) {
	if !c.retry.Can("backlog:" + feedID) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.queued[feedID] || c.ctx.Err() != nil {
		return
	}
	c.queued[feedID] = true
	c.queue = append(c.queue, feedID)
	if !c.running {
		c.running = true
		c.idle = make(chan struct{})
		go c.processQueue(c.idle)
	}
}

// WaitIdle blocks until the queue has drained (tests, shutdown).
func (c *Crawler) WaitIdle(ctx context.Context) error {
	c.mu.Lock()
	idle := c.idle
	c.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Crawler) processQueue(idle chan struct{}) {
	for {
		c.mu.Lock()
		if len(c.queue) == 0 || c.ctx.Err() != nil {
			c.queue = nil
			c.queued = map[string]bool{}
			c.running = false
			close(idle)
			c.mu.Unlock()
			return
		}
		feedID := c.queue[0]
		c.queue = c.queue[1:]
		c.mu.Unlock()

		c.crawlSafely(feedID)

		c.mu.Lock()
		delete(c.queued, feedID)
		c.mu.Unlock()
	}
}

// crawlSafely contains a panic to the one feed: it is reported and the
// feed backs off, and the queue continues.
func (c *Crawler) crawlSafely(feedID string) {
	defer func() {
		if r := recover(); r != nil {
			c.retry.Defer("backlog:"+feedID, 0)
			err := fmt.Errorf("backlog crawl failed for %s: %v", feedID, r)
			log.Printf("%v\n%s", err, debug.Stack())
			if c.OnPanic != nil {
				c.OnPanic(err)
			}
		}
	}()
	c.crawl(feedID)
}

func (c *Crawler) crawl(feedID string) {
	series, err := c.store.ReadSeries(feedID)
	if err != nil || series == nil || series.BacklogComplete || !c.retry.Can("backlog:"+feedID) {
		return
	}

	start := "start"
	resumed := "fra start"
	if series.BacklogCursor != nil {
		start = *series.BacklogCursor
		resumed = "gjenopptatt"
	}
	log.Printf("Backlog crawl for %s starting (cursor: %s)", feedID, start)
	c.recordEvent(fmt.Sprintf("%s: crawl startet (%s)", feedID, resumed))
	cursor := ""
	if series.BacklogCursor != nil {
		cursor = *series.BacklogCursor
	}

	for range maxPagesPerRun {
		skip, err := c.store.ReadEpisodeIDs(feedID)
		if err != nil {
			c.pause(feedID, "databaselesing feilet")
			return
		}
		blocked, _ := c.store.ReadBlockedEpisodeIDs(feedID)
		for id := range blocked {
			skip[id] = true
		}
		page := c.nrk.GetEpisodePage(feedID, cursor, skip,
			func(episodeID string) { c.store.RecordEpisodeFailure(feedID, episodeID) }, series.CatalogKind)
		if page == nil {
			// NRK hiccup: keep the cursor so a later run resumes from here
			log.Printf("Backlog crawl for %s paused at cursor %s", feedID, cursorOrNull(cursor))
			c.recordEvent(feedID + ": pauset – NRK utilgjengelig")
			c.retry.Defer("backlog:"+feedID, 0)
			return
		}

		if !c.store.HasSeries(feedID) {
			return
		}
		if len(page.Episodes) > 0 && !c.store.AddEpisodes(feedID, page.Episodes) {
			// failed write: don't advance past episodes we didn't persist
			log.Printf("Backlog crawl for %s paused: episode write failed", feedID)
			c.pause(feedID, "databaselagring feilet")
			return
		}
		// clip references for listed episodes stored earlier
		c.store.SetClipIDs(feedID, page.ClipIDs)
		if len(page.Episodes) > 0 && c.AfterPage != nil {
			c.AfterPage(feedID)
		}
		if page.TransientFailures > 0 {
			// NRK hiccups on some manifests: retry this page later instead
			// of advancing past (and losing) those episodes forever
			log.Printf("Backlog crawl for %s paused: %d transient manifest failures", feedID, page.TransientFailures)
			c.pause(feedID, fmt.Sprintf("%d forbigående manifest-feil", page.TransientFailures))
			return
		}

		completed := cursor
		cursor = page.NextHref
		if cursor == "" {
			c.store.SetBacklogState(feedID, nil, true)
			c.retry.Clear("backlog:" + feedID)
			log.Printf("Backlog crawl for %s complete", feedID)
			c.recordEvent(feedID + ": arkiv komplett")
			return
		}
		// revisit the last completed page after a pause/restart, for overlap
		var checkpoint *string
		if completed != "" {
			checkpoint = &completed
		}
		c.store.SetBacklogState(feedID, checkpoint, false)

		select {
		case <-time.After(c.pageDelay):
		case <-c.ctx.Done():
			// shutting down: the checkpoint is saved, the next request resumes
			return
		}
	}

	c.retry.Defer("backlog:"+feedID, 0)
	log.Printf("Backlog crawl for %s hit the per-run page limit, will resume later", feedID)
}

func (c *Crawler) pause(feedID, reason string) {
	c.recordEvent(feedID + ": pauset – " + reason)
	c.retry.Defer("backlog:"+feedID, 0)
}

func cursorOrNull(cursor string) string {
	if cursor == "" {
		return "null"
	}
	return cursor
}
