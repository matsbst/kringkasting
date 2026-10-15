// Package nrk is the client for NRK's public podcast/radio API
// (psapi.nrk.no): catalog listings, playback manifests, single episodes
// and search. A port of lib/nrk/nrk.ts and lib/http.ts.
package nrk

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/feedid"
	"github.com/matsbst/kringkasting/internal/retry"
	"github.com/matsbst/kringkasting/internal/stats"
	"github.com/matsbst/kringkasting/internal/store"
)

const (
	// API is NRK's API origin.
	API = "https://psapi.nrk.no"
	// fetchConcurrency bounds manifest lookups per interactive request,
	// so a large series doesn't fire hundreds of requests at once.
	fetchConcurrency = 6
	// backlogConcurrency: the background crawl is gentler.
	backlogConcurrency = 3

	searchCacheTTL        = 5 * time.Minute
	searchCacheMaxEntries = 200
	// MaxSearchQueryLength clamps queries here so no entry point can forget it.
	MaxSearchQueryLength = 100
)

// Client talks to NRK. Construct with New.
type Client struct {
	up *upstream

	searchMu    sync.Mutex
	searchCache map[string]searchEntry
	searchOrder []string // insertion order, for oldest-first eviction

	resolved resolveCache
}

type searchEntry struct {
	expires time.Time
	result  []SearchResult
}

// New returns a client; transport may be nil (http.DefaultTransport).
func New(transport http.RoundTripper, r *retry.Retry, s *stats.Stats) *Client {
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Client{
		up:          &upstream{client: &http.Client{Transport: transport}, retry: r, stats: s},
		searchCache: map[string]searchEntry{},
	}
}

// GetJSON fetches any NRK API URL (the catalog loader uses it).
func (c *Client) GetJSON(rawURL string, dst any) (status int, ok bool) {
	return c.up.getJSON(rawURL, dst)
}

// OnEpisodeFailure is told when an episode is definitively unavailable
// (for retry backoff).
type OnEpisodeFailure func(episodeID string)

// FetchOptions mirror the Deno version's options object.
type FetchOptions struct {
	MetadataOnly bool
	// SkipEpisodeIDs are episodes not to resolve (already stored, or under
	// failure backoff).
	SkipEpisodeIDs map[string]bool
	// StopEpisodeIDs end a newest-first walk (nil = use SkipEpisodeIDs).
	StopEpisodeIDs   map[string]bool
	CursorHref       *string
	OnEpisodeFailure OnEpisodeFailure
	// CatalogKind is the stored kind: try that endpoint family first.
	CatalogKind *store.CatalogKind
	// OnClipIDs is told the NRK clip reference of every listed episode,
	// including ones skipped as already stored (episode id → clip id).
	OnClipIDs func(map[string]string)
}

// reportClipIDs passes listed episodes' clip references to the callback.
func reportClipIDs(onClipIDs func(map[string]string), candidates []Episode) {
	if onClipIDs == nil {
		return
	}
	if ids := clipIDs(candidates); len(ids) > 0 {
		onClipIDs(ids)
	}
}

func clipIDs(candidates []Episode) map[string]string {
	ids := map[string]string{}
	for _, e := range candidates {
		if e.ClipID != "" {
			ids[e.ID] = e.ClipID
		}
	}
	return ids
}

// Outcome distinguishes "not found" from an upstream failure.
type Outcome int

const (
	Found Outcome = iota
	NotFound
	Unavailable // 5xx/timeouts/429: retry later, don't treat as gone
)

func catalogOrder(preferred *store.CatalogKind) []store.CatalogKind {
	if preferred != nil && *preferred == store.KindSeries {
		return []store.CatalogKind{store.KindSeries, store.KindPodcast}
	}
	return []store.CatalogKind{store.KindPodcast, store.KindSeries}
}

func isTransient(status int) bool { return status == 0 || status == 429 || status >= 500 }

// IsHLS detects HLS stream URLs. NRK serves some radio programs only as
// HLS (.m3u8), which most podcast apps can't play as an enclosure.
func IsHLS(url string) bool { return strings.Contains(url, ".m3u8") }

