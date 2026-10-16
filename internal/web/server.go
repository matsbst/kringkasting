// Package web is the HTTP layer: routing, middleware, pages, feeds, the
// search API and admin. Response bodies, status codes and headers follow
// the Deno version (testdata/baseline/http).
package web

import (
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"

	"github.com/matsbst/kringkasting/internal/catalog"
	"github.com/matsbst/kringkasting/internal/config"
	"github.com/matsbst/kringkasting/internal/crawl"
	"github.com/matsbst/kringkasting/internal/feed"
	"github.com/matsbst/kringkasting/internal/nrk"
	"github.com/matsbst/kringkasting/internal/report"
	"github.com/matsbst/kringkasting/internal/stats"
	"github.com/matsbst/kringkasting/internal/store"
	assets "github.com/matsbst/kringkasting/web"
)

// securityHeaders are set on every response, error pages and static files
// included.
var securityHeaders = map[string]string{
	"X-Content-Type-Options": "nosniff",
	"Referrer-Policy":        "same-origin",
	"X-Frame-Options":        "DENY",
}

// Deps are the services the HTTP layer uses.
type Deps struct {
	Config   config.Config
	Store    *store.Store
	NRK      *nrk.Client
	Feeds    *feed.Feeds
	Cache    *feed.Cache
	Chapters *feed.Chapters
	Crawler  *crawl.Crawler
	Catalog  *catalog.Catalog
	Stats    *stats.Stats
	Reporter *report.Reporter
}

type Server struct {
	Deps
	pages   map[string]*template.Template
	static  *staticFiles
	limiter *limiter
	// passkeys is nil when the origin can't hold passkeys
	passkeys *passkeys
}

// New builds the HTTP handler.
func New(d Deps) (*Server, error) {
	pages := map[string]*template.Template{}
	for _, name := range []string{"home", "personvern", "error", "admin"} {
		t, err := template.ParseFS(assets.FS, "templates/layout.html", "templates/partials.html", "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parse %s template: %w", name, err)
		}
		pages[name] = t
	}
	staticFS, err := fs.Sub(assets.FS, "static")
	if err != nil {
		return nil, err
	}
	files, err := newStaticFiles(staticFS)
	if err != nil {
		return nil, err
	}
	s := &Server{Deps: d, pages: pages, static: files, limiter: newLimiter(d.Stats)}
	if s.adminEnabled() {
		s.passkeys = newPasskeys(d.Config.Origin)
		s.endSessionsIfTokenChanged()
	}
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for name, value := range securityHeaders {
		w.Header().Set(name, value)
	}
	defer func() {
		if rec := recover(); rec != nil {
			if rec == http.ErrAbortHandler {
				panic(rec)
			}
			err := fmt.Errorf("panic serving %s %s: %v", r.Method, r.URL.Path, rec)
			log.Printf("%v\n%s", err, debug.Stack())
			// only the method and path are attached, never queries or IPs
			s.Reporter.Capture(err, map[string]string{"method": r.Method, "path": r.URL.Path})
			s.errorPage(w, r, http.StatusInternalServerError)
		}
	}()
	s.route(w, r)
}

func readMethod(r *http.Request) bool {
	return r.Method == http.MethodGet || r.Method == http.MethodHead
}

// route dispatches on the raw (still escaped) path, so an encoded slash
// stays inside one id segment and is rejected as an invalid id, as before.
func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	path := r.URL.EscapedPath()
	switch {
	case path == "/" && readMethod(r):
		s.home(w, r)
	case path == "/personvern" && readMethod(r):
		p := s.page(r, "Personvern – Kringkasting")
		p.Privacy = s.privacy(r, p)
		s.render(w, r, http.StatusOK, "personvern", p)
	case adminTabPath(path) && (readMethod(r) || r.Method == http.MethodPost):
		s.admin(w, r)
	case strings.HasPrefix(path, "/admin/passkey/"):
		s.passkeyRoutes(w, r, strings.TrimPrefix(path, "/admin/passkey/"))
	case path == "/sitemap.xml" && readMethod(r):
		s.sitemap(w, r)
	case path == "/api/search" && readMethod(r):
		s.searchAPI(w, r)
	case path == "/api/search/fragment" && readMethod(r):
		s.searchFragment(w, r)
	case path == "/api/qr" && readMethod(r):
		s.qrCode(w, r)
	case strings.HasPrefix(path, "/api/feeds/") && readMethod(r):
		s.feedRoutes(w, r, strings.Split(strings.TrimPrefix(path, "/api/feeds/"), "/"))
	case readMethod(r) && s.static.serve(w, r, path, s.origin(r)):
	default:
		s.errorPage(w, r, http.StatusNotFound)
	}
}

