// Package testutil holds helpers shared by tests: a programmable fake NRK
// and a temporary database.
package testutil

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/matsbst/kringkasting/internal/store"
)

// Reply is a fake NRK response.
type Reply struct {
	Status  int
	Body    string
	Headers map[string]string
	// Err simulates a network error or timeout.
	Err error
}

// FakeNRK is an http.RoundTripper answering through Handle. It counts
// requests and records the highest number seen in flight at once.
type FakeNRK struct {
	Handle func(method, url string) Reply

	mu          sync.Mutex
	Requests    []string
	inFlight    atomic.Int32
	MaxInFlight atomic.Int32
	// Gate, when set, blocks every request until it is closed.
	Gate chan struct{}
}

func (f *FakeNRK) RoundTrip(req *http.Request) (*http.Response, error) {
	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		max := f.MaxInFlight.Load()
		if n <= max || f.MaxInFlight.CompareAndSwap(max, n) {
			break
		}
	}
	f.mu.Lock()
	f.Requests = append(f.Requests, req.Method+" "+req.URL.String())
	gate := f.Gate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	reply := f.Handle(req.Method, req.URL.String())
	if reply.Err != nil {
		return nil, reply.Err
	}
	header := http.Header{}
	for k, v := range reply.Headers {
		header.Set(k, v)
	}
	return &http.Response{StatusCode: reply.Status, Header: header, Request: req,
		Body: io.NopCloser(strings.NewReader(reply.Body))}, nil
}

// Count returns how many requests matched substr.
func (f *FakeNRK) Count(substr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.Requests {
		if strings.Contains(r, substr) {
			n++
		}
	}
	return n
}

// OpenStore opens a fresh database in a temp dir, closed at cleanup.
func OpenStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite3")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}
