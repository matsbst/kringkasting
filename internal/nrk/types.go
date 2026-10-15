package nrk

import "encoding/json"

// NRK API response shapes, covering only the fields Kringkasting reads.
// Checked against the recorded responses in testdata/nrk and the opt-in
// live tests (go test -tags live ./internal/nrk).

type link struct {
	Href string `json:"href"`
}

type image struct {
	URL   string `json:"url"`
	Width int    `json:"width"`
}

type titles struct {
	Title    string  `json:"title"`
	Subtitle *string `json:"subtitle"`
}

// Episode is an episode as listed in a catalog page.
type Episode struct {
	ID                string `json:"id"`
	EpisodeID         string `json:"episodeId"`
	Titles            titles `json:"titles"`
	Date              string `json:"date"`
	DurationInSeconds int64  `json:"durationInSeconds"`
	Links             struct {
		Share *link `json:"share"`
	} `json:"_links"`
}

// episodeWithMedia is an episode with its resolved download link.
type episodeWithMedia struct {
	Episode
	URL   string
	Bytes *int64
}

// episodesPage is /radio/catalog/{kind}/{id}/episodes[?page=…].
type episodesPage struct {
	Embedded struct {
		Episodes []Episode `json:"episodes"`
	} `json:"_embedded"`
	Links struct {
		Next *link `json:"next"`
	} `json:"_links"`
}

// seriesResponse is /radio/catalog/{kind}/{id}.
type seriesResponse struct {
	SeriesType string `json:"seriesType"`
	Type       string `json:"type"`
	Series     *struct {
		ID          string  `json:"id"`
		Titles      titles  `json:"titles"`
		SquareImage []image `json:"squareImage"`
	} `json:"series"`
	Links struct {
		Seasons []link `json:"seasons"`
	} `json:"_links"`
}

// seasonResponse is /radio/catalog/{kind}/{id}/seasons/{seasonId}.
type seasonResponse struct {
	Titles      titles  `json:"titles"`
	SquareImage []image `json:"squareImage"`
	Embedded    struct {
		Episodes *struct {
			Embedded struct {
				Episodes []Episode `json:"episodes"`
			} `json:"_embedded"`
			Links struct {
				Next *link `json:"next"`
			} `json:"_links"`
		} `json:"episodes"`
	} `json:"_embedded"`
}

// manifest is /playback/manifest/{podcast|program}/{episodeId}.
type manifest struct {
	Playable *struct {
		Assets []*struct {
			URL string `json:"url"`
		} `json:"assets"`
	} `json:"playable"`
}

// IndexPoint is a chapter marker on a single episode.
type IndexPoint struct {
	// Title is kept raw: absent and null must round-trip differently
	Title      json.RawMessage `json:"title"`
	StartPoint *string         `json:"startPoint"`
}

// EpisodeDetail is /radio/catalog/{kind}/{id}/episodes/{episodeId}.
type EpisodeDetail struct {
	IndexPoints []IndexPoint `json:"indexPoints"`
}

// SearchImage is an artwork variant of a search hit.
type SearchImage struct {
	URI   string `json:"uri"`
	Width *int   `json:"width"`
}

// SearchResult is one series hit from /radio/search/search.
type SearchResult struct {
	SeriesID    string        `json:"seriesId"`
	SeasonID    *string       `json:"seasonId"`
	Type        string        `json:"type"`
	Title       string        `json:"title"`
	Description *string       `json:"description"`
	Images      []SearchImage `json:"images"`
	Images1x1   []SearchImage `json:"images_1_1"`
}

type searchResponse struct {
	Results struct {
		Series *struct {
			Results []SearchResult `json:"results"`
		} `json:"series"`
	} `json:"results"`
}
