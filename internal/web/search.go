package web

import (
	"encoding/base64"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/matsbst/kringkasting/internal/feedid"
	"github.com/matsbst/kringkasting/internal/nrk"
	"github.com/matsbst/kringkasting/internal/nrklink"
	"github.com/matsbst/kringkasting/internal/store"
)

const (
	minQueryLength = 2
	maxQueryLength = 100
	// searchTTL matches the server-side search micro-cache.
	searchTTL = 5 * 60
)

// fallbackSuggestions are shown until the live catalog has loaded.
var fallbackSuggestions = []string{
	"Oppdatert", "Hele historien", "Radioresepsjonen", "Abels tårn", "Trygdekontoret", "Berrum & Beyer",
}

// jsLength is a string's length as JavaScript counts it (UTF-16 units).
func jsLength(s string) int { return len(utf16.Encode([]rune(s))) }

// SeriesSummary is the slim shape the UI needs for a search hit, and what
// /api/search returns.
type SeriesSummary struct {
	SeriesID string `json:"seriesId"`
	// SeasonID is set when the hit is a customSeason: a standalone show
	// published as a season of an umbrella podcast.
	SeasonID    string  `json:"seasonId,omitempty"`
	Title       string  `json:"title"`
	Description *string `json:"description"`
	ImageURL    *string `json:"imageUrl"`
	// StreamOnly: NRK offers this show only as HLS; needs an HLS-capable app.
	StreamOnly bool `json:"streamOnly,omitempty"`
}

// FeedID is also the path under /api/feeds/ and NRK's path under /podkast/.
func (s SeriesSummary) FeedID() string {
	if s.SeasonID != "" {
		return feedid.Season(s.SeriesID, s.SeasonID)
	}
	return s.SeriesID
}

// toSummaries maps NRK hits to summaries, flagging the ones already crawled
// and found to be stream-only.
func (s *Server) toSummaries(results []nrk.SearchResult) []SeriesSummary {
	summaries := make([]SeriesSummary, len(results))
	ids := make([]string, len(results))
	for i, result := range results {
		// prefer square (1:1) artwork over the default 16:9 images
		images := result.Images1x1
		if images == nil {
			images = result.Images
		}
		summary := SeriesSummary{SeriesID: result.SeriesID, Title: result.Title, Description: result.Description}
		if result.Type == "customSeason" && result.SeasonID != nil && *result.SeasonID != "" {
			summary.SeasonID = *result.SeasonID
		}
		if image := pickImage(images, 400); image != "" {
			summary.ImageURL = &image
		}
		summaries[i] = summary
		ids[i] = summary.FeedID()
	}
	streamOnly := s.Store.StreamOnlySeriesIDs(ids)
	for i := range summaries {
		summaries[i].StreamOnly = streamOnly[ids[i]]
	}
	return summaries
}

// pickImage picks the image variant closest to the wanted rendered size.
func pickImage(images []nrk.SearchImage, wanted int) string {
	if len(images) == 0 {
		return ""
	}
	width := func(i nrk.SearchImage) int {
		if i.Width == nil {
			return 0
		}
		return *i.Width
	}
	abs := func(n int) int {
		if n < 0 {
			return -n
		}
		return n
	}
	best := images[0]
	for _, candidate := range images[1:] {
		if abs(width(candidate)-wanted) < abs(width(best)-wanted) {
			best = candidate
		}
	}
	return best.URI
}

// maxLinkLength bounds pasted NRK links, which run longer than names (an
// episode link is ~110 characters).
const maxLinkLength = 500

// found is the outcome of a query: a name search, or a pasted NRK link.
type found struct {
	summaries []SeriesSummary
	labels    []string // per summary; set for link results offering a choice
	link      bool     // the query was a supported NRK link
	episode   bool     // ... to a single episode
	// unsupported: the query looks like a link, but not one we can resolve
	unsupported bool
	// notFound: a supported link to a podcast NRK doesn't know
	notFound bool
	// failed: NRK was unavailable
	failed bool
}

// queryAllowed applies the length limits: names 2–100 characters, links
// up to maxLinkLength.
func queryAllowed(query string) bool {
	n := jsLength(query)
	if _, result := nrklink.Parse(query); result != nrklink.NotALink {
		return n <= maxLinkLength
	}
	return n >= minQueryLength && n <= maxQueryLength
}

