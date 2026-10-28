package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/nfentik/nfentik-go/internal/plan"
	"github.com/nfentik/nfentik-go/internal/store"
)

type planRequest struct {
	Code        string `json:"code"`
	Label       string `json:"label"`
	Kind        string `json:"kind"`
	Days        int    `json:"days"`
	QuotaCount  int64  `json:"quota_count"`
	Unlimited   bool   `json:"unlimited"`
	Custom      bool   `json:"custom"`
	ExpiryDays  int    `json:"expiry_days"`
	Enabled     *bool  `json:"enabled"`
	Sort        int    `json:"sort"`
	PriceCents  int64  `json:"price_cents"`
	Currency    string `json:"currency"`
	Description string `json:"description"`
	Badge       string `json:"badge"`
}

func (p planRequest) toPlan(code string) plan.Plan {
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	if strings.TrimSpace(code) == "" {
		code = strings.TrimSpace(p.Code)
	}
	return plan.Plan{
		Code: code, Label: strings.TrimSpace(p.Label),
		Kind: plan.Kind(strings.ToLower(strings.TrimSpace(p.Kind))),
		Days: p.Days, QuotaCount: p.QuotaCount, Unlimited: p.Unlimited,
		Custom: p.Custom, ExpiryDays: p.ExpiryDays, Enabled: enabled,
		Sort: p.Sort, PriceCents: p.PriceCents, Currency: p.Currency,
		Description: p.Description, Badge: p.Badge,
	}
}

func (s *Server) adminPlans(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			plans, err := s.store.ListPlansWithUsage()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "plans": plans})
		case http.MethodPost:
			var req planRequest
			if err := decodeJSON(r, &req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
				return
			}
			p, err := s.store.CreatePlan(req.toPlan(""))
			if err != nil {
				status := http.StatusBadRequest
				if errors.Is(err, store.ErrPlanExists) {
					status = http.StatusConflict
				}
				writeJSON(w, status, map[string]any{"success": false, "message": err.Error()})
				return
			}
			s.audit("plan_create", 0, "", `{"code":"`+p.Code+`"}`)
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "plan": p})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	code := strings.TrimSpace(rest[0])
	switch r.Method {
	case http.MethodPut, http.MethodPost:
		var req planRequest
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
			return
		}
		if req.Enabled != nil && !*req.Enabled {
			plans, _ := s.store.ListPlans()
			enabled := 0
			for _, item := range plans {
				if item.Enabled {
					enabled++
				}
			}
			if code == s.config.Settings().DefaultPlanCode {
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "默认套餐不能停用，请先切换默认套餐"})
				return
			}
			if enabled <= 1 {
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "至少需要保留一个启用套餐"})
				return
			}
		}
		p, err := s.store.UpdatePlan(req.toPlan(code))
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, store.ErrPlanNotFound) {
				status = http.StatusNotFound
			}
			writeJSON(w, status, map[string]any{"success": false, "message": err.Error()})
			return
		}
		s.audit("plan_update", 0, "", `{"code":"`+p.Code+`"}`)
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "plan": p})
	case http.MethodDelete:
		if code == s.config.Settings().DefaultPlanCode {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "默认套餐不能删除，请先切换默认套餐"})
			return
		}
		if target, err := s.store.GetPlan(code); err == nil && target.Enabled {
			plans, _ := s.store.ListPlans()
			enabled := 0
			for _, item := range plans {
				if item.Enabled {
					enabled++
				}
			}
			if enabled <= 1 {
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "至少需要保留一个启用套餐"})
				return
			}
		}
		if err := s.store.DeletePlan(code); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, store.ErrPlanInUse) {
				status = http.StatusConflict
			} else if errors.Is(err, store.ErrPlanNotFound) {
				status = http.StatusNotFound
			}
			writeJSON(w, status, map[string]any{"success": false, "message": err.Error()})
			return
		}
		s.audit("plan_delete", 0, "", `{"code":"`+code+`"}`)
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