// mapConcurrent runs fn over items with at most limit in flight,
// preserving order.
func mapConcurrent[T, R any](items []T, limit int, fn func(T) R) []R {
	results := make([]R, len(items))
	if len(items) == 0 {
		return results
	}
	var (
		mu   sync.Mutex
		next int
		wg   sync.WaitGroup
	)
	for range min(limit, len(items)) {
		wg.Go(func() {
			for {
				mu.Lock()
				index := next
				next++
				mu.Unlock()
				if index >= len(items) {
					return
				}
				results[index] = fn(items[index])
			}
		})
	}
	wg.Wait()
	return results
}

// ParseDate parses NRK's dates the way JavaScript's Date constructor did.
func ParseDate(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC()
		}
	}
	return time.UnixMilli(0).UTC()
}

// ParseEpisode turns a resolved NRK episode into a stored episode.
func ParseEpisode(e episodeWithMedia) store.Episode {
	share := ""
	if e.Links.Share != nil {
		share = e.Links.Share.Href
	}
	episode := store.Episode{
		ID: e.ID, Title: e.Titles.Title, Subtitle: e.Titles.Subtitle, URL: e.URL, ShareLink: share,
		Date: ParseDate(e.Date), DurationInSeconds: e.DurationInSeconds, Bytes: e.Bytes,
	}
	if e.ClipID != "" {
		clip := e.ClipID
		episode.ClipID = &clip
	}
	return episode
}

func parseEpisodes(episodes []episodeWithMedia) []store.Episode {
	out := make([]store.Episode, len(episodes))
	for i, e := range episodes {
		out[i] = ParseEpisode(e)
	}
	return out
}

func lastImage(images []image) string {
	if len(images) == 0 {
		return ""
	}
	return images[len(images)-1].URL
}

type mediaResult int

const (
	mediaOK mediaResult = iota
	mediaGone
	mediaTransient
)

// episodeWithDownloadLink resolves the episode's download link. Gone when
// NRK definitively has no playable manifest (404, or 200 with
// playable=null: geo-blocked, expired, removed), transient on timeouts
// and 5xx.
func (c *Client) episodeWithDownloadLink(e Episode, kind string) (episodeWithMedia, mediaResult) {
	endpoint := "podcast"
	if kind == "series" {
		endpoint = "program"
	}
	var m manifest
	status, _ := c.up.getJSON(API+"/playback/manifest/"+endpoint+"/"+e.EpisodeID, &m)

	// prefer a progressive download (MP3) over an HLS stream: some radio
	// programs offer both, and only the file works as a podcast enclosure
	url := ""
	if m.Playable != nil {
		for _, asset := range m.Playable.Assets {
			if asset != nil && asset.URL != "" && !IsHLS(asset.URL) {
				url = asset.URL
				break
			}
		}
		if url == "" && len(m.Playable.Assets) > 0 && m.Playable.Assets[0] != nil {
			url = m.Playable.Assets[0].URL
		}
	}
	if status == http.StatusOK && url != "" {
		var bytes *int64
		// HLS playlists have no meaningful file size; only HEAD real files
		if !IsHLS(url) {
			_, bytes = c.up.head(url)
		}
		return episodeWithMedia{Episode: e, URL: url, Bytes: bytes}, mediaOK
	}

	log.Printf("No playable manifest for episode %s (status %d)", e.EpisodeID, status)
	if status == http.StatusNotFound || status == http.StatusOK {
		return episodeWithMedia{}, mediaGone
	}
	return episodeWithMedia{}, mediaTransient
}

type resolved struct {
	media  episodeWithMedia
	result mediaResult
}

func (c *Client) resolveAll(candidates []Episode, limit int, kind string) []resolved {
	return mapConcurrent(candidates, limit, func(e Episode) resolved {
		media, result := c.episodeWithDownloadLink(e, kind)
		return resolved{media, result}
	})
}

