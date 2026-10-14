package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/nfentik/nfentik-go/internal/store"
)

func TestUserLoginMeAndLogs(t *testing.T) {
	srv, ts := testServer(t)
	u := srv.createTestUser(t, "alice", "count", 5, 0)

	code, payload := doJSON(t, http.MethodPost, ts.URL+"/api/user/login", map[string]any{"token": u.Token}, nil)
	if code != http.StatusOK || payload["success"] != true {
		t.Fatalf("login: %d %v", code, payload)
	}

	code, payload = doJSON(t, http.MethodGet, ts.URL+"/api/user/me", nil, map[string]string{"Authorization": "Bearer " + u.Token})
	if code != http.StatusOK || payload["status"] != "active" {
		t.Fatalf("me: %d %v", code, payload)
	}

	code, payload = doJSON(t, http.MethodGet, ts.URL+"/api/user/logs", nil, map[string]string{"Authorization": "Bearer " + u.Token})
	if code != http.StatusOK {
		t.Fatalf("logs: %d %v", code, payload)
	}
}

func TestUserEndpointsRejectBadToken(t *testing.T) {
	_, ts := testServer(t)
	code, _ := doJSON(t, http.MethodGet, ts.URL+"/api/user/me", nil, map[string]string{"Authorization": "Bearer nope"})
	if code != http.StatusUnauthorized {
		t.Fatalf("bad token code = %d, want 401", code)
	}
	code, _ = doJSON(t, http.MethodGet, ts.URL+"/api/user/me", nil, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("missing token code = %d, want 401", code)
	}
}

func TestUserResetTokenInvalidatesOld(t *testing.T) {
	srv, ts := testServer(t)
	u := srv.createTestUser(t, "bob", "count", 3, 0)

	code, payload := doJSON(t, http.MethodPost, ts.URL+"/api/user/reset-token", nil, map[string]string{"Authorization": "Bearer " + u.Token})
	if code != http.StatusOK {
		t.Fatalf("reset: %d %v", code, payload)
	}
	newToken, _ := payload["token"].(string)
	if newToken == u.Token || newToken == "" {
		t.Fatalf("token unchanged")
	}
	code, _ = doJSON(t, http.MethodGet, ts.URL+"/api/user/me", nil, map[string]string{"Authorization": "Bearer " + u.Token})
	if code != http.StatusUnauthorized {
		t.Fatalf("old token still valid: %d", code)
	}
	code, _ = doJSON(t, http.MethodGet, ts.URL+"/api/user/me", nil, map[string]string{"Authorization": "Bearer " + newToken})
	if code != http.StatusOK {
		t.Fatalf("new token invalid: %d", code)
	}
}

func TestUserLogsAreIsolatedPerUser(t *testing.T) {
	srv, ts := testServer(t)
	a := srv.createTestUser(t, "carol", "count", 3, 0)
	b := srv.createTestUser(t, "dave", "count", 3, 0)
	_ = srv.store.InsertUserLog(store.UserRequestLog{UserID: a.ID, Timestamp: "2026-01-01 00:00:00", Question: "a", Source: "ai", Status: "ok"})
	_ = srv.store.InsertUserLog(store.UserRequestLog{UserID: b.ID, Timestamp: "2026-01-01 00:00:00", Question: "b", Source: "ai", Status: "ok"})

	_, payload := doJSON(t, http.MethodGet, ts.URL+"/api/user/logs", nil, map[string]string{"Authorization": "Bearer " + a.Token})
	items, _ := payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("user a saw %d logs, want 1", len(items))
	}
	first, _ := items[0].(map[string]any)
	if first["question"] != "a" {
		t.Fatalf("user a saw question %v", first["question"])
	}
}

func TestAdminUserCRUDAndStats(t *testing.T) {
	srv, ts := testServer(t)
	u := srv.createTestUser(t, "erin", "count", 10, 0)

	code, payload := doJSON(t, http.MethodGet, ts.URL+"/api/admin/users", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("list: %d %v", code, payload)
	}
	users, _ := payload["users"].([]any)
	if len(users) != 1 {
		t.Fatalf("users = %d, want 1", len(users))
	}

	code, payload = doJSON(t, http.MethodPut, ts.URL+"/api/admin/users/"+itoa(u.ID), map[string]any{"note": "vip"}, nil)
	if code != http.StatusOK {
		t.Fatalf("update: %d %v", code, payload)
	}

	code, payload = doJSON(t, http.MethodPost, ts.URL+"/api/admin/users/"+itoa(u.ID)+"/renew", map[string]any{"plan_code": "count", "total_count": 5}, nil)
	if code != http.StatusOK {
		t.Fatalf("renew: %d %v", code, payload)
	}
	got := payload["user"].(map[string]any)
	if got["remain_count"].(float64) != 15 {
		t.Fatalf("remain = %v, want 15", got["remain_count"])
	}

	code, payload = doJSON(t, http.MethodGet, ts.URL+"/api/admin/users/stats?range=14", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("stats: %d %v", code, payload)
	}

	code, payload = doJSON(t, http.MethodGet, ts.URL+"/api/admin/users/audits", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("audits: %d %v", code, payload)
	}
	audits, _ := payload["audits"].([]any)
	if len(audits) < 2 {
		t.Fatalf("audits = %d, want >=2", len(audits))
	}

	code, _ = doJSON(t, http.MethodDelete, ts.URL+"/api/admin/users/"+itoa(u.ID), nil, nil)
	if code != http.StatusOK {
		t.Fatalf("delete: %d", code)
	}
}

func TestAdminCreateUserValidation(t *testing.T) {
	srv, _ := testServer(t)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"no plan", map[string]any{"note": "x"}},
		{"bad plan", map[string]any{"plan_code": "nope"}},
		{"zero count", map[string]any{"plan_code": "count", "total_count": 0}},
		{"custom no days", map[string]any{"plan_code": "duration_custom", "days": 0}},
	}
	for _, tc := range cases {
		req := newJSONRequest(http.MethodPost, "/api/admin/users", tc.body)
		rec := newRecorder()
		srv.adminCreateUser(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: code = %d, want 400 (%s)", tc.name, rec.Code, rec.Body.String())
		}
	}
}

func TestAdminBatchCreateUsers(t *testing.T) {
	srv, _ := testServer(t)
	req := newJSONRequest(http.MethodPost, "/api/admin/users/batch",
		map[string]any{"count": 3, "plan_code": "count", "total_count": 5})
	rec := newRecorder()
	srv.adminBatchCreateUsers(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch code = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Count  int      `json:"count"`
		Tokens []string `json:"tokens"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Count != 3 || len(out.Tokens) != 3 {
		t.Fatalf("count=%d tokens=%d, want 3/3", out.Count, len(out.Tokens))
	}
	seen := map[string]bool{}
	for _, tk := range out.Tokens {
		if tk == "" || seen[tk] {
			t.Fatalf("token invalid or duplicate: %q", tk)
		}
		seen[tk] = true
	}
}

func TestAdminBatchCreateValidation(t *testing.T) {
	srv, _ := testServer(t)
	for _, body := range []map[string]any{
		{"count": 0, "plan_code": "count", "total_count": 5},
		{"count": 501, "plan_code": "count", "total_count": 5},
		{"count": 2, "plan_code": "nope"},
	} {
		req := newJSONRequest(http.MethodPost, "/api/admin/users/batch", body)
		rec := newRecorder()
		srv.adminBatchCreateUsers(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %v: code = %d, want 400 (%s)", body, rec.Code, rec.Body.String())
		}
	}
}
