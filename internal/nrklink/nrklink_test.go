package nrklink

import "testing"

func TestParse(t *testing.T) {
	for _, c := range []struct {
		in     string
		result Result
		link   Link
	}{
		// ordinary searches
		{"trygdekontoret", NotALink, Link{}},
		{"Abels tårn", NotALink, Link{}},
		{"radio.nrk.no", NotALink, Link{}},
		{"", NotALink, Link{}},

		// podcasts
		{"https://radio.nrk.no/podkast/trygdekontoret", Supported, Link{Kind: Podcast, SeriesID: "trygdekontoret"}},
		{"  https://radio.nrk.no/podkast/trygdekontoret/  ", Supported, Link{Kind: Podcast, SeriesID: "trygdekontoret"}},
		{"radio.nrk.no/podkast/trygdekontoret", Supported, Link{Kind: Podcast, SeriesID: "trygdekontoret"}},
		{"HTTPS://RADIO.NRK.NO/podkast/trygdekontoret?utm_source=x#top", Supported, Link{Kind: Podcast, SeriesID: "trygdekontoret"}},
		{"http://radio.nrk.no/podkast/trygdekontoret", Supported, Link{Kind: Podcast, SeriesID: "trygdekontoret"}},

		// seasons and episodes
		{"https://radio.nrk.no/podkast/musikkhistorier/sesong/miles-davis--bak-mytene", Supported,
			Link{Kind: Podcast, SeriesID: "musikkhistorier", SeasonID: "miles-davis--bak-mytene"}},
		{"https://radio.nrk.no/podkast/sentralbordet/l_1de12aee-3b7a-4a14-a12a-ee3b7a8a14a7", Supported,
			Link{Kind: Podcast, SeriesID: "sentralbordet", EpisodeID: "l_1de12aee-3b7a-4a14-a12a-ee3b7a8a14a7"}},
		{"https://radio.nrk.no/podkast/musikkhistorier/sesong/miles-davis--bak-mytene/l_b3b3ad33-6357-4ccd-b3ad-3363579ccdd1", Supported,
			Link{Kind: Podcast, SeriesID: "musikkhistorier", SeasonID: "miles-davis--bak-mytene", EpisodeID: "l_b3b3ad33-6357-4ccd-b3ad-3363579ccdd1"}},

		// radio series
		{"https://radio.nrk.no/serie/radioteatret-nrk-p1pluss", Supported, Link{Kind: Series, SeriesID: "radioteatret-nrk-p1pluss"}},
		{"https://radio.nrk.no/serie/radioteatret-nrk-p1pluss/DMPO39010014", Supported,
			Link{Kind: Series, SeriesID: "radioteatret-nrk-p1pluss", EpisodeID: "DMPO39010014"}},
		{"https://radio.nrk.no/serie/kveldsnytt/sesong/202610/DKOS99101026", Supported,
			Link{Kind: Series, SeriesID: "kveldsnytt", SeasonID: "202610", EpisodeID: "DKOS99101026"}},

		// links that are not supported podcast links
		{"https://tv.nrk.no/serie/skam", Unsupported, Link{}},
		{"https://www.nrk.no/norge/en-sak-1.123", Unsupported, Link{}},
		{"https://example.com/podkast/trygdekontoret", Unsupported, Link{}},
		{"https://radio.nrk.no.evil.example/podkast/x", Unsupported, Link{}},
		{"https://user@radio.nrk.no/podkast/x", Unsupported, Link{}},
		{"https://radio.nrk.no:8443/podkast/x", Unsupported, Link{}},
		{"https://radio.nrk.no/", Unsupported, Link{}},
		{"https://radio.nrk.no/podkast", Unsupported, Link{}},
		{"https://radio.nrk.no/direkte/p1", Unsupported, Link{}},
		{"https://radio.nrk.no/podkast/x/sesong", Unsupported, Link{}},
		{"https://radio.nrk.no/podkast/x/a/b/c/d", Unsupported, Link{}},
		{"https://radio.nrk.no/podkast/..%2F..%2Fadmin", Unsupported, Link{}},
		{"https://radio.nrk.no/podkast/x%3Fq%3D1", Unsupported, Link{}},
		{"javascript://radio.nrk.no/podkast/x", Unsupported, Link{}},
		{"ftp://radio.nrk.no/podkast/x", Unsupported, Link{}},
	} {
		link, result := Parse(c.in)
		if result != c.result || link != c.link {
			t.Errorf("Parse(%q) = %+v, %v; want %+v, %v", c.in, link, result, c.link, c.result)
		}
	}
}
