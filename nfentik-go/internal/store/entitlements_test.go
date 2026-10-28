package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nfentik/nfentik-go/internal/plan"
)

func userEntitlement(t *testing.T, s *Store, userID int64, kind string) Entitlement {
	t.Helper()
	ents, err := s.GetEntitlements(userID)
	if err != nil {
		t.Fatalf("get entitlements: %v", err)
	}
	for _, e := range ents {
		if e.Kind == kind {
			return e
		}
	}
	t.Fatalf("missing %s entitlement in %+v", kind, ents)
	return Entitlement{}
}

func TestDurationAndCountStackTogether(t *testing.T) {
	s := testStore(t)
	exp := time.Now().AddDate(0, 0, 30).Format("2006-01-02 15:04:05")
	u, err := s.CreateUser(User{PlanType: "duration", PlanCode: "monthly", PlanLabel: "包月", ExpireAt: &exp, Enabled: true})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	_, codes, err := s.CreateRedemptionBatch("count", "count", 5, 0, 1, "", "")
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if _, err := s.RedeemCode(u.ID, HashRedemptionCode(codes[0])); err != nil {
		t.Fatalf("redeem count: %v", err)
	}
	count := userEntitlement(t, s, u.ID, "count")
	if count.RemainCount == nil || *count.RemainCount != 5 {
		t.Fatalf("count = %+v", count)
	}
	got, _ := s.GetUserByID(u.ID)
	if got.PlanType != "duration" {
		t.Fatalf("primary = %s, want duration", got.PlanType)
	}

	_, durCodes, err := s.CreateRedemptionBatch("duration", "monthly", 0, 0, 1, "", "")
	if err != nil {
		t.Fatalf("duration batch: %v", err)
	}
	if _, err := s.RedeemCode(u.ID, HashRedemptionCode(durCodes[0])); err != nil {
		t.Fatalf("redeem duration: %v", err)
	}
	duration := userEntitlement(t, s, u.ID, "duration")
	count = userEntitlement(t, s, u.ID, "count")
	if count.RemainCount == nil || *count.RemainCount != 5 {
		t.Fatalf("count changed after duration redeem: %+v", count)
	}
	if duration.ExpireAt == nil {
		t.Fatalf("duration has no expiry")
	}
}

func TestCountPlansStackAndDisabledCardsStillRedeem(t *testing.T) {
	s := testStore(t)
	u, err := s.CreateUser(User{PlanType: "count", PlanCode: "count", PlanLabel: "按次", TotalCount: int64ptr(0), RemainCount: int64ptr(0), Enabled: true})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	p := plan.Plan{Code: fmt.Sprintf("count_500_%d", time.Now().UnixNano()), Label: "500 次", Kind: plan.KindCount, QuotaCount: 500, Enabled: true, Sort: 100}
	if _, err := s.CreatePlan(p); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	t.Cleanup(func() { _ = s.DeletePlan(p.Code) })

	_, codes, err := s.CreateRedemptionBatch("count", p.Code, 0, 0, 2, "", "")
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if _, err := s.RedeemCode(u.ID, HashRedemptionCode(codes[0])); err != nil {
		t.Fatalf("redeem 1: %v", err)
	}
	if _, err := s.RedeemCode(u.ID, HashRedemptionCode(codes[1])); err != nil {
		t.Fatalf("redeem 2: %v", err)
	}
	count := userEntitlement(t, s, u.ID, "count")
	if count.TotalCount == nil || *count.TotalCount != 1000 || count.RemainCount == nil || *count.RemainCount != 1000 {
		t.Fatalf("stacked count = %+v", count)
	}

	p.Enabled = false
	if _, err := s.UpdatePlan(p); err != nil {
		t.Fatalf("disable plan: %v", err)
	}
	_, more, err := s.CreateRedemptionBatch("count", "count", 3, 0, 1, "", "")
	if err != nil {
		t.Fatalf("batch for redemption: %v", err)
	}
	if _, err := s.RedeemCode(u.ID, HashRedemptionCode(more[0])); err != nil {
		t.Fatalf("redeem after other plan disabled: %v", err)
	}
	if _, _, err := s.CreateRedemptionBatch("count", p.Code, 0, 0, 1, "", ""); err == nil {
		t.Fatalf("disabled plan generated a new batch")
	}
}

func TestUnlimitedDurationRejectsDurationCard(t *testing.T) {
	s := testStore(t)
	u, err := s.CreateUser(User{PlanType: "duration", PlanCode: "unlimited", PlanLabel: "无限", Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, codes, err := s.CreateRedemptionBatch("duration", "monthly", 0, 0, 1, "", "")
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if _, err := s.RedeemCode(u.ID, HashRedemptionCode(codes[0])); !errors.Is(err, ErrCodeUnlimited) {
		t.Fatalf("redeem = %v, want ErrCodeUnlimited", err)
	}
}

func TestConsumeCountEntitlement(t *testing.T) {
	s := testStore(t)
	u, err := s.CreateUser(User{PlanType: "count", PlanCode: "count", PlanLabel: "按次", TotalCount: int64ptr(2), RemainCount: int64ptr(2), Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 2; i++ {
		ok, err := s.ConsumeCountEntitlement(u.ID)
		if err != nil || !ok {
			t.Fatalf("consume %d: ok=%v err=%v", i, ok, err)
		}
	}
	ok, err := s.ConsumeCountEntitlement(u.ID)
	if err != nil || ok {
		t.Fatalf("over-consume: ok=%v err=%v", ok, err)
	}
	got, _ := s.GetUserByID(u.ID)
	if got.RemainCount == nil || *got.RemainCount != 0 || got.UsedCount != 2 {
		t.Fatalf("projection remain=%v used=%d", got.RemainCount, got.UsedCount)
	}
}
