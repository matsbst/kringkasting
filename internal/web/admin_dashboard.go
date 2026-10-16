package web

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/feedid"
	"github.com/matsbst/kringkasting/internal/stats"
	"github.com/matsbst/kringkasting/internal/store"
)

const (
	// clientWindow is how far back apps and their subscriber counts count.
	// Behind a CDN the server sees few polls, so a short window would miss
	// apps that poll rarely.
	clientWindow = 14 * 24 * time.Hour
	// cleanupAfter: feeds no app requested for this long are offered for
	// cleanup, once activity has been measured this long.
	cleanupAfter = 180 * 24 * time.Hour
	// warnErrorRate of NRK errors today turns the health line amber.
	warnErrorRate = 0.05
	// warnMinRequests: below this many NRK requests today, a few errors
	// don't say much.
	warnMinRequests = 20
	chartDays       = 30
)

type tile struct{ Value, Label, Href string }

type upstreamRow struct {
	Category         string
	Requests, Errors int
}

type eventRow struct{ At, Message string }

type failedRow struct {
	SeriesID, EpisodeID, NextRetryAt string
	Attempts                         int
}

type action struct{ Action, Label string }

// healthView is the status line at the top: Level is ok, warn, down or idle.
type healthView struct{ Level, Title, Detail string }

type chartView struct {
	Title, Today, Total string
	SVG                 template.HTML
}

type feedRow struct {
	ID, Title string
	// Parent names the podcast a season feed belongs to
	Parent            string
	Episodes, Videos  int
	Archive           string
	ArchiveActive     bool
	LastUsed, Apps    string
	Subscribers       string
	LastFetched       string
	DeleteHref        template.URL
	lastUsed          time.Time
	lastFetchedMs     int64
	subscriberCount   int
	complete, measure bool
}

type option struct {
	Value, Label string
	Selected     bool
}

type confirmView struct {
	ID, Title, LastUsed, Apps, Subscribers string
	Episodes                               int
	CancelHref                             template.URL
}

type cleanupView struct {
	Available     bool
	AvailableFrom string
	Titles        []string
	IDs           []string
	Episodes      int
	Hash          string
}

type dashboard struct {
	Version, StartedAt, Notice string
	Health                     healthView
	Tiles                      []tile
	Charts                     []chartView
	TrafficTiles               []tile
	Upstream                   []upstreamRow
	QueueLength                int
	Running                    bool
	Events                     []eventRow
	Failed                     []failedRow
	// the feed list, filtered and sorted
	Query       string
	Show, Sort  []option
	Rows        []feedRow
	Total       int
	Filtered    bool
	Back        string
	Confirm     *confirmView
	Cleanup     cleanupView
	Actions     []action
	Measured    bool
	MeasureFrom string
}

type adminView struct {
	// Failed: wrong token; Limited: too many failed attempts
	Login, Failed, Limited bool
	Dashboard              *dashboard
	// Client is the visitor's address as the rate limits see it, so a
	// self-hoster can check that their proxy passes it on
	Client string
}

// the feed list's filters and sort orders; the first is the default
var (
	showOptions = []option{{"", "Alle", false}, {"brukt", "Brukt siste 30 dager", false},
		{"ubrukt", "Ubrukt i 90+ dager", false}, {"henter", "Arkiv under innhenting", false},
		{"video", "Med video", false}, {"sesonger", "Sesonger", false}}
	sortOptions = []option{{"", "Sist brukt", false}, {"abonnenter", "Abonnenter", false},
		{"tittel", "Tittel", false}, {"episoder", "Episoder", false}, {"hentet", "Sist hentet fra NRK", false}}
)

// listQuery is the feed list's state, kept across actions.
type listQuery struct{ Query, Show, Sort string }

func validOption(options []option, value string) string {
	for _, o := range options {
		if o.Value == value {
			return value
		}
	}
	return ""
}

func parseListQuery(values url.Values) listQuery {
	query := strings.TrimSpace(values.Get("q"))
	if len(query) > 100 {
		query = query[:100]
	}
	return listQuery{Query: query, Show: validOption(showOptions, values.Get("vis")), Sort: validOption(sortOptions, values.Get("sorter"))}
}

