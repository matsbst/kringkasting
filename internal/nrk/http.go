package nrk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/retry"
	"github.com/matsbst/kringkasting/internal/stats"
)

const requestTimeout = 15 * time.Second

// maxBody bounds what is read from one NRK response; real ones (a page of
// 50 episodes) are well under a megabyte.
const maxBody = 16 << 20

const (
	// maxConcurrentUpstream caps concurrent NRK requests across ALL
	// operations. Per-operation pools bound their own fan-out, but many
	// simultaneous cold requests would multiply those; this bounds the
	// total pressure on NRK whatever happens above it.
	maxConcurrentUpstream = 12
	// maxQueuedUpstream: beyond this, requests fail fast instead of
	// queueing without bound.
	maxQueuedUpstream = 64
)

var errSaturated = errors.New("upstream queue saturated")

// upstream is the HTTP layer for every NRK request: a global concurrency
// cap with a bounded FIFO queue, timeouts, durable retry deadlines and
// stats. Errors never escape: network failures and timeouts are status 0.
type upstream struct {
	client *http.Client
	retry  *retry.Retry
	stats  *stats.Stats
	// ctx ends on shutdown: requests in flight are cancelled, so
	// background work stops promptly instead of outliving the database
	ctx  context.Context
	stop context.CancelFunc

	mu       sync.Mutex
	inFlight int
	waiters  []chan struct{}
}

func (u *upstream) acquire() error {
	u.mu.Lock()
	if u.inFlight < maxConcurrentUpstream {
		u.inFlight++
		u.mu.Unlock()
		return nil
	}
	if len(u.waiters) >= maxQueuedUpstream {
		u.mu.Unlock()
		return errSaturated
	}
	ready := make(chan struct{})
	u.waiters = append(u.waiters, ready)
	u.mu.Unlock()
	<-ready
	return nil
}

func (u *upstream) release() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.waiters) > 0 {
		// hand the slot directly to the next waiter
		next := u.waiters[0]
		u.waiters = u.waiters[1:]
		close(next)
		return
	}
	u.inFlight--
}

func originOf(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return parsed.Scheme + "://" + parsed.Host
}

func (u *upstream) allowed(method, rawURL string) bool {
	return u.retry.Can(method+":"+rawURL) && u.retry.Can("origin:"+originOf(rawURL))
}

// getJSON fetches rawURL and decodes a 2xx body into dst. It returns the
// HTTP status, or 0 for network errors, timeouts and undecodable bodies,
// or 503 while a retry deadline for the URL or its origin is pending.
// ok is true only when dst was filled.
func (u *upstream) getJSON(rawURL string, dst any) (status int, ok bool) {
	if !u.allowed("GET", rawURL) {
		return 503, false
	}
	if err := u.acquire(); err != nil {
		return 0, false
	}
	defer u.release()
	// a request that was in flight while we queued may have set a deadline
	if !u.allowed("GET", rawURL) {
		return 503, false
	}

	ctx, cancel := context.WithTimeout(u.ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, false
	}
	req.Header.Set("accept", "application/json")
	resp, err := u.client.Do(req)
	if err != nil && u.ctx.Err() != nil {
		// shutting down: not NRK's fault, so no stats and no backoff
		return 0, false
	}
	if err != nil {
		log.Printf("GET %s failed: %v", rawURL, err)
		u.stats.RecordUpstreamRequest(rawURL, "GET", 0)
		u.retry.Defer("GET:"+rawURL, 0)
		return 0, false
	}
	defer resp.Body.Close()
	body := io.LimitReader(resp.Body, maxBody)

	u.recordOutcome(rawURL, "GET", resp)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		u.stats.RecordUpstreamRequest(rawURL, "GET", resp.StatusCode)
		_, _ = io.Copy(io.Discard, body)
		return resp.StatusCode, false
	}
	// counted once: as its status when usable, as a failure when not
	if err := json.NewDecoder(body).Decode(dst); err != nil {
		log.Printf("GET %s failed: %v", rawURL, err)
		u.stats.RecordUpstreamRequest(rawURL, "GET", 0)
		u.retry.Defer("GET:"+rawURL, 0)
		return 0, false
	}
	u.stats.RecordUpstreamRequest(rawURL, "GET", resp.StatusCode)
	return resp.StatusCode, true
}

// head returns the status and Content-Length of rawURL (for enclosure
// sizes); contentLength is nil when unknown.
func (u *upstream) head(rawURL string) (status int, contentLength *int64) {
	if !u.allowed("HEAD", rawURL) {
		return 503, nil
	}
	if err := u.acquire(); err != nil {
		return 0, nil
	}
	defer u.release()
	if !u.allowed("HEAD", rawURL) {
		return 503, nil
	}

	ctx, cancel := context.WithTimeout(u.ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return 0, nil
	}
	resp, err := u.client.Do(req)
	if err != nil && u.ctx.Err() != nil {
		return 0, nil
	}
	if err != nil {
		log.Printf("HEAD %s failed: %v", rawURL, err)
		u.stats.RecordUpstreamRequest(rawURL, "HEAD", 0)
		u.retry.Defer("HEAD:"+rawURL, 0)
		return 0, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	resp.Body.Close()
	u.stats.RecordUpstreamRequest(rawURL, "HEAD", resp.StatusCode)
	u.recordOutcome(rawURL, "HEAD", resp)
	// an error page's length isn't the file's size
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, nil
	}
	if raw := resp.Header.Get("content-length"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			contentLength = &n
		}
	}
	return resp.StatusCode, contentLength
}

func (u *upstream) recordOutcome(rawURL, method string, resp *http.Response) {
	key := method + ":" + rawURL
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		deadline := retry.ParseRetryAfter(resp.Header.Get("retry-after"), clock.NowMs())
		u.retry.Defer(key, deadline)
		// the origin pauses exactly as long as NRK asked, without the
		// escalating backoff: a burst of 429s must not stop all traffic
		// for a day
		if deadline > clock.NowMs() {
			u.retry.SetDeadline("origin:"+originOf(rawURL), deadline)
		}
		return
	}
	u.retry.Clear(key)
}
