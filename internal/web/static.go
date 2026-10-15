package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// staticFiles serves the embedded static files with the Deno version's
// headers: revalidate everything (weak SHA-256 ETag, 304 on match), except
// fonts and versioned assets (?v=build), which never change.
type staticFiles struct {
	files map[string]staticFile
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
	return &staticFiles{files: files}, err
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
	if strings.HasPrefix(urlPath, "/fonts/") || r.URL.Query().Get("v") != "" {
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
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.body)
	return true
}