// find runs a name search, or resolves a pasted NRK link to the feeds it
// can be subscribed as. The caller has applied rate limits.
func (s *Server) find(query string) found {
	link, result := nrklink.Parse(query)
	switch result {
	case nrklink.Unsupported:
		return found{unsupported: true}
	case nrklink.Supported:
		resolution, outcome := s.NRK.Resolve(link)
		switch outcome {
		case nrk.Unavailable:
			return found{link: true, failed: true}
		case nrk.NotFound:
			return found{link: true, notFound: true}
		}
		f := found{link: true, episode: resolution.Episode}
		ids := make([]string, len(resolution.Shows))
		for i, show := range resolution.Shows {
			summary := SeriesSummary{SeriesID: show.SeriesID, SeasonID: show.SeasonID, Title: show.Title,
				Description: show.Description}
			if show.ImageURL != "" {
				image := show.ImageURL
				summary.ImageURL = &image
			}
			f.summaries = append(f.summaries, summary)
			ids[i] = summary.FeedID()
		}
		streamOnly := s.Store.StreamOnlySeriesIDs(ids)
		for i := range f.summaries {
			f.summaries[i].StreamOnly = streamOnly[ids[i]]
		}
		// with a season and the whole podcast on offer, say which is which
		if len(resolution.Shows) > 1 {
			whole := resolution.Shows[len(resolution.Shows)-1]
			for _, show := range resolution.Shows {
				switch {
				case show.SeasonID != "":
					f.labels = append(f.labels, "Sesong i «"+whole.Title+"»")
				case link.Kind == nrklink.Series:
					f.labels = append(f.labels, "Hele serien")
				default:
					f.labels = append(f.labels, "Hele podkasten")
				}
			}
		}
		return f
	}
	results := s.NRK.Search(query)
	if results == nil {
		return found{failed: true}
	}
	return found{summaries: s.toSummaries(results)}
}

func (s *Server) searchAPI(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if !queryAllowed(query) {
		writeJSON(w, http.StatusOK, map[string][]SeriesSummary{"results": {}})
		return
	}
	if !s.limiter.allowScoped("search", clientKey(r), 30, 0.5, 120, 2) {
		tooManyRequests(w, 30)
		return
	}
	s.Stats.RecordSearchServed()
	f := s.find(query)
	if f.failed {
		// upstream failure: never cache it as an empty result
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusServiceUnavailable, message{"Search unavailable"})
		return
	}
	setCacheHeaders(w, searchTTL, searchTTL)
	summaries := f.summaries
	if summaries == nil {
		summaries = []SeriesSummary{}
	}
	writeJSON(w, http.StatusOK, map[string][]SeriesSummary{"results": summaries})
}

// searchView is the search UI state rendered below the form.
type searchView struct {
	// Input is the query as typed (the input's value); Query is trimmed
	Input       string
	Query       string
	Status      string
	HasResults  bool
	Failed      bool
	Cards       []card
	Suggestions []string
	// Link: the results come from a pasted NRK link
	Link bool
	// Notice explains a link result (e.g. a link to a single episode)
	Notice string
	// UnsupportedLink / LinkNotFound: messages instead of results
	UnsupportedLink bool
	LinkNotFound    bool
}

type appLink struct {
	ID, Name string
	Href     template.URL
	Icon     template.HTML
	// Platforms the app exists on, space-separated ("ios android"). On a
	// phone, app.js hides the others; computers list every app.
	Platforms string
}

type card struct {
	SeriesSummary
	FeedURL, ShareURL, NrkURL, DialogID string
	ImageURL, Description               string
	// Label says what a link result subscribes to ("Hele podkasten")
	Label       string
	Apps        []appLink
	SpotifyIcon template.HTML
}

var notIDChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func withoutScheme(u string) string {
	return strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
}

// apps are the podcast apps in the subscribe dialog, with their deep links.
func apps(feedURL string) []appLink {
	encoded := nrk.EncodeURIComponent(feedURL)
	links := []appLink{
		{ID: "apple", Name: "Apple Podcasts", Href: template.URL("podcast://" + withoutScheme(feedURL)), Platforms: "ios"},
		{ID: "overcast", Name: "Overcast", Href: template.URL("overcast://x-callback-url/add?url=" + encoded), Platforms: "ios"},
		{ID: "pocketcasts", Name: "Pocket Casts", Href: template.URL("pktc://subscribe/" + withoutScheme(feedURL)), Platforms: "ios android"},
		{ID: "castro", Name: "Castro", Href: template.URL("castro://subscribe/" + withoutScheme(feedURL)), Platforms: "ios"},
		{ID: "downcast", Name: "Downcast", Href: template.URL("downcast://" + withoutScheme(feedURL)), Platforms: "ios"},
		{ID: "podcastaddict", Name: "Podcast Addict", Href: template.URL("podcastaddict://" + withoutScheme(feedURL)), Platforms: "android"},
		{ID: "antennapod", Name: "AntennaPod", Href: template.URL("https://antennapod.org/deeplink/subscribe?url=" + encoded), Platforms: "android"},
		// RFC 4648 §5 base64url, the encoding YouTube Music expects
		{ID: "youtubemusic", Name: "YouTube Music", Href: template.URL("https://music.youtube.com/library/podcasts?addrssfeed=" +
			base64.RawURLEncoding.EncodeToString([]byte(feedURL))), Platforms: "ios android"},
	}
	for i := range links {
		links[i].Icon = appIcons[links[i].ID]
	}
	return links
}

