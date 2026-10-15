// Package feedid holds feed identity helpers.
//
// A feed is either a whole series/podcast (id = NRK's seriesId) or one
// customSeason of an umbrella podcast (id = "{seriesId}/sesong/{seasonId}",
// mirroring NRK's own URL structure at radio.nrk.no/podkast/...). The
// composite id doubles as the storage key and the feed URL path.
package feedid

import (
	"regexp"
	"strings"
)

// NRK series/season/episode ids are short slugs. Rejecting anything else
// keeps user-controlled route params from steering requests to other
// paths on NRK's API (or growing the database keyspace with garbage).
var resourceID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)

const seasonSeparator = "/sesong/"

// ValidResource reports whether id is a well-formed NRK id.
func ValidResource(id string) bool { return resourceID.MatchString(id) }

// Season builds a season feed id.
func Season(seriesID, seasonID string) string { return seriesID + seasonSeparator + seasonID }

// Ref is a parsed feed id; SeasonID is empty for whole-series feeds.
type Ref struct{ SeriesID, SeasonID string }

// Parse splits a feed id into its NRK series and season ids.
func Parse(feedID string) Ref {
	i := strings.Index(feedID, seasonSeparator)
	if i == -1 {
		return Ref{SeriesID: feedID}
	}
	return Ref{SeriesID: feedID[:i], SeasonID: feedID[i+len(seasonSeparator):]}
}

// Valid accepts a plain series id or a well-formed "{seriesId}/sesong/{seasonId}".
func Valid(feedID string) bool {
	ref := Parse(feedID)
	if !ValidResource(ref.SeriesID) {
		return false
	}
	return !strings.Contains(feedID, seasonSeparator) || ValidResource(ref.SeasonID)
}
