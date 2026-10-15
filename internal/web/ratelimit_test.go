package web

import (
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
	for i := range 3 {
		if !l.allow("k", 3, 1) {
			t.Fatalf("request %d refused inside the burst", i)
		}
	}
	if l.allow("k", 3, 1) {
		t.Fatal("burst exceeded but allowed")
	}
	clock.Pin(start.Add(1100 * time.Millisecond))
	if !l.allow("k", 3, 1) {
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
	if l.buckets["s:d"].tokens != 1 {
		t.Error("a globally refused request must not cost the client a token")
	}
}

func TestClientKey(t *testing.T) {
	long := strings.Repeat("x", 10_000)
	for _, c := range []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"cloudflare", map[string]string{"CF-Connecting-IP": " 203.0.113.7 "}, "203.0.113.7"},
		{"nearest proxy hop", map[string]string{"X-Forwarded-For": "198.51.100.1, 203.0.113.8"}, "203.0.113.8"},
		{"ipv6 canonical", map[string]string{"X-Forwarded-For": "2001:DB8::0:1"}, "2001:db8::1"},
		{"junk falls back to the connection", map[string]string{"CF-Connecting-IP": long, "X-Forwarded-For": long}, "192.0.2.1"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
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
