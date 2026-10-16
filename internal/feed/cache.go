package feed

import (
	"container/list"
	"crypto/sha1"
	"encoding/hex"
	"sync"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/store"
)

const (
	maxCacheEntries = 500
	// maxCacheBytes bounds the cached XML: full archives run to several MB
	// each, so a count alone doesn't bound memory
	maxCacheBytes = 64 << 20
	// maxValidators bounds the Last-Modified dates kept for evicted feeds
	maxValidators = 10_000
)

// Rendered is a feed's XML with its validators.
type Rendered struct {
	XML          string
	ETag         string
	LastModified time.Time
}

type cacheEntry struct {
	Rendered
	key     string
	origin  string
	version int64
}

// validator is what a feed's Last-Modified must stay consistent with,
// kept after its XML is evicted.
type validator struct {
	etag         string
	lastModified time.Time
}

// Cache memoizes rendered feed XML (and its ETag) per feed, invalidated
// through the store's data version whenever the feed's content changes.
// Serving a poll, 200 or 304, then costs no rendering or hashing. Each feed
// has one entry, for the origin it was last rendered for: without
// APP_ORIGIN the origin comes from the request Host, and keying on it would
// let arbitrary Host values multiply full copies of a feed in memory. The
// least recently used feeds are evicted beyond maxCacheEntries or
// maxCacheBytes.
type Cache struct {
	mu         sync.Mutex
	entries    map[string]*list.Element // of *cacheEntry
	lru        *list.List               // most recently used first
	bytes      int
	validators map[string]validator
	// limits, constants except in tests
	maxEntries, maxBytes int
}

func NewCache() *Cache {
	return &Cache{entries: map[string]*list.Element{}, lru: list.New(), validators: map[string]validator{},
		maxEntries: maxCacheEntries, maxBytes: maxCacheBytes}
}

// get returns the entry for key and marks it used. c.mu must be held.
func (c *Cache) get(key string) *cacheEntry {
	element := c.entries[key]
	if element == nil {
		return nil
	}
	c.lru.MoveToFront(element)
	return element.Value.(*cacheEntry)
}

// Peek returns the cached feed if its version snapshot is still current.
func (c *Cache) Peek(feedID, origin string, version int64) *Rendered {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.get(feedID); e != nil && e.origin == origin && e.version == version {
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
	cached := c.get(key)
	previous, known := c.validators[key]
	c.mu.Unlock()
	// an equal or newer render is already cached: never go back
	if cached != nil && cached.origin == origin && cached.version >= version {
		return cached.Rendered
	}

	xml := AssembleFeed(series, origin)
	etag := ETagFor(xml)
	// Last-Modified must move whenever the XML changes (archive backfills
	// add OLD episodes, so the newest episode date would stand still and
	// If-Modified-Since clients would never fetch the update), and only
	// then. HTTP dates have whole seconds: a change within the same second
	// as the last one moves it a second on.
	lastModified := clock.Now()
	if known {
		if previous.etag == etag {
			lastModified = previous.lastModified
		} else if last := previous.lastModified.Truncate(time.Second); !lastModified.Truncate(time.Second).After(last) {
			lastModified = last.Add(time.Second)
		}
	}
	entry := &cacheEntry{Rendered: Rendered{XML: xml, ETag: etag, LastModified: lastModified},
		key: key, origin: origin, version: version}

	c.mu.Lock()
	defer c.mu.Unlock()
	if version == 0 {
		// not read from the store: serve it, but don't cache it
		return entry.Rendered
	}
	if current := c.get(key); current != nil && current.origin == origin && current.version > version {
		// a newer render landed while this one was assembled
		return current.Rendered
	}
	c.remember(key, entry)
	if element := c.entries[key]; element != nil {
		c.bytes -= len(element.Value.(*cacheEntry).XML)
		element.Value = entry
		c.lru.MoveToFront(element)
	} else {
		c.entries[key] = c.lru.PushFront(entry)
	}
	c.bytes += len(xml)
	for c.lru.Len() > 1 && (c.lru.Len() > c.maxEntries || c.bytes > c.maxBytes) {
		oldest := c.lru.Back()
		evicted := oldest.Value.(*cacheEntry)
		c.lru.Remove(oldest)
		delete(c.entries, evicted.key)
		c.bytes -= len(evicted.XML)
	}
	return entry.Rendered
}

// remember keeps the feed's validators past eviction. c.mu must be held.
func (c *Cache) remember(key string, entry *cacheEntry) {
	if _, exists := c.validators[key]; !exists && len(c.validators) >= maxValidators {
		c.validators = map[string]validator{}
	}
	c.validators[key] = validator{etag: entry.ETag, lastModified: entry.LastModified}
}
