package stats

import "testing"

func TestClientName(t *testing.T) {
	for ua, want := range map[string]string{
		"Overcast/1.0 Podcast Sync (12 subscribers; feed-id=123; +http://overcast.fm/)": "Overcast",
		"PocketCasts/1.0 (Pocket Casts Feed Parser; +http://pocketcasts.com/)":          "Pocket Casts",
		"AppleCoreMedia/1.0.0.21A350 (iPhone; U; CPU OS 17_0 like Mac OS X; nb_no)":     "Apple Podcasts",
		"iTMS":                           "Apple Podcasts",
		"Podcasts/1710.1 CFNetwork/1474": "Apple Podcasts",
		"AntennaPod/3.5.0":               "AntennaPod",
		"PodcastAddict/v5 (+https://podcastaddict.com/; Android podcast app)": "Podcast Addict",
		"Castro 2024.1": "Castro",
		"Feedly/1.0 (+http://www.feedly.com/fetcher.html; 5 subscribers; like FeedFetcher-Google)": "Feedly",
		"Inoreader/1.0 (+http://www.inoreader.com/feed-fetcher; 3 subscribers; )":                  "Inoreader",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Safari/605.1.15":     "Nettleser",
		"curl/8.7.1": "Annet",
		"":           "Annet",
	} {
		if got := ClientName(ua); got != want {
			t.Errorf("%q: %q, want %q", ua, got, want)
		}
	}
}

func TestReportedSubscribers(t *testing.T) {
	for ua, want := range map[string]int{
		"Overcast/1.0 Podcast Sync (12 subscribers; feed-id=123)":        12,
		"Feedly/1.0 (+http://www.feedly.com/fetcher.html; 1 subscriber)": 1,
		"NewsBlur Feed Fetcher - 7 subscribers - https://newsblur.com":   7,
		"Inoreader/1.0 (3 Subscribers)":                                  3,
	} {
		if got, ok := ReportedSubscribers(ua); !ok || got != want {
			t.Errorf("%q: %d %v, want %d", ua, got, ok, want)
		}
	}
	for _, ua := range []string{"AntennaPod/3.5.0", "Overcast/1.0 (subscribers unknown)", "Bot (99999999 subscribers)", ""} {
		if n, ok := ReportedSubscribers(ua); ok {
			t.Errorf("%q: reported %d, want none", ua, n)
		}
	}
}
