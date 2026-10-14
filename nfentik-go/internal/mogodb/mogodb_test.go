package mogodb

import (
	"context"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestQuoteIdentifier(t *testing.T) {
	for _, ok := range []string{"question", "q_1", "_x", "aBc123"} {
		got, err := QuoteIdentifier(ok)
		if err != nil || got != "`"+ok+"`" {
			t.Fatalf("QuoteIdentifier(%q) = %q, %v", ok, got, err)
		}
	}
	for _, bad := range []string{"", "1a", "a-b", "a b", "a;b", "a`b", "题目"} {
		if _, err := QuoteIdentifier(bad); err == nil {
			t.Fatalf("QuoteIdentifier(%q) accepted an unsafe identifier", bad)
		}
	}
}

func TestDSNCarriesConnectionFields(t *testing.T) {
	cfg := Config{Host: "8.134.186.10", Port: "3307", User: "readonly", Password: "p@ss:w/rd", Database: "mogo"}
	parsed, err := mysql.ParseDSN(cfg.DSN())
	if err != nil {
		t.Fatalf("ParseDSN: %v", err)
	}
	if parsed.User != "readonly" || parsed.Addr != "8.134.186.10:3307" || parsed.DBName != "mogo" || parsed.Passwd != "p@ss:w/rd" {
		t.Fatalf("DSN round trip mismatch: %+v", parsed)
	}
	if parsed.Params["charset"] != "utf8mb4" {
		t.Fatalf("charset param missing: %v", parsed.Params)
	}
}

func TestColumnMapDefaults(t *testing.T) {
	cm := Config{Columns: ColumnMap{Question: "stem"}}.EffectiveColumns()
	if cm.Question != "stem" {
		t.Fatalf("explicit mapping lost: %q", cm.Question)
	}
	if cm.ID != "id" || cm.Answer != "answer" || cm.Options != "opt" || cm.QuestionType != "questionType" {
		t.Fatalf("defaults not applied: %+v", cm)
	}
}

func TestLimiterRateBounds(t *testing.T) {
	ctx := context.Background()
	lim := NewLimiter(1000, 0)
	t0 := time.Now()
	if err := lim.Wait(ctx, t0, 100); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if elapsed := time.Since(t0); elapsed < 80*time.Millisecond {
		t.Fatalf("100 rows at 1000/s waited %v, want >= ~100ms", elapsed)
	}
}

func TestLimiterDisabled(t *testing.T) {
	ctx := context.Background()
	lim := NewLimiter(0, 0)
	t0 := time.Now()
	if err := lim.Wait(ctx, t0, 100000); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if elapsed := time.Since(t0); elapsed > 20*time.Millisecond {
		t.Fatalf("disabled limiter waited %v", elapsed)
	}
}

func TestLimiterCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	lim := NewLimiter(1, 0) // one row per second
	cancel()
	if err := lim.Wait(ctx, time.Now(), 100); err == nil {
		t.Fatalf("expected the cancelled context to abort the wait")
	}
}
