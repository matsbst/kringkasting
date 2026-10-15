package feed

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/nrk"
	"github.com/matsbst/kringkasting/internal/retry"
	"github.com/matsbst/kringkasting/internal/store"
)

const chaptersTTL = 7 * 24 * time.Hour

// Chapter is one entry of a Podcasting 2.0 chapters file. Title keeps
// NRK's raw value: absent is omitted and null stays null, as in the Deno
// version's JSON.
type Chapter struct {
	Title     json.RawMessage `json:"title,omitempty"`
	StartTime *float64        `json:"startTime,omitempty"`
}

// ChaptersResult is cached in app_state under "chapters:{series}:{episodeId}".
type ChaptersResult struct {
	Status   int       `json:"status"`
	Chapters []Chapter `json:"chapters"`
	Expires  int64     `json:"expiresAt,omitempty"`
}

// Chapters looks up and caches episode chapters.
type Chapters struct {
	store *store.Store
	nrk   *nrk.Client
	retry *retry.Retry

	mu       sync.Mutex
	inFlight map[string]*chapterCall
}

type chapterCall struct {
	done   chan struct{}
	result ChaptersResult
}

func NewChapters(s *store.Store, client *nrk.Client, r *retry.Retry) *Chapters {
	return &Chapters{store: s, nrk: client, retry: r, inFlight: map[string]*chapterCall{}}
}

// Get returns an episode's chapters. Status is 200, 404 (episode unknown
// to NRK) or 503 (lookup unavailable). feedEpisodeID is the id the feed
// links with: NRK's hash id, translated to NRK's episodeId through the
// stored share link; ids that aren't stored pass through unchanged. The
// cache key uses the resolved id, so 404s cached under hash ids by
// earlier versions are never read again.
func (c *Chapters) Get(seriesID, feedEpisodeID string) ChaptersResult {
	episodeID := c.store.ReadNrkEpisodeID(seriesID, feedEpisodeID)
	if episodeID == "" {
		episodeID = feedEpisodeID
	}
	key := "chapters:" + seriesID + ":" + episodeID

	var cached ChaptersResult
	hasCached := c.store.ReadState(key, &cached)
	if hasCached && cached.Expires > clock.NowMs() {
		return cached
	}
	if !c.retry.Can(key) {
		if hasCached {
			return cached
		}
		return ChaptersResult{Status: 503}
	}

	c.mu.Lock()
	call := c.inFlight[key]
	if call == nil {
		call = &chapterCall{done: make(chan struct{})}
		c.inFlight[key] = call
		c.mu.Unlock()
		call.result = c.fetch(key, seriesID, episodeID, cached, hasCached)
		c.mu.Lock()
		delete(c.inFlight, key)
		c.mu.Unlock()
		close(call.done)
	} else {
		c.mu.Unlock()
		<-call.done
	}
	return call.result
}

func (c *Chapters) fetch(key, seriesID, episodeID string, cached ChaptersResult, hasCached bool) ChaptersResult {
	fail := func() ChaptersResult {
		c.retry.Defer(key, 0)
		if hasCached {
			return cached
		}
		return ChaptersResult{Status: 503}
	}

	episode, status := c.nrk.GetEpisodeResult(seriesID, episodeID)
	if status != 200 && status != 404 {
		return fail()
	}
	var chapters []Chapter
	if episode != nil && episode.IndexPoints != nil {
		chapters = []Chapter{}
		for _, point := range episode.IndexPoints {
			chapter := Chapter{Title: point.Title}
			if point.StartPoint != nil && *point.StartPoint != "" {
				seconds, err := durationSeconds(*point.StartPoint)
				if err != nil {
					return fail()
				}
				chapter.StartTime = &seconds
			}
			chapters = append(chapters, chapter)
		}
	}
	result := ChaptersResult{Status: status, Chapters: chapters}
	stored := result
	stored.Expires = clock.Now().Add(chaptersTTL).UnixMilli()
	c.store.WriteState(key, stored)
	c.retry.Clear(key)
	return result
}

var isoDuration = regexp.MustCompile(
	`^P(?:(\d+(?:[.,]\d+)?)Y)?(?:(\d+(?:[.,]\d+)?)M)?(?:(\d+(?:[.,]\d+)?)W)?(?:(\d+(?:[.,]\d+)?)D)?` +
		`(?:T(?:(\d+(?:[.,]\d+)?)H)?(?:(\d+(?:[.,]\d+)?)M)?(?:(\d+(?:[.,]\d+)?)S)?)?$`)

// durationSeconds converts an ISO 8601 duration ("PT3M19S") to seconds,
// as the iso8601-duration package did: calendar parts are applied to the
// current date, time parts are exact.
func durationSeconds(value string) (float64, error) {
	m := isoDuration.FindStringSubmatch(value)
	if m == nil || value == "P" || value == "PT" {
		return 0, fmt.Errorf("invalid ISO 8601 duration %q", value)
	}
	num := func(s string) float64 {
		if s == "" {
			return 0
		}
		n, _ := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
		return n
	}
	years, months, weeks, days := num(m[1]), num(m[2]), num(m[3]), num(m[4])
	hours, minutes, seconds := num(m[5]), num(m[6]), num(m[7])

	start := clock.Now()
	end := start.AddDate(int(years), int(months), int(days+weeks*7))
	calendar := end.Sub(start).Seconds()
	return calendar + hours*3600 + minutes*60 + seconds, nil
}
