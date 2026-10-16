package feed

import (
	"strings"
	"testing"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/store"
)

func series(id, title string, version int64) *store.Series {
	return &store.Series{ID: id, Title: title, Episodes: []store.Episode{}, Version: version}
}

// The least recently used feed goes first, and the byte budget holds
// whatever the number of feeds.
func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	c := NewCache()
	c.maxEntries = 2
	c.Render(series("a", "A", 1), "o")
	c.Render(series("b", "B", 1), "o")
	c.Peek("a", "o", 1) // a is now the most recent
	c.Render(series("c", "C", 1), "o")
	if c.Peek("a", "o", 1) == nil || c.Peek("b", "o", 1) != nil || c.Peek("c", "o", 1) == nil {
		t.Error("the least recently used feed must be evicted")
	}

	c = NewCache()
	big := c.Render(series("big", strings.Repeat("x", 1000), 1), "o")
	c.maxBytes = len(big.XML) + 100
	c.Render(series("small", "S", 1), "o")
	if c.bytes > c.maxBytes || c.Peek("big", "o", 1) != nil || c.Peek("small", "o", 1) == nil {
		t.Errorf("byte budget not kept: %d of %d bytes", c.bytes, c.maxBytes)
	}
}

// Last-Modified survives eviction while the XML is unchanged, and moves
// at least a second when the XML changes within the same second.
func TestCacheLastModified(t *testing.T) {
	defer clock.Unpin()
	start := time.Date(2026, 10, 16, 12, 0, 0, 100e6, time.UTC)
	clock.Pin(start)
	c := NewCache()
	c.maxEntries = 1
	first := c.Render(series("a", "A", 1), "o")
	c.Render(series("b", "B", 1), "o") // evicts a
	clock.Pin(start.Add(time.Hour))
	again := c.Render(series("a", "A", 2), "o")
	if !again.LastModified.Equal(first.LastModified) {
		t.Errorf("unchanged XML after eviction: Last-Modified %v, want %v", again.LastModified, first.LastModified)
	}
	clock.Pin(start.Add(time.Hour + 300*time.Millisecond))
	changed := c.Render(series("a", "A2", 3), "o")
	if !changed.LastModified.Truncate(time.Second).After(again.LastModified.Truncate(time.Second)) {
		t.Errorf("changed XML: Last-Modified %v must be a later second than %v", changed.LastModified, again.LastModified)
	}
	clock.Pin(start.Add(time.Hour + 400*time.Millisecond))
	changedAgain := c.Render(series("a", "A3", 4), "o")
	if !changedAgain.LastModified.Truncate(time.Second).After(changed.LastModified.Truncate(time.Second)) {
		t.Error("a second change in the same second must move Last-Modified again")
	}
}
