// Package stats keeps in-memory operational counters for the admin
// dashboard. They reset on restart by design (the dashboard says "siden
// omstart"). No per-user data is ever recorded.
package stats

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
)

const (
	healthFailureThreshold = 8
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
}

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
}

func New() *Stats {
	s := &Stats{startedAt: clock.Now(), upstream: map[string]*Bucket{}}
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
	if status == 0 || status >= 500 {
		bucket.Errors++
	}
	var alert error
	if !IsHealthFailure(status) {
		s.consecutiveFailures = 0
	} else {
		s.consecutiveFailures++
		if s.consecutiveFailures >= healthFailureThreshold && clock.Now().Sub(s.lastHealthAlertAt) > healthAlertThrottle {
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
	} else {
		s.feeds200++
	}
}

func (s *Stats) RecordSearchServed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.searches++
}

func (s *Stats) RecordRateLimited() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limited++
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
