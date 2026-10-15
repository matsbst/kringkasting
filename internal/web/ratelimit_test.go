package web

import (
	"testing"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/stats"
)

func TestTokenBucket(t *testing.T) {
	defer clock.Unpin()
	start := time.Now()
	clock.Pin(start)
	l := newLimiter(stats.New())
	for i := range 3 {
		if !l.allow("k", 3, 1) {
			t.Fatalf("request %d refused inside the burst", i)
		}
	}
	if l.allow("k", 3, 1) {
		t.Fatal("burst exceeded but allowed")
	}
	clock.Pin(start.Add(1100 * time.Millisecond))
	if !l.allow("k", 3, 1) {
		t.Error("bucket should refill over time")
	}
	if l.stats.Snapshot().RateLimited != 1 {
		t.Error("refusals are counted")
	}
}
