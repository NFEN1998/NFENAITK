package store

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestAccountCreateBindUnbind(t *testing.T) {
	s := testStore(t)
	acc, user, err := s.CreateAccountWithNewUser("alice_test", "hash-placeholder")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if acc.UserID == nil || *acc.UserID != user.ID {
		t.Fatalf("account user id = %v, want %d", acc.UserID, user.ID)
	}
	if user.PlanType != "count" || user.TotalCount == nil || *user.TotalCount != 0 ||
		user.RemainCount == nil || *user.RemainCount != 0 || user.UsedCount != 0 {
		t.Fatalf("new user is not zero-quota: %+v", user)
	}
	if user.Token == "" {
		t.Fatalf("user token was not generated")
	}

	got, err := s.GetAccountByUsername("ALICE_TEST")
	if err != nil || got.ID != acc.ID {
		t.Fatalf("case-insensitive lookup: id=%d err=%v", got.ID, err)
	}
	if _, _, err := s.CreateAccountWithNewUser("Alice_Test", "hash-placeholder"); !errors.Is(err, ErrAccountExists) {
		t.Fatalf("duplicate username error = %v, want ErrAccountExists", err)
	}

	if err := s.UnbindAccount(acc.ID); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	got, err = s.GetAccountByID(acc.ID)
	if err != nil {
		t.Fatalf("load unbound: %v", err)
	}
	if got.UserID != nil || got.Status != "unbound" {
		t.Fatalf("unbound state: userID=%v status=%q", got.UserID, got.Status)
	}
	if err := s.BindAccount(acc.ID, user.ID); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	got, err = s.GetAccountByID(acc.ID)
	if err != nil {
		t.Fatalf("load rebound: %v", err)
	}
	if got.UserID == nil || *got.UserID != user.ID || got.Status != "enabled" {
		t.Fatalf("rebound state: userID=%v status=%q", got.UserID, got.Status)
	}
	if _, err := s.CreateAccountForUser("bob_test", "hash-placeholder", user.ID); !errors.Is(err, ErrAccountBound) {
		t.Fatalf("second account for same user = %v, want ErrAccountBound", err)
	}
	if err := s.DeleteUser(user.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if _, err := s.GetAccountByID(acc.ID); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("account after user deletion = %v, want ErrAccountNotFound", err)
	}
}

func TestSessionsCreateGetDelete(t *testing.T) {
	s := testStore(t)
	acc, _, err := s.CreateAccountWithNewUser("session_test", "hash-placeholder")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	plain, err := NewSessionToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	hash := HashSessionToken(plain)
	sess, err := s.CreateSession(acc.ID, hash, time.Hour, "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	got, err := s.GetSession(hash)
	if err != nil || got.ID != sess.ID || got.AccountID != acc.ID {
		t.Fatalf("get session: %+v err=%v", got, err)
	}
	if err := s.DeleteSession(hash); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := s.GetSession(hash); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("deleted session error = %v, want ErrSessionNotFound", err)
	}

	expired, _ := NewSessionToken()
	expiredHash := HashSessionToken(expired)
	sess, err = s.CreateSession(acc.ID, expiredHash, time.Hour, "", "")
	if err != nil {
		t.Fatalf("create expired session: %v", err)
	}
	if _, err := s.exec(`UPDATE AccountSessions SET ExpiresAt = $1 WHERE Id = $2`,
		time.Now().Add(-time.Hour).Format("2006-01-02 15:04:05"), sess.ID); err != nil {
		t.Fatalf("expire session: %v", err)
	}
	if _, err := s.GetSession(expiredHash); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expired session error = %v, want ErrSessionNotFound", err)
	}

	if err := s.DeleteAccountSessions(acc.ID); err != nil {
		t.Fatalf("delete account sessions: %v", err)
	}
}

