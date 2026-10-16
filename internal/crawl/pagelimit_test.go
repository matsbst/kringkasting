package crawl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/nrk"
	"github.com/matsbst/kringkasting/internal/retry"
	"github.com/matsbst/kringkasting/internal/stats"
	"github.com/matsbst/kringkasting/internal/store"
	"github.com/matsbst/kringkasting/internal/testutil"
)

// A round that stops at the page limit pauses for a fixed few minutes:
// nothing failed, so the backoff mustn't escalate round after round.
func TestPageLimitPausesWithoutEscalating(t *testing.T) {
	s, _ := testutil.OpenStore(t)
	r := retry.New(s)
	page := func(n int) testutil.Reply {
		body, _ := json.Marshal(map[string]any{
			"_embedded": map[string]any{"episodes": []any{}},
			"_links": map[string]any{"next": map[string]any{
				"href": fmt.Sprintf("/radio/catalog/podcast/lang/episodes?page=%d&pageSize=50", n+1)}},
		})
		return testutil.Reply{Status: 200, Body: string(body)}
	}
	fake := &testutil.FakeNRK{Handle: func(method, url string) testutil.Reply {
		var n int
		if _, err := fmt.Sscanf(url[strings.Index(url, "page=")+5:], "%d", &n); err != nil {
			n = 1
		}
		return page(n)
	}}
	kind := store.KindPodcast
	s.WriteSeries(&store.Series{ID: "lang", Title: "Lang", Link: "l", LastFetchedAt: clock.Now(), CatalogKind: &kind,
		Episodes: []store.Episode{{ID: "e", Title: "e", URL: "https://x/e.mp3", ShareLink: "s", Date: clock.Now()}}})
	// earlier failures have escalated the backoff; its deadline has passed
	s.WriteState("retry:backlog:lang", map[string]int64{"attempts": 5, "nextRetryAt": 0})

	c := New(context.Background(), s, nrk.New(fake, r, stats.New()), r, time.Microsecond)
	c.Enqueue("lang")
	if err := c.WaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	wait := time.Duration(r.At("backlog:lang")-clock.NowMs()) * time.Millisecond
	if wait <= 0 || wait > pageLimitPause+time.Second {
		t.Errorf("paused %v after the page limit, want about %v", wait, pageLimitPause)
	}
}
