package stats

import (
	"regexp"
	"strconv"
	"strings"
)

// clientRules map a user agent to the app's name, first match wins. Only
// the name is kept: the user agent itself is never stored.
var clientRules = []struct{ match, name string }{
	{"overcast", "Overcast"},
	{"pocketcasts", "Pocket Casts"},
	{"pocket casts", "Pocket Casts"},
	{"castro", "Castro"},
	{"downcast", "Downcast"},
	{"podcastaddict", "Podcast Addict"},
	{"podcast addict", "Podcast Addict"},
	{"antennapod", "AntennaPod"},
	{"anytime", "Anytime"},
	{"podverse", "Podverse"},
	{"fountain", "Fountain"},
	{"player fm", "Player FM"},
	{"playerfm", "Player FM"},
	{"castbox", "Castbox"},
	{"gpodder", "gPodder"},
	{"podcastindex", "Podcast Index"},
	{"feedly", "Feedly"},
	{"inoreader", "Inoreader"},
	{"newsblur", "NewsBlur"},
	{"youtube", "YouTube Music"},
	{"google", "Google"},
	{"applecoremedia", "Apple Podcasts"},
	{"itms", "Apple Podcasts"},
	{"podcasts/", "Apple Podcasts"},
	{"apple podcasts", "Apple Podcasts"},
	{"mozilla/", "Nettleser"},
}

// ClientName is the app a user agent belongs to, or "Annet".
func ClientName(userAgent string) string {
	ua := strings.ToLower(userAgent)
	for _, rule := range clientRules {
		if strings.Contains(ua, rule.match) {
			return rule.name
		}
	}
	return "Annet"
}

// services that fetch for many users say how many in their user agent,
// e.g. "Feedly/1.0 (+http://www.feedly.com/fetcher.html; 5 subscribers)"
var subscribersPattern = regexp.MustCompile(`(?i)(\d{1,7})\s+(?:subscribers?|readers?)\b`)

// maxSubscribers rejects absurd counts.
const maxSubscribers = 1_000_000

// ReportedSubscribers is the subscriber count a user agent reports.
func ReportedSubscribers(userAgent string) (int, bool) {
	m := subscribersPattern.FindStringSubmatch(userAgent)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n > maxSubscribers {
		return 0, false
	}
	return n, true
}
