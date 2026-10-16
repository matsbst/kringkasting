package web

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/matsbst/kringkasting/internal/clock"
)

// Admin passkeys (WebAuthn). They need a fixed HTTPS address, so they are
// offered only when APP_ORIGIN is https (or localhost, for development).
// ADMIN_TOKEN stays the first login and the way back in; with
// ADMIN_PASSKEY_ONLY it stops working once a passkey exists.

const (
	// ceremonyTTL is how long a started registration or login stays valid
	ceremonyTTL = 3 * time.Minute
	// maxCeremonies bounds the ones waiting at once
	maxCeremonies  = 100
	ceremonyCookie = "kringkasting_ceremony"
	// userHandleKey stores the admin's random WebAuthn user handle
	userHandleKey  = "admin-user-handle"
	maxPasskeys    = 20
	maxPasskeyName = 60
)

type ceremony struct {
	session  webauthn.SessionData
	register bool
	name     string
	expires  time.Time
}

type passkeys struct {
	webauthn *webauthn.WebAuthn

	mu         sync.Mutex
	ceremonies map[string]ceremony
}

// newPasskeys returns nil when the configured origin can't hold passkeys.
func newPasskeys(origin string) *passkeys {
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	if u.Scheme != "https" && u.Hostname() != "localhost" {
		return nil
	}
	w, err := webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: "Kringkasting",
		RPOrigins:     []string{origin},
	})
	if err != nil {
		log.Printf("passkeys unavailable: %v", err)
		return nil
	}
	return &passkeys{webauthn: w, ceremonies: map[string]ceremony{}}
}

// adminUser is the one admin account, as the WebAuthn library sees it.
type adminUser struct {
	id          []byte
	credentials []webauthn.Credential
	// byID maps a credential id (base64url) to its stored row id
	byID map[string]string
}

func (u *adminUser) WebAuthnID() []byte                         { return u.id }
func (u *adminUser) WebAuthnName() string                       { return "admin" }
func (u *adminUser) WebAuthnDisplayName() string                { return "Kringkasting admin" }
func (u *adminUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

func (s *Server) adminUser() (*adminUser, error) {
	var handle string
	if !s.Store.ReadState(userHandleKey, &handle) || handle == "" {
		handle = base64.RawURLEncoding.EncodeToString(randomBytes(32))
		s.Store.WriteState(userHandleKey, handle)
	}
	id, err := base64.RawURLEncoding.DecodeString(handle)
	if err != nil {
		return nil, err
	}
	stored, err := s.Store.ListPasskeys()
	if err != nil {
		return nil, err
	}
	user := &adminUser{id: id, byID: map[string]string{}}
	for _, p := range stored {
		var credential webauthn.Credential
		if err := json.Unmarshal(p.Credential, &credential); err != nil {
			log.Printf("passkey %s unreadable: %v", p.ID, err)
			continue
		}
		user.credentials = append(user.credentials, credential)
		user.byID[base64.RawURLEncoding.EncodeToString(credential.ID)] = p.ID
	}
	return user, nil
}

// tokenLoginOff: ADMIN_PASSKEY_ONLY is set and a passkey can be used.
func (s *Server) tokenLoginOff() bool {
	return s.Config.AdminPasskeyOnly && s.passkeys != nil && s.Store.CountPasskeys() > 0
}

// start keeps a ceremony's session data server side, under a random id
// in a short-lived cookie.
func (p *passkeys) start(w http.ResponseWriter, secure bool, c ceremony) {
	id := base64.RawURLEncoding.EncodeToString(randomBytes(24))
	now := clock.Now()
	c.expires = now.Add(ceremonyTTL)
	p.mu.Lock()
	for key, waiting := range p.ceremonies {
		if waiting.expires.Before(now) {
			delete(p.ceremonies, key)
		}
	}
	if len(p.ceremonies) >= maxCeremonies {
		p.ceremonies = map[string]ceremony{}
	}
	p.ceremonies[id] = c
	p.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: ceremonyCookie, Value: id, Path: "/admin", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(ceremonyTTL.Seconds())})
}

// finish takes the ceremony back out; each can be finished once.
func (p *passkeys) finish(r *http.Request, register bool) (ceremony, bool) {
	cookie, err := r.Cookie(ceremonyCookie)
	if err != nil {
		return ceremony{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.ceremonies[cookie.Value]
	delete(p.ceremonies, cookie.Value)
	return c, ok && c.register == register && c.expires.After(clock.Now())
}

// passkeyRoutes serves /admin/passkey/{register,login}/{begin,finish}:
// JSON for admin.js, POST only, from this origin only.
func (s *Server) passkeyRoutes(w http.ResponseWriter, r *http.Request, action string) {
	noStore(w)
	if !s.adminEnabled() || s.passkeys == nil {
		writeJSON(w, http.StatusNotFound, message{"Passkeys are not available"})
		return
	}
	if r.Method != http.MethodPost || !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, message{"Forbidden"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, adminMaxBody)
	switch action {
	case "register/begin":
		s.passkeyRegisterBegin(w, r)
	case "register/finish":
		s.passkeyRegisterFinish(w, r)
	case "login/begin":
		s.passkeyLoginBegin(w, r)
	case "login/finish":
		s.passkeyLoginFinish(w, r)
	default:
		writeJSON(w, http.StatusNotFound, message{"Not found"})
	}
}

func (s *Server) passkeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	if !s.authenticated(r) {
		writeJSON(w, http.StatusUnauthorized, message{"Logg inn først"})
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "Passnøkkel"
	}
	if utf8.RuneCountInString(name) > maxPasskeyName {
		name = string([]rune(name)[:maxPasskeyName])
	}
	user, err := s.adminUser()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, message{"Kunne ikke lese passnøklene"})
		return
	}
	if len(user.credentials) >= maxPasskeys {
		writeJSON(w, http.StatusConflict, message{"Fjern en passnøkkel før du legger til flere"})
		return
	}
	creation, session, err := s.passkeys.webauthn.BeginRegistration(user,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(webauthn.Credentials(user.credentials).CredentialDescriptors()))
	if err != nil {
		log.Printf("passkey registration: %v", err)
		writeJSON(w, http.StatusInternalServerError, message{"Kunne ikke starte registreringen"})
		return
	}
	s.passkeys.start(w, s.isHTTPS(r), ceremony{session: *session, register: true, name: name})
	writeJSON(w, http.StatusOK, creation)
}

