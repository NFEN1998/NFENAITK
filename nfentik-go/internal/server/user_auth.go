package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nfentik/nfentik-go/internal/plan"
	"github.com/nfentik/nfentik-go/internal/store"
)

// ErrUserTokenMissing is returned when a request carries no user token.
var ErrUserTokenMissing = errors.New("缺少令牌")

// ErrUserDisabled is returned when a user's account is disabled.
var ErrUserDisabled = errors.New("账号已停用")

// userTokenFromRequest extracts a user token from the Authorization header or
// the ?token= query parameter, preferring the header.
func userTokenFromRequest(r *http.Request) string {
	if t := bearerToken(r); t != "" {
		return t
	}
	return strings.TrimSpace(r.URL.Query().Get("token"))
}

// userFromRequest resolves the user behind a request and validates that the
// account is enabled. Plan expiry and quota are checked separately by the
// caller so it can distinguish the failure reasons.
func (s *Server) userFromRequest(r *http.Request) (*store.User, error) {
	token := userTokenFromRequest(r)
	if token == "" {
		return nil, ErrUserTokenMissing
	}
	return s.userFromToken(token)
}

// userFromToken resolves a usertoken value and validates the account is enabled.
func (s *Server) userFromToken(token string) (*store.User, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrUserTokenMissing
	}
	u, err := s.store.GetUserByToken(token)
	if err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			return nil, errors.New("令牌无效")
		}
		return nil, err
	}
	if !u.Enabled {
		return nil, ErrUserDisabled
	}
	return u, nil
}

// userUsable classifies a user's plan status.
func userUsable(u *store.User) string {
	in := plan.StatusInput{Enabled: u.Enabled, RemainCount: u.RemainCount, TotalCount: u.TotalCount}
	if u.ExpireAt != nil {
		if t, err := parseStoreTime(*u.ExpireAt); err == nil {
			in.ExpireAt = &t
		}
	}
	return plan.Status(in, timeNow())
}

// handleUserLogin resolves a usertoken and returns the user profile.
func (s *Server) handleUserLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请求体解析失败"})
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请输入用户令牌"})
		return
	}
	settings := s.config.Settings()
	if settings.AdminToken != "" && token == settings.AdminToken {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "role": "admin", "name": "管理员", "token": token})
		return
	}
	u, err := s.store.GetUserByToken(token)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "令牌无效"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "role": "user", "user": u})
}

// handleUserMe returns the current user's plan and status.
func (s *Server) handleUserMe(w http.ResponseWriter, r *http.Request) {
	u, err := s.userFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "user": u, "status": userUsable(u), "plans": plan.Plans,
	})
}

// handleUserLogs returns the current user's own request logs.
func (s *Server) handleUserLogs(w http.ResponseWriter, r *http.Request) {
	u, err := s.userFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": err.Error()})
		return
	}
	page := int(parseIntDefault(r.URL.Query().Get("page"), 1))
	pageSize := int(parseIntDefault(r.URL.Query().Get("page_size"), 20))
	items, total, err := s.store.UserLogs(u.ID, page, pageSize)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "items": items, "total": total, "page": page, "page_size": pageSize,
	})
}

// handleUserResetToken issues a new token for the current user.
func (s *Server) handleUserResetToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, err := s.userFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": err.Error()})
		return
	}
	newToken, err := s.store.ResetUserToken(u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	if err := s.store.InsertAudit("self_reset_token", u.ID, newToken, `{}`, "user"); err != nil {
		s.PublishError("audit self reset token: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "token": newToken})
}

// handleUserProfile lets a user update their own note.
func (s *Server) handleUserProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, err := s.userFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Note *string `json:"note"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	if req.Note == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "缺少备注"})
		return
	}
	if err := s.store.UpdateUser(u.ID, req.Note, nil); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	updated, _ := s.store.GetUserByID(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "user": updated})
}

// handleUser routes every /api/user/* endpoint.
func (s *Server) handleUser(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/user"), "/")
	switch path {
	case "login":
		s.handleUserLogin(w, r)
	case "me":
		s.handleUserMe(w, r)
	case "logs":
		s.handleUserLogs(w, r)
	case "reset-token":
		s.handleUserResetToken(w, r)
	case "profile":
		s.handleUserProfile(w, r)
	default:
		http.NotFound(w, r)
	}
}

// timeNow is a seam for tests.
var timeNow = func() time.Time { return time.Now() }

func parseStoreTime(v string) (time.Time, error) {
	layouts := []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00", "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("无法解析时间")
}
