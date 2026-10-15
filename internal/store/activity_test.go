package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openActivityStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "activity.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func intPtr(n int) *int { return &n }

func TestFlushActivity(t *testing.T) {
	s := openActivityStore(t)
	if _, ok := s.ActivitySince(); ok {
		t.Fatal("activity-since set before any activity")
	}
	day := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	err := s.FlushActivity(ActivityBatch{
		Feeds: map[string]time.Time{"a": day, "b": day.Add(-time.Hour)},
		Clients: []ClientSighting{
			{FeedID: "a", Client: "Overcast", At: day, Subscribers: intPtr(12)},
			{FeedID: "a", Client: "Pocket Casts", At: day},
			{FeedID: "a", Client: "Feedly", At: day.Add(-20 * 24 * time.Hour), Subscribers: intPtr(5)},
		},
		Daily: map[string]map[string]int{"2026-10-15": {"nrk": 3, "feeds_200": 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	since, ok := s.ActivitySince()
	if !ok || since.IsZero() {
		t.Fatal("activity-since not set")
	}

	// a later batch: newer request, a new count replaces the old one, a
	// sighting without a count keeps it, counters add up
	err = s.FlushActivity(ActivityBatch{
		Feeds:   map[string]time.Time{"a": day.Add(time.Hour), "b": day.Add(-2 * time.Hour)},
		Clients: []ClientSighting{{FeedID: "a", Client: "Overcast", At: day.Add(time.Hour), Subscribers: intPtr(15)}, {FeedID: "a", Client: "Overcast", At: day.Add(time.Hour)}},
		Daily:   map[string]map[string]int{"2026-10-15": {"nrk": 2}, "2026-10-16": {"nrk": 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := s.ActivitySince(); !again.Equal(since) {
		t.Error("activity-since must keep its first value")
	}

	activity, err := s.ReadFeedActivity(day.Add(-14 * 24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	a := activity["a"]
	if a == nil || !a.LastRequestedAt.Equal(day.Add(time.Hour)) {
		t.Fatalf("feed a: %+v", a)
	}
	if !activity["b"].LastRequestedAt.Equal(day.Add(-time.Hour)) {
		t.Error("an older request must not move last use back")
	}
	// Feedly was seen 20 days ago: outside the window, not counted
	if len(a.Clients) != 2 || a.Subscribers == nil || *a.Subscribers != 15 {
		t.Errorf("clients %v, subscribers %v; want Overcast and Pocket Casts, 15", a.Clients, a.Subscribers)
	}
	if activity["b"].Subscribers != nil {
		t.Error("no reported count must stay unknown, not zero")
	}

	daily, err := s.ReadDailyStats("2026-10-15")
	if err != nil {
		t.Fatal(err)
	}
	if daily["2026-10-15"]["nrk"] != 5 || daily["2026-10-15"]["feeds_200"] != 1 || daily["2026-10-16"]["nrk"] != 1 {
		t.Errorf("daily: %v", daily)
	}
	s.PruneDailyStats("2026-10-16")
	if daily, _ := s.ReadDailyStats("2000-01-01"); len(daily) != 1 {
		t.Errorf("after pruning: %v", daily)
	}
}

func TestDeleteSeriesRemovesActivity(t *testing.T) {
	s := openActivityStore(t)
	now := time.Now()
	if err := s.FlushActivity(ActivityBatch{
		Feeds:   map[string]time.Time{"gone": now},
		Clients: []ClientSighting{{FeedID: "gone", Client: "Overcast", At: now, Subscribers: intPtr(3)}},
	}); err != nil {
		t.Fatal(err)
	}
	s.DeleteSeriesByID("gone")
	if activity, _ := s.ReadFeedActivity(now.Add(-time.Hour)); activity["gone"] != nil {
		t.Error("deleting a feed must delete its activity")
	}
}
