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
// the ?token= query parameter, preferring the header. ?key= is accepted as an
// alias because MOGO calls its token a key.
func userTokenFromRequest(r *http.Request) string {
	if t := bearerToken(r); t != "" {
		return t
	}
	query := r.URL.Query()
	if t := firstQueryValue(query["token"]); t != "" {
		return t
	}
	return firstQueryValue(query["key"])
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

// entitlementStatus classifies one balance.
func entitlementStatus(e store.Entitlement, now time.Time) string {
	in := plan.StatusInput{Enabled: true, Unlimited: e.Unlimited}
	if e.ExpireAt != nil {
		if t, err := parseStoreTime(*e.ExpireAt); err == nil {
			in.ExpireAt = &t
		}
	}
	if e.Kind == string(plan.KindCount) {
		remain := e.RemainCount
		if remain == nil {
			zero := int64(0)
			remain = &zero
		}
		in.RemainCount = remain
		in.TotalCount = e.TotalCount
	}
	return plan.Status(in, now)
}

func findUserEntitlement(u *store.User, kind string) *store.Entitlement {
	for i := range u.Entitlements {
		if u.Entitlements[i].Kind == kind {
			return &u.Entitlements[i]
		}
	}
	return nil
}

// userUsable classifies a user by the primary balance. Duration wins while it
// is active; otherwise an active count balance is used.
func userUsable(u *store.User) string {
	if u == nil {
		return plan.StatusDisabled
	}
	if !u.Enabled {
		return plan.StatusDisabled
	}
	if len(u.Entitlements) > 0 {
		now := timeNow()
		duration := findUserEntitlement(u, string(plan.KindDuration))
		count := findUserEntitlement(u, string(plan.KindCount))
		if duration != nil {
			status := entitlementStatus(*duration, now)
			if status == plan.StatusActive || status == plan.StatusExpiring {
				return status
			}
		}
		if count != nil {
			status := entitlementStatus(*count, now)
			if status == plan.StatusActive || status == plan.StatusExpiring {
				return status
			}
		}
		if duration != nil {
			return entitlementStatus(*duration, now)
		}
		if count != nil {
			return entitlementStatus(*count, now)
		}
	}
	in := plan.StatusInput{Enabled: u.Enabled, RemainCount: u.RemainCount, TotalCount: u.TotalCount}
	if u.ExpireAt != nil {
		if t, err := parseStoreTime(*u.ExpireAt); err == nil {
			in.ExpireAt = &t
		}
	}
	return plan.Status(in, timeNow())
}

// queryAccess reports which balance should serve a query. An empty kind means
// the caller is anonymous or the user system is disabled.
func queryAccess(u *store.User) (string, error) {
	if u == nil {
		return "", nil
	}
	if !u.Enabled {
		return "", errors.New("账号已停用")
	}
	now := timeNow()
	if len(u.Entitlements) > 0 {
		duration := findUserEntitlement(u, string(plan.KindDuration))
		count := findUserEntitlement(u, string(plan.KindCount))
		if duration != nil {
			status := entitlementStatus(*duration, now)
			if status == plan.StatusActive || status == plan.StatusExpiring {
				return string(plan.KindDuration), nil
			}
		}
		if count != nil {
			status := entitlementStatus(*count, now)
			if status == plan.StatusActive || status == plan.StatusExpiring {
				return string(plan.KindCount), nil
			}
		}
		if duration != nil {
			return "", errors.New("套餐已过期")
		}
		if count != nil {
			if count.ExpireAt != nil {
				if t, err := parseStoreTime(*count.ExpireAt); err == nil && now.After(t) {
					return "", errors.New("次数套餐已过期")
				}
			}
			return "", errors.New("次数已用尽")
		}
	}
	switch userUsable(u) {
	case plan.StatusExpired:
		return "", errors.New("套餐已过期")
	case plan.StatusExhausted:
		return "", errors.New("次数已用尽")
	case plan.StatusDisabled:
		return "", errors.New("账号已停用")
	}
	if u.PlanType == string(plan.KindCount) {
		return string(plan.KindCount), nil
	}
	return string(plan.KindDuration), nil
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
	u, _, err := s.currentUser(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "user": u, "status": userUsable(u), "plans": s.userPlanCatalogue(),
	})
}

// handleUserLogs returns the current user's own request logs.
func (s *Server) handleUserLogs(w http.ResponseWriter, r *http.Request) {
	u, _, err := s.currentUser(r)
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
	u, _, err := s.currentUser(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": err.Error()})
		return
	}
	oldToken := u.Token
	newToken, err := s.store.ResetUserToken(u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	s.auditTokenReset("self_reset_token", u.ID, oldToken, newToken, "user", r)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "token": newToken})
}

// handleUserProfile lets a user update their own note.
func (s *Server) handleUserProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, _, err := s.currentUser(r)
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
	case "register":
		s.handleUserRegister(w, r)
	case "me":
		s.handleUserMe(w, r)
	case "logs":
		s.handleUserLogs(w, r)
	case "reset-token":
		s.handleUserResetToken(w, r)
	case "profile":
		s.handleUserProfile(w, r)
	case "redeem":
		s.handleUserRedeem(w, r)
	case "account/login":
		s.handleAccountLogin(w, r)
	case "account/logout":
		s.handleAccountLogout(w, r)
	case "account/me":
		s.handleAccountMe(w, r)
	case "account/bind":
		s.handleAccountBind(w, r)
	case "account/password":
		s.handleAccountPassword(w, r)
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

// userPlanCatalogue returns the catalogue used for labels in the user portal.
// Disabled plans remain visible so an existing user can still see the name of
// the plan they hold.
func (s *Server) userPlanCatalogue() []plan.Plan {
	plans, err := s.store.ListPlans()
	if err != nil || len(plans) == 0 {
		return plan.Plans
	}
	return plans
}
