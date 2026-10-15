package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/matsbst/kringkasting/internal/feedid"
	"github.com/matsbst/kringkasting/internal/nrk"
	"github.com/matsbst/kringkasting/internal/stats"
)

// Cookie auth for /admin, driven by ADMIN_TOKEN. No token = admin disabled.
const adminCookie = "kringkasting_admin"

type tile struct{ Value, Label string }

type upstreamRow struct {
	Category         string
	Requests, Errors int
}

type eventRow struct{ At, Message string }

type failedRow struct {
	SeriesID, EpisodeID, NextRetryAt string
	Attempts                         int
}

type seriesRow struct {
	ID, Title, LastFetchedAt string
	EpisodeCount             int
	Complete                 bool
}

type gcRow struct {
	At      string
	Deleted int
}

type action struct{ Action, Label string }

type dashboard struct {
	Version, StartedAt, Notice string
	Tiles, TrafficTiles        []tile
	Upstream                   []upstreamRow
	QueueLength                int
	Running                    bool
	LastGc                     *gcRow
	Events                     []eventRow
	Failed                     []failedRow
	Series                     []seriesRow
	Actions                    []action
}

type adminView struct {
	Login, Failed bool
	Dashboard     *dashboard
}

// formatTime matches the Deno version's nb-NO toLocaleString with
// day/month/hour/minute ("5.3., 07:09") in the server's time zone.
func formatTime(t time.Time) string {
	local := t.Local()
	return fmt.Sprintf("%d.%d., %02d:%02d", local.Day(), int(local.Month()), local.Hour(), local.Minute())
}

func formatBytes(bytes int64) string {
	if bytes > 1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	}
	return fmt.Sprintf("%d kB", int64(math.Round(float64(bytes)/1024)))
}

func (s *Server) adminEnabled() bool { return s.Config.AdminToken != "" }

// tokenMatches compares hashes, so the comparison takes constant time.
func (s *Server) tokenMatches(candidate string) bool {
	if !s.adminEnabled() {
		return false
	}
	a, b := sha256.Sum256([]byte(candidate)), sha256.Sum256([]byte(s.Config.AdminToken))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

func (s *Server) authenticated(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Cookie"), ";") {
		name, value, _ := strings.Cut(strings.TrimSpace(part), "=")
		if name == adminCookie {
			decoded, err := url.PathUnescape(value)
			if err != nil {
				return false
			}
			return s.tokenMatches(decoded)
		}
	}
	return false
}

// isHTTPS: behind Cloudron/Cloudflare the upstream request is plain HTTP,
// so trust the forwarded proto or the configured origin for the cookie's
// Secure flag; only genuinely local HTTP development skips it.
func (s *Server) isHTTPS(r *http.Request) bool {
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return strings.TrimSpace(strings.Split(proto, ",")[0]) == "https"
	}
	if configured, https := s.Config.ConfiguredOriginHTTPS(); configured {
		return https
	}
	return r.TLS != nil
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
}

func (s *Server) adminPage(w http.ResponseWriter, r *http.Request, view adminView) {
	noStore(w)
	data := s.page(r, "Admin – Kringkasting")
	data.Robots = "noindex"
	data.Admin = view
	s.render(w, r, http.StatusOK, "admin", data)
}

func redirectAdmin(w http.ResponseWriter, location string) {
	noStore(w)
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusSeeOther)
}

