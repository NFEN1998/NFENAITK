package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nfentik/nfentik-go/internal/store"
)

// startAccountSession creates a server-side session and sets the session and
// CSRF cookies. It returns the CSRF token for the response body.
func (s *Server) startAccountSession(w http.ResponseWriter, r *http.Request, acc store.Account) (string, time.Time, error) {
	settings := s.config.Settings()
	ttlDays := settings.AccountSessionTTLDays
	if ttlDays <= 0 {
		ttlDays = 30
	}
	ttl := time.Duration(ttlDays) * 24 * time.Hour
	plain, err := store.NewSessionToken()
	if err != nil {
		return "", time.Time{}, err
	}
	if _, err := s.store.CreateSession(acc.ID, store.HashSessionToken(plain), ttl, clientIP(r), r.UserAgent()); err != nil {
		return "", time.Time{}, err
	}
	s.setSessionCookie(w, r, plain, ttl)
	csrf, err := newCSRFToken()
	if err != nil {
		return "", time.Time{}, err
	}
	s.setCSRFCookie(w, r, csrf, ttl)
	return csrf, time.Now().Add(ttl), nil
}

func (s *Server) userAudit(action string, u *store.User, detail string) {
	if u == nil {
		return
	}
	if err := s.store.InsertAudit(action, u.ID, u.Token, detail, "user"); err != nil {
		s.PublishError("audit %s: %v", action, err)
	}
}

func writeAccountError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "服务器内部错误"
	switch {
	case errors.Is(err, store.ErrAccountExists):
		status, message = http.StatusConflict, "用户名已被占用"
	case errors.Is(err, store.ErrAccountBound):
		status, message = http.StatusConflict, "该令牌已绑定账号"
	case errors.Is(err, store.ErrAccountNotBound):
		status, message = http.StatusConflict, "账号未绑定令牌"
	case errors.Is(err, store.ErrAccountNotFound):
		status, message = http.StatusNotFound, "账号不存在"
	case errors.Is(err, ErrSessionMissing):
		status, message = http.StatusUnauthorized, "请先登录"
	case errors.Is(err, ErrAccountDisabled), errors.Is(err, ErrUserDisabled), errors.Is(err, store.ErrRedeemUserDisabled):
		status, message = http.StatusForbidden, "账号已停用"
	case errors.Is(err, ErrCSRF):
		status, message = http.StatusForbidden, ErrCSRF.Error()
	case errors.Is(err, ErrBadOrigin):
		status, message = http.StatusForbidden, ErrBadOrigin.Error()
	case errors.Is(err, store.ErrCodeExpired):
		status, message = http.StatusBadRequest, "卡密已过期"
	case errors.Is(err, store.ErrCodeInvalid), errors.Is(err, store.ErrCodeInvalidCodeInput):
		status, message = http.StatusBadRequest, "卡密无效或已使用"
	case errors.Is(err, store.ErrCodeNeedsDuration):
		status, message = http.StatusConflict, "当前套餐请使用时长卡密"
	case errors.Is(err, store.ErrCodeNeedsCount):
		status, message = http.StatusConflict, "当前套餐请使用次数卡密"
	case errors.Is(err, store.ErrCodeNoNeed):
		status, message = http.StatusConflict, "当前为无限套餐，无需充值"
	}
	writeJSON(w, status, map[string]any{"success": false, "message": message})
}

