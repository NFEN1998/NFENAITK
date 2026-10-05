package server

import (
	"net/http"
	"strings"
)

// handleLogin validates an admin or multi-user token.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请输入访问令牌"})
		return
	}

	settings := s.config.Settings()
	if settings.AdminToken != "" && token == settings.AdminToken {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "role": "admin", "name": "管理员", "token": token,
		})
		return
	}
	if settings.MultiUser.Enabled {
		for _, u := range settings.MultiUser.Users {
			if u.Token == token {
				writeJSON(w, http.StatusOK, map[string]any{
					"success": true, "role": "user", "id": u.ID, "token": token,
				})
				return
			}
		}
	}
	writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "令牌无效"})
}

// requireAdmin returns true when the request carries a valid admin token.
// When no admin token is configured the console is open by design.
func (s *Server) requireAdmin(r *http.Request) bool {
	settings := s.config.Settings()
	if settings.AdminToken == "" {
		return true
	}
	token := bearerToken(r)
	if token == "" {
		token = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	return token == settings.AdminToken
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (s *Server) unauthorized(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "未授权，请提供有效的管理令牌"})
}
