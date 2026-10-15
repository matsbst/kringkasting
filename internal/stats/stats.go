// Package stats keeps operational counters for the admin dashboard. The
// "siden omstart" counters live in memory and reset on restart; per-day
// totals and per-feed activity accumulate in a batch that the app flushes
// to the database (TakeBatch). No per-user data is ever recorded: feed
// ids, app names, counts and times only.
package stats

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/store"
)

// Daily counter keys, as stored in daily_stats.
const (
	DayNRK         = "nrk"
	DayNRKErrors   = "nrk_errors"
	DayFeeds200    = "feeds_200"
	DayFeeds304    = "feeds_304"
	DaySearches    = "searches"
	DayRateLimited = "rate_limited"
)

// Day is the daily_stats key for a time, in the server's time zone (the
// zone the dashboard shows times in).
func Day(t time.Time) string { return t.Local().Format("2006-01-02") }

const (
	// HealthFailureThreshold failures in a row count as an NRK outage
	HealthFailureThreshold = 8
	healthAlertThrottle    = 30 * time.Minute
)

// Categories in display order (the admin table iterates them in this order).
var Categories = []string{"katalog", "episodeliste", "manifest", "filstørrelse", "søk", "annet"}

type Bucket struct{ Requests, Errors int }

type Gc struct {
	At      time.Time
	Deleted int
}

// Snapshot is a consistent copy of the counters.
type Snapshot struct {
	StartedAt      time.Time
	Upstream       map[string]Bucket
	UpstreamTotal  int
	UpstreamErrors int
	Feeds200       int
	Feeds304       int
	Searches       int
	RateLimited    int
	LastGc         *Gc
	// NRK health
	LastUpstreamSuccess time.Time
	LastUpstreamFailure time.Time
	LastFailureStatus   int
	ConsecutiveFailures int
	// FailingSince is the first failure of the current run of failures
	FailingSince time.Time
}

type clientKey struct{ feed, client string }

type Stats struct {
	// Alert reports a sustained NRK outage (wired to error reporting).
	Alert func(err error, tags map[string]string)

	mu                  sync.Mutex
	startedAt           time.Time
	upstream            map[string]*Bucket
	feeds200, feeds304  int
	searches, limited   int
	lastGc              *Gc
	consecutiveFailures int
	lastHealthAlertAt   time.Time
	lastSuccess         time.Time
	lastFailure         time.Time
	lastFailureStatus   int
	failingSince        time.Time

	// the batch waiting for the next flush
	feeds   map[string]time.Time
	clients map[clientKey]store.ClientSighting
	daily   map[string]map[string]int
}

func New() *Stats {
	s := &Stats{startedAt: clock.Now(), upstream: map[string]*Bucket{}}
	s.resetBatch()
	for _, c := range Categories {
		s.upstream[c] = &Bucket{}
	}
	return s
}

func categorize(url, method string) string {
	switch {
	case method == "HEAD":
		return "filstørrelse"
	case strings.Contains(url, "/playback/manifest/"):
		return "manifest"
	case strings.Contains(url, "/radio/search/search"):
		return "søk"
	case strings.Contains(url, "/episodes"):
		return "episodeliste"
	case strings.Contains(url, "/radio/catalog/") || strings.Contains(url, "/radio/search/categories/"):
		return "katalog"
	}
	return "annet"
}

// IsHealthFailure: only network/timeout (0), 5xx and 429/403 count; any
// other response proves NRK is reachable.
func IsHealthFailure(status int) bool {
	return status == 0 || status >= 500 || status == 429 || status == 403
}

// RecordUpstreamRequest counts one NRK request (status 0 = network error)
// and alerts once, throttled, when failures run on.
func (s *Stats) RecordUpstreamRequest(url, method string, status int) {
	s.mu.Lock()
	bucket := s.upstream[categorize(url, method)]
	bucket.Requests++
	s.countDay(DayNRK)
	if status == 0 || status >= 500 {
		bucket.Errors++
		s.countDay(DayNRKErrors)
	}
	var alert error
	if !IsHealthFailure(status) {
		s.consecutiveFailures = 0
		s.lastSuccess = clock.Now()
	} else {
		if s.consecutiveFailures == 0 {
			s.failingSince = clock.Now()
		}
		s.lastFailure, s.lastFailureStatus = clock.Now(), status
		s.consecutiveFailures++
		if s.consecutiveFailures >= HealthFailureThreshold && clock.Now().Sub(s.lastHealthAlertAt) > healthAlertThrottle {
			s.lastHealthAlertAt = clock.Now()
			alert = fmt.Errorf("NRK API appears unhealthy: %d consecutive upstream failures (latest status %d)",
				s.consecutiveFailures, status)
		}
	}
	s.mu.Unlock()
	if alert != nil && s.Alert != nil {
		s.Alert(alert, map[string]string{"kind": "upstream-health", "status": fmt.Sprint(status)})
	}
}