// handleUserRegister registers an account, optionally binding an existing
// token, and starts a session.
func (s *Server) handleUserRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.requireSameOrigin(r); err != nil {
		writeAccountError(w, err)
		return
	}
	settings := s.config.Settings()
	if !settings.AccountRegistrationEnabled {
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "message": "注册暂未开放"})
		return
	}
	if !s.limiter.allow("register:"+clientIP(r), 5, time.Hour) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"success": false, "message": "注册过于频繁，请稍后再试"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Token    string `json:"token"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请求体解析失败"})
		return
	}
	username, err := validateUsername(req.Username)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	if err := validatePassword(req.Password); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		writeAccountError(w, err)
		return
	}

	var acc store.Account
	var user *store.User
	if token := strings.TrimSpace(req.Token); token != "" {
		u, err := s.userFromToken(token)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "令牌无效或已被绑定"})
			return
		}
		if _, err := s.store.GetAccountByUserID(u.ID); err == nil {
			writeJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "令牌无效或已被绑定"})
			return
		} else if !errors.Is(err, store.ErrAccountNotFound) {
			writeAccountError(w, err)
			return
		}
		acc, err = s.store.CreateAccountForUser(username, hash, u.ID)
		if err != nil {
			writeAccountError(w, err)
			return
		}
		user = u
	} else {
		newAcc, newUser, err := s.store.CreateAccountWithNewUser(username, hash)
		if err != nil {
			writeAccountError(w, err)
			return
		}
		acc, user = newAcc, &newUser
	}

	csrf, expires, err := s.startAccountSession(w, r, acc)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	s.userAudit("register", user, `{"source":"web"}`)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "user": user, "account": acc,
		"csrf_token": csrf, "session_expires_at": expires.Format(time.RFC3339),
	})
}

// handleAccountLogin authenticates a username and password.
func (s *Server) handleAccountLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.requireSameOrigin(r); err != nil {
		writeAccountError(w, err)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请求体解析失败"})
		return
	}
	settings := s.config.Settings()
	window := time.Duration(settings.AccountLoginWindowMinutes) * time.Minute
	if window <= 0 {
		window = 15 * time.Minute
	}
	limit := settings.AccountLoginMaxFailures
	if limit <= 0 {
		limit = 5
	}
	key := "login:" + clientIP(r) + ":" + strings.ToLower(strings.TrimSpace(req.Username))
	if !s.limiter.check(key, limit, window) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"success": false, "message": "登录过于频繁，请稍后再试"})
		return
	}
	acc, err := s.store.GetAccountByUsername(req.Username)
	if err != nil || !checkPassword(acc.PasswordHash, req.Password) {
		s.limiter.hit(key, window)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "用户名或密码错误"})
		return
	}
	switch acc.Status {
	case "enabled":
	case "unbound":
		writeJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "账号未绑定令牌"})
		return
	default:
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "message": "账号已停用"})
		return
	}
	if acc.UserID == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "账号未绑定令牌"})
		return
	}
	u, err := s.store.GetUserByID(*acc.UserID)
	if err != nil || !u.Enabled {
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "message": "账号已停用"})
		return
	}
	s.limiter.reset(key)
	_ = s.store.TouchAccountLogin(acc.ID)
	csrf, expires, err := s.startAccountSession(w, r, acc)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	s.userAudit("login", &u, `{"source":"account"}`)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "user": u, "account": acc,
		"csrf_token": csrf, "session_expires_at": expires.Format(time.RFC3339),
	})
}

// handleAccountLogout revokes the current session.
func (s *Server) handleAccountLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.requireSameOrigin(r); err != nil {
		writeAccountError(w, err)
		return
	}
	if err := s.requireCSRF(r); err != nil {
		writeAccountError(w, err)
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		_ = s.store.DeleteSession(store.HashSessionToken(cookie.Value))
	}
	s.clearSessionCookie(w, r)
	s.clearCSRFCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// handleAccountMe returns the account bound to the current login.
func (s *Server) handleAccountMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, acc, err := s.currentUser(r)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "user": u, "account": acc})
}

// handleAccountBind binds an account to the token used for this request.
func (s *Server) handleAccountBind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.requireSameOrigin(r); err != nil {
		writeAccountError(w, err)
		return
	}
	token := userTokenFromRequest(r)
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "请先使用令牌登录"})
		return
	}
	u, err := s.userFromToken(token)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "令牌无效"})
		return
	}
	if _, err := s.store.GetAccountByUserID(u.ID); err == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "该令牌已绑定账号"})
		return
	} else if !errors.Is(err, store.ErrAccountNotFound) {
		writeAccountError(w, err)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请求体解析失败"})
		return
	}
	username, err := validateUsername(req.Username)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	if err := validatePassword(req.Password); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	acc, err := s.store.CreateAccountForUser(username, hash, u.ID)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	csrf, expires, err := s.startAccountSession(w, r, acc)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	s.userAudit("bind", u, `{"source":"token"}`)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "user": u, "account": acc,
		"csrf_token": csrf, "session_expires_at": expires.Format(time.RFC3339),
	})
}

// handleAccountPassword changes the password and rotates the session.
func (s *Server) handleAccountPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.requireSameOrigin(r); err != nil {
		writeAccountError(w, err)
		return
	}
	if err := s.requireCSRF(r); err != nil {
		writeAccountError(w, err)
		return
	}
	u, acc, err := s.currentUser(r)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	if acc == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "请先绑定账号"})
		return
	}
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请求体解析失败"})
		return
	}
	if !checkPassword(acc.PasswordHash, req.OldPassword) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "原密码错误"})
		return
	}
	if err := validatePassword(req.NewPassword); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	hash, err := hashPassword(req.NewPassword)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	if err := s.store.SetAccountPassword(acc.ID, hash); err != nil {
		writeAccountError(w, err)
		return
	}
	if err := s.store.DeleteAccountSessions(acc.ID); err != nil {
		writeAccountError(w, err)
		return
	}
	csrf, expires, err := s.startAccountSession(w, r, *acc)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	s.userAudit("password", u, `{"source":"account"}`)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "csrf_token": csrf, "session_expires_at": expires.Format(time.RFC3339),
	})
}

// handleUserRedeem applies a redemption code to the current user.
func (s *Server) handleUserRedeem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.requireSameOrigin(r); err != nil {
		writeAccountError(w, err)
		return
	}
	if err := s.requireCSRF(r); err != nil {
		writeAccountError(w, err)
		return
	}
	u, _, err := s.currentUser(r)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请求体解析失败"})
		return
	}
	settings := s.config.Settings()
	limit := settings.RedeemMaxFailuresPerHour
	if limit <= 0 {
		limit = 10
	}
	key := "redeem:" + strconv.FormatInt(u.ID, 10)
	if !s.limiter.check(key, limit, time.Hour) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"success": false, "message": "兑换过于频繁，请稍后再试"})
		return
	}
	code := store.NormalizeRedemptionCode(req.Code)
	if len(code) < 8 {
		s.limiter.hit(key, time.Hour)
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "卡密格式无效"})
		return
	}
	result, err := s.store.RedeemCode(u.ID, store.HashRedemptionCode(code))
	if err != nil {
		s.limiter.hit(key, time.Hour)
		writeAccountError(w, err)
		return
	}
	s.limiter.reset(key)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "user": result.User, "result": result})
}
