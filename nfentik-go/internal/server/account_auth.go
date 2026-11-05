package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nfentik/nfentik-go/internal/store"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookieName = "nfentik_session"
	csrfCookieName    = "nfentik_csrf"
)

var (
	// ErrSessionMissing means the request has no account session cookie.
	ErrSessionMissing = errors.New("请先登录")
	// ErrAccountDisabled means the account exists but cannot log in.
	ErrAccountDisabled = errors.New("账号已停用")
	// ErrCSRF means a cookie-authenticated write is missing a valid token.
	ErrCSRF = errors.New("请求校验失败，请刷新页面后重试")
	// ErrBadOrigin means a browser request came from another origin.
	ErrBadOrigin = errors.New("请求来源无效")
)

// accountSession resolves the account behind the session cookie.
func (s *Server) accountSession(r *http.Request) (store.Session, *store.Account, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return store.Session{}, nil, ErrSessionMissing
	}
	sess, err := s.store.GetSession(store.HashSessionToken(cookie.Value))
	if err != nil {
		return store.Session{}, nil, ErrSessionMissing
	}
	acc, err := s.store.GetAccountByID(sess.AccountID)
	if err != nil {
		return store.Session{}, nil, ErrSessionMissing
	}
	switch acc.Status {
	case "enabled":
	case "unbound":
		return store.Session{}, nil, store.ErrAccountNotBound
	default:
		return store.Session{}, nil, ErrAccountDisabled
	}
	if acc.UserID == nil {
		return store.Session{}, nil, store.ErrAccountNotBound
	}
	if t, err := time.Parse("2006-01-02 15:04:05", sess.LastSeenAt); err != nil || time.Since(t) > 5*time.Minute {
		_ = s.store.TouchSession(sess.ID)
	}
	return sess, &acc, nil
}

// currentUser resolves the user for user-center endpoints. A bearer token takes
// precedence for API compatibility; otherwise a session cookie is used.
func (s *Server) currentUser(r *http.Request) (*store.User, *store.Account, error) {
	if token := userTokenFromRequest(r); token != "" {
		u, err := s.userFromToken(token)
		if err != nil {
			return nil, nil, err
		}
		acc, err := s.store.GetAccountByUserID(u.ID)
		if errors.Is(err, store.ErrAccountNotFound) {
			return u, nil, nil
		}
		if err != nil {
			return nil, nil, err
		}
		return u, &acc, nil
	}
	_, acc, err := s.accountSession(r)
	if err != nil {
		return nil, nil, err
	}
	u, err := s.store.GetUserByID(*acc.UserID)
	if err != nil {
		return nil, nil, err
	}
	if !u.Enabled {
		return nil, nil, ErrUserDisabled
	}
	return &u, acc, nil
}

// requireCSRF validates the double-submit token for cookie-authenticated writes.
func (s *Server) requireCSRF(r *http.Request) error {
	if userTokenFromRequest(r) != "" {
		return nil
	}
	if _, err := r.Cookie(sessionCookieName); err != nil {
		return nil
	}
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return ErrCSRF
	}
	header := r.Header.Get("X-CSRF-Token")
	if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
		return ErrCSRF
	}
	return nil
}

// requireSameOrigin rejects cross-site browser requests when Origin or Referer
// is present.
func (s *Server) requireSameOrigin(r *http.Request) error {
	want := s.origin(r)
	if origin := r.Header.Get("Origin"); origin != "" && origin != want {
		return ErrBadOrigin
	}
	if ref := r.Header.Get("Referer"); ref != "" && ref != want && !strings.HasPrefix(ref, want+"/") {
		return ErrBadOrigin
	}
	return nil
}

func (s *Server) secureCookie(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   s.secureCookie(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secureCookie(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) setCSRFCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: false,
		Secure:   s.secureCookie(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearCSRFCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   s.secureCookie(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// newCSRFToken returns a random token for the double-submit cookie.
func newCSRFToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// validateUsername enforces the documented username shape.
func validateUsername(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) < 3 || len(raw) > 32 {
		return "", errors.New("用户名需为 3 到 32 个字符")
	}
	for i, r := range raw {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			if i == 0 && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
				return "", errors.New("用户名需以字母开头")
			}
			continue
		}
		return "", errors.New("用户名只能包含字母、数字和下划线")
	}
	return raw, nil
}

// validatePassword enforces the bcrypt-safe password length.
func validatePassword(raw string) error {
	if len(raw) < 8 {
		return errors.New("密码至少 8 个字符")
	}
	if len(raw) > 72 {
		return errors.New("密码最多 72 个字节")
	}
	return nil
}

func hashPassword(raw string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(raw), 12)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func checkPassword(hash, raw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(raw)) == nil
}
