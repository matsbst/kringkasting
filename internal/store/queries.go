package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
)

const (
	dayMs               = 24 * 60 * 60 * 1000
	maxRetryBackoffDays = 30
)

// versions is an in-memory per-series data version, bumped on every
// content write. It lets the feed cache know its rendered XML is out of
// date without touching the database. Resets on restart, as the cache does.
// Versions start at 1, so 0 never matches a current version.
type versions struct {
	mu sync.Mutex
	m  map[string]int64
}

func (v *versions) bump(id string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.m == nil {
		v.m = map[string]int64{}
	}
	v.m[id]++
}

func (v *versions) get(id string) int64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.m[id] + 1
}

// DataVersion is the series' in-memory content version.
func (s *Store) DataVersion(seriesID string) int64 { return s.versions.get(seriesID) }

// HasSeries is a cheap existence check without loading episodes.
func (s *Store) HasSeries(id string) bool {
	var one int
	return s.read.QueryRow("SELECT 1 FROM series WHERE id = ?", id).Scan(&one) == nil
}

// ReadSeriesMeta reads freshness data without the (possibly huge) episode list.
func (s *Store) ReadSeriesMeta(id string) (*SeriesMeta, error) {
	var lastFetched, complete int64
	err := s.read.QueryRow("SELECT last_fetched_at, backlog_complete FROM series WHERE id = ?", id).
		Scan(&lastFetched, &complete)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var newest sql.NullInt64
	if err := s.read.QueryRow("SELECT MAX(date) FROM episodes WHERE series_id = ?", id).Scan(&newest); err != nil {
		return nil, err
	}
	meta := &SeriesMeta{ID: id, LastFetchedAt: fromMs(lastFetched), BacklogComplete: complete == 1}
	if newest.Valid {
		t := fromMs(newest.Int64)
		meta.NewestEpisodeAt = &t
	}
	return meta, nil
}

// FullReadCount is how many times a full episode list was read (tests).
func (s *Store) FullReadCount() int64 { return s.fullReads.Load() }

// ReadSeries reads a series with all its episodes, newest first.
func (s *Store) ReadSeries(id string) (*Series, error) {
	s.fullReads.Add(1)
	// snapshot before reading: a write landing during the read moves the
	// version past it, so a render of this read can't pass for newer data
	version := s.versions.get(id)
	var (
		series                          Series
		subtitle, cursor, kind          sql.NullString
		lastFetched, complete, umbrella int64
		metadataRefreshed               sql.NullInt64
	)
	err := s.read.QueryRow(`SELECT id, title, subtitle, link, image_url, last_fetched_at, backlog_complete,
		backlog_cursor, catalog_kind, is_umbrella, metadata_refreshed_at FROM series WHERE id = ?`, id).
		Scan(&series.ID, &series.Title, &subtitle, &series.Link, &series.ImageURL, &lastFetched, &complete,
			&cursor, &kind, &umbrella, &metadataRefreshed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	series.Subtitle = nullString(subtitle)
	series.LastFetchedAt = fromMs(lastFetched)
	series.BacklogComplete = complete == 1
	series.BacklogCursor = nullString(cursor)
	if kind.Valid {
		k := CatalogKind(kind.String)
		series.CatalogKind = &k
	}
	series.IsUmbrella = umbrella == 1
	if metadataRefreshed.Valid {
		t := fromMs(metadataRefreshed.Int64)
		series.MetadataRefreshedAt = &t
	}

	rows, err := s.read.Query(`SELECT id, title, subtitle, url, share_link, date, duration_seconds, bytes,
		clip_id, video_url FROM episodes WHERE series_id = ? ORDER BY date DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	series.Episodes = []Episode{}
	for rows.Next() {
		var (
			e        Episode
			sub      sql.NullString
			date     int64
			duration float64 // REAL in rows written from fractional JS numbers
			bytes    sql.NullInt64
			clip     sql.NullString
			video    sql.NullString
		)
		if err := rows.Scan(&e.ID, &e.Title, &sub, &e.URL, &e.ShareLink, &date, &duration, &bytes, &clip, &video); err != nil {
			return nil, err
		}
		e.Subtitle = nullString(sub)
		e.ClipID = nullString(clip)
		e.VideoURL = nullString(video)
		e.Date = fromMs(date)
		e.DurationInSeconds = int64(math.Round(duration))
		if bytes.Valid {
			b := bytes.Int64
			e.Bytes = &b
		}
		series.Episodes = append(series.Episodes, e)
	}
	series.Version = version
	return &series, rows.Err()
}

func nullString(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

// WriteSeries upserts the series metadata (refreshing last_fetched_at and
// stamping the metadata fresh) and adds/refreshes its episodes. Stored
// episodes missing from series.Episodes are kept: that is how the archive
// accumulates beyond NRK's page size. Backlog state is not touched.
func (s *Store) WriteSeries(series *Series) bool {
	var kind any
	if series.CatalogKind != nil {
		kind = string(*series.CatalogKind)
	}
	err := s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
    INSERT INTO series (id, title, subtitle, link, image_url, last_fetched_at, catalog_kind, is_umbrella, metadata_refreshed_at)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(id) DO UPDATE SET
      title = excluded.title,
      subtitle = excluded.subtitle,
      link = excluded.link,
      image_url = excluded.image_url,
      last_fetched_at = excluded.last_fetched_at,
      catalog_kind = COALESCE(excluded.catalog_kind, series.catalog_kind),
      is_umbrella = excluded.is_umbrella,
      metadata_refreshed_at = excluded.metadata_refreshed_at`,
			series.ID, series.Title, series.Subtitle, series.Link, series.ImageURL, ms(series.LastFetchedAt),
			kind, boolInt(series.IsUmbrella), clock.NowMs()); err != nil {
			return err
		}
		return insertEpisodes(tx, series.ID, series.Episodes)
	})
	if err != nil {
		log.Printf("Failed to write series %s: %v", series.ID, err)
		return false
	}
	s.versions.bump(series.ID)
	return true
}

