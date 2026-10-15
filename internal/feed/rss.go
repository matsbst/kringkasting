// Package feed builds and caches the RSS feeds and decides when a feed is
// refreshed from NRK. A port of lib/rss.ts, lib/feed-cache.ts,
// lib/caching.ts and lib/chapters.ts.
package feed

import (
	"net/http"
	"strconv"

	"github.com/matsbst/kringkasting/internal/feedid"
	"github.com/matsbst/kringkasting/internal/nrk"
	"github.com/matsbst/kringkasting/internal/store"
)

// IsStreamOnly: NRK offers every episode as HLS and none as a download.
// Those play only in HLS-capable apps, so the UI warns rather than hides.
func IsStreamOnly(series *store.Series) bool {
	if len(series.Episodes) == 0 {
		return false
	}
	for _, e := range series.Episodes {
		if !nrk.IsHLS(e.URL) {
			return false
		}
	}
	return true
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Stylesheet is the path of the stylesheet feeds link for browsers.
const Stylesheet = "/feed.css"

// AssembleFeed renders a series as RSS. origin is this instance's public
// origin, for absolute URLs back to the app (self link, chapters).
func AssembleFeed(series *store.Series, origin string) string {
	// Drop stray HLS episodes from shows that also have downloadable ones,
	// so ordinary feeds carry only playable enclosures. Keep them for
	// wholly stream-only shows, where dropping would leave an empty feed.
	episodes := series.Episodes
	if !IsStreamOnly(series) {
		episodes = nil
		for _, e := range series.Episodes {
			if !nrk.IsHLS(e.URL) {
				episodes = append(episodes, e)
			}
		}
	}

	// season feeds have a composite id; chapters resolve against NRK's
	// catalog by the parent seriesId, which the chapters route expects
	chaptersSeriesID := feedid.Parse(series.ID).SeriesID

	channel := []tag{
		text("title", series.Title),
		text("link", series.Link),
		text("language", "no"),
		text("atom:link", "",
			attr{"href", origin + "/api/feeds/" + series.ID}, attr{"rel", "self"}, attr{"type", "application/rss+xml"}),
		text("itunes:author", "NRK"),
		text("itunes:explicit", "false"),
		// NRK's categories don't overlap Apple's; these feeds aren't indexed
		// by Apple anyway, so a static valid category passes validators
		text("itunes:category", "", attr{"text", "Government"}),
		node("itunes:owner", []tag{
			text("itunes:name", "NRK"),
			text("itunes:email", "nrkpodcast@nrk.no"),
		}),
		text("description", deref(series.Subtitle)),
		text("ttl", "60"),
	}
	if series.ImageURL != "" {
		channel = append(channel,
			text("itunes:image", "", attr{"href", series.ImageURL}),
			node("image", []tag{
				text("url", series.ImageURL),
				text("title", series.Title),
				text("link", series.Link),
			}))
	}
	for _, e := range episodes {
		channel = append(channel, assembleEpisode(e, chaptersSeriesID, origin))
	}

	// ~800 bytes per episode, so large feeds are built in one allocation
	return serialize(
		[]attr{{"version", "1.0"}, {"encoding", "UTF-8"}},
		// a browser opening the feed shows a readable page instead of raw
		// XML; podcast apps ignore it. CSS, not XSLT: browsers are
		// removing XSLT, while CSS for XML stays
		[]attr{{"type", "text/css"}, {"href", Stylesheet}},
		node("rss", []tag{node("channel", channel)},
			attr{"version", "2.0"},
			attr{"xmlns:itunes", "http://www.itunes.com/dtds/podcast-1.0.dtd"},
			attr{"xmlns:content", "http://purl.org/rss/1.0/modules/content/"},
			attr{"xmlns:podcast", "https://podcastindex.org/namespace/1.0"},
			attr{"xmlns:atom", "http://www.w3.org/2005/Atom"},
		),
		2048+800*len(episodes),
	)
}

func assembleEpisode(e store.Episode, seriesID, origin string) tag {
	description := deref(e.Subtitle)
	var length int64
	if e.Bytes != nil {
		length = *e.Bytes
	}
	// label HLS honestly instead of audio/mpeg, which makes apps report
	// "invalid audio file" when the download isn't an MP3
	mediaType := "audio/mpeg"
	if nrk.IsHLS(e.URL) {
		mediaType = "application/vnd.apple.mpegurl"
	}
	children := []tag{
		text("title", e.Title),
		text("link", e.ShareLink),
		text("description", description),
		text("itunes:summary", description),
		text("guid", e.ID, attr{"isPermaLink", "false"}),
		text("pubDate", e.Date.UTC().Format(http.TimeFormat)),
		text("itunes:duration", strconv.FormatInt(e.DurationInSeconds, 10)),
		text("podcast:chapters", "",
			attr{"url", origin + "/api/feeds/" + seriesID + "/" + e.ID + "/chapters"},
			attr{"type", "application/json+chapters"}),
		text("enclosure", "",
			attr{"url", e.URL},
			// RSS length is the file size in bytes ("0" = unknown)
			attr{"length", strconv.FormatInt(length, 10)},
			attr{"type", mediaType}),
	}
	if e.VideoURL != nil {
		// NRK's own adaptive HLS playlist, as is: players that support the
		// extension can offer video, others keep the audio enclosure. The
		// length is unknown ("0", as for enclosures); no bitrate or height
		// is invented.
		children = append(children, node("podcast:alternateEnclosure",
			[]tag{text("podcast:source", "", attr{"uri", *e.VideoURL})},
			attr{"type", "application/x-mpegURL"}, attr{"length", "0"}, attr{"lang", "no"}, attr{"title", "Video"}))
	}
	return node("item", children)
}
