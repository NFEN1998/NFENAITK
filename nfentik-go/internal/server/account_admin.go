package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/nfentik/nfentik-go/internal/store"
)

// adminAccounts routes /api/admin/accounts/*.
func (s *Server) adminAccounts(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		page := int(parseIntDefault(r.URL.Query().Get("page"), 1))
		pageSize := int(parseIntDefault(r.URL.Query().Get("page_size"), 50))
		items, total, err := s.store.ListAccounts(r.URL.Query().Get("q"), page, pageSize)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "accounts": items, "total": total, "page": page, "page_size": pageSize,
		})
		return
	}
	id, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	acc, err := s.store.GetAccountByID(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "账号不存在"})
		return
	}
	if len(rest) == 1 && r.Method == http.MethodDelete {
		if err := s.store.DeleteAccountSessions(id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		if err := s.store.DeleteAccount(id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		s.audit("account_delete", accountUserID(acc), "", `{}`)
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}
	action := ""
	if len(rest) > 1 {
		action = rest[1]
	}
	switch action {
	case "reset-password":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Password string `json:"password"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请求体解析失败"})
			return
		}
		if err := validatePassword(req.Password); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
			return
		}
		hash, err := hashPassword(req.Password)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "密码处理失败"})
			return
		}
		if err := s.store.SetAccountPassword(id, hash); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		_ = s.store.DeleteAccountSessions(id)
		s.audit("account_reset_password", accountUserID(acc), "", `{}`)
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	case "enable", "disable":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		status := "enabled"
		if action == "disable" {
			status = "disabled"
		}
		if err := s.store.SetAccountStatus(id, status); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		if status == "disabled" {
			_ = s.store.DeleteAccountSessions(id)
		}
		s.audit("account_"+action, accountUserID(acc), "", `{}`)
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	case "unbind":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.store.UnbindAccount(id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		_ = s.store.DeleteAccountSessions(id)
		s.audit("account_unbind", accountUserID(acc), "", `{}`)
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	case "bind":
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
		u, err := s.store.GetUserByToken(strings.TrimSpace(req.Token))
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "令牌无效"})
			return
		}
		if bound, err := s.store.GetAccountByUserID(u.ID); err == nil && bound.ID != id {
			writeJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "该令牌已绑定其他账号"})
			return
		}
		if err := s.store.BindAccount(id, u.ID); err != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"success": false, "message": err.Error()})
			return
		}
		_ = s.store.DeleteAccountSessions(id)
		s.audit("account_bind", u.ID, u.Token, `{}`)
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	case "revoke-sessions":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.store.DeleteAccountSessions(id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		s.audit("account_revoke_sessions", accountUserID(acc), "", `{}`)
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	default:
		http.NotFound(w, r)
	}
}

func accountUserID(acc store.Account) int64 {
	if acc.UserID == nil {
		return 0
	}
	return *acc.UserID
}

// adminRedeem routes /api/admin/redeem/*.
func (s *Server) adminRedeem(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		http.NotFound(w, r)
		return
	}
	switch rest[0] {
	case "batches":
		if len(rest) == 1 {
			switch r.Method {
			case http.MethodGet:
				page := int(parseIntDefault(r.URL.Query().Get("page"), 1))
				pageSize := int(parseIntDefault(r.URL.Query().Get("page_size"), 50))
				items, total, err := s.store.ListRedemptionBatches(page, pageSize)
				if err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"success": true, "batches": items, "total": total, "page": page, "page_size": pageSize,
				})
			case http.MethodPost:
				s.adminCreateRedemptionBatch(w, r)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		if len(rest) >= 3 && rest[2] == "revoke" && r.Method == http.MethodPost {
			id, err := strconv.ParseInt(rest[1], 10, 64)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			n, err := s.store.RevokeRedemptionBatch(id)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
				return
			}
			s.audit("redeem_batch_revoke", 0, "", `{"batch_id":`+strconv.FormatInt(id, 10)+`}`)
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "revoked": n})
			return
		}
	}
	http.NotFound(w, r)
}

func (s *Server) adminCreateRedemptionBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind      string `json:"kind"`
		PlanCode  string `json:"plan_code"`
		Amount    int64  `json:"amount"`
		Days      int    `json:"days"`
		Quantity  int    `json:"quantity"`
		ExpiresAt string `json:"expires_at"`
		Note      string `json:"note"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请求体解析失败"})
		return
	}
	batch, codes, err := s.store.CreateRedemptionBatch(req.Kind, req.PlanCode, req.Amount, req.Days, req.Quantity, req.ExpiresAt, req.Note)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	detail, _ := json.Marshal(map[string]any{
		"batch_id": batch.ID, "batch_no": batch.BatchNo, "quantity": batch.Quantity,
	})
	s.audit("redeem_batch_create", 0, "", string(detail))
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "batch": batch, "codes": codes})
}