// ConsecutiveFailures is exposed for tests.
func (s *Stats) ConsecutiveFailures() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consecutiveFailures
}

func (s *Stats) RecordFeedServed(notModified bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if notModified {
		s.feeds304++
		s.countDay(DayFeeds304)
	} else {
		s.feeds200++
		s.countDay(DayFeeds200)
	}
}

func (s *Stats) RecordSearchServed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.searches++
	s.countDay(DaySearches)
}

func (s *Stats) RecordRateLimited() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limited++
	s.countDay(DayRateLimited)
}

func (s *Stats) RecordGcRun(deleted int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastGc = &Gc{At: clock.Now(), Deleted: deleted}
}

func (s *Stats) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{
		StartedAt: s.startedAt, Upstream: map[string]Bucket{},
		Feeds200: s.feeds200, Feeds304: s.feeds304, Searches: s.searches, RateLimited: s.limited,
		LastUpstreamSuccess: s.lastSuccess, LastUpstreamFailure: s.lastFailure, LastFailureStatus: s.lastFailureStatus,
		ConsecutiveFailures: s.consecutiveFailures,
	}
	if s.consecutiveFailures > 0 {
		snap.FailingSince = s.failingSince
	}
	for name, b := range s.upstream {
		snap.Upstream[name] = *b
		snap.UpstreamTotal += b.Requests
		snap.UpstreamErrors += b.Errors
	}
	if s.lastGc != nil {
		gc := *s.lastGc
		snap.LastGc = &gc
	}
	return snap
}

// countDay adds one to today's counter. Callers hold s.mu.
func (s *Stats) countDay(key string) {
	day := Day(clock.Now())
	if s.daily[day] == nil {
		s.daily[day] = map[string]int{}
	}
	s.daily[day][key]++
}

// RecordFeedRequest notes that an app requested a feed: when, which app,
// and the subscriber count it reports, if any. The user agent is reduced
// to the app's name here and not kept.
func (s *Stats) RecordFeedRequest(feedID, userAgent string) {
	now := clock.Now()
	sighting := store.ClientSighting{FeedID: feedID, Client: ClientName(userAgent), At: now}
	if n, ok := ReportedSubscribers(userAgent); ok {
		sighting.Subscribers = &n
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.feeds[feedID] = now
	key := clientKey{feedID, sighting.Client}
	if previous, ok := s.clients[key]; ok && sighting.Subscribers == nil {
		// a sighting without a count keeps the count seen earlier
		sighting.Subscribers = previous.Subscribers
	}
	s.clients[key] = sighting
}

func (s *Stats) resetBatch() {
	s.feeds = map[string]time.Time{}
	s.clients = map[clientKey]store.ClientSighting{}
	s.daily = map[string]map[string]int{}
}

// TakeBatch hands over everything recorded since the last call.
func (s *Stats) TakeBatch() store.ActivityBatch {
	s.mu.Lock()
	defer s.mu.Unlock()
	batch := store.ActivityBatch{Feeds: s.feeds, Daily: s.daily}
	for _, c := range s.clients {
		batch.Clients = append(batch.Clients, c)
	}
	s.resetBatch()
	return batch
}

// ReturnBatch puts back a batch that could not be written, so the next
// flush retries it. Newer values recorded meanwhile win.
func (s *Stats) ReturnBatch(b store.ActivityBatch) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for feed, at := range b.Feeds {
		if at.After(s.feeds[feed]) {
			s.feeds[feed] = at
		}
	}
	for _, c := range b.Clients {
		key := clientKey{c.FeedID, c.Client}
		if newer, ok := s.clients[key]; ok {
			if newer.Subscribers == nil {
				newer.Subscribers = c.Subscribers
				s.clients[key] = newer
			}
			continue
		}
		s.clients[key] = c
	}
	for day, counters := range b.Daily {
		if s.daily[day] == nil {
			s.daily[day] = map[string]int{}
		}
		for key, n := range counters {
			s.daily[day][key] += n
		}
	}
}

// PendingDay is a day's counters not yet flushed, so the dashboard's
// numbers for today aren't up to a minute behind.
func (s *Stats) PendingDay(day string) map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := map[string]int{}
	for key, n := range s.daily[day] {
		pending[key] = n
	}
	return pending
}
