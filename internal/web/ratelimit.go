package web

import (
	"net"
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

// refund returns a token taken by allow.
func (l *limiter) refund(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if b := l.buckets[key]; b != nil {
		b.tokens++
	}
}

// allowScoped applies a per-client limit plus a global backstop for the
// scope. Client identity comes from forwarding headers, which a
// direct-to-origin attacker can forge; the global bucket bounds the damage.
// The client bucket is checked first, so a client over its own limit
// cannot drain the shared budget.
func (l *limiter) allowScoped(scope, client string, clientCapacity, clientRefill, globalCapacity, globalRefill float64) bool {
	clientKey := scope + ":" + client
	if !l.allow(clientKey, clientCapacity, clientRefill) {
		return false
	}
	if !l.allow("global:"+scope, globalCapacity, globalRefill) {
		l.refund(clientKey)
		return false
	}
	return true
}

// clientKey is the best client identity available behind Cloudflare or a
// reverse proxy: CF-Connecting-IP, else the X-Forwarded-For entry the
// nearest proxy appended (the last one), else the connection's address.
// Only well-formed IPs are used, so header values cannot bloat the
// limiter's keys.
func clientKey(r *http.Request) string {
	if ip := parseIP(r.Header.Get("cf-connecting-ip")); ip != "" {
		return ip
	}
	if forwarded := r.Header.Values("x-forwarded-for"); len(forwarded) > 0 {
		entries := strings.Split(forwarded[len(forwarded)-1], ",")
		if ip := parseIP(entries[len(entries)-1]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := parseIP(host); ip != "" {
		return ip
	}
	return "unknown"
}

// parseIP returns the canonical form of an IP address, "" when value is not one.
func parseIP(value string) string {
	if ip := net.ParseIP(strings.TrimSpace(value)); ip != nil {
		return ip.String()
	}
	return ""
}
