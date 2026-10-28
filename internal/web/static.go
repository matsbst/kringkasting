package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"
)

// staticFiles serves the embedded static files with the Deno version's
// headers: revalidate everything (weak SHA-256 ETag, 304 on match), except
// fonts and assets requested with the current version (?v=version), which
// never change.
type staticFiles struct {
	files map[string]staticFile
	// version is a hash of every static file: pages link assets with it,
	// so a release that changes any of them gets new URLs, whatever its
	// build ID (an image built without GIT_REVISION is "dev")
	version string
}

type staticFile struct {
	body        []byte
	etag        string
	contentType string
}

// contentTypes override Go's defaults where the Deno version differed.
var contentTypes = map[string]string{
	".txt":  "text/plain; charset=UTF-8",
	".json": "application/json; charset=UTF-8",
	".ico":  "image/vnd.microsoft.icon",
	".js":   "text/javascript; charset=UTF-8",
	".css":  "text/css; charset=UTF-8",
	".mp4":  "video/mp4",
}

func newStaticFiles(root fs.FS) (*staticFiles, error) {
	files := map[string]staticFile{}
	err := fs.WalkDir(root, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || strings.HasPrefix(path.Base(name), ".") {
			return err
		}
		body, err := fs.ReadFile(root, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		contentType := contentTypes[path.Ext(name)]
		if contentType == "" {
			contentType = mime.TypeByExtension(path.Ext(name))
		}
		files["/"+name] = staticFile{body: body, etag: `W/"` + hex.EncodeToString(sum[:]) + `"`, contentType: contentType}
		return nil
	})
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	all := sha256.New()
	for _, name := range names {
		all.Write([]byte(name + files[name].etag))
	}
	return &staticFiles{files: files, version: hex.EncodeToString(all.Sum(nil))[:12]}, err
}

// publicOrigin is the public instance's address as written in the text
// files that describe the site; each instance serves them with its own.
const publicOrigin = "https://kringkast.ing"

// withOrigin are the text files naming the instance's address.
var withOrigin = map[string]bool{"/robots.txt": true, "/llms.txt": true}

// serve writes the file at urlPath; false when there is none. origin
// replaces publicOrigin in the withOrigin files.
func (s *staticFiles) serve(w http.ResponseWriter, r *http.Request, urlPath, origin string) bool {
	file, ok := s.files[urlPath]
	if !ok {
		return false
	}
	if withOrigin[urlPath] && origin != publicOrigin {
		body := []byte(strings.ReplaceAll(string(file.body), publicOrigin, origin))
		sum := sha256.Sum256(body)
		file = staticFile{body: body, etag: `W/"` + hex.EncodeToString(sum[:]) + `"`, contentType: file.contentType}
	}
	h := w.Header()
	// only the current version is immutable: any other ?v= (an old link,
	// or one made up) would otherwise pin whatever it got for a year, and
	// the origin-dependent text files never are
	if strings.HasPrefix(urlPath, "/fonts/") || (r.URL.Query().Get("v") == s.version && !withOrigin[urlPath]) {
		// content-stable: browsers and the CDN may keep it forever
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache, no-store, max-age=0, must-revalidate")
	}
	h.Set("ETag", file.etag)
	h.Set("Vary", "If-None-Match")
	if etagMatches(r.Header.Get("If-None-Match"), strings.TrimPrefix(file.etag, "W/")) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	if file.contentType != "" {
		h.Set("Content-Type", file.contentType)
	}
	// Video players and link preview crawlers may request a byte range.
	// ServeContent handles Range and HEAD while retaining our cache headers.
	if path.Ext(urlPath) == ".mp4" {
		http.ServeContent(w, r, path.Base(urlPath), time.Time{}, bytes.NewReader(file.body))
		return true
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.body)
	return true
}
