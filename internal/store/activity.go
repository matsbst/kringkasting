package store

import (
	"database/sql"
	"log"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
)

// Activity tables for the admin dashboard.
// They hold feed ids, app names, counts and times only: nothing about
// individual listeners. Older versions ignore them, so rollbacks work.
const activitySchema = `
CREATE TABLE IF NOT EXISTS feed_activity (
  feed_id TEXT PRIMARY KEY,
  first_requested_at INTEGER NOT NULL,
  last_requested_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS feed_clients (
  feed_id TEXT NOT NULL,
  client TEXT NOT NULL,
  last_seen_at INTEGER NOT NULL,
  subscribers INTEGER,
  PRIMARY KEY (feed_id, client)
);
CREATE TABLE IF NOT EXISTS daily_stats (
  day TEXT NOT NULL,
  key TEXT NOT NULL,
  value INTEGER NOT NULL,
  PRIMARY KEY (day, key)
);
`

// activitySinceKey is when activity was first recorded: the cleanup of
// unused feeds waits until enough time has been measured.
const activitySinceKey = "activity-since"

// ClientSighting is one app seen requesting one feed.
type ClientSighting struct {
	FeedID, Client string
	At             time.Time
	// Subscribers is the count the app reported, if any
	Subscribers *int
}

// ActivityBatch is what accumulated in memory since the last flush.
type ActivityBatch struct {
	// Feeds maps a feed id to its latest request
	Feeds   map[string]time.Time
	Clients []ClientSighting
	// Daily maps a day ("2006-01-02") to counter deltas
	Daily map[string]map[string]int
}

// Empty reports whether there is nothing to write.
func (b ActivityBatch) Empty() bool {
	return len(b.Feeds) == 0 && len(b.Clients) == 0 && len(b.Daily) == 0
}

// FlushActivity writes a batch in one transaction. Later times win; a
// client's reported subscriber count replaces the previous one, and a
// sighting without a count keeps it.
func (s *Store) FlushActivity(b ActivityBatch) error {
	if b.Empty() {
		return nil
	}
	err := s.tx(func(tx *sql.Tx) error {
		// a batch keeps each feed's latest request, so a new feed's first
		// request is recorded to within one flush interval
		for feedID, at := range b.Feeds {
			if _, err := tx.Exec(`INSERT INTO feed_activity (feed_id, first_requested_at, last_requested_at) VALUES (?, ?, ?)
				ON CONFLICT(feed_id) DO UPDATE SET last_requested_at = MAX(last_requested_at, excluded.last_requested_at)`,
				feedID, at.UnixMilli(), at.UnixMilli()); err != nil {
				return err
			}
		}
		for _, c := range b.Clients {
			if _, err := tx.Exec(`INSERT INTO feed_clients (feed_id, client, last_seen_at, subscribers) VALUES (?, ?, ?, ?)
				ON CONFLICT(feed_id, client) DO UPDATE SET
				  last_seen_at = MAX(last_seen_at, excluded.last_seen_at),
				  subscribers = COALESCE(excluded.subscribers, subscribers)`,
				c.FeedID, c.Client, c.At.UnixMilli(), c.Subscribers); err != nil {
				return err
			}
		}
		for day, counters := range b.Daily {
			for key, delta := range counters {
				if _, err := tx.Exec(`INSERT INTO daily_stats (day, key, value) VALUES (?, ?, ?)
					ON CONFLICT(day, key) DO UPDATE SET value = value + excluded.value`, day, key, delta); err != nil {
					return err
				}
			}
		}
		if len(b.Feeds) > 0 {
			_, err := tx.Exec(`INSERT INTO app_state (key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING`,
				activitySinceKey, clock.NowMs())
			return err
		}
		return nil
	})
	return err
}

// ActivitySince is when activity was first recorded; false before that.
func (s *Store) ActivitySince() (time.Time, bool) {
	var ms int64
	if !s.ReadState(activitySinceKey, &ms) {
		return time.Time{}, false
	}
	return fromMs(ms), true
}

// FeedActivity is a feed's measured use.
type FeedActivity struct {
	LastRequestedAt time.Time
	// Clients seen within the window, most recently seen first
	Clients []string
	// Subscribers sums the latest count each client in the window
	// reported; nil when none reported one
	Subscribers *int
}

// ReadFeedActivity returns every feed's activity; clients and subscriber
// counts only from sightings after since.
func (s *Store) ReadFeedActivity(since time.Time) (map[string]*FeedActivity, error) {
	activity := map[string]*FeedActivity{}
	rows, err := s.read.Query("SELECT feed_id, last_requested_at FROM feed_activity")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var (
			id string
			ms int64
		)
		if err := rows.Scan(&id, &ms); err != nil {
			rows.Close()
			return nil, err
		}
		activity[id] = &FeedActivity{LastRequestedAt: fromMs(ms)}
	}
	rows.Close()

	rows, err = s.read.Query(`SELECT feed_id, client, subscribers FROM feed_clients
		WHERE last_seen_at >= ? ORDER BY last_seen_at DESC`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, client  string
			subscribers sql.NullInt64
		)
		if err := rows.Scan(&id, &client, &subscribers); err != nil {
			return nil, err
		}
		a := activity[id]
		if a == nil {
			continue
		}
		a.Clients = append(a.Clients, client)
		if subscribers.Valid {
			total := int(subscribers.Int64)
			if a.Subscribers != nil {
				total += *a.Subscribers
			}
			a.Subscribers = &total
		}
	}
	return activity, rows.Err()
}

// ReadDailyStats returns the counters for days from fromDay on.
func (s *Store) ReadDailyStats(fromDay string) (map[string]map[string]int, error) {
	rows, err := s.read.Query("SELECT day, key, value FROM daily_stats WHERE day >= ?", fromDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := map[string]map[string]int{}
	for rows.Next() {
		var (
			day, key string
			value    int
		)
		if err := rows.Scan(&day, &key, &value); err != nil {
			return nil, err
		}
		if days[day] == nil {
			days[day] = map[string]int{}
		}
		days[day][key] = value
	}
	return days, rows.Err()
}

// PruneDailyStats deletes days before beforeDay.
func (s *Store) PruneDailyStats(beforeDay string) {
	if _, err := s.write.Exec("DELETE FROM daily_stats WHERE day < ?", beforeDay); err != nil {
		log.Printf("prune daily stats: %v", err)
	}
}
