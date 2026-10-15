package web

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/stats"
)

// limiter holds small in-memory token buckets, keyed per client, for the
// requests that can trigger upstream work (searches, cold feed fetches,
// chapter lookups, admin logins). Known/cached requests are never limited.
// IP addresses live only here, in memory.
type limiter struct {
	stats *stats.Stats

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens    float64
	updatedAt time.Time
}

const maxBuckets = 5000

func newLimiter(s *stats.Stats) *limiter { return &limiter{stats: s, buckets: map[string]*bucket{}} }

// allow consumes one token; capacity is the burst allowance and refill the
// sustained rate per second.
func (l *limiter) allow(key string, capacity, refill float64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := clock.Now()
	if len(l.buckets) >= maxBuckets {
		for k, b := range l.buckets {
			// idle for 10+ minutes: fully refilled anyway
			if now.Sub(b.updatedAt) > 10*time.Minute {
				delete(l.buckets, k)
			}
		}
	}
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: capacity, updatedAt: now}
		l.buckets[key] = b
	}
	b.tokens = min(capacity, b.tokens+now.Sub(b.updatedAt).Seconds()*refill)
	b.updatedAt = now
	if b.tokens < 1 {
		l.stats.RecordRateLimited()
		return false
	}
	b.tokens--
	return true
}

// allowScoped applies a per-client limit plus a global backstop for the
// scope. Client identity comes from forwarding headers, which a
// direct-to-origin attacker can forge; the global bucket bounds the damage.
func (l *limiter) allowScoped(scope, client string, clientCapacity, clientRefill, globalCapacity, globalRefill float64) bool {
	if !l.allow("global:"+scope, globalCapacity, globalRefill) {
		return false
	}
	return l.allow(scope+":"+client, clientCapacity, clientRefill)
}

// clientKey is the best client identity available behind Cloudflare/nginx.
func clientKey(r *http.Request) string {
	if ip := r.Header.Get("cf-connecting-ip"); ip != "" {
		return ip
	}
	if forwarded := r.Header.Values("x-forwarded-for"); len(forwarded) > 0 {
		return strings.TrimSpace(strings.Split(forwarded[0], ",")[0])
	}
	return "unknown"
}
