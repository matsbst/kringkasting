// Package app wires the services together. The server (cmd/kringkasting)
// and the end-to-end tests build the application through New, so tests
// exercise exactly the production wiring.
package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/matsbst/kringkasting/internal/catalog"
	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/config"
	"github.com/matsbst/kringkasting/internal/crawl"
	"github.com/matsbst/kringkasting/internal/feed"
	"github.com/matsbst/kringkasting/internal/nrk"
	"github.com/matsbst/kringkasting/internal/report"
	"github.com/matsbst/kringkasting/internal/retry"
	"github.com/matsbst/kringkasting/internal/stats"
	"github.com/matsbst/kringkasting/internal/store"
	"github.com/matsbst/kringkasting/internal/video"
	"github.com/matsbst/kringkasting/internal/web"
)

// Options are the knobs tests turn; production uses the zero values.
type Options struct {
	// Transport replaces the HTTP transport for NRK requests.
	Transport http.RoundTripper
	// PageDelay overrides the pause between archive pages.
	PageDelay time.Duration
}

type App struct {
	Handler http.Handler
	Store   *store.Store
	Crawler *crawl.Crawler
	Videos  *video.Worker
	Stats   *stats.Stats

	flushMu sync.Mutex
	flushed chan struct{} // closed when the flush loop has stopped
}

const (
	// flushInterval is how often dashboard counters reach the database.
	flushInterval = time.Minute
	// historyDays is how long daily totals are kept.
	historyDays = 400
)

// New opens the database and builds the application. Background work
// (archive crawls) stops when ctx is cancelled.
func New(ctx context.Context, cfg config.Config, opts Options) (*App, error) {
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", cfg.DBPath, err)
	}
	pageDelay := opts.PageDelay
	if pageDelay == 0 {
		pageDelay = crawl.DefaultPageDelay
	}

	reporter := report.New(cfg.SentryDSN, cfg.BuildID, cfg.Origin != "")
	counters := stats.New()
	counters.Alert = reporter.Capture
	retries := retry.New(db)
	client := nrk.New(opts.Transport, retries, counters)
	crawler := crawl.New(ctx, db, client, retries, pageDelay)
	crawler.OnPanic = func(err error) { reporter.Capture(err, map[string]string{"kind": "backlog"}) }
	videos := video.New(ctx, db, client, retries)
	videos.OnPanic = func(err error) { reporter.Capture(err, map[string]string{"kind": "video"}) }
	crawler.AfterPage = videos.Enqueue
	feeds := feed.NewFeeds(db, client, retries, crawler)
	feeds.Videos = videos

	handler, err := web.New(web.Deps{
		Config: cfg, Store: db, NRK: client, Crawler: crawler, Stats: counters, Reporter: reporter,
		Feeds:    feeds,
		Cache:    feed.NewCache(),
		Chapters: feed.NewChapters(db, client, retries),
		Catalog:  catalog.New(db, client, retries),
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	a := &App{Handler: handler, Store: db, Crawler: crawler, Videos: videos, Stats: counters, flushed: make(chan struct{})}
	go a.flushLoop(ctx)
	return a, nil
}

// flushLoop writes the dashboard counters once a minute until ctx ends,
// and prunes old daily totals at start and once a day.
func (a *App) flushLoop(ctx context.Context) {
	defer close(a.flushed)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	pruned := ""
	for {
		if today := stats.Day(clock.Now()); today != pruned {
			a.Store.PruneDailyStats(stats.Day(clock.Now().AddDate(0, 0, -historyDays)))
			pruned = today
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.Flush()
		}
	}
}

// Flush writes the counters recorded since the last flush. A failed
// write keeps them for the next try; it never blocks a request.
func (a *App) Flush() {
	a.flushMu.Lock()
	defer a.flushMu.Unlock()
	batch := a.Stats.TakeBatch()
	if err := a.Store.FlushActivity(batch); err != nil {
		log.Printf("flush dashboard counters: %v", err)
		a.Stats.ReturnBatch(batch)
	}
}

// Close waits (bounded by ctx) for the crawler to stop, then closes the
// database. Cancel the context given to New first.
func (a *App) Close(ctx context.Context) error {
	waitErr := a.Crawler.WaitIdle(ctx)
	if err := a.Videos.WaitIdle(ctx); err != nil && waitErr == nil {
		waitErr = err
	}
	// the last counters: a deploy loses nothing
	select {
	case <-a.flushed:
	case <-ctx.Done():
	}
	a.Flush()
	if err := a.Store.Close(); err != nil {
		return err
	}
	return waitErr
}