// encode is the list state as a query string, without defaults.
func (q listQuery) encode() string {
	values := url.Values{}
	if q.Query != "" {
		values.Set("q", q.Query)
	}
	if q.Show != "" {
		values.Set("vis", q.Show)
	}
	if q.Sort != "" {
		values.Set("sorter", q.Sort)
	}
	return values.Encode()
}

func selected(options []option, value string) []option {
	out := make([]option, len(options))
	for i, o := range options {
		o.Selected = o.Value == value
		out[i] = o
	}
	return out
}

var norwegianMonths = []string{"jan.", "feb.", "mars", "april", "mai", "juni", "juli", "aug.", "sep.", "okt.", "nov.", "des."}
var norwegianMonthsLong = []string{"januar", "februar", "mars", "april", "mai", "juni", "juli", "august", "september", "oktober", "november", "desember"}

func formatDay(t time.Time) string {
	t = t.Local()
	return fmt.Sprintf("%d. %s", t.Day(), norwegianMonths[t.Month()-1])
}

func formatDate(t time.Time) string {
	t = t.Local()
	return fmt.Sprintf("%d. %s %d", t.Day(), norwegianMonthsLong[t.Month()-1], t.Year())
}

func formatMonth(t time.Time) string {
	t = t.Local()
	return fmt.Sprintf("%s %d", norwegianMonthsLong[t.Month()-1], t.Year())
}

// ago is a short Norwegian relative time ("for 2 t siden").
func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("for %d s siden", max(0, int(d.Seconds())))
	case d < time.Hour:
		return fmt.Sprintf("for %d min siden", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("for %d t siden", int(d.Hours()))
	case d < 48*time.Hour:
		return "for 1 dag siden"
	case d < 30*24*time.Hour:
		return fmt.Sprintf("for %d dager siden", int(d.Hours()/24))
	}
	return formatDate(t)
}

// formatInt groups thousands with a non-breaking space, as Norwegian does.
func formatInt(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + formatInt(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + " " + s[i:]
	}
	return s
}

var cursorPage = regexp.MustCompile(`[?&]page=(\d+)`)

// archiveText says how far a feed's archive has come and what it's doing.
func archiveText(o store.SeriesOverview, current string, queue []string) (string, bool) {
	reach := ""
	if o.OldestEpisode != nil {
		reach = "tilbake til " + formatMonth(*o.OldestEpisode)
	}
	if o.BacklogComplete {
		if o.OldestEpisode != nil {
			return "Komplett · fra " + formatMonth(*o.OldestEpisode), false
		}
		return "Komplett", false
	}
	parts := []string{}
	active := false
	switch position := slices.Index(queue, o.ID); {
	case o.ID == current:
		parts, active = append(parts, "Henter nå"), true
	case position == 0:
		parts, active = append(parts, "Neste i kø"), true
	case position > 0:
		parts, active = append(parts, fmt.Sprintf("I kø (%d foran)", position)), true
	default:
		parts = append(parts, "Venter på neste forespørsel")
	}
	if reach != "" {
		parts = append(parts, reach)
	}
	if o.BacklogCursor != nil {
		if m := cursorPage.FindStringSubmatch(*o.BacklogCursor); m != nil {
			parts = append(parts, "side "+m[1])
		}
	}
	return strings.Join(parts, " · "), active
}

func (s *Server) health(snap stats.Snapshot, today map[string]int, now time.Time) healthView {
	if snap.ConsecutiveFailures >= stats.HealthFailureThreshold {
		status := "nettverksfeil"
		if snap.LastFailureStatus != 0 {
			status = "status " + strconv.Itoa(snap.LastFailureStatus)
		}
		return healthView{"down", "NRK svarer ikke",
			fmt.Sprintf("%d feil på rad siden %s · siste: %s. Strømmene viser lagrede episoder så lenge.",
				snap.ConsecutiveFailures, formatTime(snap.FailingSince), status)}
	}
	if snap.LastUpstreamSuccess.IsZero() {
		return healthView{"idle", "Ingen kall til NRK siden oppstart", "Status vises etter første forespørsel til NRK."}
	}
	requests, errors := today[stats.DayNRK], today[stats.DayNRKErrors]
	rate := 0.0
	if requests > 0 {
		rate = float64(errors) / float64(requests)
	}
	detail := fmt.Sprintf("siste vellykkede kall %s · %s %% feil i dag (%s av %s)", ago(snap.LastUpstreamSuccess, now),
		strings.Replace(strconv.FormatFloat(rate*100, 'f', 1, 64), ".", ",", 1), formatInt(errors), formatInt(requests))
	if requests >= warnMinRequests && rate > warnErrorRate {
		return healthView{"warn", "NRK svarer, men med feil", detail}
	}
	return healthView{"ok", "NRK svarer", detail}
}

