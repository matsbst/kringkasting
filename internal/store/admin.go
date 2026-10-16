package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/matsbst/kringkasting/internal/clock"
)

// Admin sessions and passkeys. New tables only, so older versions ignore
// them and rollbacks keep working.
const adminSchema = `
CREATE TABLE IF NOT EXISTS admin_sessions (
  token_hash TEXT PRIMARY KEY,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  via TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS admin_passkeys (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  credential TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  last_used_at INTEGER
);
`

// Passkey is a registered admin passkey. Credential is the library's JSON
// for it: public key, sign count and flags, never a secret.
type Passkey struct {
	ID         string
	Name       string
	Credential []byte
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// CreateAdminSession stores a session by the hash of its cookie value.
func (s *Store) CreateAdminSession(tokenHash, via string, expires time.Time) error {
	// expired sessions go whenever a new one starts
	if _, err := s.write.Exec("DELETE FROM admin_sessions WHERE expires_at <= ?", clock.NowMs()); err != nil {
		return err
	}
	_, err := s.write.Exec("INSERT INTO admin_sessions (token_hash, created_at, expires_at, via) VALUES (?, ?, ?, ?)",
		tokenHash, clock.NowMs(), expires.UnixMilli(), via)
	return err
}

// AdminSessionValid reports whether a session exists and hasn't expired.
func (s *Store) AdminSessionValid(tokenHash string) bool {
	var one int
	return s.read.QueryRow("SELECT 1 FROM admin_sessions WHERE token_hash = ? AND expires_at > ?",
		tokenHash, clock.NowMs()).Scan(&one) == nil
}

// DeleteAdminSession ends one session.
func (s *Store) DeleteAdminSession(tokenHash string) error {
	_, err := s.write.Exec("DELETE FROM admin_sessions WHERE token_hash = ?", tokenHash)
	return err
}

// DeleteAdminSessions ends every session.
func (s *Store) DeleteAdminSessions() error {
	_, err := s.write.Exec("DELETE FROM admin_sessions")
	return err
}

// CountAdminSessions counts the sessions that haven't expired.
func (s *Store) CountAdminSessions() int {
	var n int
	_ = s.read.QueryRow("SELECT COUNT(*) FROM admin_sessions WHERE expires_at > ?", clock.NowMs()).Scan(&n)
	return n
}

// SavePasskey adds a passkey.
func (s *Store) SavePasskey(id, name string, credential []byte) error {
	_, err := s.write.Exec("INSERT INTO admin_passkeys (id, name, credential, created_at) VALUES (?, ?, ?, ?)",
		id, name, string(credential), clock.NowMs())
	return err
}

// UsePasskey records a login: the credential's new state (sign count)
// and when it was used.
func (s *Store) UsePasskey(id string, credential []byte) error {
	_, err := s.write.Exec("UPDATE admin_passkeys SET credential = ?, last_used_at = ? WHERE id = ?",
		string(credential), clock.NowMs(), id)
	return err
}

// DeletePasskey removes a passkey; false when there was none.
func (s *Store) DeletePasskey(id string) (bool, error) {
	result, err := s.write.Exec("DELETE FROM admin_passkeys WHERE id = ?", id)
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	return n > 0, nil
}

// ListPasskeys returns the passkeys, oldest first.
func (s *Store) ListPasskeys() ([]Passkey, error) {
	rows, err := s.read.Query("SELECT id, name, credential, created_at, last_used_at FROM admin_passkeys ORDER BY created_at, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var passkeys []Passkey
	for rows.Next() {
		var (
			p          Passkey
			credential string
			created    int64
			lastUsed   sql.NullInt64
		)
		if err := rows.Scan(&p.ID, &p.Name, &credential, &created, &lastUsed); err != nil {
			return nil, err
		}
		p.Credential, p.CreatedAt = []byte(credential), fromMs(created)
		if lastUsed.Valid {
			t := fromMs(lastUsed.Int64)
			p.LastUsedAt = &t
		}
		passkeys = append(passkeys, p)
	}
	return passkeys, rows.Err()
}

// CountPasskeys counts the registered passkeys.
func (s *Store) CountPasskeys() int {
	var n int
	if err := s.read.QueryRow("SELECT COUNT(*) FROM admin_passkeys").Scan(&n); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0
	}
	return n
}
