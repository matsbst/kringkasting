package catalog_test

import (
	"sync"
	"testing"
	"time"

	"github.com/matsbst/kringkasting/internal/catalog"
	"github.com/matsbst/kringkasting/internal/nrk"
	"github.com/matsbst/kringkasting/internal/retry"
	"github.com/matsbst/kringkasting/internal/stats"
	"github.com/matsbst/kringkasting/internal/testutil"
)

// Concurrent requests start one background catalog load, not one each;
// a failed load keeps no partial catalog and backs off.
func TestCatalogLoadsOnce(t *testing.T) {
	for _, status := range []int{200, 503} {
		s, _ := testutil.OpenStore(t)
		gate := make(chan struct{})
		fake := &testutil.FakeNRK{Gate: gate, Handle: func(string, string) testutil.Reply {
			if status != 200 {
				return testutil.Reply{Status: status}
			}
			return testutil.Reply{Status: 200, Body: `{"series":[{"seriesId":"a","title":"Abels tårn","type":"podcast"}]}`}
		}}
		r := retry.New(s)
		c := catalog.New(s, nrk.New(fake, r, stats.New()), r)
		var wg sync.WaitGroup
		for range 20 {
			wg.Go(func() { c.RandomTitles(6) })
		}
		wg.Wait()
		close(gate)
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if status == 200 && c.Size() == 1 || status != 200 && !r.Can("catalog") {
				break
			}
		}
		if got := len(fake.Requests); got != 1 {
			t.Errorf("status %d: %d catalog requests for 20 callers, want 1", status, got)
		}
		if status == 200 && c.Size() != 1 {
			t.Errorf("catalog not loaded: size %d", c.Size())
		}
		if status != 200 && (c.Size() != -1 || r.Can("catalog")) {
			t.Error("a failed load must keep no catalog and back off")
		}
	}
}