// barChart draws one bar per day as inline SVG: no script, no library.
// overlay (errors) draws over the bars in the brand colour.
func barChart(label string, days []string, values, overlay []int) template.HTML {
	const width, height, slot = 300, 56, 10
	peak := 1
	for _, v := range values {
		peak = max(peak, v)
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" width="100%%" height="%d" preserveAspectRatio="none" role="img" aria-label="%s">`,
		width, height, height, template.HTMLEscapeString(label))
	for i, v := range values {
		x := i * slot
		h := max(1, v*height/peak)
		opacity := ""
		if v == 0 {
			opacity = ` opacity="0.25"`
		}
		title := formatDay(dayTime(days[i])) + ": " + formatInt(v)
		if overlay != nil {
			title += " (" + formatInt(overlay[i]) + " feil)"
		}
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="1.5" fill="currentColor"%s><title>%s</title></rect>`,
			x+1, height-h, slot-2, h, opacity, template.HTMLEscapeString(title))
		if overlay != nil && overlay[i] > 0 {
			eh := max(2, overlay[i]*height/peak)
			fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="1.5" fill="var(--color-brand)"><title>%s</title></rect>`,
				x+1, height-eh, slot-2, eh, template.HTMLEscapeString(title))
		}
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

func dayTime(day string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02", day, time.Local)
	return t
}

func (s *Server) charts(daily map[string]map[string]int, days []string) []chartView {
	series := func(keys ...string) []int {
		values := make([]int, len(days))
		for i, day := range days {
			for _, key := range keys {
				values[i] += daily[day][key]
			}
		}
		return values
	}
	sum := func(values []int) int {
		total := 0
		for _, v := range values {
			total += v
		}
		return total
	}
	last := len(days) - 1
	nrk, nrkErrors := series(stats.DayNRK), series(stats.DayNRKErrors)
	feeds, searches, limited := series(stats.DayFeeds200, stats.DayFeeds304), series(stats.DaySearches), series(stats.DayRateLimited)
	chart := func(title string, values, overlay []int, todayNote string) chartView {
		today := formatInt(values[last]) + " i dag" + todayNote
		total := formatInt(sum(values)) + " siste 30 dager"
		return chartView{Title: title, Today: today, Total: total,
			SVG: barChart(title+" siste 30 dager: "+today+", "+total, days, values, overlay)}
	}
	return []chartView{
		chart("Forespørsler til NRK", nrk, nrkErrors, fmt.Sprintf(" (%s feil)", formatInt(nrkErrors[last]))),
		chart("Strømmer levert", feeds, nil, ""),
		chart("Søk", searches, nil, ""),
		chart("Avvist (429)", limited, nil, ""),
	}
}

func cleanupHash(ids []string) string {
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	return hex.EncodeToString(sum[:8])
}

// unusedFeeds are the feeds no app has requested within cleanupAfter,
// sorted by id. Only meaningful once activity has been measured that long.
func unusedFeeds(series []store.SeriesOverview, activity map[string]*store.FeedActivity, now time.Time) []store.SeriesOverview {
	var unused []store.SeriesOverview
	for _, o := range series {
		if a := activity[o.ID]; a == nil || now.Sub(a.LastRequestedAt) > cleanupAfter {
			unused = append(unused, o)
		}
	}
	slices.SortFunc(unused, func(a, b store.SeriesOverview) int { return cmp.Compare(a.ID, b.ID) })
	return unused
}