func (s *Server) feedRoutes(w http.ResponseWriter, r *http.Request, segments []string) {
	unescaped := make([]string, len(segments))
	for i, segment := range segments {
		value, err := url.PathUnescape(segment)
		if err != nil {
			s.errorPage(w, r, http.StatusNotFound)
			return
		}
		unescaped[i] = value
	}
	switch {
	case len(segments) == 1 && segments[0] != "":
		s.seriesFeed(w, r, unescaped[0])
	case len(segments) == 3 && segments[1] == "sesong" && segments[0] != "" && segments[2] != "":
		s.seasonFeed(w, r, unescaped[0], unescaped[2])
	case len(segments) == 3 && segments[2] == "chapters" && segments[0] != "" && segments[1] != "":
		s.chapters(w, r, unescaped[0], unescaped[1])
	default:
		s.errorPage(w, r, http.StatusNotFound)
	}
}

// origin is the public origin: configured, or from the request.
func (s *Server) origin(r *http.Request) string {
	if s.Config.Origin != "" {
		return s.Config.Origin
	}
	scheme := "http"
	if requestHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// requestHTTPS reports whether the visitor used HTTPS: TLS here, or
// X-Forwarded-Proto from a proxy that sets it (a local one, or
// Cloudflare). Clients connecting directly can't claim it.
func requestHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	return proto != "" && fromProxy(r) && strings.TrimSpace(strings.Split(proto, ",")[0]) == "https"
}

type umami struct{ Src, WebsiteID string }

// page holds what the layout and the page templates need.
type page struct {
	Title, Canonical, Origin, Path, AssetVersion string
	Umami                                        *umami
	Robots                                       string
	JSONLD                                       template.HTML
	Search                                       searchView
	NotFound                                     bool
	Admin                                        adminView
	// AdminPage: the admin login or dashboard (wide layout, admin.js)
	AdminPage bool
	Privacy   *privacy
}

// privacy is what the privacy page says about this instance: every
// installation describes its own setup, not the public one's.
type privacy struct {
	// Umami: analytics are configured; UmamiSelfHosted unless the script
	// comes from Umami's cloud
	Umami, UmamiSelfHosted bool
	// Cloudflare: this request came through Cloudflare
	Cloudflare bool
	// ErrorReports: SENTRY_DSN is set; ErrorReportsSelfHosted unless it
	// points at sentry.io
	ErrorReports, ErrorReportsSelfHosted bool
}

func (s *Server) privacy(r *http.Request, p *page) *privacy {
	notHostedBy := func(raw, domain string) bool {
		u, err := url.Parse(raw)
		if err != nil {
			return false
		}
		host := u.Hostname()
		return host != domain && !strings.HasSuffix(host, "."+domain)
	}
	return &privacy{
		Umami:                  p.Umami != nil,
		UmamiSelfHosted:        p.Umami != nil && notHostedBy(p.Umami.Src, "umami.is"),
		Cloudflare:             viaCloudflare(r),
		ErrorReports:           s.Config.SentryDSN != "",
		ErrorReportsSelfHosted: s.Config.SentryDSN != "" && notHostedBy(s.Config.SentryDSN, "sentry.io"),
	}
}

const defaultTitle = "Kringkasting – NRK-podkaster som RSS"

func (s *Server) page(r *http.Request, title string) *page {
	origin := s.origin(r)
	p := &page{
		Title: title, Origin: origin, Path: r.URL.Path,
		Canonical: origin + r.URL.EscapedPath(), AssetVersion: s.static.version,
	}
	// optional cookieless Umami analytics; both values must be set
	if s.Config.UmamiSrc != "" && s.Config.UmamiID != "" {
		p.Umami = &umami{Src: s.Config.UmamiSrc, WebsiteID: s.Config.UmamiID}
	}
	return p
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, data *page) {
	var buf strings.Builder
	if err := s.pages[name].ExecuteTemplate(&buf, "layout", data); err != nil {
		log.Printf("render %s: %v", name, err)
		s.Reporter.Capture(err, map[string]string{"method": r.Method, "path": r.URL.Path})
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", s.contentSecurityPolicy(data))
	w.WriteHeader(status)
	_, _ = io.WriteString(w, buf.String())
}

// contentSecurityPolicy allows the pages what they use and nothing more:
// scripts from this server (and Umami's, when configured), podcast artwork
// from anywhere over HTTPS, and inline styles (the decorative SVG and the
// no-JavaScript fallback use them). The JSON-LD block is data, which CSP
// doesn't govern.
func (s *Server) contentSecurityPolicy(p *page) string {
	scripts := "'self'"
	if p.Umami != nil {
		if u, err := url.Parse(p.Umami.Src); err == nil && u.Scheme != "" && u.Host != "" {
			scripts += " " + u.Scheme + "://" + u.Host
		}
	}
	return "default-src 'self'; script-src " + scripts + "; connect-src " + scripts +
		"; img-src 'self' https: data:; style-src 'self' 'unsafe-inline'; font-src 'self'" +
		"; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
}

// errorPage renders the 404 or 500 page.
func (s *Server) errorPage(w http.ResponseWriter, r *http.Request, status int) {
	data := s.page(r, defaultTitle)
	data.NotFound = status == http.StatusNotFound
	s.render(w, r, status, "error", data)
}
