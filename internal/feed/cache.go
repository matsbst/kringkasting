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
	origin  string
	version int64
}

// Cache memoizes rendered feed XML (and its ETag) per feed, invalidated
// through the store's data version whenever the feed's content changes.
// Serving a poll, 200 or 304, then costs no rendering or hashing. Each feed
// has one entry, for the origin it was last rendered for: without
// APP_ORIGIN the origin comes from the request Host, and keying on it would
// let arbitrary Host values multiply full copies of a feed in memory.
type Cache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
	order   []string // insertion order, for oldest-first eviction
}

func NewCache() *Cache { return &Cache{entries: map[string]*cacheEntry{}} }

// Peek returns the cached feed if its version snapshot is still current.
func (c *Cache) Peek(feedID, origin string, version int64) *Rendered {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[feedID]; e != nil && e.origin == origin && e.version == version {
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

// Render renders (or reuses) the feed, cached under series.Version: the
// data version snapshotted before the series was read. A write landing
// during that read leaves the render under the OLD version, which no
// longer matches and heals on the next request; stale XML is never cached
// under the newest version. (A request that joined another's fetch gets
// that fetch's series, so its own, later snapshot must not be used.)
func (c *Cache) Render(series *store.Series, origin string) Rendered {
	key, version := series.ID, series.Version
	c.mu.Lock()
	cached := c.entries[key]
	c.mu.Unlock()
	// an equal or newer render is already cached: never go back
	if cached != nil && cached.origin == origin && cached.version >= version {
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
	entry := &cacheEntry{Rendered: Rendered{XML: xml, ETag: ETagFor(xml), LastModified: lastModified},
		origin: origin, version: version}

	c.mu.Lock()
	defer c.mu.Unlock()
	if version == 0 {
		// not read from the store: serve it, but don't cache it
		return entry.Rendered
	}
	if current := c.entries[key]; current != nil && current.origin == origin && current.version > version {
		// a newer render landed while this one was assembled
		return current.Rendered
	}
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
