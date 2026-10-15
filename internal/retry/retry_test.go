package retry_test

import (
	"testing"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/retry"
	"github.com/matsbst/kringkasting/internal/testutil"
)

// Deadlines grow 5 min, 10 min, … capped at a day; Retry-After (seconds
// or an HTTP date) takes precedence when later.
func TestDeadlinesAndRetryAfter(t *testing.T) {
	s, _ := testutil.OpenStore(t)
	r := retry.New(s)
	defer clock.Unpin()
	start := time.Now().Truncate(time.Millisecond)
	clock.Pin(start)
	for _, want := range []time.Duration{5, 10, 20, 40, 80, 160, 320, 640, 1280, 1440, 1440} {
		r.Defer("k", 0)
		// the pinned clock advances in real time: allow a few seconds of
		// drift on slow runners (the race detector slows tests a lot)
		got := time.Duration(r.At("k")-start.UnixMilli()) * time.Millisecond
		if got < want*time.Minute || got > want*time.Minute+5*time.Second {
			t.Errorf("delay %v, want %v", got, want*time.Minute)
		}
	}
	if r.Can("k") {
		t.Error("should wait for the deadline")
	}
	r.Clear("k")
	if !r.Can("k") {
		t.Error("cleared key should be allowed")
	}

	later := start.Add(3 * 24 * time.Hour).UnixMilli()
	r.Defer("upstream", later)
	if r.At("upstream") != later {
		t.Errorf("Retry-After deadline not honoured: %d", r.At("upstream"))
	}

	now := start.UnixMilli()
	if got := retry.ParseRetryAfter("120", now); got != now+120_000 {
		t.Errorf("seconds: %d", got)
	}
	date := start.Add(time.Hour).UTC().Format(time.RFC1123)
	if got := retry.ParseRetryAfter(date, now); got != start.Add(time.Hour).Truncate(time.Second).UnixMilli() {
		t.Errorf("date: %d", got)
	}
	if retry.ParseRetryAfter("", now) != 0 || retry.ParseRetryAfter("soon", now) != 0 {
		t.Error("invalid values must give 0")
	}
}