func (s *Server) cards(origin string, summaries []SeriesSummary) []card {
	cards := make([]card, len(summaries))
	for i, summary := range summaries {
		feedURL := origin + "/api/feeds/" + summary.FeedID()
		c := card{
			SeriesSummary: summary,
			FeedURL:       feedURL,
			// the QR code's address: finds exactly this feed, dialog open
			ShareURL:    handoffURL(origin, summary.FeedID()),
			NrkURL:      "https://radio.nrk.no/podkast/" + summary.FeedID(),
			DialogID:    dialogID(summary.FeedID()),
			Apps:        apps(feedURL),
			SpotifyIcon: appIcons["spotify"],
		}
		if summary.ImageURL != nil {
			c.ImageURL = *summary.ImageURL
		}
		if summary.Description != nil {
			c.Description = *summary.Description
		}
		cards[i] = c
	}
	return cards
}

func (s *Server) suggestions() []string {
	if titles := s.Catalog.RandomTitles(6); titles != nil {
		return titles
	}
	return fallbackSuggestions
}

// applyFound fills the search view from a find result.
func (s *Server) applyFound(view *searchView, origin string, f found) {
	switch {
	case f.failed:
		view.Failed = true
		view.Status = "Søket feilet. Prøv igjen."
	case f.unsupported:
		view.UnsupportedLink = true
		view.Status = "Lenken støttes ikke"
	case f.notFound:
		view.LinkNotFound = true
		view.Status = "Fant ikke podkasten i lenken"
	default:
		view.HasResults = true
		view.Link = f.link
		view.Cards = s.cards(origin, f.summaries)
		for i := range view.Cards {
			if i < len(f.labels) {
				view.Cards[i].Label = f.labels[i]
			}
		}
		if f.episode {
			view.Notice = "Lenken går til én episode. Abonnerer du, får du alle episodene."
		}
		if f.link {
			view.Status = resultsStatus("NRK-lenken", len(view.Cards))
		} else {
			view.Status = resultsStatus(view.Query, len(view.Cards))
		}
	}
}

func resultsStatus(query string, count int) string {
	if count == 0 {
		return "Ingen treff for " + query
	}
	return strconv.Itoa(count) + " treff for " + query
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	origin := s.origin(r)
	view := searchView{Input: query, Suggestions: s.suggestions()}

	trimmed := strings.TrimSpace(query)
	if query != "" && queryAllowed(trimmed) {
		// same admission control as /api/search; over the limit the page
		// renders without results rather than querying NRK
		if s.limiter.allowScoped("search", clientKey(r), 30, 0.5, 120, 2) {
			view.Query = trimmed
			f := s.find(trimmed)
			if f.failed && !f.link {
				// as before: a failed name search renders as no matches
				f = found{summaries: []SeriesSummary{}}
			}
			s.applyFound(&view, origin, f)
		}
	}

	title := defaultTitle
	data := s.page(r, title)
	if query != "" {
		data.Title = "Søk: " + query + " – Kringkasting"
		// search-result URLs are duplicates of the front page for crawlers
		data.Robots = "noindex,follow"
	}
	data.JSONLD = jsonLD(origin)
	data.Search = view
	s.render(w, r, http.StatusOK, "home", data)
}

// jsonLD is the WebSite structured data for the front page.
func jsonLD(origin string) template.HTML {
	encoded, _ := store.MarshalJSON(map[string]any{
		"@context":    "https://schema.org",
		"@type":       "WebSite",
		"name":        "Kringkasting",
		"url":         origin,
		"description": "Åpne RSS-strømmer for NRK sine podkaster, med komplette episodearkiv – hør dem i den podkast-appen du selv vil.",
		"inLanguage":  "nb",
		"potentialAction": map[string]any{
			"@type":       "SearchAction",
			"target":      map[string]any{"@type": "EntryPoint", "urlTemplate": origin + "/?query={search_term_string}"},
			"query-input": "required name=search_term_string",
		},
	})
	// "<" can't end the script element early when escaped
	return template.HTML(`<script type="application/ld+json">` + strings.ReplaceAll(encoded, "<", "\\u003c") + `</script>`)
}

// searchFragment returns the rendered results area for the client-side
// search, so result markup exists only once (in the templates). It is a
// separate endpoint from /api/search, which keeps returning JSON.
func (s *Server) searchFragment(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	view := searchView{Query: query}
	status := http.StatusOK

	switch {
	case jsLength(query) < minQueryLength || !queryAllowed(query):
		view.Suggestions = s.suggestions()
		w.Header().Set("Cache-Control", "no-store")
	case !s.limiter.allowScoped("search", clientKey(r), 30, 0.5, 120, 2):
		w.Header().Set("Retry-After", "30")
		w.Header().Set("Cache-Control", "no-store")
		view.Failed, status = true, http.StatusTooManyRequests
	default:
		s.Stats.RecordSearchServed()
		f := s.find(query)
		s.applyFound(&view, s.origin(r), f)
		if f.failed {
			w.Header().Set("Cache-Control", "no-store")
			status = http.StatusServiceUnavailable
			break
		}
		setCacheHeaders(w, searchTTL, searchTTL)
	}

	var buf strings.Builder
	if err := s.pages["home"].ExecuteTemplate(&buf, "search-results", view); err != nil {
		log.Printf("render search fragment: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Search-Status", url.PathEscape(view.Status))
	w.WriteHeader(status)
	_, _ = io.WriteString(w, buf.String())
}