func TestRedemptionCountAndDuration(t *testing.T) {
	s := testStore(t)
	_, user, err := s.CreateAccountWithNewUser("redeem_count", "hash-placeholder")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	batch, codes, err := s.CreateRedemptionBatch("count", "count", 5, 0, 1, "", "")
	if err != nil {
		t.Fatalf("create count batch: %v", err)
	}
	if len(codes) != 1 || batch.Kind != "count" {
		t.Fatalf("batch = %+v codes=%v", batch, codes)
	}
	result, err := s.RedeemCode(user.ID, HashRedemptionCode(codes[0]))
	if err != nil {
		t.Fatalf("redeem count: %v", err)
	}
	if result.User.PlanType != "count" || *result.User.TotalCount != 5 || *result.User.RemainCount != 5 {
		t.Fatalf("count result: %+v", result.User)
	}
	if _, err := s.RedeemCode(user.ID, HashRedemptionCode(codes[0])); !errors.Is(err, ErrCodeInvalid) {
		t.Fatalf("second redeem = %v, want ErrCodeInvalid", err)
	}

	_, durationUser, err := s.CreateAccountWithNewUser("redeem_duration", "hash-placeholder")
	if err != nil {
		t.Fatalf("create duration user: %v", err)
	}
	_, durationCodes, err := s.CreateRedemptionBatch("duration", "monthly", 0, 30, 1, "", "")
	if err != nil {
		t.Fatalf("create duration batch: %v", err)
	}
	result, err = s.RedeemCode(durationUser.ID, HashRedemptionCode(durationCodes[0]))
	if err != nil {
		t.Fatalf("redeem duration: %v", err)
	}
	if result.User.PlanType != "duration" || result.User.ExpireAt == nil {
		t.Fatalf("duration result: %+v", result.User)
	}
	if _, err := s.RedeemCode(user.ID, HashRedemptionCode(durationCodes[0])); !errors.Is(err, ErrCodeInvalid) {
		t.Fatalf("used duration code = %v, want ErrCodeInvalid", err)
	}

	_, mismatchCodes, err := s.CreateRedemptionBatch("duration", "monthly", 0, 30, 1, "", "")
	if err != nil {
		t.Fatalf("create mismatch batch: %v", err)
	}
	result, err = s.RedeemCode(user.ID, HashRedemptionCode(mismatchCodes[0]))
	if err != nil {
		t.Fatalf("duration card on count user: %v", err)
	}
	if result.User.PlanType != "duration" || result.User.ExpireAt == nil {
		t.Fatalf("duration priority result: %+v", result.User)
	}
	ents, err := s.GetEntitlements(user.ID)
	if err != nil {
		t.Fatalf("get entitlements: %v", err)
	}
	var countRemain int64
	for _, e := range ents {
		if e.Kind == "count" && e.RemainCount != nil {
			countRemain = *e.RemainCount
		}
	}
	if countRemain != 5 {
		t.Fatalf("count balance after duration redeem = %d, want 5", countRemain)
	}
}

func TestRedemptionConcurrentSingleUse(t *testing.T) {
	s := testStore(t)
	_, user, err := s.CreateAccountWithNewUser("redeem_race", "hash-placeholder")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, codes, err := s.CreateRedemptionBatch("count", "count", 10, 0, 1, "", "")
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	hash := HashRedemptionCode(codes[0])
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, results[idx] = s.RedeemCode(user.ID, hash)
		}(i)
	}
	wg.Wait()
	success := 0
	for _, err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrCodeInvalid) {
			t.Fatalf("unexpected redeem error: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("successful redemptions = %d, want 1", success)
	}
	updated, err := s.GetUserByID(user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if updated.RemainCount == nil || *updated.RemainCount != 10 {
		t.Fatalf("remain = %v, want 10", updated.RemainCount)
	}
}

func TestRedemptionRevoke(t *testing.T) {
	s := testStore(t)
	_, user, err := s.CreateAccountWithNewUser("redeem_revoke", "hash-placeholder")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	batch, codes, err := s.CreateRedemptionBatch("count", "count", 1, 0, 2, "", "")
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	n, err := s.RevokeRedemptionBatch(batch.ID)
	if err != nil || n != 2 {
		t.Fatalf("revoke = %d err=%v, want 2", n, err)
	}
	if _, err := s.RedeemCode(user.ID, HashRedemptionCode(codes[0])); !errors.Is(err, ErrCodeInvalid) {
		t.Fatalf("revoked code = %v, want ErrCodeInvalid", err)
	}
}
