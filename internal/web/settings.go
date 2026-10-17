package web

import (
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
	"github.com/matsbst/kringkasting/internal/report"
)

// integrations are the analytics and error reporting set on the
// Innstillinger tab. The environment wins: a value set there (UMAMI_SRC,
// UMAMI_WEBSITE_ID, SENTRY_DSN) is shown but can't be changed here.
type integrations struct {
	UmamiSrc  string `json:"umamiSrc,omitempty"`
	UmamiID   string `json:"umamiId,omitempty"`
	SentryDSN string `json:"sentryDsn,omitempty"`
}

const integrationsKey = "settings:integrations"

// freshLogin: the Umami script runs on every public page and error
// reports carry stack traces, so changing either needs a login this
// recent, not just a session a stolen cookie could ride on.
const freshLogin = 10 * time.Minute

var umamiID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// loadIntegrations reads the stored settings and applies them.
func (s *Server) loadIntegrations() {
	var stored integrations
	s.Store.ReadState(integrationsKey, &stored)
	s.applyIntegrations(stored)
}

func (s *Server) applyIntegrations(stored integrations) {
	s.stored.Store(&stored)
	if s.Config.SentryDSN == "" && s.Reporter != nil {
		s.Reporter.SetDSN(stored.SentryDSN)
	}
}

// integrations are the values in effect: the environment's, else the
// stored ones.
func (s *Server) integrations() integrations {
	effective := integrations{UmamiSrc: s.Config.UmamiSrc, UmamiID: s.Config.UmamiID, SentryDSN: s.Config.SentryDSN}
	if stored := s.stored.Load(); stored != nil {
		if effective.UmamiSrc == "" {
			effective.UmamiSrc = stored.UmamiSrc
		}
		if effective.UmamiID == "" {
			effective.UmamiID = stored.UmamiID
		}
		if effective.SentryDSN == "" {
			effective.SentryDSN = stored.SentryDSN
		}
	}
	return effective
}

// httpsURL: an absolute https address; a DSN carries its key as the user,
// a script address carries none.
func httpsURL(raw string, user bool) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && (u.User != nil) == user && len(raw) <= 500
}

// saveIntegrations validates and stores the form; the notice says how it
// went.
func (s *Server) saveIntegrations(r *http.Request) string {
	if !s.freshSession(r) {
		return "Logg inn på nytt for å endre analyse og feilrapporter"
	}
	field := func(name string) string { return strings.TrimSpace(r.PostForm.Get(name)) }
	next := integrations{}
	if stored := s.stored.Load(); stored != nil {
		next = *stored
	}
	// fields set in the environment aren't in the form
	if s.Config.UmamiSrc == "" {
		next.UmamiSrc = field("umami-src")
	}
	if s.Config.UmamiID == "" {
		next.UmamiID = field("umami-id")
	}
	if s.Config.SentryDSN == "" {
		next.SentryDSN = field("sentry-dsn")
	}
	switch {
	case next.UmamiSrc != "" && !httpsURL(next.UmamiSrc, false):
		return "Umami-skriptet må ha en https-adresse"
	case next.UmamiID != "" && !umamiID.MatchString(next.UmamiID):
		return "Nettsteds-id-en for Umami kan bare ha bokstaver, tall og bindestrek"
	case next.SentryDSN != "" && (!httpsURL(next.SentryDSN, true) || !report.ValidDSN(next.SentryDSN)):
		return "DSN-en må se ut som https://nøkkel@vert/prosjekt"
	}
	effective := s.integrations()
	if s.Config.UmamiSrc == "" {
		effective.UmamiSrc = next.UmamiSrc
	}
	if s.Config.UmamiID == "" {
		effective.UmamiID = next.UmamiID
	}
	if (effective.UmamiSrc == "") != (effective.UmamiID == "") {
		return "Umami trenger både skriptadresse og nettsteds-id"
	}
	s.Store.WriteState(integrationsKey, next)
	s.applyIntegrations(next)
	log.Print("Admin changed the analytics and error reporting settings")
	return "Lagret. Endringene gjelder med en gang, også på personvernsiden."
}

// freshSession: this browser signed in within freshLogin.
func (s *Server) freshSession(r *http.Request) bool {
	cookie, err := r.Cookie(adminCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	started, ok := s.Store.AdminSessionStarted(hashSession(cookie.Value))
	return ok && clock.Now().Sub(started) <= freshLogin
}

// integrationsView is the Innstillinger tab's analytics and error
// reporting form.
type integrationsView struct {
	UmamiSrc, UmamiID, SentryDSN string
	// set in the environment: shown, not editable
	UmamiSrcEnv, UmamiIDEnv, SentryDSNEnv bool
	// UmamiHost and SentryHost say where each sends its data; empty when
	// it's off. SentryHost is all that's shown of a DSN from the environment
	UmamiHost, SentryHost string
	// Fresh: signed in recently enough to change them
	Fresh bool
}

func (s *Server) integrationsView(r *http.Request) integrationsView {
	effective := s.integrations()
	v := integrationsView{
		UmamiSrc: effective.UmamiSrc, UmamiID: effective.UmamiID, SentryDSN: effective.SentryDSN,
		UmamiSrcEnv: s.Config.UmamiSrc != "", UmamiIDEnv: s.Config.UmamiID != "", SentryDSNEnv: s.Config.SentryDSN != "",
		Fresh: s.freshSession(r),
	}
	host := func(raw string) string {
		if u, err := url.Parse(raw); err == nil {
			return u.Host
		}
		return ""
	}
	if effective.UmamiSrc != "" && effective.UmamiID != "" {
		v.UmamiHost = host(effective.UmamiSrc)
	}
	if effective.SentryDSN != "" {
		v.SentryHost = host(effective.SentryDSN)
	}
	if v.SentryDSNEnv {
		v.SentryDSN = ""
	}
	return v
}