func (s *Server) dashboard(notice string, values url.Values) *dashboard {
	now := clock.Now()
	snap := s.Stats.Snapshot()
	series, _ := s.Store.ListSeriesOverview()
	failed, _ := s.Store.ListFailedEpisodes(30)
	status := s.Crawler.Status()
	activity, _ := s.Store.ReadFeedActivity(now.Add(-clientWindow))
	since, measured := s.Store.ActivitySince()
	list := parseListQuery(values)

	// the last 30 days, today from the database plus what's not flushed
	days := make([]string, chartDays)
	for i := range days {
		days[i] = stats.Day(now.AddDate(0, 0, i-chartDays+1))
	}
	daily, _ := s.Store.ReadDailyStats(days[0])
	if daily == nil {
		daily = map[string]map[string]int{}
	}
	today := days[chartDays-1]
	if daily[today] == nil {
		daily[today] = map[string]int{}
	}
	for key, n := range s.Stats.PendingDay(today) {
		daily[today][key] += n
	}

	version := s.Config.BuildID
	if len(version) > 10 {
		version = version[:10]
	}
	d := &dashboard{
		Version: version, StartedAt: formatTime(snap.StartedAt), Notice: notice,
		Health:      s.health(snap, daily[today], now),
		Charts:      s.charts(daily, days),
		QueueLength: status.QueueLength, Running: status.Running,
		Query: list.Query, Show: selected(showOptions, list.Show), Sort: selected(sortOptions, list.Sort),
		Back:     list.encode(),
		Actions:  []action{{"refresh", "Oppdater"}, {"recrawl", "Re-crawl"}},
		Measured: measured,
	}
	if measured {
		d.MeasureFrom = formatDate(since)
	}

	titles := map[string]string{}
	for _, o := range series {
		titles[o.ID] = o.Title
	}
	incomplete, usedRecently, subscribers, anySubscribers := 0, 0, 0, false
	var rows []feedRow
	for _, o := range series {
		row := feedRow{ID: o.ID, Title: o.Title, Episodes: o.EpisodeCount, Videos: o.VideoCount,
			LastFetched: formatTime(o.LastFetchedAt), lastFetchedMs: o.LastFetchedAt.UnixMilli(),
			complete: o.BacklogComplete, LastUsed: "ikke målt ennå", Subscribers: "–"}
		if ref := feedid.Parse(o.ID); ref.SeasonID != "" {
			row.Parent = cmp.Or(titles[ref.SeriesID], ref.SeriesID)
		}
		row.Archive, row.ArchiveActive = archiveText(o, status.Current, status.Queue)
		if !o.BacklogComplete {
			incomplete++
		}
		if a := activity[o.ID]; a != nil {
			row.measure, row.lastUsed = true, a.LastRequestedAt
			row.LastUsed = ago(a.LastRequestedAt, now)
			row.Apps = strings.Join(a.Clients, ", ")
			if now.Sub(a.LastRequestedAt) <= 30*24*time.Hour {
				usedRecently++
			}
			if a.Subscribers != nil {
				row.subscriberCount = *a.Subscribers
				row.Subscribers = "≈ " + formatInt(*a.Subscribers)
				subscribers += *a.Subscribers
				anySubscribers = true
			}
		}
		rows = append(rows, row)
	}
	d.Total = len(rows)

	// filter
	unusedBefore := now.Add(-90 * 24 * time.Hour)
	query := strings.ToLower(list.Query)
	for _, row := range rows {
		keep := true
		switch list.Show {
		case "brukt":
			keep = row.measure && now.Sub(row.lastUsed) <= 30*24*time.Hour
		case "ubrukt":
			keep = (row.measure && row.lastUsed.Before(unusedBefore)) || (!row.measure && measured && since.Before(unusedBefore))
		case "henter":
			keep = !row.complete
		case "video":
			keep = row.Videos > 0
		case "sesonger":
			keep = row.Parent != ""
		}
		if keep && query != "" {
			keep = strings.Contains(strings.ToLower(row.Title), query) || strings.Contains(strings.ToLower(row.ID), query)
		}
		if keep {
			d.Rows = append(d.Rows, row)
		}
	}
	d.Filtered = list.Query != "" || list.Show != ""
	for i := range d.Rows {
		values := url.Values{"slett": {d.Rows[i].ID}}
		for key, v := range mustParseQuery(d.Back) {
			values[key] = v
		}
		d.Rows[i].DeleteHref = template.URL("/admin?" + values.Encode())
	}

	// sort; ties by title
	byTitle := func(a, b feedRow) int { return cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)) }
	slices.SortStableFunc(d.Rows, func(a, b feedRow) int {
		var c int
		switch list.Sort {
		case "abonnenter":
			c = cmp.Compare(b.subscriberCount, a.subscriberCount)
		case "tittel":
			c = 0
		case "episoder":
			c = cmp.Compare(b.Episodes, a.Episodes)
		case "hentet":
			c = cmp.Compare(b.lastFetchedMs, a.lastFetchedMs)
		default:
			// never measured last
			c = cmp.Compare(b.lastUsed.UnixMilli(), a.lastUsed.UnixMilli())
		}
		return cmp.Or(c, byTitle(a, b))
	})

	subscriberTile := "–"
	if anySubscribers {
		subscriberTile = "≈ " + formatInt(subscribers)
	}
	followed := "strømmer"
	if size := s.Catalog.Size(); size > 0 {
		followed = fmt.Sprintf("strømmer (av %s podkaster hos NRK)", formatInt(size))
	}
	d.Tiles = []tile{
		{formatInt(len(series)), followed, ""},
		{formatInt(usedRecently), "brukt siste 30 dager", "/admin?vis=brukt"},
		{subscriberTile, "abonnenter, minst", "#abonnenter"},
		{formatInt(s.Store.CountEpisodes()), "episoder i arkiv", ""},
		{formatInt(incomplete), "arkiv under innhenting", "/admin?vis=henter"},
		{formatBytes(s.Store.DatabaseSizeBytes()), "database", ""},
	}
	d.TrafficTiles = []tile{
		{formatInt(snap.UpstreamTotal), "forespørsler til NRK", ""},
		{formatInt(snap.UpstreamErrors), "NRK-feil", ""},
		{fmt.Sprintf("%s / %s", formatInt(snap.Feeds200), formatInt(snap.Feeds304)), "strøm 200 / 304", ""},
		{formatInt(snap.RateLimited), "429 servert", ""},
	}
	for _, category := range stats.Categories {
		if b := snap.Upstream[category]; b.Requests > 0 {
			d.Upstream = append(d.Upstream, upstreamRow{category, b.Requests, b.Errors})
		}
	}
	for i, e := range status.Events {
		if i == 8 {
			break
		}
		d.Events = append(d.Events, eventRow{At: formatTime(e.At), Message: e.Message})
	}
	for _, f := range failed {
		d.Failed = append(d.Failed, failedRow{SeriesID: f.SeriesID, EpisodeID: f.EpisodeID,
			Attempts: f.Attempts, NextRetryAt: formatTime(f.NextRetryAt)})
	}

	// the delete confirmation
	if id := values.Get("slett"); feedid.Valid(id) {
		for _, row := range rows {
			if row.ID == id {
				d.Confirm = &confirmView{ID: row.ID, Title: row.Title, Episodes: row.Episodes,
					LastUsed: row.LastUsed, Apps: row.Apps, Subscribers: row.Subscribers,
					CancelHref: template.URL("/admin?" + d.Back)}
			}
		}
	}

	// cleanup of unused feeds, once activity has been measured long enough
	switch {
	case !measured:
		d.Cleanup.AvailableFrom = "180 dager etter at målingen av bruk starter"
	case now.Sub(since) < cleanupAfter:
		d.Cleanup.AvailableFrom = formatDate(since.Add(cleanupAfter))
	default:
		d.Cleanup.Available = true
		for _, o := range unusedFeeds(series, activity, now) {
			d.Cleanup.IDs = append(d.Cleanup.IDs, o.ID)
			d.Cleanup.Titles = append(d.Cleanup.Titles, o.Title)
			d.Cleanup.Episodes += o.EpisodeCount
		}
		d.Cleanup.Hash = cleanupHash(d.Cleanup.IDs)
	}
	return d
}

func mustParseQuery(query string) url.Values {
	values, _ := url.ParseQuery(query)
	return values
}
