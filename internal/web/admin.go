package web

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/feedid"
	"github.com/matsbst/kringkasting/internal/nrk"
)

// Cookie auth for /admin, driven by ADMIN_TOKEN. No token = admin disabled.
const adminCookie = "kringkasting_admin"

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

// isHTTPS: behind Cloudron/Cloudflare the upstream request is plain HTTP,
// so trust the forwarded proto or the configured origin for the cookie's
// Secure flag; only genuinely local HTTP development skips it.
func (s *Server) isHTTPS(r *http.Request) bool {
	// a configured https origin wins: a proxy hop that speaks plain HTTP
	// (Cloudflare's Flexible mode) must not drop the cookie's Secure flag
	if configured, https := s.Config.ConfiguredOriginHTTPS(); configured {
		return https
	}
	return requestHTTPS(r)
}

const adminMaxBody = 16 << 10

// sameOrigin rejects cross-origin form posts. Browsers send Sec-Fetch-Site
// (or at least Origin) on POSTs; requests with neither are not from a
// browser and carry no ambient cookie risk.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host == r.Host
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
}

func (s *Server) adminPage(w http.ResponseWriter, r *http.Request, view adminView) {
	noStore(w)
	view.Passkeys = s.passkeyView(view.Dashboard != nil)
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
		query := r.URL.Query()
		s.adminPage(w, r, adminView{Dashboard: s.dashboard(query.Get("m"), query), Client: clientKey(r)})
		return
	}

	// SameSite=Strict still lets sibling subdomains (same site, other
	// origin) post with the cookie, so require the browser to say the
	// form came from this origin. The admin forms are a few short fields.
	r.Body = http.MaxBytesReader(w, r.Body, adminMaxBody)
	if !sameOrigin(r) || r.ParseForm() != nil {
		s.adminPage(w, r, adminView{Login: true})
		return
	}
	// login, throttled: 5 failed attempts, then one per minute. Only
	// failures count, and the client address can't be forged with
	// headers, so others can't lock the admin out from one address.
	if tokens, ok := r.PostForm["token"]; ok {
		client := clientKey(r)
		if !s.limiter.allowScoped("adminlogin", client, 5, 1.0/60, 20, 1.0/30) {
			s.adminPage(w, r, adminView{Login: true, Limited: true})
			return
		}
		if !s.tokenMatches(tokens[0]) {
			s.adminPage(w, r, adminView{Login: true, Failed: true})
			return
		}
		s.limiter.refundScoped("adminlogin", client)
		if s.tokenLoginOff() {
			s.adminPage(w, r, adminView{Login: true, TokenOff: true})
			return
		}
		if err := s.startSession(w, r, "token"); err != nil {
			log.Printf("start admin session: %v", err)
			s.adminPage(w, r, adminView{Login: true, Failed: true})
			return
		}
		redirectAdmin(w, "/admin")
		return
	}
	if r.PostForm.Get("action") == "logout" {
		s.endSession(w, r)
		redirectAdmin(w, "/admin")
		return
	}
	if !s.authenticated(r) {
		s.adminPage(w, r, adminView{Login: true})
		return
	}

	// admin actions (POST + the SameSite=Strict cookie + sameOrigin)
	seriesID := r.PostForm.Get("seriesId")
	notice := "Ukjent handling"
	// back to the same filtered list
	back := parseListQuery(func() url.Values { v, _ := url.ParseQuery(r.PostForm.Get("back")); return v }()).encode()
	if back != "" {
		back = "&" + back
	}
	switch r.PostForm.Get("action") {
	case "logout-all":
		if err := s.Store.DeleteAdminSessions(); err != nil {
			log.Printf("end admin sessions: %v", err)
		}
		s.setSession(w, r, "", 0)
		redirectAdmin(w, "/admin")
		return
	case "passkey-delete":
		redirectAdmin(w, "/admin?m="+nrk.EncodeURIComponent(s.deletePasskey(r.PostForm.Get("passkey")))+back)
		return
	}
	if r.PostForm.Get("action") == "cleanup" {
		redirectAdmin(w, "/admin?m="+nrk.EncodeURIComponent(s.cleanup(r.PostForm))+back)
		return
	}
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
			// only from the confirmation, which names the feed it deletes;
			// anything else (an old tab, a stray post) shows it first
			if r.PostForm.Get("confirm") != seriesID {
				redirectAdmin(w, "/admin?slett="+nrk.EncodeURIComponent(seriesID)+back)
				return
			}
			s.Store.DeleteSeriesByID(seriesID)
			notice = seriesID + " slettet"
		}
	}
	redirectAdmin(w, "/admin?m="+nrk.EncodeURIComponent(notice)+back)
}

// cleanup deletes the unused feeds the admin was shown, and only those:
// the form carries the preview's count and a hash of its feed ids.
func (s *Server) cleanup(form url.Values) string {
	now := clock.Now()
	since, measured := s.Store.ActivitySince()
	if !measured || now.Sub(since) < cleanupAfter {
		return "Opprydding er ikke tilgjengelig ennå"
	}
	series, err := s.Store.ListSeriesOverview()
	if err != nil {
		return "Opprydding feilet"
	}
	activity, err := s.Store.ReadFeedActivity(now.Add(-clientWindow))
	if err != nil {
		return "Opprydding feilet"
	}
	unused := unusedFeeds(series, activity, now)
	ids := make([]string, len(unused))
	for i, o := range unused {
		ids[i] = o.ID
	}
	if form.Get("count") != strconv.Itoa(len(ids)) || form.Get("hash") != cleanupHash(ids) {
		return "Listen over ubrukte strømmer har endret seg. Se over den på nytt."
	}
	for _, id := range ids {
		s.Store.DeleteSeriesByID(id)
	}
	return fmt.Sprintf("%d ubrukte strømmer slettet", len(ids))
}
