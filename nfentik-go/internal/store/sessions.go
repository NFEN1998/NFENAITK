package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// ErrSessionNotFound is returned when a session cookie is unknown or expired.
var ErrSessionNotFound = errors.New("会话已失效")

// Session is one server-side account login.
type Session struct {
	ID         int64  `json:"id"`
	AccountID  int64  `json:"account_id"`
	TokenHash  string `json:"-"`
	ExpiresAt  string `json:"expires_at"`
	CreatedAt  string `json:"created_at"`
	LastSeenAt string `json:"last_seen_at"`
	IP         string `json:"ip"`
	UserAgent  string `json:"user_agent"`
}

// NewSessionToken returns a random plaintext session token.
func NewSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashSessionToken returns the SHA-256 digest stored in the database.
func HashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func sessionTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

// CreateSession stores a new session and prunes expired rows.
func (s *Store) CreateSession(accountID int64, tokenHash string, ttl time.Duration, ip, ua string) (Session, error) {
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	now := time.Now()
	sess := Session{
		AccountID:  accountID,
		TokenHash:  tokenHash,
		ExpiresAt:  sessionTime(now.Add(ttl)),
		CreatedAt:  sessionTime(now),
		LastSeenAt: sessionTime(now),
		IP:         ip,
		UserAgent:  ua,
	}
	_, _ = s.exec(`DELETE FROM AccountSessions WHERE ExpiresAt < $1`, sessionTime(now))
	id, err := s.insertReturningID(`INSERT INTO AccountSessions
		(AccountId, TokenHash, ExpiresAt, CreatedAt, LastSeenAt, IP, UserAgent)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		sess.AccountID, sess.TokenHash, sess.ExpiresAt, sess.CreatedAt, sess.LastSeenAt,
		truncateRunes(sess.IP, 64), truncateRunes(sess.UserAgent, 255))
	if err != nil {
		return Session{}, err
	}
	sess.ID = id
	return sess, nil
}

// GetSession loads a session by its token digest and rejects expired rows.
func (s *Store) GetSession(tokenHash string) (Session, error) {
	var (
		sess      Session
		expiresAt sql.NullString
		createdAt sql.NullString
		lastSeen  sql.NullString
	)
	err := s.queryRow(`SELECT Id, AccountId, TokenHash, ExpiresAt, CreatedAt, LastSeenAt, IP, UserAgent
		FROM AccountSessions WHERE TokenHash = $1`, tokenHash).
		Scan(&sess.ID, &sess.AccountID, &sess.TokenHash, &expiresAt, &createdAt, &lastSeen, &sess.IP, &sess.UserAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, err
	}
	sess.ExpiresAt = expiresAt.String
	sess.CreatedAt = createdAt.String
	sess.LastSeenAt = lastSeen.String
	if sess.ExpiresAt <= sessionTime(time.Now()) {
		_, _ = s.exec(`DELETE FROM AccountSessions WHERE Id = $1`, sess.ID)
		return Session{}, ErrSessionNotFound
	}
	return sess, nil
}

// TouchSession updates the last-seen timestamp.
func (s *Store) TouchSession(id int64) error {
	_, err := s.exec(`UPDATE AccountSessions SET LastSeenAt = $1 WHERE Id = $2`, sessionTime(time.Now()), id)
	return err
}

// DeleteSession removes one session by token digest.
func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.exec(`DELETE FROM AccountSessions WHERE TokenHash = $1`, tokenHash)
	return err
}

// DeleteAccountSessions removes every session for an account.
func (s *Store) DeleteAccountSessions(accountID int64) error {
	_, err := s.exec(`DELETE FROM AccountSessions WHERE AccountId = $1`, accountID)
	return err
}

// PruneExpiredSessions removes expired sessions.
func (s *Store) PruneExpiredSessions() error {
	_, err := s.exec(`DELETE FROM AccountSessions WHERE ExpiresAt < $1`, sessionTime(time.Now()))
	return err
}