// collect separates playable episodes from failures: definitive ones are
// reported for backoff, transient ones counted so the caller can retry.
func collect(candidates []Episode, results []resolved, onFailure OnEpisodeFailure) (episodes []episodeWithMedia, transient int) {
	episodes = []episodeWithMedia{}
	for i, r := range results {
		switch r.result {
		case mediaGone:
			if onFailure != nil {
				onFailure(candidates[i].ID)
			}
		case mediaTransient:
			transient++
		default:
			episodes = append(episodes, r.media)
		}
	}
	return episodes, transient
}

func filterSkipped(candidates []Episode, skip map[string]bool) []Episode {
	if skip == nil {
		return candidates
	}
	out := []Episode{}
	for _, e := range candidates {
		if !skip[e.ID] {
			out = append(out, e)
		}
	}
	return out
}

// GetSeries fetches a series with episode download links. With
// SkipEpisodeIDs the result holds only episodes NOT in that set (an
// incremental update the caller merges with what it has).
func (c *Client) GetSeries(seriesID string, opts FetchOptions) (*store.Series, Outcome) {
	var (
		episodeStatus, seriesStatus int
		episodesBody                episodesPage
		seriesBody                  seriesResponse
		usedKind                    = store.KindPodcast
		transient                   bool
	)

	// the stored catalog kind goes first, so wrong-family requests are only
	// spent on brand-new series (or ones NRK has recatalogued)
	for _, kind := range catalogOrder(opts.CatalogKind) {
		episodesBody, seriesBody = episodesPage{}, seriesResponse{}
		var wg sync.WaitGroup
		if opts.MetadataOnly {
			episodeStatus = http.StatusOK
		} else {
			wg.Go(func() {
				episodeStatus, _ = c.up.getJSON(API+"/radio/catalog/"+string(kind)+"/"+seriesID+"/episodes", &episodesBody)
			})
		}
		wg.Go(func() {
			seriesStatus, _ = c.up.getJSON(API+"/radio/catalog/"+string(kind)+"/"+seriesID, &seriesBody)
		})
		wg.Wait()
		transient = transient || isTransient(episodeStatus) || isTransient(seriesStatus)
		usedKind = kind
		if episodeStatus == http.StatusOK && seriesStatus == http.StatusOK {
			break
		}
	}

	if episodeStatus == http.StatusOK && seriesStatus == http.StatusOK && seriesBody.Series != nil &&
		(opts.MetadataOnly || len(episodesBody.Embedded.Episodes) > 0) {
		episodes := []episodeWithMedia{}
		if !opts.MetadataOnly {
			episodes = c.extractEpisodes(&seriesBody, &episodesBody, opts.SkipEpisodeIDs, opts.OnEpisodeFailure, opts.OnClipIDs)
		}
		kind := usedKind
		s := seriesBody.Series
		return &store.Series{
			ID: s.ID, Title: s.Titles.Title, Subtitle: s.Titles.Subtitle,
			Link:          "https://radio.nrk.no/podkast/" + s.ID,
			ImageURL:      lastImage(s.SquareImage),
			LastFetchedAt: clock.Now(),
			CatalogKind:   &kind,
			IsUmbrella:    seriesBody.SeriesType == "umbrella",
			Episodes:      parseEpisodes(episodes),
		}, Found
	}
	log.Printf("Error getting episodes for %s: EpisodeStatus: %d. SerieStatus: %d", seriesID, episodeStatus, seriesStatus)
	// 5xx/timeouts are outages, not proof the series doesn't exist
	if transient {
		return nil, Unavailable
	}
	return nil, NotFound
}

