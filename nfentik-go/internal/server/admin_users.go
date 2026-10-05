package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nfentik/nfentik-go/internal/plan"
	"github.com/nfentik/nfentik-go/internal/store"
)

// adminUsers routes /api/admin/users/* endpoints.
func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			s.adminListUsers(w, r)
		case http.MethodPost:
			s.adminCreateUser(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	// Sub-resources that are not user ids.
	if rest[0] == "stats" && r.Method == http.MethodGet {
		s.adminUserStats(w, r)
		return
	}
	if rest[0] == "audits" && r.Method == http.MethodGet {
		s.adminUserAudits(w, r)
		return
	}
	if rest[0] == "logs" && len(rest) > 1 && rest[1] == "prune" && r.Method == http.MethodPost {
		s.adminPruneUserLogs(w, r)
		return
	}

	id, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if len(rest) > 1 {
		switch rest[1] {
		case "renew":
			if r.Method == http.MethodPost {
				s.adminRenewUser(w, r, id)
				return
			}
		case "reset-token":
			if r.Method == http.MethodPost {
				s.adminResetUserToken(w, r, id)
				return
			}
		}
	}
	switch r.Method {
	case http.MethodGet:
		u, err := s.store.GetUserByID(id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "user": u, "status": userUsable(&u)})
	case http.MethodPut:
		s.adminUpdateUser(w, r, id)
	case http.MethodDelete:
		u, _ := s.store.GetUserByID(id)
		if err := s.store.DeleteUser(id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		s.audit("delete", id, u.Name, `{}`)
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) adminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	type userView struct {
		store.User
		Status string `json:"status"`
	}
	views := make([]userView, 0, len(users))
	for _, u := range users {
		views = append(views, userView{User: u, Status: userUsable(&u)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "users": views, "plans": plan.Plans})
}

type userPlanRequest struct {
	Name          string `json:"name"`
	PlanCode      string `json:"plan_code"`
	Days          int    `json:"days"`
	TotalCount    int64  `json:"total_count"`
	EnforceExpiry bool   `json:"enforce_expiry"`
	ExpiryDays    int    `json:"expiry_days"`
	Note          string `json:"note"`
}

// buildPlan validates the request and resolves the plan plus derived fields.
func (s *Server) buildPlan(req userPlanRequest) (plan.Plan, *string, *int64, *int64, error) {
	code := strings.TrimSpace(req.PlanCode)
	if code == "" {
		code = s.config.Settings().DefaultPlanCode
	}
	if code == "" {
		return plan.Plan{}, nil, nil, nil, errors.New("请选择套餐")
	}
	p, ok := plan.Lookup(code)
	if !ok {
		return plan.Plan{}, nil, nil, nil, errors.New("套餐无效")
	}
	now := time.Now()
	var expireAt *string
	var total, remain *int64
	switch p.Kind {
	case plan.KindCount:
		if req.TotalCount <= 0 {
			return plan.Plan{}, nil, nil, nil, errors.New("总次数必须大于 0")
		}
		t := req.TotalCount
		total = &t
		r := req.TotalCount
		remain = &r
		if req.EnforceExpiry {
			if req.ExpiryDays <= 0 {
				return plan.Plan{}, nil, nil, nil, errors.New("有效期天数必须大于 0")
			}
			v := now.AddDate(0, 0, req.ExpiryDays).Format("2006-01-02 15:04:05")
			expireAt = &v
		}
	default:
		if p.Custom && req.Days <= 0 {
			return plan.Plan{}, nil, nil, nil, errors.New("自定义天数必须大于 0")
		}
		if exp := plan.ExpireAt(now, p, req.Days); exp != nil {
			v := exp.Format("2006-01-02 15:04:05")
			expireAt = &v
		}
	}
	return p, expireAt, total, remain, nil
}

func (s *Server) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req userPlanRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "用户名不能为空"})
		return
	}
	p, expireAt, total, remain, err := s.buildPlan(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	u, err := s.store.CreateUser(store.User{
		Name: name, PlanType: string(p.Kind), PlanCode: p.Code, PlanLabel: p.Label,
		StartAt: time.Now().Format("2006-01-02 15:04:05"), ExpireAt: expireAt,
		TotalCount: total, RemainCount: remain, Note: req.Note, Enabled: true,
	})
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrUserExists) {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]any{"success": false, "message": err.Error()})
		return
	}
	s.audit("create", u.ID, u.Name, `{"plan_code":"`+u.PlanCode+`"}`)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "user": u})
}

func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Name    *string `json:"name"`
		Note    *string `json:"note"`
		Enabled *bool   `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	if err := s.store.UpdateUser(id, req.Name, req.Note, req.Enabled); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrUserExists) {
			status = http.StatusConflict
		} else if strings.Contains(err.Error(), "不能为空") || strings.Contains(err.Error(), "过长") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]any{"success": false, "message": err.Error()})
		return
	}
	u, _ := s.store.GetUserByID(id)
	s.audit("update", id, u.Name, `{}`)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "user": u})
}

func (s *Server) adminRenewUser(w http.ResponseWriter, r *http.Request, id int64) {
	var req userPlanRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	p, _, _, _, err := s.buildPlan(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	if _, err := s.store.GetUserByID(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": err.Error()})
		return
	}
	before, _ := s.store.GetUserByID(id)
	action := "renew"
	if before.PlanCode != p.Code {
		action = "switch_plan"
	}
	u, err := s.store.RenewUser(id, p, req.Days, req.TotalCount, req.EnforceExpiry, req.ExpiryDays)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	s.audit(action, id, u.Name, `{"from":"`+before.PlanCode+`","to":"`+p.Code+`"}`)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "user": u})
}

func (s *Server) adminResetUserToken(w http.ResponseWriter, r *http.Request, id int64) {
	u, err := s.store.GetUserByID(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": err.Error()})
		return
	}
	token, err := s.store.ResetUserToken(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	s.audit("reset_token", id, u.Name, `{}`)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "token": token})
}

func (s *Server) adminUserStats(w http.ResponseWriter, r *http.Request) {
	window := int(parseIntDefault(r.URL.Query().Get("range"), 14))
	report, err := s.store.UsageReport(window)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "report": report})
}

func (s *Server) adminUserAudits(w http.ResponseWriter, r *http.Request) {
	page := int(parseIntDefault(r.URL.Query().Get("page"), 1))
	pageSize := int(parseIntDefault(r.URL.Query().Get("page_size"), 50))
	items, total, err := s.store.ListAudits(page, pageSize)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "audits": items, "total": total, "page": page, "page_size": pageSize,
	})
}

func (s *Server) adminPruneUserLogs(w http.ResponseWriter, r *http.Request) {
	settings := s.config.Settings()
	if err := s.store.PruneUserLogs(settings.UserLogPerUserLimit, settings.UserLogRetentionDays, settings.UserLogGlobalLimit); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	if err := s.store.PruneAudits(settings.UserAuditRetentionDays); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// audit records an admin action without failing the request on error.
func (s *Server) audit(action string, userID int64, userName, detail string) {
	if err := s.store.InsertAudit(action, userID, userName, detail, "admin"); err != nil {
		s.PublishError("audit %s: %v", action, err)
	}
}
