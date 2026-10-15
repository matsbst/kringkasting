package store

import (
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
)

// Video metadata for episodes (docs/nrk-video-pass-through.md). The video
// columns are written only here; episode upserts leave them alone, and
// databases without them (or an older image ignoring them) keep working.

// VideoCandidate is a stored episode whose video is due for a check.
type VideoCandidate struct {
	SeriesID          string
	EpisodeID         string
	ClipID            string
	Date              time.Time
	DurationInSeconds int64
	VideoURL          *string
}

// SetClipIDs records NRK clip references seen in catalog listings for
// episodes already stored (new episodes get theirs when inserted).
func (s *Store) SetClipIDs(seriesID string, clipIDs map[string]string) {
	if len(clipIDs) == 0 {
		return
	}
	err := s.tx(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`UPDATE episodes SET clip_id = ?
			WHERE series_id = ? AND id = ? AND (clip_id IS NULL OR clip_id != ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for episodeID, clipID := range clipIDs {
			if _, err := stmt.Exec(clipID, seriesID, episodeID, clipID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("record clip ids for %s: %v", seriesID, err)
	}
}

// VideosDue returns up to limit episodes of a feed with a clip reference
// whose video hasn't been checked, or is due again; newest first.
func (s *Store) VideosDue(seriesID string, limit int) ([]VideoCandidate, error) {
	rows, err := s.read.Query(`SELECT id, clip_id, date, duration_seconds, video_url FROM episodes
		WHERE series_id = ? AND clip_id IS NOT NULL AND (video_next_check_at IS NULL OR video_next_check_at <= ?)
		ORDER BY date DESC LIMIT ?`, seriesID, clock.NowMs(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var due []VideoCandidate
	for rows.Next() {
		var (
			c        = VideoCandidate{SeriesID: seriesID}
			date     int64
			duration float64
			video    sql.NullString
		)
		if err := rows.Scan(&c.EpisodeID, &c.ClipID, &date, &duration, &video); err != nil {
			return nil, err
		}
		c.Date = fromMs(date)
		c.DurationInSeconds = int64(duration)
		c.VideoURL = nullString(video)
		due = append(due, c)
	}
	return due, rows.Err()
}

// KnownVideo returns a result already confirmed for the same clip in
// another feed (a season feed and its podcast share episodes), checked
// after `since`, so the clip isn't looked up twice.
func (s *Store) KnownVideo(clipID string, since time.Time) (url *string, nextCheck time.Time, found bool) {
	var (
		video sql.NullString
		next  int64
	)
	err := s.read.QueryRow(`SELECT video_url, video_next_check_at FROM episodes
		WHERE clip_id = ? AND video_next_check_at IS NOT NULL AND video_next_check_at > ?
		ORDER BY video_next_check_at DESC LIMIT 1`, clipID, since.UnixMilli()).Scan(&video, &next)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return nil, time.Time{}, false
	}
	return nullString(video), fromMs(next), true
}

// SetVideo stores a check's outcome: the playlist URL (nil = no matching
// video) and when to check again. The feed's data version is bumped only
// when the URL changes, so feed caches and validators move with it.
func (s *Store) SetVideo(seriesID, episodeID string, url *string, nextCheck time.Time) {
	result, err := s.write.Exec(`UPDATE episodes SET video_url = ?, video_next_check_at = ?
		WHERE series_id = ? AND id = ? AND video_url IS NOT ?`, url, ms(nextCheck), seriesID, episodeID, url)
	if err != nil {
		log.Printf("store video for %s/%s: %v", seriesID, episodeID, err)
		return
	}
	if n, _ := result.RowsAffected(); n > 0 {
		s.versions.bump(seriesID)
		return
	}
	// unchanged URL: only move the next check
	if _, err := s.write.Exec(`UPDATE episodes SET video_next_check_at = ? WHERE series_id = ? AND id = ?`,
		ms(nextCheck), seriesID, episodeID); err != nil {
		log.Printf("store video check for %s/%s: %v", seriesID, episodeID, err)
	}
}

// HasVideo reports whether any episode of the feed has a confirmed video.
func (s *Store) HasVideo(seriesID string) bool {
	var one int
	return s.read.QueryRow("SELECT 1 FROM episodes WHERE series_id = ? AND video_url IS NOT NULL LIMIT 1", seriesID).
		Scan(&one) == nil
}

// CatalogKindOf reads a feed's stored catalog kind without its episodes.
func (s *Store) CatalogKindOf(seriesID string) *CatalogKind {
	var kind sql.NullString
	if err := s.read.QueryRow("SELECT catalog_kind FROM series WHERE id = ?", seriesID).Scan(&kind); err != nil || !kind.Valid {
		return nil
	}
	k := CatalogKind(kind.String)
	return &k
}
