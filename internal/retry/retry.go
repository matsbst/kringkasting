// Package retry keeps durable retry deadlines for upstream work (NRK
// requests, refreshes, crawls) in the app_state table under "retry:{key}",
// so backoff survives restarts.
package retry

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/store"
)

const prefix = "retry:"

// state is stored as JSON {"attempts":n,"nextRetryAt":ms}, the Deno format.
type state struct {
	Attempts    int   `json:"attempts"`
	NextRetryAt int64 `json:"nextRetryAt"`
}

type Retry struct{ store *store.Store }

func New(s *store.Store) *Retry { return &Retry{store: s} }

// At returns the retry deadline (Unix ms), 0 when none.
func (r *Retry) At(key string) int64 {
	var st state
	if !r.store.ReadState(prefix+key, &st) {
		return 0
	}
	return st.NextRetryAt
}

// Can reports whether the deadline for key has passed.
func (r *Retry) Can(key string) bool {
	return r.At(key) <= clock.NowMs()
}

// Defer backs off: five minutes, ten minutes, … up to a day. An upstream
// deadline (notBefore, Unix ms) takes precedence when later.
func (r *Retry) Defer(key string, notBefore int64) {
	var previous state
	r.store.ReadState(prefix+key, &previous)
	attempts := min(previous.Attempts+1, 10)
	delay := min(int64(24*time.Hour/time.Millisecond), int64(5*time.Minute/time.Millisecond)<<(attempts-1))
	r.store.WriteState(prefix+key, state{Attempts: attempts, NextRetryAt: max(clock.NowMs()+delay, notBefore)})
}

// Clear removes the deadline for key.
func (r *Retry) Clear(key string) {
	r.store.DeleteState(prefix + key)
}

var digits = regexp.MustCompile(`^\d+$`)

// ParseRetryAfter turns a Retry-After header (seconds or an HTTP date)
// into a deadline in Unix ms; 0 when absent or unparseable.
func ParseRetryAfter(value string, now int64) int64 {
	if value == "" {
		return 0
	}
	if trimmed := strings.TrimSpace(value); digits.MatchString(trimmed) {
		seconds, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return 0
		}
		return now + seconds*1000
	}
	parsed, err := http.ParseTime(value)
	if err != nil {
		// as lenient as JavaScript's Date.parse for the common variants
		for _, layout := range []string{time.RFC1123, time.RFC1123Z, time.RFC3339} {
			if parsed, err = time.Parse(layout, strings.TrimSpace(value)); err == nil {
				break
			}
		}
		if err != nil {
			return 0
		}
	}
	return max(now, parsed.UnixMilli())
}
