package feed

import (
	"crypto/sha1"
	"encoding/hex"
	"sync"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/store"
)

const maxCacheEntries = 500

// Rendered is a feed's XML with its validators.
type Rendered struct {
	XML          string
	ETag         string
	LastModified time.Time
}

type cacheEntry struct {
	Rendered
	version int64
}

// Cache memoizes rendered feed XML (and its ETag) per feed and origin,
// invalidated through the store's data version whenever the feed's
// content changes. Serving a poll, 200 or 304, then costs no rendering or
// hashing.
type Cache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
	order   []string // insertion order, for oldest-first eviction
}

func NewCache() *Cache { return &Cache{entries: map[string]*cacheEntry{}} }

func cacheKey(feedID, origin string) string { return feedID + "|" + origin }

// Peek returns the cached feed if its version snapshot is still current.
func (c *Cache) Peek(feedID, origin string, version int64) *Rendered {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[cacheKey(feedID, origin)]; e != nil && e.version == version {
		r := e.Rendered
		return &r
	}
	return nil
}

// ETagFor is a strong ETag: the quoted SHA-1 hex of the body.
func ETagFor(content string) string {
	sum := sha1.Sum([]byte(content))
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// Render renders (or reuses) the feed. version must be snapshotted BEFORE
// the series was read: a write landing between snapshot and read then
// caches stale XML under the OLD version, which no longer matches and
// heals on the next request, instead of stale XML being cached under the
// newest version and served indefinitely.
func (c *Cache) Render(series *store.Series, origin string, version int64) Rendered {
	key := cacheKey(series.ID, origin)
	c.mu.Lock()
	cached := c.entries[key]
	c.mu.Unlock()
	if cached != nil && cached.version == version {
		return cached.Rendered
	}

	xml := AssembleFeed(series, origin)
	// Last-Modified must move whenever the XML changes (archive backfills
	// add OLD episodes, so the newest episode date would stand still and
	// If-Modified-Since clients would never fetch the update)
	lastModified := clock.Now()
	if cached != nil && cached.XML == xml {
		lastModified = cached.LastModified
	}
	entry := &cacheEntry{Rendered: Rendered{XML: xml, ETag: ETagFor(xml), LastModified: lastModified}, version: version}

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists {
		if len(c.entries) >= maxCacheEntries && len(c.order) > 0 {
			delete(c.entries, c.order[0])
			c.order = c.order[1:]
		}
		c.order = append(c.order, key)
	}
	c.entries[key] = entry
	return entry.Rendered
}