// extractEpisodes resolves download links for a series' episodes. Umbrella
// shows list their episodes per season (first page of each season).
// Non-playable episodes are dropped and reported, not fatal.
func (c *Client) extractEpisodes(series *seriesResponse, page *episodesPage, skip map[string]bool,
	onFailure OnEpisodeFailure, onClipIDs func(map[string]string)) []episodeWithMedia {
	var candidates []Episode
	if series.SeriesType == "umbrella" {
		seasons := mapConcurrent(series.Links.Seasons, fetchConcurrency, func(season link) []Episode {
			var body seasonResponse
			if _, ok := c.up.getJSON(API+season.Href, &body); !ok || body.Embedded.Episodes == nil {
				return nil
			}
			return body.Embedded.Episodes.Embedded.Episodes
		})
		for _, s := range seasons {
			candidates = append(candidates, s...)
		}
	} else {
		candidates = page.Embedded.Episodes
	}
	reportClipIDs(onClipIDs, candidates)
	fresh := filterSkipped(candidates, skip)
	episodes, _ := collect(fresh, c.resolveAll(fresh, fetchConcurrency, series.Type), onFailure)
	return episodes
}

// GetSeason fetches one customSeason of an umbrella podcast as its own
// feed (NRK publishes standalone shows this way). The result has the
// composite "{seriesId}/sesong/{seasonId}" id. Seasons are small, so
// every refresh walks the whole season.
func (c *Client) GetSeason(seriesID, seasonID string, opts FetchOptions) (*store.Series, Outcome) {
	transient := false
	for _, kind := range catalogOrder(opts.CatalogKind) {
		var body seasonResponse
		status, ok := c.up.getJSON(API+"/radio/catalog/"+string(kind)+"/"+seriesID+"/seasons/"+seasonID, &body)
		transient = transient || isTransient(status)
		if status != http.StatusOK || !ok {
			continue
		}

		// the embedded list is the first page; follow pagination so long
		// seasons aren't silently truncated
		var candidates []Episode
		next := ""
		if body.Embedded.Episodes != nil {
			candidates = body.Embedded.Episodes.Embedded.Episodes
			if body.Embedded.Episodes.Links.Next != nil {
				next = body.Embedded.Episodes.Links.Next.Href
			}
		}
		for p := 0; p < 30 && next != ""; p++ {
			var nextPage episodesPage
			if status, ok := c.up.getJSON(API+next, &nextPage); status != http.StatusOK || !ok {
				// a partial listing would silently lose the tail; retry later
				return nil, Unavailable
			}
			candidates = append(candidates, nextPage.Embedded.Episodes...)
			next = ""
			if nextPage.Links.Next != nil {
				next = nextPage.Links.Next.Href
			}
		}

		episodes := []episodeWithMedia{}
		if !opts.MetadataOnly {
			reportClipIDs(opts.OnClipIDs, candidates)
			fresh := filterSkipped(candidates, opts.SkipEpisodeIDs)
			episodes, _ = collect(fresh, c.resolveAll(fresh, fetchConcurrency, string(kind)), opts.OnEpisodeFailure)
		}
		feedID := feedid.Season(seriesID, seasonID)
		k := kind
		return &store.Series{
			ID: feedID, Title: body.Titles.Title, Subtitle: body.Titles.Subtitle,
			Link:          "https://radio.nrk.no/podkast/" + feedID,
			ImageURL:      lastImage(body.SquareImage),
			LastFetchedAt: clock.Now(),
			CatalogKind:   &k,
			Episodes:      parseEpisodes(episodes),
		}, Found
	}
	if transient {
		return nil, Unavailable
	}
	return nil, NotFound
}

// NewEpisodesResult is the outcome of a cheap, listing-only refresh.
type NewEpisodesResult struct {
	Episodes []store.Episode
	// SawAllPages is false when pagination stopped early (a page failed,
	// or the page cap was hit before reaching known episodes); the caller
	// must resume, or episodes beyond the break would be lost.
	SawAllPages bool
	// ResumeHref is the last safe page to revisit (one page of overlap).
	ResumeHref *string
}

