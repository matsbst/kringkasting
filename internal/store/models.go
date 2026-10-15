package store

import "time"

// Episode is one stored episode. ID is NRK's hash `id` (the feed GUID).
type Episode struct {
	ID                string
	Title             string
	Subtitle          *string
	URL               string
	ShareLink         string
	Date              time.Time
	DurationInSeconds int64
	// Bytes is the enclosure file size, when known.
	Bytes *int64
}

// CatalogKind is which NRK catalog a series lives in ("podcast" or
// "series"); it decides the endpoints used.
type CatalogKind string

const (
	KindPodcast CatalogKind = "podcast"
	KindSeries  CatalogKind = "series"
)

// Series is a feed: an NRK series/podcast, or one customSeason of an
// umbrella podcast (ID "{seriesId}/sesong/{seasonId}").
type Series struct {
	ID            string
	Title         string
	Subtitle      *string
	Link          string
	ImageURL      string
	LastFetchedAt time.Time
	// BacklogComplete is true once the full archive has been crawled.
	BacklogComplete bool
	// BacklogCursor is the NRK API href of the next backlog page to crawl.
	BacklogCursor *string
	CatalogKind   *CatalogKind
	// IsUmbrella shows list episodes per season and always take full refreshes.
	IsUmbrella bool
	// MetadataRefreshedAt is when title/artwork was last fetched.
	MetadataRefreshedAt *time.Time
	Episodes            []Episode
}

// SeriesMeta is freshness data, read without the episode list.
type SeriesMeta struct {
	ID              string
	LastFetchedAt   time.Time
	BacklogComplete bool
	NewestEpisodeAt *time.Time
}

// SeriesOverview is a row of the admin dashboard's series table.
type SeriesOverview struct {
	ID              string
	Title           string
	EpisodeCount    int
	BacklogComplete bool
	LastFetchedAt   time.Time
	CatalogKind     *string
}

// FailedEpisode is an episode under retry backoff.
type FailedEpisode struct {
	SeriesID    string
	EpisodeID   string
	Attempts    int
	NextRetryAt time.Time
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMs(v int64) time.Time { return time.UnixMilli(v).UTC() }
