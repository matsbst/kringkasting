package report

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseDSN(t *testing.T) {
	d := parseDSN("https://key@bugsink.example/sub/path/42")
	if d == nil || d.endpoint != "https://bugsink.example/sub/path/api/42/store/" || d.publicKey != "key" {
		t.Errorf("parsed %+v", d)
	}
	for _, bad := range []string{"", "not a url", "https://bugsink.example/42", "https://key@bugsink.example/"} {
		if parseDSN(bad) != nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestDisabledIsNoop(t *testing.T) {
	r := New("", "dev", false)
	if r.Enabled() {
		t.Fatal("enabled without DSN")
	}
	r.Capture(errors.New("ignored"), nil)
	var nilReporter *Reporter
	nilReporter.Capture(errors.New("ignored"), nil)
}

// Events carry the exception and tags only, with URLs scrubbed of queries.
func TestEventIsScrubbed(t *testing.T) {
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("x-sentry-auth"), "sentry_key=key") {
			t.Error("missing auth header")
		}
		var event map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &event)
		received <- event
	}))
	defer server.Close()
	r := New(strings.Replace(server.URL, "http://", "http://key@", 1)+"/1", "abc", true)
	r.Capture(errors.New("failed to fetch https://psapi.nrk.no/x?token=secret&user=me"), map[string]string{"kind": "test"})
	select {
	case event := <-received:
		value := event["exception"].(map[string]any)["values"].([]any)[0].(map[string]any)["value"]
		if value != "failed to fetch https://psapi.nrk.no/x" {
			t.Errorf("not scrubbed: %v", value)
		}
		if event["release"] != "abc" || event["environment"] != "production" || event["tags"].(map[string]any)["kind"] != "test" {
			t.Errorf("event %v", event)
		}
		for _, field := range []string{"user", "request", "server_name"} {
			if _, ok := event[field]; ok {
				t.Errorf("event carries %s", field)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event sent")
	}
}
