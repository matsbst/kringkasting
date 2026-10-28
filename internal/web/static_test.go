package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestStaticVideoByteRange(t *testing.T) {
	files, err := newStaticFiles(fstest.MapFS{
		"og-video.mp4": &fstest.MapFile{Data: []byte("0123456789"), Mode: fs.FileMode(0644)},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/og-video.mp4", nil)
	req.Header.Set("Range", "bytes=2-5")
	rec := httptest.NewRecorder()
	if !files.serve(rec, req, "/og-video.mp4", publicOrigin) {
		t.Fatal("video was not served")
	}
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "2345" {
		t.Fatalf("range response: status %d, body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "video/mp4" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 2-5/10" {
		t.Fatalf("Content-Range = %q", got)
	}
}