// GetNewEpisodes is the cheap refresh for non-umbrella series with fresh
// metadata: it walks the newest-first listing until a page contains a
// known episode, resolving manifests only for new ones. nil means the
// first page failed.
func (c *Client) GetNewEpisodes(seriesID string, kind store.CatalogKind, opts FetchOptions) *NewEpisodesResult {
	episodes := []episodeWithMedia{}
	href := "/radio/catalog/" + string(kind) + "/" + seriesID + "/episodes?page=1&pageSize=50"
	if opts.CursorHref != nil {
		href = *opts.CursorHref
	}
	previous := href
	stop := opts.StopEpisodeIDs
	if stop == nil {
		stop = opts.SkipEpisodeIDs
	}

	for p := 0; p < 10 && href != ""; p++ {
		var body episodesPage
		status, ok := c.up.getJSON(API+href, &body)
		if status != http.StatusOK || !ok {
			if p == 0 {
				return nil
			}
			prev := previous
			return &NewEpisodesResult{Episodes: parseEpisodes(episodes), ResumeHref: &prev}
		}
		candidates := body.Embedded.Episodes
		reportClipIDs(opts.OnClipIDs, candidates)
		fresh := filterSkipped(candidates, opts.SkipEpisodeIDs)
		collected, transient := collect(fresh, c.resolveAll(fresh, fetchConcurrency, string(kind)), opts.OnEpisodeFailure)
		episodes = append(episodes, collected...)
		if transient > 0 {
			prev := previous
			return &NewEpisodesResult{Episodes: parseEpisodes(episodes), ResumeHref: &prev}
		}
		sawKnown := false
		for _, e := range candidates {
			if stop[e.ID] {
				sawKnown = true
				break
			}
		}
		previous = href
		href = ""
		if !sawKnown && body.Links.Next != nil {
			href = body.Links.Next.Href
		}
	}
	result := &NewEpisodesResult{Episodes: parseEpisodes(episodes), SawAllPages: href == ""}
	if href != "" {
		prev := previous
		result.ResumeHref = &prev
	}
	return result
}

// GetEpisodeResult fetches one episode (for its chapters) by NRK's
// episodeId. Status is 200, 404 (not in either catalog) or 503.
func (c *Client) GetEpisodeResult(seriesID, episodeID string) (*EpisodeDetail, int) {
	failed := false
	for _, kind := range []string{"podcast", "series"} {
		var detail EpisodeDetail
		status, ok := c.up.getJSON(API+"/radio/catalog/"+kind+"/"+seriesID+"/episodes/"+episodeID, &detail)
		if status == http.StatusOK && ok {
			return &detail, status
		}
		if status != http.StatusNotFound {
			failed = true
		}
	}
	if failed {
		return nil, 503
	}
	return nil, 404
}

// EpisodePage is one page of a series' archive.
type EpisodePage struct {
	Episodes []store.Episode
	// NextHref is the NRK API href of the next page, "" on the last page.
	NextHref string
	// TransientFailures are manifests that failed transiently; the page
	// should be retried before advancing.
	TransientFailures int
	// ClipIDs are the clip references of every listed episode (episode id →
	// clip id), skipped ones included.
	ClipIDs map[string]string
}

// GetEpisodePage fetches one page of a feed's archive, resolving download
// links only for episodes not in skip. cursorHref "" starts from the
// first page (trying both catalogs); afterwards pass NextHref. nil means
// the page could not be fetched; retry later from the same cursor.
func (c *Client) GetEpisodePage(feedID, cursorHref string, skip map[string]bool, onFailure OnEpisodeFailure,
	kind *store.CatalogKind) *EpisodePage {
	body, href, ok := c.listPage(feedID, cursorHref, kind)
	if !ok {
		return nil
	}

	// the catalog kind in the href decides which playback endpoint to use
	playbackKind := "podcast"
	if strings.Contains(href, "/catalog/series/") {
		playbackKind = "series"
	}
	fresh := filterSkipped(body.Embedded.Episodes, skip)
	if fresh == nil {
		fresh = []Episode{}
	}
	collected, transient := collect(fresh, c.resolveAll(fresh, backlogConcurrency, playbackKind), onFailure)
	next := ""
	if body.Links.Next != nil {
		next = body.Links.Next.Href
	}
	return &EpisodePage{Episodes: parseEpisodes(collected), NextHref: next, TransientFailures: transient,
		ClipIDs: clipIDs(body.Embedded.Episodes)}
}

