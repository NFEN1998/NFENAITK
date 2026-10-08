package store

import (
	"os"
	"testing"
	"time"

	"github.com/nfentik/nfentik-go/internal/plan"
)

// testStore connects to PostgreSQL for storage tests. It skips when no database
// is reachable so the unit suite still passes in environments without one.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://zerror:zerror@127.0.0.1:5432/zerror?sslmode=disable"
	}
	s, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	t.Cleanup(func() {
		s.db.Exec(`DELETE FROM UserRequestLogs`)
		s.db.Exec(`DELETE FROM UserAuditLogs`)
		s.db.Exec(`DELETE FROM Users`)
		s.Close()
	})
	return s
}

func strptr(v string) *string { return &v }
func boolptr(v bool) *bool    { return &v }

func TestCreateUserRejectsDuplicate(t *testing.T) {
	s := testStore(t)
	remain := int64(10)
	base := User{Token: "nf_dup_token", PlanType: "count", PlanCode: "count", PlanLabel: "按次", RemainCount: &remain, TotalCount: &remain, Enabled: true}
	if _, err := s.CreateUser(base); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.CreateUser(base); err != ErrUserExists {
		t.Fatalf("duplicate err = %v, want ErrUserExists", err)
	}
}

func TestGetUserByTokenAndReset(t *testing.T) {
	s := testStore(t)
	u, err := s.CreateUser(User{PlanType: "duration", PlanCode: "unlimited", PlanLabel: "无限", Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetUserByToken(u.Token)
	if err != nil || got.ID != u.ID {
		t.Fatalf("get by token: %v %+v", err, got)
	}
	newToken, err := s.ResetUserToken(u.ID)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if newToken == u.Token {
		t.Fatalf("token not changed")
	}
	if _, err := s.GetUserByToken(u.Token); err != ErrUserNotFound {
		t.Fatalf("old token still valid: %v", err)
	}
	if _, err := s.GetUserByToken(newToken); err != nil {
		t.Fatalf("new token invalid: %v", err)
	}
}

func TestResetUserTokenUnknown(t *testing.T) {
	s := testStore(t)
	if _, err := s.ResetUserToken(999999); err != ErrUserNotFound {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
}

func TestConsumeQuota(t *testing.T) {
	s := testStore(t)
	remain := int64(2)
	u, err := s.CreateUser(User{PlanType: "count", PlanCode: "count", PlanLabel: "按次", RemainCount: &remain, TotalCount: &remain, Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 2; i++ {
		ok, err := s.ConsumeQuota(u.ID)
		if err != nil || !ok {
			t.Fatalf("consume %d: ok=%v err=%v", i, ok, err)
		}
	}
	ok, err := s.ConsumeQuota(u.ID)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if ok {
		t.Fatalf("consume past zero succeeded")
	}
	got, _ := s.GetUserByID(u.ID)
	if *got.RemainCount != 0 || got.UsedCount != 2 {
		t.Fatalf("remain=%d used=%d", *got.RemainCount, got.UsedCount)
	}
}

func TestConsumeQuotaDurationPlan(t *testing.T) {
	s := testStore(t)
	exp := time.Now().AddDate(0, 0, 30).Format("2006-01-02 15:04:05")
	u, err := s.CreateUser(User{PlanType: "duration", PlanCode: "monthly", PlanLabel: "包月", ExpireAt: &exp, Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	ok, err := s.ConsumeQuota(u.ID)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if ok {
		t.Fatalf("duration plan consumed quota")
	}
}

func TestAddCountTopUp(t *testing.T) {
	s := testStore(t)
	remain := int64(5)
	u, _ := s.CreateUser(User{PlanType: "count", PlanCode: "count", PlanLabel: "按次", RemainCount: &remain, TotalCount: &remain, Enabled: true})
	if err := s.AddCountTopUp(u.ID, 3); err != nil {
		t.Fatalf("topup: %v", err)
	}
	got, _ := s.GetUserByID(u.ID)
	if *got.RemainCount != 8 || *got.TotalCount != 8 {
		t.Fatalf("remain=%d total=%d", *got.RemainCount, *got.TotalCount)
	}
}

func TestUpdateUserNoteAndBool(t *testing.T) {
	s := testStore(t)
	u, _ := s.CreateUser(User{PlanType: "duration", PlanCode: "unlimited", PlanLabel: "无限", Enabled: true})
	if err := s.UpdateUser(u.ID, strptr("重点客户"), boolptr(false)); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := s.GetUserByID(u.ID)
	if got.Note != "重点客户" || got.Enabled {
		t.Fatalf("note=%q enabled=%v", got.Note, got.Enabled)
	}
	long := make([]rune, 201)
	for i := range long {
		long[i] = 'x'
	}
	if err := s.UpdateUser(u.ID, strptr(string(long)), nil); err == nil {
		t.Fatalf("expected note too long error")
	}
}

func TestUserLogsAndPrune(t *testing.T) {
	s := testStore(t)
	u, _ := s.CreateUser(User{PlanType: "duration", PlanCode: "unlimited", PlanLabel: "无限", Enabled: true})
	for i := 0; i < 5; i++ {
		if err := s.InsertUserLog(UserRequestLog{UserID: u.ID, Timestamp: time.Now().Format("2006-01-02 15:04:05"), Question: "q", Source: "ai", Status: "ok"}); err != nil {
			t.Fatalf("insert log: %v", err)
		}
	}
	logs, total, err := s.UserLogs(u.ID, 1, 10)
	if err != nil || total != 5 || len(logs) != 5 {
		t.Fatalf("total=%d len=%d err=%v", total, len(logs), err)
	}
	if err := s.PruneUserLogs(3, 30, 1000); err != nil {
		t.Fatalf("prune: %v", err)
	}
	_, total, _ = s.UserLogs(u.ID, 1, 10)
	if total != 3 {
		t.Fatalf("after prune total=%d, want 3", total)
	}
}

func TestAuditInsertListPrune(t *testing.T) {
	s := testStore(t)
	if err := s.InsertAudit("create", 1, "alice", "{}", "admin"); err != nil {
		t.Fatalf("insert audit: %v", err)
	}
	audits, total, err := s.ListAudits(1, 10)
	if err != nil || total != 1 || len(audits) != 1 {
		t.Fatalf("total=%d err=%v", total, err)
	}
	if audits[0].Action != "create" || audits[0].Actor != "admin" {
		t.Fatalf("audit = %+v", audits[0])
	}
	if err := s.PruneAudits(0); err != nil {
		t.Fatalf("prune audits noop: %v", err)
	}
}

func TestRenewCountPlan(t *testing.T) {
	s := testStore(t)
	remain := int64(5)
	u, _ := s.CreateUser(User{PlanType: "count", PlanCode: "count", PlanLabel: "按次", RemainCount: &remain, TotalCount: &remain, Enabled: true})
	p, _ := plan.Lookup("count")
	got, err := s.RenewUser(u.ID, p, 0, 10, false, 0)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if *got.RemainCount != 15 || *got.TotalCount != 15 {
		t.Fatalf("remain=%d total=%d", *got.RemainCount, *got.TotalCount)
	}
}

func TestRenewDurationExtendsFromExpiry(t *testing.T) {
	s := testStore(t)
	exp := time.Now().AddDate(0, 0, 10).Format("2006-01-02 15:04:05")
	u, _ := s.CreateUser(User{PlanType: "duration", PlanCode: "monthly", PlanLabel: "包月", ExpireAt: &exp, Enabled: true})
	p, _ := plan.Lookup("monthly")
	got, err := s.RenewUser(u.ID, p, 0, 0, false, 0)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	want, _ := time.Parse("2006-01-02 15:04:05", exp)
	want = want.AddDate(0, 0, 30)
	if got.ExpireAt == nil {
		t.Fatalf("expire nil")
	}
	if parsed, _ := time.Parse("2006-01-02 15:04:05", *got.ExpireAt); !parsed.Equal(want) {
		t.Fatalf("expire=%s want=%s", *got.ExpireAt, want)
	}
}

func TestSwitchPlanDropsOldQuota(t *testing.T) {
	s := testStore(t)
	remain := int64(50)
	u, _ := s.CreateUser(User{PlanType: "count", PlanCode: "count", PlanLabel: "按次", RemainCount: &remain, TotalCount: &remain, Enabled: true})
	_, _ = s.ConsumeQuota(u.ID)
	p, _ := plan.Lookup("yearly")
	got, err := s.SwitchPlan(u.ID, p, 0, 0, false, 0)
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	if got.PlanType != "duration" || got.TotalCount != nil || got.UsedCount != 0 {
		t.Fatalf("plan=%s total=%v used=%d", got.PlanType, got.TotalCount, got.UsedCount)
	}
	if got.ExpireAt == nil {
		t.Fatalf("expire nil")
	}
}

func TestUsageReport(t *testing.T) {
	s := testStore(t)
	remain := int64(10)
	u, _ := s.CreateUser(User{PlanType: "count", PlanCode: "count", PlanLabel: "按次", RemainCount: &remain, TotalCount: &remain, Enabled: true})
	_ = s.InsertUserLog(UserRequestLog{UserID: u.ID, Timestamp: time.Now().Format("2006-01-02 15:04:05"), Source: "bank", Status: "ok"})
	_ = s.InsertUserLog(UserRequestLog{UserID: u.ID, Timestamp: time.Now().Format("2006-01-02 15:04:05"), Source: "ai", Status: "ok"})
	rep, err := s.UsageReport(14)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(rep.Users) != 1 || rep.Users[0].TotalCalls != 2 {
		t.Fatalf("users = %+v", rep.Users)
	}
	if rep.Summary.TotalCalls != 2 {
		t.Fatalf("summary total = %d", rep.Summary.TotalCalls)
	}
	if len(rep.Daily) != 1 || rep.Daily[0].Count != 2 {
		t.Fatalf("daily = %+v", rep.Daily)
	}
}
