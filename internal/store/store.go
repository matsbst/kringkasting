// Package store owns the SQLite database: opening it, the schema and its
// additive migrations, and every query. It is a port of lib/storage.ts and
// must stay compatible with databases written by the Deno version, in both
// directions.
//
// Connection policy: all writes go through a single connection, so a
// multi-statement transaction can never be split across connections and
// writers never contend. Reads use a separate small pool, which WAL mode
// lets run alongside the writer.
package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS app_state (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS series (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  subtitle TEXT,
  link TEXT NOT NULL,
  image_url TEXT NOT NULL,
  last_fetched_at INTEGER NOT NULL,
  backlog_complete INTEGER NOT NULL DEFAULT 0,
  backlog_cursor TEXT,
  catalog_kind TEXT,
  is_umbrella INTEGER NOT NULL DEFAULT 0,
  metadata_refreshed_at INTEGER
);
CREATE TABLE IF NOT EXISTS episodes (
  series_id TEXT NOT NULL,
  id TEXT NOT NULL,
  title TEXT NOT NULL,
  subtitle TEXT,
  url TEXT NOT NULL,
  share_link TEXT NOT NULL,
  date INTEGER NOT NULL,
  duration_seconds INTEGER NOT NULL,
  bytes INTEGER,
  PRIMARY KEY (series_id, id)
);
CREATE INDEX IF NOT EXISTS episodes_series_date ON episodes(series_id, date DESC);
CREATE TABLE IF NOT EXISTS failed_episodes (
  series_id TEXT NOT NULL,
  id TEXT NOT NULL,
  attempts INTEGER NOT NULL,
  next_retry_at INTEGER NOT NULL,
  PRIMARY KEY (series_id, id)
);
`

// additive migrations for databases created before these columns existed;
// "duplicate column" errors mean the column is already there
var migrations = []string{
	"ALTER TABLE series ADD COLUMN catalog_kind TEXT",
	"ALTER TABLE series ADD COLUMN is_umbrella INTEGER NOT NULL DEFAULT 0",
	"ALTER TABLE series ADD COLUMN metadata_refreshed_at INTEGER",
	// NRK video for an episode: the clip
	// reference from catalog listings, the confirmed HLS playlist URL, and
	// when to check again. NULL next-check = never checked.
	"ALTER TABLE episodes ADD COLUMN clip_id TEXT",
	"ALTER TABLE episodes ADD COLUMN video_url TEXT",
	"ALTER TABLE episodes ADD COLUMN video_next_check_at INTEGER",
	// lookups by clip (KnownVideo) and video counts per feed, which would
	// otherwise read every episode; partial, so they stay small
	"CREATE INDEX IF NOT EXISTS episodes_clip ON episodes(clip_id) WHERE clip_id IS NOT NULL",
	"CREATE INDEX IF NOT EXISTS episodes_video ON episodes(series_id) WHERE video_url IS NOT NULL",
}

const readPoolSize = 4

// Store is the application's database.
type Store struct {
	write *sql.DB // exactly one connection
	read  *sql.DB // small pool, query-only

	versions  versions
	fullReads atomic.Int64
}

// Open opens (creating if needed) the database at path. Every connection
// gets busy_timeout (and the read pool query_only) through the DSN, so they hold
// for connections the pool opens later too, not just the first one.
func Open(path string) (*Store, error) {
	write, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, err
	}
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)
	write.SetConnMaxLifetime(0)

	// journal_mode is persistent in the file; asserting it here also
	// converts databases that were never opened in WAL mode
	var mode string
	if err := write.QueryRow("PRAGMA journal_mode = WAL").Scan(&mode); err != nil {
		write.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		write.Close()
		return nil, fmt.Errorf("enable WAL: journal mode is %q", mode)
	}
	if _, err := write.Exec(schema + activitySchema); err != nil {
		write.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	for _, migration := range migrations {
		if _, err := write.Exec(migration); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			write.Close()
			return nil, fmt.Errorf("migrate %q: %w", migration, err)
		}
	}

	read, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		write.Close()
		return nil, err
	}
	read.SetMaxOpenConns(readPoolSize)
	read.SetMaxIdleConns(readPoolSize)

	return &Store{write: write, read: read}, nil
}

func dsn(path string, readOnly bool) string {
	params := url.Values{}
	params.Add("_pragma", "busy_timeout(5000)")
	if readOnly {
		params.Add("_pragma", "query_only(1)")
	}
	// a URI: "?", "#" and "%" in the path must not start the query,
	// a fragment or an escape
	escaped := strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(path)
	return "file:" + escaped + "?" + params.Encode()
}

// Close closes both pools. The write connection closes last so SQLite can
// checkpoint the WAL on the final close.
func (s *Store) Close() error {
	readErr := s.read.Close()
	if err := s.write.Close(); err != nil {
		return err
	}
	return readErr
}
