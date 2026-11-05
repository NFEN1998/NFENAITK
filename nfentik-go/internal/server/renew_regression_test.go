package server

import (
	"net/http"
	"testing"

	"github.com/nfentik/nfentik-go/internal/store"
)

func TestRenewCountWithExpiryAddsToExistingQuota(t *testing.T) {
	srv, ts := testServer(t)
	remain := int64(100)
	u, err := srv.store.CreateUser(store.User{PlanType: "count", PlanCode: "count", PlanLabel: "按次", TotalCount: &remain, RemainCount: &remain, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	code, payload := doJSON(t, http.MethodPost, ts.URL+"/api/admin/users/"+itoa(u.ID)+"/renew", map[string]any{
		"plan_code": "count", "total_count": 5, "enforce_expiry": true, "expiry_days": 30,
	}, nil)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, payload)
	}
	got, err := srv.store.GetUserByID(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalCount == nil || *got.TotalCount != 105 {
		t.Fatalf("total=%v", got.TotalCount)
	}
	if got.RemainCount == nil || *got.RemainCount != 105 {
		t.Fatalf("remain=%v", got.RemainCount)
	}
}

func TestBatchCreateCountWithExpiryUsesTotalCount(t *testing.T) {
	srv, ts := testServer(t)
	code, payload := doJSON(t, http.MethodPost, ts.URL+"/api/admin/users/batch", map[string]any{
		"count": 3, "plan_code": "count", "total_count": 100,
		"enforce_expiry": true, "expiry_days": 30,
	}, nil)
	if code != http.StatusOK {
		t.Fatalf("batch create = %d %v", code, payload)
	}
	tokens, _ := payload["tokens"].([]any)
	if len(tokens) != 3 {
		t.Fatalf("tokens = %v", payload["tokens"])
	}
	for _, raw := range tokens {
		token, _ := raw.(string)
		u, err := srv.store.GetUserByToken(token)
		if err != nil {
			t.Fatalf("load token: %v", err)
		}
		if u.TotalCount == nil || *u.TotalCount != 100 || u.RemainCount == nil || *u.RemainCount != 100 {
			t.Fatalf("token %s total=%v remain=%v, want 100/100", token, u.TotalCount, u.RemainCount)
		}
		if u.ExpireAt == nil {
			t.Fatalf("token %s has no expiry", token)
		}
	}
}
