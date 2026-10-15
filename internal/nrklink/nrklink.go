// Package nrklink recognises NRK podcast and radio links pasted into the
// search field. It only parses text: the pasted URL is never fetched.
// Resolution goes through NRK's API with ids taken from supported paths.
package nrklink

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/matsbst/kringkasting/internal/feedid"
)

// Kind is the catalog a link points into.
type Kind string

const (
	Podcast Kind = "podcast" // radio.nrk.no/podkast/…
	Series  Kind = "series"  // radio.nrk.no/serie/…
)

// Link is a parsed NRK link. SeasonID and EpisodeID are empty when the
// link doesn't name them.
type Link struct {
	Kind      Kind
	SeriesID  string
	SeasonID  string
	EpisodeID string
}

// Result classifies a search query.
type Result int

const (
	// NotALink: an ordinary name search.
	NotALink Result = iota
	// Unsupported: looks like a link, but not a supported NRK podcast link.
	Unsupported
	// Supported: a link to a podcast, season or episode.
	Supported
)

// supportedHost is the only host whose links are accepted.
const supportedHost = "radio.nrk.no"

var (
	scheme   = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://`)
	hostPath = regexp.MustCompile(`(?i)^(www\.)?[a-z0-9-]+(\.[a-z0-9-]+)+(:\d+)?/`)
)

// looksLikeLink: a scheme, or a bare host followed by a path.
func looksLikeLink(s string) bool {
	return scheme.MatchString(s) || hostPath.MatchString(s)
}

// Parse classifies the query and, for supported links, extracts the ids.
func Parse(query string) (Link, Result) {
	s := strings.TrimSpace(query)
	if s == "" || strings.ContainsAny(s, " \t\n") || !looksLikeLink(s) {
		return Link{}, NotALink
	}
	if !scheme.MatchString(s) {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !strings.EqualFold(u.Hostname(), supportedHost) ||
		u.User != nil || (u.Port() != "" && u.Port() != "443" && u.Port() != "80") {
		return Link{}, Unsupported
	}

	var segments []string
	for _, segment := range strings.Split(u.EscapedPath(), "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	if len(segments) < 2 {
		return Link{}, Unsupported
	}
	link := Link{SeriesID: segments[1]}
	switch segments[0] {
	case "podkast":
		link.Kind = Podcast
	case "serie":
		link.Kind = Series
	default:
		return Link{}, Unsupported
	}

	rest := segments[2:]
	switch {
	case len(rest) == 0:
	case len(rest) == 1 && rest[0] != "sesong":
		link.EpisodeID = rest[0]
	case len(rest) == 2 && rest[0] == "sesong":
		link.SeasonID = rest[1]
	case len(rest) == 3 && rest[0] == "sesong":
		link.SeasonID, link.EpisodeID = rest[1], rest[2]
	default:
		return Link{}, Unsupported
	}

	// every id must be an NRK-style slug, so nothing else reaches NRK's API
	for _, id := range []string{link.SeriesID, link.SeasonID, link.EpisodeID} {
		if id != "" && !feedid.ValidResource(id) {
			return Link{}, Unsupported
		}
	}
	return link, Supported
}
