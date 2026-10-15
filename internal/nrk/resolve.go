package nrk

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/nrklink"
	"github.com/matsbst/kringkasting/internal/store"
)

// Show is a podcast, radio series or season a pasted NRK link resolves to.
type Show struct {
	SeriesID    string
	SeasonID    string // set for a season feed
	Title       string
	Description *string
	ImageURL    string
	// Umbrella: the series publishes standalone shows as seasons
	Umbrella bool
}

// Resolution is what a link resolves to: the season (when the link is to
// a season, or to an episode of an umbrella podcast's season) followed by
// the whole podcast or series.
type Resolution struct {
	Shows []Show
	// Episode: the link pointed at a single episode.
	Episode bool
}

const resolveCacheTTL = 5 * time.Minute

type resolveEntry struct {
	expires time.Time
	result  Resolution
}

// resolveCache shares the search micro-cache's lifetime; links are pasted
// and shared, so the same one tends to arrive more than once.
type resolveCache struct {
	mu      sync.Mutex
	entries map[nrklink.Link]resolveEntry
}

func (r *resolveCache) get(link nrklink.Link) (Resolution, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[link]
	if !ok || !e.expires.After(clock.Now()) {
		return Resolution{}, false
	}
	return e.result, true
}

func (r *resolveCache) put(link nrklink.Link, result Resolution) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil || len(r.entries) >= searchCacheMaxEntries {
		r.entries = map[nrklink.Link]resolveEntry{}
	}
	r.entries[link] = resolveEntry{expires: clock.Now().Add(resolveCacheTTL), result: result}
}

// closestImage picks the variant closest to the wanted rendered width,
// preferring square artwork.
func closestImage(square, wide []image, wanted int) string {
	images := square
	if len(images) == 0 {
		images = wide
	}
	if len(images) == 0 {
		return ""
	}
	best := images[0]
	for _, candidate := range images[1:] {
		if abs(candidate.Width-wanted) < abs(best.Width-wanted) {
			best = candidate
		}
	}
	return best.URL
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// show fetches a podcast or radio series' metadata.
func (c *Client) show(kind store.CatalogKind, seriesID string) (*Show, store.CatalogKind, Outcome) {
	transient := false
	for _, k := range catalogOrder(&kind) {
		var body seriesResponse
		status, ok := c.up.getJSON(API+"/radio/catalog/"+string(k)+"/"+seriesID, &body)
		transient = transient || isTransient(status)
		if status == http.StatusOK && ok && body.Series != nil {
			s := body.Series
			return &Show{SeriesID: s.ID, Title: s.Titles.Title, Description: s.Titles.Subtitle,
				ImageURL: closestImage(s.SquareImage, s.Image, 400), Umbrella: body.SeriesType == "umbrella"}, k, Found
		}
	}
	if transient {
		return nil, kind, Unavailable
	}
	return nil, kind, NotFound
}

// season fetches one season's metadata.
func (c *Client) season(kind store.CatalogKind, seriesID, seasonID string) (*Show, Outcome) {
	var body seasonResponse
	status, ok := c.up.getJSON(API+"/radio/catalog/"+string(kind)+"/"+seriesID+"/seasons/"+seasonID, &body)
	switch {
	case status == http.StatusOK && ok:
		return &Show{SeriesID: seriesID, SeasonID: seasonID, Title: body.Titles.Title, Description: body.Titles.Subtitle,
			ImageURL: closestImage(body.SquareImage, body.Image, 400)}, Found
	case isTransient(status):
		return nil, Unavailable
	}
	return nil, NotFound
}

// episodeSeason returns the season an episode belongs to ("" if none).
func (c *Client) episodeSeason(kind store.CatalogKind, seriesID, episodeID string) (string, Outcome) {
	var body EpisodeDetail
	status, ok := c.up.getJSON(API+"/radio/catalog/"+string(kind)+"/"+seriesID+"/episodes/"+episodeID, &body)
	switch {
	case status == http.StatusOK && ok:
		if body.Links.Season == nil {
			return "", Found
		}
		href := strings.TrimSuffix(body.Links.Season.Href, "/")
		return href[strings.LastIndex(href, "/")+1:], Found
	case isTransient(status):
		return "", Unavailable
	}
	return "", NotFound
}

// Resolve turns a pasted NRK link into the feeds it can be subscribed as.
// Only NRK's API is called, with ids already validated by nrklink, through
// the same concurrency cap, retry deadlines and cache lifetime as search.
// NotFound means NRK doesn't know the podcast or series.
func (c *Client) Resolve(link nrklink.Link) (Resolution, Outcome) {
	if cached, ok := c.resolved.get(link); ok {
		return cached, Found
	}
	preferred := store.KindPodcast
	if link.Kind == nrklink.Series {
		preferred = store.KindSeries
	}

	series, kind, outcome := c.show(preferred, link.SeriesID)
	if outcome != Found {
		return Resolution{}, outcome
	}
	result := Resolution{Episode: link.EpisodeID != ""}

	seasonID := link.SeasonID
	if seasonID == "" && link.EpisodeID != "" && series.Umbrella {
		// an umbrella podcast publishes standalone shows as seasons: an
		// episode link offers its own show first
		id, outcome := c.episodeSeason(kind, link.SeriesID, link.EpisodeID)
		if outcome == Unavailable {
			return Resolution{}, Unavailable
		}
		seasonID = id
	}
	if seasonID != "" {
		season, outcome := c.season(kind, link.SeriesID, seasonID)
		if outcome == Unavailable {
			return Resolution{}, Unavailable
		}
		if season != nil {
			result.Shows = append(result.Shows, *season)
		}
	}
	result.Shows = append(result.Shows, *series)
	c.resolved.put(link, result)
	return result, Found
}
