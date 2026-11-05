package plan

import (
	"testing"
	"time"
)

func TestExpireAtFixedPlans(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := map[string]int{
		"monthly":   30,
		"quarterly": 90,
		"half_year": 180,
		"yearly":    365,
	}
	for code, days := range cases {
		p, ok := Lookup(code)
		if !ok {
			t.Fatalf("plan %s missing", code)
		}
		got := ExpireAt(start, p, 0)
		if got == nil {
			t.Fatalf("plan %s expire is nil", code)
		}
		want := start.AddDate(0, 0, days)
		if !got.Equal(want) {
			t.Errorf("plan %s expire = %v, want %v", code, got, want)
		}
	}
}

func TestExpireAtUnlimited(t *testing.T) {
	p, _ := Lookup("unlimited")
	if got := ExpireAt(time.Now(), p, 0); got != nil {
		t.Errorf("unlimited expire = %v, want nil", got)
	}
}

func TestExpireAtCustomDays(t *testing.T) {
	p, _ := Lookup("duration_custom")
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := ExpireAt(start, p, 10); got == nil || !got.Equal(start.AddDate(0, 0, 10)) {
		t.Errorf("custom 10 days = %v", got)
	}
	if got := ExpireAt(start, p, 0); got != nil {
		t.Errorf("custom 0 days = %v, want nil", got)
	}
	if got := ExpireAt(start, p, -5); got != nil {
		t.Errorf("custom -5 days = %v, want nil", got)
	}
}

func TestStatusDuration(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	future := now.Add(30 * 24 * time.Hour)
	soon := now.Add(3 * 24 * time.Hour)
	past := now.Add(-1 * time.Hour)

	if got := Status(StatusInput{Enabled: true, ExpireAt: &future}, now); got != StatusActive {
		t.Errorf("future = %s, want active", got)
	}
	if got := Status(StatusInput{Enabled: true, ExpireAt: &soon}, now); got != StatusExpiring {
		t.Errorf("soon = %s, want expiring", got)
	}
	if got := Status(StatusInput{Enabled: true, ExpireAt: &past}, now); got != StatusExpired {
		t.Errorf("past = %s, want expired", got)
	}
	if got := Status(StatusInput{Enabled: false, ExpireAt: &future}, now); got != StatusDisabled {
		t.Errorf("disabled = %s, want disabled", got)
	}
	if got := Status(StatusInput{Enabled: true, Unlimited: true}, now); got != StatusActive {
		t.Errorf("unlimited = %s, want active", got)
	}
}

func TestStatusCount(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	remain, total := int64(5), int64(100)
	if got := Status(StatusInput{Enabled: true, RemainCount: &remain, TotalCount: &total}, now); got != StatusActive {
		t.Errorf("count active = %s", got)
	}
	zero := int64(0)
	if got := Status(StatusInput{Enabled: true, RemainCount: &zero, TotalCount: &total}, now); got != StatusExhausted {
		t.Errorf("count exhausted = %s", got)
	}
	// count plan with optional expiry
	future := now.Add(3 * 24 * time.Hour)
	if got := Status(StatusInput{Enabled: true, RemainCount: &remain, TotalCount: &total, ExpireAt: &future}, now); got != StatusExpiring {
		t.Errorf("count expiring = %s", got)
	}
	past := now.Add(-time.Hour)
	if got := Status(StatusInput{Enabled: true, RemainCount: &remain, TotalCount: &total, ExpireAt: &past}, now); got != StatusExpired {
		t.Errorf("count expired = %s", got)
	}
}
