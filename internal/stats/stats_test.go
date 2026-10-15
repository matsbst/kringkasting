package stats

import (
	"testing"
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
