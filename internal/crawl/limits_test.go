package crawl

import (
	"testing"
	"time"
)

// NRK load limits must keep their values (docs/nrk-api-usage.md).
func TestCrawlLimits(t *testing.T) {
	if DefaultPageDelay != time.Second || maxPagesPerRun != 200 {
		t.Errorf("page delay %v, pages per run %d", DefaultPageDelay, maxPagesPerRun)
	}
}
