package web

import (
	"net"
	"net/http"
	"net/netip"
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

	mu       sync.Mutex
	clients  map[string]*bucket // per-client buckets, at most maxBuckets
	global   map[string]*bucket // one per scope
	prunedAt time.Time
}

type bucket struct {
	tokens    float64
	updatedAt time.Time
}

const (
	maxBuckets = 5000
	// idle buckets have refilled anyway; dropping them loses nothing
	bucketIdle = 10 * time.Minute
)

func newLimiter(s *stats.Stats) *limiter {
	return &limiter{stats: s, clients: map[string]*bucket{}, global: map[string]*bucket{}}
}

// take consumes one token from the bucket at key in buckets; capacity is
// the burst allowance and refill the sustained rate per second.
func (l *limiter) take(buckets map[string]*bucket, key string, capacity, refill float64, now time.Time) bool {
	b := buckets[key]
	if b == nil {
		b = &bucket{tokens: capacity, updatedAt: now}
		buckets[key] = b
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

// makeRoom keeps the client map bounded: idle buckets go first (at most
// one sweep a minute), and if many clients are active at once, all client
// buckets are dropped. That only resets per-client allowances; the global
// buckets keep bounding the total.
func (l *limiter) makeRoom(now time.Time) {
	if len(l.clients) < maxBuckets {
		return
	}
	if now.Sub(l.prunedAt) >= time.Minute {
		l.prunedAt = now
		for k, b := range l.clients {
			if now.Sub(b.updatedAt) > bucketIdle {
				delete(l.clients, k)
			}
		}
	}
	if len(l.clients) >= maxBuckets {
		l.clients = map[string]*bucket{}
	}
}

// allowScoped applies a per-client limit plus a global backstop for the
// scope. The client bucket is checked first, so a client over its own
// limit cannot drain the shared budget.
func (l *limiter) allowScoped(scope, client string, clientCapacity, clientRefill, globalCapacity, globalRefill float64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := clock.Now()
	clientKey := scope + ":" + client
	if l.clients[clientKey] == nil {
		l.makeRoom(now)
	}
	if !l.take(l.clients, clientKey, clientCapacity, clientRefill, now) {
		return false
	}
	if !l.take(l.global, scope, globalCapacity, globalRefill, now) {
		l.clients[clientKey].tokens++
		return false
	}
	return true
}

// refundScoped returns the tokens a successful allowScoped took, for limits
// that should only count failures (admin logins).
func (l *limiter) refundScoped(scope, client string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if b := l.clients[scope+":"+client]; b != nil {
		b.tokens++
	}
	if b := l.global[scope]; b != nil {
		b.tokens++
	}
}

// clientKey is the client's address, as far as it can be trusted.
// Forwarding headers are believed only from proxies that set them:
//
//   - X-Forwarded-For, read from the right, while the address it came from
//     is local (loopback or private: a reverse proxy on the same host or
//     network, as on Cloudron, Docker or a NAS). The first public address
//     is the one the outermost local proxy saw. A local proxy that sends
//     only X-Real-IP is believed too.
//   - CF-Connecting-IP, only when that address is Cloudflare's.
//
// A client reaching the server directly can't choose its identity with
// headers. IPv6 clients are keyed by their /64, which one home or
// server usually gets whole. Only well-formed IPs are used, so header
// values cannot bloat the limiter's keys.
func clientKey(r *http.Request) string {
	peer := nearestPublic(r)
	if !peer.IsValid() {
		return "unknown"
	}
	if isLocal(peer) {
		if real := parseAddr(r.Header.Get("x-real-ip")); real.IsValid() {
			peer = real
		}
	}
	if isCloudflare(peer) {
		if client := parseAddr(r.Header.Get("cf-connecting-ip")); client.IsValid() {
			peer = client
		}
	}
	return keyFor(peer)
}

// nearestPublic is the connection's address, or, behind local proxies,
// the first public address in X-Forwarded-For read from the right: the
// one the outermost local proxy saw. Invalid when the connection's
// address is.
func nearestPublic(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := parseAddr(host)
	if !peer.IsValid() {
		return peer
	}
	var hops []string
	for _, value := range r.Header.Values("x-forwarded-for") {
		hops = append(hops, strings.Split(value, ",")...)
	}
	for i := len(hops) - 1; i >= 0 && isLocal(peer); i-- {
		hop := parseAddr(hops[i])
		if !hop.IsValid() {
			break
		}
		peer = hop
	}
	return peer
}

// fromProxy reports whether the connection comes from a proxy whose
// forwarding headers can be believed: a local one, or Cloudflare.
func fromProxy(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := parseAddr(host)
	return peer.IsValid() && (isLocal(peer) || isCloudflare(peer))
}

// viaCloudflare reports whether the request reached this server through
// Cloudflare (directly, or behind local proxies).
func viaCloudflare(r *http.Request) bool {
	peer := nearestPublic(r)
	return peer.IsValid() && isCloudflare(peer)
}

func parseAddr(value string) netip.Addr {
	addr, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap().WithZone("")
}

func isLocal(addr netip.Addr) bool {
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast()
}

// keyFor is the address as a limiter key: IPv6 by its /64.
func keyFor(addr netip.Addr) string {
	if addr.Is6() {
		prefix, _ := addr.Prefix(64)
		return prefix.String()
	}
	return addr.String()
}

// cloudflareRanges are Cloudflare's published edge addresses
// (https://www.cloudflare.com/ips/). They change rarely; an address
// missing here only means the edge address is used as the client key.
var cloudflareRanges = func() []netip.Prefix {
	var prefixes []netip.Prefix
	for _, cidr := range []string{
		"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
		"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
		"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
		"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
		"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
		"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
	} {
		prefixes = append(prefixes, netip.MustParsePrefix(cidr))
	}
	return prefixes
}()

func isCloudflare(addr netip.Addr) bool {
	for _, prefix := range cloudflareRanges {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
