package server

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/nfentik/nfentik-go/internal/store"
)

func uniquePlanCode(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func TestAdminPlanCRUD(t *testing.T) {
	srv, ts := testServer(t)
	planCode := uniquePlanCode("count_100")
	status, payload := doJSON(t, http.MethodPost, ts.URL+"/api/admin/plans", map[string]any{
		"code": planCode, "label": "100 次", "kind": "count", "quota_count": 100,
		"enabled": true, "sort": 500, "price_cents": 990,
	}, nil)
	if status != http.StatusOK || payload["success"] != true {
		t.Fatalf("create plan = %d %v", status, payload)
	}
	planObj, _ := payload["plan"].(map[string]any)
	if planObj["QuotaCount"] != float64(100) {
		t.Fatalf("plan = %v", planObj)
	}
	createdCode, _ := planObj["Code"].(string)
	t.Cleanup(func() { _ = srv.store.DeletePlan(createdCode) })

	status, list := doJSON(t, http.MethodGet, ts.URL+"/api/admin/plans", nil, nil)
	if status != http.StatusOK || list["success"] != true {
		t.Fatalf("list plans = %d %v", status, list)
	}

	status, updated := doJSON(t, http.MethodPut, ts.URL+"/api/admin/plans/"+createdCode, map[string]any{
		"label": "120 次", "kind": "count", "quota_count": 120,
		"enabled": true, "sort": 510,
	}, nil)
	if status != http.StatusOK || updated["success"] != true {
		t.Fatalf("update plan = %d %v", status, updated)
	}

	status, deleted := doJSON(t, http.MethodDelete, ts.URL+"/api/admin/plans/"+createdCode, nil, nil)
	if status != http.StatusOK || deleted["success"] != true {
		t.Fatalf("delete plan = %d %v", status, deleted)
	}
}

func TestAdminCreateFixedCountUser(t *testing.T) {
	srv, ts := testServer(t)
	code := uniquePlanCode("count_fixed")
	_, payload := doJSON(t, http.MethodPost, ts.URL+"/api/admin/plans", map[string]any{
		"code": code, "label": "固定 500 次", "kind": "count", "quota_count": 500,
		"enabled": true,
	}, nil)
	if payload["success"] != true {
		t.Fatalf("create plan = %v", payload)
	}
	t.Cleanup(func() { _ = srv.store.DeletePlan(code) })

	status, created := doJSON(t, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"plan_code": code,
	}, nil)
	if status != http.StatusOK || created["success"] != true {
		t.Fatalf("create user = %d %v", status, created)
	}
	user, _ := created["user"].(map[string]any)
	if user["total_count"] != float64(500) || user["remain_count"] != float64(500) {
		t.Fatalf("user = %v", user)
	}
}

func TestQueryPrefersDurationThenCount(t *testing.T) {
	srv, _ := testServer(t)
	srv.setTokenForQuery(t, true)
	title := uniqueQuestion("双权益查询")
	if _, err := srv.store.InsertAIResponse(title, "答案", nil, nil); err != nil {
		t.Fatalf("seed question: %v", err)
	}

	exp := time.Now().AddDate(0, 0, 30).Format("2006-01-02 15:04:05")
	u, err := srv.store.CreateUser(store.User{
		PlanType: "duration", PlanCode: "monthly", PlanLabel: "包月",
		ExpireAt: &exp, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, codes, err := srv.store.CreateRedemptionBatch("count", "count", 5, 0, 1, "", "")
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if _, err := srv.store.RedeemCode(u.ID, store.HashRedemptionCode(codes[0])); err != nil {
		t.Fatalf("redeem: %v", err)
	}

	if code, payload := srv.queryWithToken(t, title, u.Token); code != http.StatusOK {
		t.Fatalf("duration query = %d %v", code, payload)
	}
	ents, err := srv.store.GetEntitlements(u.ID)
	if err != nil {
		t.Fatalf("entitlements: %v", err)
	}
	countRemain := int64(-1)
	for _, e := range ents {
		if e.Kind == "count" && e.RemainCount != nil {
			countRemain = *e.RemainCount
		}
	}
	if countRemain != 5 {
		t.Fatalf("count consumed while duration active: %d", countRemain)
	}

	past := time.Now().Add(-time.Hour).Format("2006-01-02 15:04:05")
	if err := srv.store.SetExpiry(u.ID, &past); err != nil {
		t.Fatalf("expire duration: %v", err)
	}
	if code, payload := srv.queryWithToken(t, title, u.Token); code != http.StatusOK {
		t.Fatalf("count query = %d %v", code, payload)
	}
	ents, err = srv.store.GetEntitlements(u.ID)
	if err != nil {
		t.Fatalf("entitlements: %v", err)
	}
	for _, e := range ents {
		if e.Kind == "count" && (e.RemainCount == nil || *e.RemainCount != 4) {
			t.Fatalf("count after duration expiry = %+v", e)
		}
	}
}