// AddEpisodes adds episodes to an existing series (the backlog crawler).
func (s *Store) AddEpisodes(seriesID string, episodes []Episode) bool {
	err := s.tx(func(tx *sql.Tx) error { return insertEpisodes(tx, seriesID, episodes) })
	if err != nil {
		log.Printf("Failed to add episodes to %s: %v", seriesID, err)
		return false
	}
	s.versions.bump(seriesID)
	return true
}

func insertEpisodes(tx *sql.Tx, seriesID string, episodes []Episode) error {
	stmt, err := tx.Prepare(`
    INSERT INTO episodes (series_id, id, title, subtitle, url, share_link, date, duration_seconds, bytes, clip_id)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(series_id, id) DO UPDATE SET
      title = excluded.title,
      subtitle = excluded.subtitle,
      url = excluded.url,
      share_link = excluded.share_link,
      date = excluded.date,
      duration_seconds = excluded.duration_seconds,
      bytes = COALESCE(excluded.bytes, episodes.bytes),
      clip_id = COALESCE(excluded.clip_id, episodes.clip_id)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range episodes {
		if _, err := stmt.Exec(seriesID, e.ID, e.Title, e.Subtitle, e.URL, e.ShareLink, ms(e.Date),
			e.DurationInSeconds, e.Bytes, e.ClipID); err != nil {
			return err
		}
	}
	return nil
}

// ReadEpisodeIDs returns the ids of a series' stored episodes.
func (s *Store) ReadEpisodeIDs(seriesID string) (map[string]bool, error) {
	return s.idSet("SELECT id FROM episodes WHERE series_id = ?", seriesID)
}

// ReadNrkEpisodeID returns NRK's own episode id (`episodeId`, e.g. "l_…"
// or "DMPO…") for a stored episode. Feeds identify episodes by NRK's hash
// `id`, but NRK's episode endpoint only answers to `episodeId`, which
// every share link ends with. Season episodes are stored under
// "{seriesId}/sesong/{seasonId}", so the parent series id matches them
// too. Empty when the episode isn't stored.
func (s *Store) ReadNrkEpisodeID(seriesID, id string) string {
	var shareLink string
	// two primary-key lookups: the series itself, then its seasons as a
	// key range ("0" sorts right after "/")
	err := s.read.QueryRow("SELECT share_link FROM episodes WHERE series_id = ? AND id = ?", seriesID, id).Scan(&shareLink)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.read.QueryRow(
			"SELECT share_link FROM episodes WHERE series_id >= ? AND series_id < ? AND id = ? LIMIT 1",
			seriesID+"/sesong/", seriesID+"/sesong0", id).Scan(&shareLink)
	}
	if err != nil {
		return ""
	}
	return shareLink[strings.LastIndex(shareLink, "/")+1:]
}

// TouchLastFetched renews the freshness timestamp without touching
// metadata (episodes-only refresh).
func (s *Store) TouchLastFetched(seriesID string) {
	if _, err := s.write.Exec("UPDATE series SET last_fetched_at = ? WHERE id = ?", clock.NowMs(), seriesID); err != nil {
		log.Printf("touch %s: %v", seriesID, err)
	}
}

// StreamOnlySeriesIDs returns which of the given series are stream-only
// (have episodes, all HLS), in one query. Unknown series are absent.
func (s *Store) StreamOnlySeriesIDs(ids []string) map[string]bool {
	result := map[string]bool{}
	if len(ids) == 0 {
		return result
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.read.Query(fmt.Sprintf(`
    SELECT series_id FROM episodes
    WHERE series_id IN (%s)
    GROUP BY series_id
    HAVING SUM(CASE WHEN url LIKE '%%.m3u8%%' THEN 0 ELSE 1 END) = 0`,
		strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")), args...)
	if err != nil {
		log.Printf("stream-only lookup: %v", err)
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			result[id] = true
		}
	}
	return result
}

// SetBacklogState saves the crawl checkpoint.
func (s *Store) SetBacklogState(seriesID string, cursor *string, complete bool) {
	if _, err := s.write.Exec("UPDATE series SET backlog_cursor = ?, backlog_complete = ? WHERE id = ?",
		cursor, boolInt(complete), seriesID); err != nil {
		log.Printf("set backlog state %s: %v", seriesID, err)
	}
}

// RecordEpisodeFailure puts an episode whose playback manifest failed
// (geo-blocked, expired, removed) under exponential backoff: 1 day, 2, 4,
// …, capped at 30, instead of retrying on every refresh.
func (s *Store) RecordEpisodeFailure(seriesID, episodeID string) {
	err := s.tx(func(tx *sql.Tx) error {
		var attempts int64
		err := tx.QueryRow("SELECT attempts FROM failed_episodes WHERE series_id = ? AND id = ?", seriesID, episodeID).
			Scan(&attempts)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		attempts++
		delayDays := min(maxRetryBackoffDays, int64(1)<<min(attempts-1, 30))
		_, err = tx.Exec(`
      INSERT INTO failed_episodes (series_id, id, attempts, next_retry_at) VALUES (?, ?, ?, ?)
      ON CONFLICT(series_id, id) DO UPDATE SET attempts = excluded.attempts, next_retry_at = excluded.next_retry_at`,
			seriesID, episodeID, attempts, clock.NowMs()+delayDays*dayMs)
		return err
	})
	if err != nil {
		log.Printf("record failure %s/%s: %v", seriesID, episodeID, err)
	}
}

// ReadBlockedEpisodeIDs returns failed episodes whose retry window has not passed.
func (s *Store) ReadBlockedEpisodeIDs(seriesID string) (map[string]bool, error) {
	return s.idSet("SELECT id FROM failed_episodes WHERE series_id = ? AND next_retry_at > ?", seriesID, clock.NowMs())
}

func (s *Store) idSet(query string, args ...any) (map[string]bool, error) {
	rows, err := s.read.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

// DeleteStaleSeries deletes series nobody has requested for maxAge.
func (s *Store) DeleteStaleSeries(maxAge time.Duration) int {
	cutoff := clock.NowMs() - maxAge.Milliseconds()
	stale, err := s.idSet("SELECT id FROM series WHERE last_fetched_at < ?", cutoff)
	if err != nil || len(stale) == 0 {
		return 0
	}
	err = s.tx(func(tx *sql.Tx) error {
		for id := range stale {
			for _, q := range []string{
				"DELETE FROM episodes WHERE series_id = ?",
				"DELETE FROM failed_episodes WHERE series_id = ?",
				"DELETE FROM series WHERE id = ?",
			} {
				if _, err := tx.Exec(q, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("Garbage collection failed: %v", err)
		return 0
	}
	for id := range stale {
		s.versions.bump(id)
	}
	return len(stale)
}

// ListSeriesOverview returns everything the admin series table needs.
func (s *Store) ListSeriesOverview() ([]SeriesOverview, error) {
	rows, err := s.read.Query(`
    SELECT s.id, s.title, s.backlog_complete, s.last_fetched_at, s.catalog_kind, s.backlog_cursor,
           (SELECT COUNT(*) FROM episodes e WHERE e.series_id = s.id) AS episode_count,
           (SELECT MIN(e.date) FROM episodes e WHERE e.series_id = s.id) AS oldest,
           (SELECT COUNT(*) FROM episodes e WHERE e.series_id = s.id AND e.video_url IS NOT NULL) AS videos
    FROM series s
    ORDER BY s.last_fetched_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []SeriesOverview
	for rows.Next() {
		var (
			o                     SeriesOverview
			complete, lastFetched int64
			kind, cursor          sql.NullString
			oldest                sql.NullInt64
		)
		if err := rows.Scan(&o.ID, &o.Title, &complete, &lastFetched, &kind, &cursor,
			&o.EpisodeCount, &oldest, &o.VideoCount); err != nil {
			return nil, err
		}
		o.BacklogComplete = complete == 1
		o.LastFetchedAt = fromMs(lastFetched)
		o.CatalogKind = nullString(kind)
		o.BacklogCursor = nullString(cursor)
		if oldest.Valid {
			t := fromMs(oldest.Int64)
			o.OldestEpisode = &t
		}
		list = append(list, o)
	}
	return list, rows.Err()
}

// ListFailedEpisodes returns episodes under backoff, soonest retry first.
func (s *Store) ListFailedEpisodes(limit int) ([]FailedEpisode, error) {
	rows, err := s.read.Query(
		"SELECT series_id, id, attempts, next_retry_at FROM failed_episodes ORDER BY next_retry_at LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []FailedEpisode
	for rows.Next() {
		var (
			f    FailedEpisode
			next int64
		)
		if err := rows.Scan(&f.SeriesID, &f.EpisodeID, &f.Attempts, &next); err != nil {
			return nil, err
		}
		f.NextRetryAt = fromMs(next)
		list = append(list, f)
	}
	return list, rows.Err()
}

// CountEpisodes counts all stored episodes.
func (s *Store) CountEpisodes() int {
	var n int
	_ = s.read.QueryRow("SELECT COUNT(*) FROM episodes").Scan(&n)
	return n
}

// DatabaseSizeBytes is page_count × page_size.
func (s *Store) DatabaseSizeBytes() int64 {
	var pages, size int64
	_ = s.read.QueryRow("PRAGMA page_count").Scan(&pages)
	_ = s.read.QueryRow("PRAGMA page_size").Scan(&size)
	return pages * size
}

// DeleteSeriesByID deletes one series and everything belonging to it (admin).
func (s *Store) DeleteSeriesByID(seriesID string) {
	err := s.tx(func(tx *sql.Tx) error {
		for _, q := range []string{
			"DELETE FROM episodes WHERE series_id = ?",
			"DELETE FROM failed_episodes WHERE series_id = ?",
			"DELETE FROM series WHERE id = ?",
			"DELETE FROM feed_activity WHERE feed_id = ?",
			"DELETE FROM feed_clients WHERE feed_id = ?",
		} {
			if _, err := tx.Exec(q, seriesID); err != nil {
				return err
			}
		}
		_, err := tx.Exec("DELETE FROM app_state WHERE key IN (?, ?, ?)",
			"refresh-progress:"+seriesID, "retry:refresh:"+seriesID, "retry:backlog:"+seriesID)
		return err
	})
	if err != nil {
		log.Printf("Failed to delete series %s: %v", seriesID, err)
		return
	}
	s.versions.bump(seriesID)
}

// ExpireSeries marks a series stale so the next request refreshes it
// (admin). 9 days back beats every adaptive refresh interval while staying
// far from the 90-day GC cutoff.
func (s *Store) ExpireSeries(seriesID string) {
	if _, err := s.write.Exec("UPDATE series SET last_fetched_at = ? WHERE id = ?",
		clock.NowMs()-9*dayMs, seriesID); err != nil {
		log.Printf("expire %s: %v", seriesID, err)
	}
}

// ReadState decodes a durable cache/checkpoint value into dst; false when
// the key is absent or unreadable.
func (s *Store) ReadState(key string, dst any) bool {
	var value string
	if err := s.read.QueryRow("SELECT value FROM app_state WHERE key = ?", key).Scan(&value); err != nil {
		return false
	}
	return json.Unmarshal([]byte(value), dst) == nil
}

// HasState reports whether a durable state key exists.
func (s *Store) HasState(key string) bool {
	var raw json.RawMessage
	return s.ReadState(key, &raw)
}

// WriteState stores a value as JSON, the same encoding the Deno version used.
func (s *Store) WriteState(key string, value any) {
	encoded, err := MarshalJSON(value)
	if err != nil {
		log.Printf("encode state %s: %v", key, err)
		return
	}
	if _, err := s.write.Exec(
		"INSERT INTO app_state (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, encoded); err != nil {
		log.Printf("write state %s: %v", key, err)
	}
}

// DeleteExpiredState removes the keys under prefix whose JSON expiresAt
// (Unix ms) has passed and whose status is status.
func (s *Store) DeleteExpiredState(prefix string, status int) int {
	// a key range rather than LIKE, so the primary key index is used
	end := prefix[:len(prefix)-1] + string(prefix[len(prefix)-1]+1)
	result, err := s.write.Exec(`DELETE FROM app_state WHERE key >= ? AND key < ?
		AND json_extract(value, '$.status') = ? AND json_extract(value, '$.expiresAt') < ?`,
		prefix, end, status, clock.NowMs())
	if err != nil {
		log.Printf("delete expired %s state: %v", prefix, err)
		return 0
	}
	deleted, _ := result.RowsAffected()
	return int(deleted)
}

// DeleteState removes a durable state key.
func (s *Store) DeleteState(key string) {
	if _, err := s.write.Exec("DELETE FROM app_state WHERE key = ?", key); err != nil {
		log.Printf("delete state %s: %v", key, err)
	}
}

// tx runs fn in a transaction on the single write connection.
func (s *Store) tx(fn func(*sql.Tx) error) error {
	tx, err := s.write.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
