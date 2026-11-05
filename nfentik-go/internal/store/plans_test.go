package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nfentik/nfentik-go/internal/plan"
)

func TestPlanCatalogueCRUD(t *testing.T) {
	s := testStore(t)
	plans, err := s.ListPlans()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(plans) == 0 {
		t.Fatalf("default plans were not seeded")
	}
	if _, err := s.GetPlan("monthly"); err != nil {
		t.Fatalf("default monthly plan missing: %v", err)
	}

	code := fmt.Sprintf("count_500_%d", time.Now().UnixNano())
	p := plan.Plan{
		Code: code, Label: "500 次", Kind: plan.KindCount,
		QuotaCount: 500, Enabled: true, Sort: 900, PriceCents: 9900, Currency: "CNY",
	}
	created, err := s.CreatePlan(p)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = s.DeletePlan(created.Code) })
	if created.QuotaCount != 500 || !created.Enabled {
		t.Fatalf("created = %+v", created)
	}
	if _, err := s.CreatePlan(p); !errors.Is(err, ErrPlanExists) {
		t.Fatalf("duplicate error = %v, want ErrPlanExists", err)
	}

	created.Label = "500 次套餐"
	created.QuotaCount = 600
	created.Enabled = false
	updated, err := s.UpdatePlan(created)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Label != "500 次套餐" || updated.QuotaCount != 600 || updated.Enabled {
		t.Fatalf("updated = %+v", updated)
	}

	views, err := s.ListPlansWithUsage()
	if err != nil {
		t.Fatalf("list usage: %v", err)
	}
	found := false
	for _, view := range views {
		if view.Code == created.Code {
			found = true
			if view.UserCount != 0 || view.BatchCount != 0 {
				t.Fatalf("usage = %+v", view)
			}
		}
	}
	if !found {
		t.Fatalf("created plan missing from usage list")
	}

	if err := s.DeletePlan(created.Code); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetPlan(created.Code); !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("get after delete = %v", err)
	}
}

func TestPlanValidationAndKindImmutable(t *testing.T) {
	s := testStore(t)
	bad := plan.Plan{Code: "Bad Code", Label: "x", Kind: plan.KindDuration, Days: 30, Enabled: true}
	if _, err := s.CreatePlan(bad); err == nil {
		t.Fatalf("invalid code accepted")
	}
	p := plan.Plan{Code: fmt.Sprintf("immutable_%d", time.Now().UnixNano()), Label: "测试", Kind: plan.KindDuration, Days: 30, Enabled: true}
	if _, err := s.CreatePlan(p); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = s.DeletePlan(p.Code) })
	p.Kind = plan.KindCount
	p.QuotaCount = 100
	if _, err := s.UpdatePlan(p); err == nil {
		t.Fatalf("kind change accepted")
	}
}
