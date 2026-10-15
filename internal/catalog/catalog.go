// Package catalog keeps a weekly-refreshed, persistent list of every
// podcast in NRK's catalog, used for rotating suggestions on the front
// page. It loads lazily in the background, so no request waits for the
// paged fetches.
package catalog

import (
	"log"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/nrk"
	"github.com/matsbst/kringkasting/internal/retry"
	"github.com/matsbst/kringkasting/internal/store"
)

const (
	ttl      = 7 * 24 * time.Hour
	maxPages = 30
	stateKey = "catalog"
	// maxChipTitle: long titles blow up the chip layout (JS string length)
	maxChipTitle = 24
)

// Entry and state use the Deno version's JSON shape.
type Entry struct {
	SeriesID string `json:"seriesId"`
	Title    string `json:"title"`
}

type state struct {
	Expires int64   `json:"expires"`
	Entries []Entry `json:"entries"`
}

type page struct {
	Links *struct {
		NextPage *struct {
			Href string `json:"href"`
		} `json:"nextPage"`
	} `json:"_links"`
	Series []struct {
		SeriesID string `json:"seriesId"`
		SeasonID string `json:"seasonId"`
		Title    string `json:"title"`
		Type     string `json:"type"`
	} `json:"series"`
}

type Catalog struct {
	store *store.Store
	nrk   *nrk.Client
	retry *retry.Retry

	mu       sync.Mutex
	cache    *state
	restored bool
	loading  bool
}

func New(s *store.Store, client *nrk.Client, r *retry.Retry) *Catalog {
	return &Catalog{store: s, nrk: client, retry: r}
}

func (c *Catalog) restore() {
	if !c.restored {
		var st state
		if c.store.ReadState(stateKey, &st) {
			c.cache = &st
		}
		c.restored = true
	}
}

// ensureFresh restores the cache and starts a background refresh when it
// is missing or stale. Callers hold c.mu.
func (c *Catalog) ensureFresh() {
	c.restore()
	if (c.cache == nil || c.cache.Expires < clock.NowMs()) && !c.loading && c.retry.Can(stateKey) {
		c.loading = true
		go func() {
			defer func() {
				if r := recover(); r != nil {
					c.retry.Defer(stateKey, 0)
					log.Printf("Catalog load failed: %v", r)
				}
				c.mu.Lock()
				c.loading = false
				c.mu.Unlock()
			}()
			c.load()
		}()
	}
}

func (c *Catalog) load() {
	entries := []Entry{}
	seen := map[string]bool{}
	href := "/radio/search/categories/podcast?take=100"
	for p := 0; p < maxPages && href != ""; p++ {
		var body page
		if _, ok := c.nrk.GetJSON(nrk.API+href, &body); !ok {
			// keep the previous complete catalog; retry later
			c.retry.Defer(stateKey, 0)
			return
		}
		for _, item := range body.Series {
			if item.SeriesID == "" || item.Title == "" {
				continue
			}
			// customSeason rows are standalone shows published under
			// umbrella podcasts; they have their own feeds, so they count
			key := item.SeriesID
			if item.Type == "customSeason" {
				season := item.SeasonID
				if season == "" {
					season = item.Title
				}
				key = item.SeriesID + "/" + season
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			entries = append(entries, Entry{SeriesID: item.SeriesID, Title: item.Title})
		}
		href = ""
		if body.Links != nil && body.Links.NextPage != nil {
			href = body.Links.NextPage.Href
		}
	}
	if href != "" || len(entries) == 0 {
		c.retry.Defer(stateKey, 0)
		return
	}
	loaded := &state{Expires: clock.Now().Add(ttl).UnixMilli(), Entries: entries}
	c.store.WriteState(stateKey, loaded)
	c.retry.Clear(stateKey)
	c.mu.Lock()
	c.cache = loaded
	c.mu.Unlock()
	log.Printf("Catalog loaded: %d podcasts", len(entries))
}

// Size is the number of shows in NRK's catalog, -1 until loaded.
func (c *Catalog) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureFresh()
	if c.cache == nil {
		return -1
	}
	return len(c.cache.Entries)
}

// RandomTitles picks show titles for the suggestion chips; nil until the
// catalog has loaded once (callers fall back to a static list). A stale
// cache is served while refreshing.
func (c *Catalog) RandomTitles(count int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureFresh()
	if c.cache == nil {
		return nil
	}
	var pool []Entry
	for _, e := range c.cache.Entries {
		if len(utf16.Encode([]rune(e.Title))) <= maxChipTitle {
			pool = append(pool, e)
		}
	}
	picks := []string{}
	used := map[int]bool{}
	for len(picks) < count && len(used) < len(pool) {
		i := clock.IntN(len(pool))
		if used[i] {
			continue
		}
		used[i] = true
		picks = append(picks, pool[i].Title)
	}
	return picks
}
