package web

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/stats"
)

func TestTokenBucket(t *testing.T) {
	defer clock.Unpin()
	start := time.Now()
	clock.Pin(start)
	l := newLimiter(stats.New())
	allow := func() bool { return l.allowScoped("s", "k", 3, 1, 100, 100) }
	for i := range 3 {
		if !allow() {
			t.Fatalf("request %d refused inside the burst", i)
		}
	}
	if allow() {
		t.Fatal("burst exceeded but allowed")
	}
	clock.Pin(start.Add(1100 * time.Millisecond))
	if !allow() {
		t.Error("bucket should refill over time")
	}
	if l.stats.Snapshot().RateLimited != 1 {
		t.Error("refusals are counted")
	}
}

// A client over its own limit must not spend the shared budget.
func TestScopedLimitChecksClientFirst(t *testing.T) {
	defer clock.Unpin()
	clock.Pin(time.Now())
	l := newLimiter(stats.New())
	for range 10 {
		l.allowScoped("s", "a", 1, 0, 3, 0)
	}
	if !l.allowScoped("s", "b", 1, 0, 3, 0) || !l.allowScoped("s", "c", 1, 0, 3, 0) {
		t.Error("a's refused requests drained the global bucket")
	}
	// refused globally: the client's token is returned
	if l.allowScoped("s", "d", 1, 0, 3, 0) {
		t.Fatal("global bucket should be empty")
	}
	if l.clients["s:d"].tokens != 1 {
		t.Error("a globally refused request must not cost the client a token")
	}
	l.refundScoped("s", "b")
	if l.clients["s:b"].tokens != 1 || l.global["s"].tokens != 1 {
		t.Error("a refund returns both tokens")
	}
}

// Rotating client ids can't grow the limiter without bound, and the
// global bucket still holds.
func TestLimiterIsBounded(t *testing.T) {
	defer clock.Unpin()
	clock.Pin(time.Now())
	l := newLimiter(stats.New())
	allowed := 0
	for i := range 3 * maxBuckets {
		if l.allowScoped("s", fmt.Sprint(i), 5, 0, 100, 0) {
			allowed++
		}
	}
	if len(l.clients) > maxBuckets {
		t.Errorf("%d client buckets, cap %d", len(l.clients), maxBuckets)
	}
	if allowed != 100 {
		t.Errorf("%d allowed, want the global 100", allowed)
	}
}

func TestClientKey(t *testing.T) {
	long := strings.Repeat("x", 10_000)
	const cloudflareEdge = "172.70.1.1"
	for _, c := range []struct {
		name    string
		remote  string
		headers map[string]string
		want    string
	}{
		{"direct client", "203.0.113.9", nil, "203.0.113.9"},
		{"direct client can't forge headers", "203.0.113.9",
			map[string]string{"CF-Connecting-IP": "198.51.100.1", "X-Forwarded-For": "198.51.100.2"}, "203.0.113.9"},
		{"local proxy", "10.0.0.2", map[string]string{"X-Forwarded-For": "198.51.100.1, 203.0.113.8"}, "203.0.113.8"},
		{"chain of local proxies", "127.0.0.1", map[string]string{"X-Forwarded-For": "203.0.113.8, 192.168.1.1"}, "203.0.113.8"},
		{"cloudflare through a local proxy", "172.18.0.1",
			map[string]string{"CF-Connecting-IP": " 203.0.113.7 ", "X-Forwarded-For": "203.0.113.7, " + cloudflareEdge}, "203.0.113.7"},
		{"cloudflare directly", cloudflareEdge, map[string]string{"CF-Connecting-IP": "203.0.113.7"}, "203.0.113.7"},
		{"cf header bypassing cloudflare", "172.18.0.1",
			map[string]string{"CF-Connecting-IP": "198.51.100.1", "X-Forwarded-For": "203.0.113.9"}, "203.0.113.9"},
		{"x-real-ip from a local proxy", "172.18.0.1", map[string]string{"X-Real-IP": "203.0.113.5"}, "203.0.113.5"},
		{"x-real-ip from a direct client", "203.0.113.9", map[string]string{"X-Real-IP": "198.51.100.1"}, "203.0.113.9"},
		{"ipv6 by /64", "::1", map[string]string{"X-Forwarded-For": "2001:DB8::0:1"}, "2001:db8::/64"},
		{"ipv4-mapped ipv6", "::ffff:203.0.113.9", nil, "203.0.113.9"},
		{"junk stops at the proxy", "10.0.0.2", map[string]string{"CF-Connecting-IP": long, "X-Forwarded-For": long}, "10.0.0.2"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = net.JoinHostPort(c.remote, "1234")
		for k, v := range c.headers {
			r.Header.Set(k, v)
		}
		if got := clientKey(r); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSameOrigin(t *testing.T) {
	for _, c := range []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{"no browser headers", nil, true},
		{"same origin", map[string]string{"Sec-Fetch-Site": "same-origin"}, true},
		{"sibling subdomain", map[string]string{"Sec-Fetch-Site": "same-site"}, false},
		{"cross site", map[string]string{"Sec-Fetch-Site": "cross-site"}, false},
		{"origin matches", map[string]string{"Origin": "https://example.com"}, true},
		{"origin differs", map[string]string{"Origin": "https://other.example.com"}, false},
		{"opaque origin", map[string]string{"Origin": "null"}, false},
	} {
		r := httptest.NewRequest(http.MethodPost, "https://example.com/admin", nil)
		for k, v := range c.headers {
			r.Header.Set(k, v)
		}
		if got := sameOrigin(r); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