func (s *Server) passkeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	if !s.authenticated(r) {
		writeJSON(w, http.StatusUnauthorized, message{"Logg inn først"})
		return
	}
	c, ok := s.passkeys.finish(r, true)
	if !ok {
		writeJSON(w, http.StatusBadRequest, message{"Registreringen gikk ut på tid, prøv igjen"})
		return
	}
	user, err := s.adminUser()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, message{"Kunne ikke lese passnøklene"})
		return
	}
	credential, err := s.passkeys.webauthn.FinishRegistration(user, c.session, r)
	if err != nil {
		log.Printf("passkey registration: %v", err)
		writeJSON(w, http.StatusBadRequest, message{"Passnøkkelen kunne ikke bekreftes"})
		return
	}
	stored, err := json.Marshal(credential)
	if err == nil {
		err = s.Store.SavePasskey(base64.RawURLEncoding.EncodeToString(randomBytes(12)), c.name, stored)
	}
	if err != nil {
		log.Printf("save passkey: %v", err)
		writeJSON(w, http.StatusInternalServerError, message{"Kunne ikke lagre passnøkkelen"})
		return
	}
	writeJSON(w, http.StatusOK, message{"Passnøkkelen «" + c.name + "» er lagt til"})
}

func (s *Server) passkeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	user, err := s.adminUser()
	if err != nil || len(user.credentials) == 0 {
		writeJSON(w, http.StatusNotFound, message{"Ingen passnøkler er registrert"})
		return
	}
	assertion, session, err := s.passkeys.webauthn.BeginLogin(user)
	if err != nil {
		log.Printf("passkey login: %v", err)
		writeJSON(w, http.StatusInternalServerError, message{"Kunne ikke starte innloggingen"})
		return
	}
	s.passkeys.start(w, s.isHTTPS(r), ceremony{session: *session})
	writeJSON(w, http.StatusOK, assertion)
}

func (s *Server) passkeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	// failed attempts count against the same limit as token logins
	client := clientKey(r)
	if !s.limiter.allowScoped("adminlogin", client, 5, 1.0/60, 20, 1.0/30) {
		writeJSON(w, http.StatusTooManyRequests, message{"For mange forsøk. Vent et minutt og prøv igjen."})
		return
	}
	c, ok := s.passkeys.finish(r, false)
	if !ok {
		writeJSON(w, http.StatusBadRequest, message{"Innloggingen gikk ut på tid, prøv igjen"})
		return
	}
	user, err := s.adminUser()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, message{"Kunne ikke lese passnøklene"})
		return
	}
	credential, err := s.passkeys.webauthn.FinishLogin(user, c.session, r)
	if err == nil && credential.Authenticator.CloneWarning {
		err = errors.New("the sign count went backwards: possibly a cloned authenticator")
	}
	if err != nil {
		log.Printf("passkey login: %v", err)
		writeJSON(w, http.StatusUnauthorized, message{"Passnøkkelen ble ikke godtatt"})
		return
	}
	s.limiter.refundScoped("adminlogin", client)
	if id := user.byID[base64.RawURLEncoding.EncodeToString(credential.ID)]; id != "" {
		if stored, err := json.Marshal(credential); err == nil {
			if err := s.Store.UsePasskey(id, stored); err != nil {
				log.Printf("update passkey %s: %v", id, err)
			}
		}
	}
	if err := s.startSession(w, r, "passkey"); err != nil {
		log.Printf("start admin session: %v", err)
		writeJSON(w, http.StatusInternalServerError, message{"Kunne ikke logge inn"})
		return
	}
	writeJSON(w, http.StatusOK, message{"ok"})
}

// deletePasskey removes one (admin form); the notice to show.
func (s *Server) deletePasskey(id string) string {
	removed, err := s.Store.DeletePasskey(id)
	switch {
	case err != nil:
		log.Printf("delete passkey: %v", err)
		return "Passnøkkelen kunne ikke fjernes"
	case !removed:
		return "Fant ikke passnøkkelen"
	}
	return "Passnøkkelen er fjernet. Fjern den også fra nøkkelringen på enheten."
}

// passkeyView: availability for the login page; with list, also the
// registered passkeys and session count for the dashboard.
func (s *Server) passkeyView(list bool) passkeyView {
	view := passkeyView{Available: s.passkeys != nil}
	if !view.Available {
		return view
	}
	view.Ready = s.Store.CountPasskeys() > 0
	view.TokenLoginOff = s.tokenLoginOff()
	if !list {
		return view
	}
	stored, err := s.Store.ListPasskeys()
	if err != nil {
		log.Printf("list passkeys: %v", err)
	}
	for _, p := range stored {
		row := passkeyRow{ID: p.ID, Name: p.Name, Created: formatTime(p.CreatedAt), LastUsed: "aldri"}
		if p.LastUsedAt != nil {
			row.LastUsed = formatTime(*p.LastUsedAt)
		}
		view.List = append(view.List, row)
	}
	view.Sessions = s.Store.CountAdminSessions()
	return view
}
