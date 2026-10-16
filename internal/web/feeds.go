package web

import (
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/matsbst/kringkasting/internal/feed"
	"github.com/matsbst/kringkasting/internal/feedid"
)

const (
	// feedMaxAge is what podcast clients may cache.
	feedMaxAge = 30 * 60
	// feedSMaxAge is what shared caches (Cloudflare) may cache, roughly the
	// hourly NRK sync.
	feedSMaxAge = 60 * 60
)

func (s *Server) seriesFeed(w http.ResponseWriter, r *http.Request, seriesID string) {
	if !feedid.ValidResource(seriesID) {
		writeJSON(w, http.StatusBadRequest, message{"Invalid series id"})
		return
	}
	s.serveFeed(w, r, seriesID)
}

// seasonFeed serves one customSeason of an umbrella podcast. NRK publishes
// standalone shows this way, each with its own title and artwork.
func (s *Server) seasonFeed(w http.ResponseWriter, r *http.Request, seriesID, seasonID string) {
	if !feedid.ValidResource(seriesID) || !feedid.ValidResource(seasonID) {
		writeJSON(w, http.StatusBadRequest, message{"Invalid feed id"})
		return
	}
	s.serveFeed(w, r, feedid.Season(seriesID, seasonID))
}

// serveFeed serves a whole series or a customSeason feed by feed id (also
// the storage key and the path under /api/feeds/). The id is validated.
func (s *Server) serveFeed(w http.ResponseWriter, r *http.Request, feedID string) {
	// fetching an unknown feed costs real upstream work: cap the rate per
	// client (12 cold fetches/min, burst 10). Known feeds are never limited.
	if !s.Store.HasSeries(feedID) && !s.limiter.allowScoped("coldfeed", clientKey(r), 10, 0.2, 30, 0.5) {
		tooManyRequests(w, 60)
		return
	}
	origin := s.origin(r)

	// fast path: a fresh feed whose rendered XML is memoized needs no
	// episode read, no rendering and no hashing; most polls land here
	version := s.Store.DataVersion(feedID)
	if memoized := s.Cache.Peek(feedID, origin, version); memoized != nil && s.Feeds.MetaIsFresh(feedID) {
		s.respondFeed(w, r, feedID, *memoized)
		return
	}

	series, err := s.Feeds.GetSeries(feedID)
	if err != nil {
		log.Printf("Failed to get series %s: %v", feedID, err)
		writeJSON(w, http.StatusBadGateway, message{"Upstream error"})
		return
	}
	if series == nil {
		writeJSON(w, http.StatusNotFound, message{"Series not found"})
		return
	}
	s.respondFeed(w, r, feedID, s.Cache.Render(series, origin))
}

func (s *Server) respondFeed(w http.ResponseWriter, r *http.Request, feedID string, rendered feed.Rendered) {
	// podcast clients poll constantly; answer unchanged feeds with 304.
	// If-None-Match takes precedence over If-Modified-Since (RFC 9110).
	var notModified bool
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		notModified = etagMatches(inm, rendered.ETag)
	} else {
		notModified = notModifiedSince(r.Header.Get("If-Modified-Since"), rendered.LastModified)
	}
	s.Stats.RecordFeedServed(notModified)
	// for the dashboard: when, and by which app (only its name is kept)
	s.Stats.RecordFeedRequest(feedID, r.UserAgent())

	w.Header().Set("ETag", rendered.ETag)
	w.Header().Set("Last-Modified", rendered.LastModified.UTC().Format(http.TimeFormat))
	setCacheHeaders(w, feedMaxAge, feedSMaxAge)
	if notModified {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, rendered.XML)
}

type chaptersBody struct {
	Version  string         `json:"version"`
	Chapters []feed.Chapter `json:"chapters"`
}

func (s *Server) chapters(w http.ResponseWriter, r *http.Request, seriesID, episodeID string) {
	if !feedid.ValidResource(seriesID) || !feedid.ValidResource(episodeID) {
		writeJSON(w, http.StatusBadRequest, message{"Invalid id"})
		return
	}
	if !s.limiter.allowScoped("chapters", clientKey(r), 20, 0.5, 60, 1) {
		tooManyRequests(w, 30)
		return
	}
	result := s.Chapters.Get(seriesID, episodeID)
	if result.Status == http.StatusServiceUnavailable {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusServiceUnavailable, message{"Chapters unavailable"})
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400, s-maxage=86400")
	if result.Status == http.StatusNotFound {
		writeJSON(w, http.StatusNotFound, message{"Episode " + episodeID + " is missing"})
		return
	}
	writeJSONType(w, http.StatusOK, chaptersBody{Version: "1.2.0", Chapters: result.Chapters}, "application/json+chapters")
}

// sitemapPaths grows as more indexable pages arrive.
var sitemapPaths = []string{"/", "/personvern"}

func (s *Server) sitemap(w http.ResponseWriter, r *http.Request) {
	origin := s.origin(r)
	var urls strings.Builder
	for _, path := range sitemapPaths {
		urls.WriteString("<url><loc>" + origin + path + "</loc></url>")
	}
	setCacheHeaders(w, 3600, 86400)
	w.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>`+
		`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`+urls.String()+`</urlset>`)
}
