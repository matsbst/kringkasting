package web

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
)

// Admin sessions live in the database: the cookie holds a random value,
// the database only its hash. Logging out ends the session for real, and
// "log out everywhere" ends all of them.
const sessionTTL = 30 * 24 * time.Hour

func hashSession(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// startSession signs the visitor in; via says how ("token", "passkey").
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, via string) error {
	value := base64.RawURLEncoding.EncodeToString(randomBytes(32))
	if err := s.Store.CreateAdminSession(hashSession(value), via, clock.Now().Add(sessionTTL)); err != nil {
		return err
	}
	s.setSession(w, r, value, int(sessionTTL.Seconds()))
	return nil
}

// endSession signs this browser out.
func (s *Server) endSession(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(adminCookie); err == nil {
		if err := s.Store.DeleteAdminSession(hashSession(cookie.Value)); err != nil {
			log.Printf("end admin session: %v", err)
		}
	}
	s.setSession(w, r, "", 0)
}

func (s *Server) authenticated(r *http.Request) bool {
	if !s.adminEnabled() {
		return false
	}
	cookie, err := r.Cookie(adminCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	return s.Store.AdminSessionValid(hashSession(cookie.Value))
}

// setSession sets (maxAge > 0) or clears (maxAge 0) the admin cookie.
func (s *Server) setSession(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	secure := ""
	if s.isHTTPS(r) {
		secure = " Secure;"
	}
	w.Header().Set("Set-Cookie", fmt.Sprintf("%s=%s; Path=/admin; HttpOnly;%s SameSite=Strict; Max-Age=%d",
		adminCookie, value, secure, maxAge))
}

// tokenFingerprintKey stores a slow, salted hash of ADMIN_TOKEN, so a
// changed token is noticed at startup.
const tokenFingerprintKey = "admin-token-fingerprint"

// endSessionsIfTokenChanged ends every session when ADMIN_TOKEN differs
// from the one the sessions were started under: changing the token is
// the way to sign everyone out, as it was before sessions were stored.
func (s *Server) endSessionsIfTokenChanged() {
	if !s.adminEnabled() {
		return
	}
	var stored string
	s.Store.ReadState(tokenFingerprintKey, &stored)
	if salt, _, ok := strings.Cut(stored, ":"); ok && tokenFingerprint(s.Config.AdminToken, salt) == stored {
		return
	}
	if err := s.Store.DeleteAdminSessions(); err != nil {
		log.Printf("end admin sessions: %v", err)
		return
	}
	s.Store.WriteState(tokenFingerprintKey, tokenFingerprint(s.Config.AdminToken, hex.EncodeToString(randomBytes(16))))
}

// tokenFingerprint is "salt:hash"; PBKDF2, so a database backup doesn't
// make short tokens cheap to guess.
func tokenFingerprint(token, salt string) string {
	key, err := pbkdf2.Key(sha256.New, token, []byte(salt), 100_000, 32)
	if err != nil {
		return ""
	}
	return salt + ":" + hex.EncodeToString(key)
}

// tokenMatches compares hashes, so the comparison takes constant time.
func (s *Server) tokenMatches(candidate string) bool {
	if !s.adminEnabled() {
		return false
	}
	a, b := sha256.Sum256([]byte(candidate)), sha256.Sum256([]byte(s.Config.AdminToken))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b) // never fails (crypto/rand)
	return b
}