func (s *Server) admin(w http.ResponseWriter, r *http.Request) {
	if !s.adminEnabled() {
		s.errorPage(w, r, http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		if !s.authenticated(r) {
			s.adminPage(w, r, adminView{Login: true})
			return
		}
		s.adminPage(w, r, adminView{Dashboard: s.dashboard(r.URL.Query().Get("m"))})
		return
	}

	if err := r.ParseMultipartForm(1 << 20); err != nil && err != http.ErrNotMultipart {
		s.adminPage(w, r, adminView{Login: true})
		return
	}
	// login, throttled: 5 attempts, then one per minute
	if tokens, ok := r.PostForm["token"]; ok {
		if !s.limiter.allowScoped("adminlogin", clientKey(r), 5, 1.0/60, 20, 1.0/30) || !s.tokenMatches(tokens[0]) {
			s.adminPage(w, r, adminView{Login: true, Failed: true})
			return
		}
		secure := ""
		if s.isHTTPS(r) {
			secure = " Secure;"
		}
		w.Header().Set("Set-Cookie", fmt.Sprintf("%s=%s; Path=/admin; HttpOnly;%s SameSite=Strict; Max-Age=2592000",
			adminCookie, nrk.EncodeURIComponent(tokens[0]), secure))
		redirectAdmin(w, "/admin")
		return
	}
	if !s.authenticated(r) {
		s.adminPage(w, r, adminView{Login: true})
		return
	}

	// admin actions (POST + the SameSite=Strict cookie keeps this same-origin)
	seriesID := r.PostForm.Get("seriesId")
	notice := "Ukjent handling"
	// season feeds have composite "{seriesId}/sesong/{seasonId}" ids
	if feedid.Valid(seriesID) {
		switch r.PostForm.Get("action") {
		case "refresh":
			s.Store.ExpireSeries(seriesID)
			if _, err := s.Feeds.GetSeries(seriesID); err != nil {
				notice = seriesID + " kunne ikke oppdateres"
			} else {
				notice = seriesID + " oppdatert"
			}
		case "recrawl":
			s.Store.SetBacklogState(seriesID, nil, false)
			s.Crawler.Enqueue(seriesID)
			notice = "Arkiv-innhenting startet på nytt for " + seriesID
		case "delete":
			s.Store.DeleteSeriesByID(seriesID)
			notice = seriesID + " slettet"
		}
	}
	redirectAdmin(w, "/admin?m="+nrk.EncodeURIComponent(notice))
}

func (s *Server) dashboard(notice string) *dashboard {
	snap := s.Stats.Snapshot()
	series, _ := s.Store.ListSeriesOverview()
	failed, _ := s.Store.ListFailedEpisodes(30)
	status := s.Crawler.Status()

	version := s.Config.BuildID
	if len(version) > 10 {
		version = version[:10]
	}
	d := &dashboard{
		Version: version, StartedAt: formatTime(snap.StartedAt), Notice: notice,
		QueueLength: status.QueueLength, Running: status.Running,
		Actions: []action{{"refresh", "Oppdater"}, {"recrawl", "Re-crawl"}, {"delete", "Slett"}},
	}

	incomplete := 0
	for _, o := range series {
		if !o.BacklogComplete {
			incomplete++
		}
		d.Series = append(d.Series, seriesRow{ID: o.ID, Title: o.Title, EpisodeCount: o.EpisodeCount,
			Complete: o.BacklogComplete, LastFetchedAt: formatTime(o.LastFetchedAt)})
	}
	followed := "serier fulgt"
	if size := s.Catalog.Size(); size > 0 {
		followed = fmt.Sprintf("serier fulgt (av %d hos NRK)", size)
	}
	d.Tiles = []tile{
		{strconv.Itoa(len(series)), followed},
		{strconv.Itoa(s.Store.CountEpisodes()), "episoder i arkiv"},
		{strconv.Itoa(incomplete), "arkiv under innhenting"},
		{formatBytes(s.Store.DatabaseSizeBytes()), "database"},
	}
	d.TrafficTiles = []tile{
		{strconv.Itoa(snap.UpstreamTotal), "forespørsler til NRK"},
		{strconv.Itoa(snap.UpstreamErrors), "NRK-feil"},
		{fmt.Sprintf("%d / %d", snap.Feeds200, snap.Feeds304), "feed 200 / 304"},
		{strconv.Itoa(snap.RateLimited), "429 servert"},
	}
	for _, category := range stats.Categories {
		if b := snap.Upstream[category]; b.Requests > 0 {
			d.Upstream = append(d.Upstream, upstreamRow{category, b.Requests, b.Errors})
		}
	}
	if snap.LastGc != nil {
		d.LastGc = &gcRow{At: formatTime(snap.LastGc.At), Deleted: snap.LastGc.Deleted}
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
	return d
}
