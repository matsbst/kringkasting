package stats

import (
	"testing"
	"time"
)

func TestHealthFailures(t *testing.T) {
	for status, want := range map[int]bool{0: true, 500: true, 503: true, 429: true, 403: true, 200: false, 301: false, 404: false} {
		if IsHealthFailure(status) != want {
			t.Errorf("IsHealthFailure(%d) != %v", status, want)
		}
	}
	s := New()
	var alerts []error
	s.Alert = func(err error, _ map[string]string) { alerts = append(alerts, err) }
	for range 7 {
		s.RecordUpstreamRequest("https://psapi.nrk.no/radio/catalog/podcast/x", "GET", 503)
	}
	if s.ConsecutiveFailures() != 7 || len(alerts) != 0 {
		t.Fatalf("failures %d, alerts %d", s.ConsecutiveFailures(), len(alerts))
	}
	s.RecordUpstreamRequest("https://psapi.nrk.no/x", "GET", 0)
	s.RecordUpstreamRequest("https://psapi.nrk.no/x", "GET", 0)
	if len(alerts) != 1 {
		t.Errorf("one throttled alert expected, got %d", len(alerts))
	}
	s.RecordUpstreamRequest("https://psapi.nrk.no/x", "GET", 404)
	if s.ConsecutiveFailures() != 0 {
		t.Error("a reachable response resets the run")
	}
	snap := s.Snapshot()
	if snap.Upstream["katalog"].Requests != 7 || snap.Upstream["annet"].Requests != 3 || snap.UpstreamErrors != 9 {
		t.Errorf("snapshot %+v", snap)
	}
}

func TestFeedRequestBatch(t *testing.T) {
	s := New()
	s.RecordFeedRequest("a", "Overcast/1.0 Podcast Sync (12 subscribers)")
	s.RecordFeedRequest("a", "Overcast/1.0 Podcast Sync")
	s.RecordFeedRequest("a", "AntennaPod/3.5")
	s.RecordFeedRequest("b", "curl/8")
	s.RecordFeedServed(true)
	s.RecordUpstreamRequest("https://psapi.nrk.no/radio/catalog/podcast/a", "GET", 503)

	batch := s.TakeBatch()
	if len(batch.Feeds) != 2 || len(batch.Clients) != 3 {
		t.Fatalf("batch: %+v", batch)
	}
	for _, c := range batch.Clients {
		if c.Client == "Overcast" && (c.Subscribers == nil || *c.Subscribers != 12) {
			t.Error("a later sighting without a count must keep the reported one")
		}
	}
	today := batch.Daily[Day(time.Now())]
	if today[DayFeeds304] != 1 || today[DayNRK] != 1 || today[DayNRKErrors] != 1 {
		t.Errorf("daily: %v", today)
	}
	if !s.TakeBatch().Empty() {
		t.Error("a taken batch must not be handed out twice")
	}

	// a failed flush puts it back, merged with what came in meanwhile
	s.RecordFeedServed(true)
	s.ReturnBatch(batch)
	again := s.TakeBatch()
	if again.Daily[Day(time.Now())][DayFeeds304] != 2 || len(again.Clients) != 3 {
		t.Errorf("returned batch: %+v", again)
	}
}

func TestUpstreamHealthTimes(t *testing.T) {
	s := New()
	s.RecordUpstreamRequest("https://psapi.nrk.no/x", "GET", 200)
	if snap := s.Snapshot(); snap.LastUpstreamSuccess.IsZero() || snap.ConsecutiveFailures != 0 || !snap.FailingSince.IsZero() {
		t.Errorf("after success: %+v", snap)
	}
	s.RecordUpstreamRequest("https://psapi.nrk.no/x", "GET", 503)
	first := s.Snapshot().FailingSince
	s.RecordUpstreamRequest("https://psapi.nrk.no/x", "GET", 0)
	snap := s.Snapshot()
	if snap.ConsecutiveFailures != 2 || snap.LastFailureStatus != 0 || !snap.FailingSince.Equal(first) {
		t.Errorf("after failures: %+v", snap)
	}
	s.RecordUpstreamRequest("https://psapi.nrk.no/x", "GET", 404)
	if snap := s.Snapshot(); snap.ConsecutiveFailures != 0 || !snap.FailingSince.IsZero() {
		t.Errorf("a 404 proves NRK answers: %+v", snap)
	}
}
