package web

import (
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/matsbst/kringkasting/internal/stats"
	"github.com/matsbst/kringkasting/internal/store"
)

func TestHealthLevels(t *testing.T) {
	s := &Server{}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.Local)
	ok := stats.Snapshot{LastUpstreamSuccess: now.Add(-12 * time.Second)}
	for name, c := range map[string]struct {
		snap  stats.Snapshot
		today map[string]int
		level string
	}{
		"no calls yet": {stats.Snapshot{}, nil, "idle"},
		"healthy":      {ok, map[string]int{stats.DayNRK: 1000, stats.DayNRKErrors: 4}, "ok"},
		"many errors":  {ok, map[string]int{stats.DayNRK: 100, stats.DayNRKErrors: 9}, "warn"},
		"few calls":    {ok, map[string]int{stats.DayNRK: 10, stats.DayNRKErrors: 3}, "ok"},
		"outage": {stats.Snapshot{LastUpstreamSuccess: now.Add(-time.Hour), ConsecutiveFailures: stats.HealthFailureThreshold,
			FailingSince: now.Add(-10 * time.Minute), LastFailureStatus: 503}, nil, "down"},
	} {
		h := s.health(c.snap, c.today, now)
		if h.Level != c.level {
			t.Errorf("%s: %s (%s – %s), want %s", name, h.Level, h.Title, h.Detail, c.level)
		}
	}
	if h := s.health(ok, map[string]int{stats.DayNRK: 1000, stats.DayNRKErrors: 4}, now); !strings.Contains(h.Detail, "0,4 % feil i dag") ||
		!strings.Contains(h.Detail, "for 12 s siden") {
		t.Errorf("detail: %s", h.Detail)
	}
}

func TestArchiveText(t *testing.T) {
	oldest := time.Date(2019, 3, 10, 12, 0, 0, 0, time.Local)
	cursor := "/radio/catalog/podcast/x/episodes?page=8&pageSize=50"
	o := store.SeriesOverview{ID: "x", OldestEpisode: &oldest, BacklogCursor: &cursor}
	for name, c := range map[string]struct {
		current string
		queue   []string
		want    string
		active  bool
	}{
		"crawling": {"x", nil, "Henter nå · tilbake til mars 2019 · side 8", true},
		"next":     {"y", []string{"x"}, "Neste i kø · tilbake til mars 2019 · side 8", true},
		"queued":   {"y", []string{"a", "b", "x"}, "I kø (2 foran) · tilbake til mars 2019 · side 8", true},
		"waiting":  {"", nil, "Venter på neste forespørsel · tilbake til mars 2019 · side 8", false},
	} {
		if got, active := archiveText(o, c.current, c.queue); got != c.want || active != c.active {
			t.Errorf("%s: %q %v", name, got, active)
		}
	}
	o.BacklogComplete = true
	if got, _ := archiveText(o, "", nil); got != "Komplett · fra mars 2019" {
		t.Errorf("complete: %q", got)
	}
	bad := "not a link"
	if got, _ := archiveText(store.SeriesOverview{ID: "x", BacklogCursor: &bad}, "", nil); got != "Venter på neste forespørsel" {
		t.Errorf("unparsable cursor: %q", got)
	}
}

func TestDashboardFormatting(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.Local)
	for d, want := range map[time.Duration]string{
		5 * time.Second: "for 5 s siden", 3 * time.Minute: "for 3 min siden", 5 * time.Hour: "for 5 t siden",
		30 * time.Hour: "for 1 dag siden", 72 * time.Hour: "for 3 dager siden", 40 * 24 * time.Hour: "5. september 2026",
	} {
		if got := ago(now.Add(-d), now); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1 000", 1234567: "1 234 567", -2500: "-2 500"} {
		if got := formatInt(n); got != want {
			t.Errorf("%d: %q", n, got)
		}
	}
	days := []string{"2026-10-14", "2026-10-15"}
	svg := string(barChart("NRK", days, []int{10, 0}, []int{2, 0}))
	if strings.Count(svg, "<rect") != 3 || !strings.Contains(svg, "<title>14. okt.: 10 (2 feil)</title>") ||
		!strings.Contains(svg, `opacity="0.25"`) || !strings.Contains(svg, `role="img" aria-label="NRK"`) {
		t.Errorf("chart: %s", svg)
	}
}

// The admin search is cut at 100 characters, never inside one.
func TestListQueryCutsByCharacter(t *testing.T) {
	q := parseListQuery(url.Values{"q": {strings.Repeat("ø", 150)}}).Query
	if !utf8.ValidString(q) || utf8.RuneCountInString(q) != 100 {
		t.Errorf("cut to %d characters, valid UTF-8: %v", utf8.RuneCountInString(q), utf8.ValidString(q))
	}
}