// ListClipIDs fetches one listing page of a feed's archive and returns the
// clip references on it, with no playback lookups (one request). ok is
// false when the page could not be fetched.
func (c *Client) ListClipIDs(feedID, cursorHref string, kind *store.CatalogKind) (clipIDsByEpisode map[string]string, next string, ok bool) {
	body, _, ok := c.listPage(feedID, cursorHref, kind)
	if !ok {
		return nil, "", false
	}
	if body.Links.Next != nil {
		next = body.Links.Next.Href
	}
	return clipIDs(body.Embedded.Episodes), next, true
}

// listPage fetches one archive listing page. An empty cursor starts from
// the first page, trying both catalogs; href is the page actually used.
func (c *Client) listPage(feedID, cursorHref string, kind *store.CatalogKind) (body episodesPage, href string, ok bool) {
	ref := feedid.Parse(feedID)
	href = cursorHref
	if href != "" {
		if !strings.HasPrefix(href, "/") {
			href = "/" + href
		}
		_, ok = c.up.getJSON(API+href, &body)
		return body, cursorHref, ok
	}
	for _, k := range catalogOrder(kind) {
		// season feeds have their own paged listing under the parent series
		if ref.SeasonID != "" {
			href = "/radio/catalog/" + string(k) + "/" + ref.SeriesID + "/seasons/" + ref.SeasonID + "/episodes?page=1&pageSize=50"
		} else {
			href = "/radio/catalog/" + string(k) + "/" + ref.SeriesID + "/episodes?page=1&pageSize=50"
		}
		body = episodesPage{}
		if _, ok = c.up.getJSON(API+href, &body); ok {
			return body, href, true
		}
	}
	return body, href, false
}

// Search searches NRK's series. An empty slice is a confirmed "no
// matches"; nil is reserved for failures. Successful results are cached
// for five minutes (the homepage suggestion chips repeat queries a lot).
func (c *Client) Search(query string) []SearchResult {
	trimmed := []rune(strings.TrimSpace(query))
	if len(trimmed) > MaxSearchQueryLength {
		trimmed = trimmed[:MaxSearchQueryLength]
	}
	q := string(trimmed)
	if q == "" {
		log.Print("Empty search query.")
		return nil
	}

	key := strings.ToLower(q)
	c.searchMu.Lock()
	if cached, ok := c.searchCache[key]; ok && cached.expires.After(clock.Now()) {
		c.searchMu.Unlock()
		return cached.result
	}
	c.searchMu.Unlock()

	var body searchResponse
	status, ok := c.up.getJSON(API+"/radio/search/search?q="+EncodeURIComponent(q), &body)
	if status != http.StatusOK || !ok {
		log.Printf("Something went wrong with %s - got status %d", q, status)
		return nil
	}
	result := []SearchResult{}
	if body.Results.Series != nil && body.Results.Series.Results != nil {
		result = body.Results.Series.Results
	}

	c.searchMu.Lock()
	defer c.searchMu.Unlock()
	if len(c.searchCache) >= searchCacheMaxEntries {
		now := clock.Now()
		kept := c.searchOrder[:0]
		for _, k := range c.searchOrder {
			if c.searchCache[k].expires.After(now) {
				kept = append(kept, k)
			} else {
				delete(c.searchCache, k)
			}
		}
		c.searchOrder = kept
		// still full of fresh entries: drop the oldest
		if len(c.searchCache) >= searchCacheMaxEntries && len(c.searchOrder) > 0 {
			delete(c.searchCache, c.searchOrder[0])
			c.searchOrder = c.searchOrder[1:]
		}
	}
	if _, exists := c.searchCache[key]; !exists {
		c.searchOrder = append(c.searchOrder, key)
	}
	c.searchCache[key] = searchEntry{expires: clock.Now().Add(searchCacheTTL), result: result}
	return result
}

// EncodeURIComponent escapes like JavaScript's encodeURIComponent, so
// request URLs (and recorded fixtures) match the Deno version's exactly.
func EncodeURIComponent(s string) string {
	const unreserved = "-_.!~*'()"
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(unreserved, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
