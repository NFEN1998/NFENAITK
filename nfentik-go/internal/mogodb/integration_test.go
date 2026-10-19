package mogodb

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMOGOIntegration is an opt-in integration check against a real MOGO
// database. It is skipped unless TEST_MOGO_HOST is set.
func TestMOGOIntegration(t *testing.T) {
	host := os.Getenv("TEST_MOGO_HOST")
	if host == "" {
		t.Skip("TEST_MOGO_HOST not set")
	}
	table := os.Getenv("TEST_MOGO_TABLE")
	if table == "" {
		table = "data"
	}
	cfg := Config{
		Host:     host,
		Port:     os.Getenv("TEST_MOGO_PORT"),
		User:     os.Getenv("TEST_MOGO_USER"),
		Password: os.Getenv("TEST_MOGO_PASS"),
		Database: os.Getenv("TEST_MOGO_DB"),
		Table:    table,
		Columns:  DefaultColumnMap(),
	}
	reader, err := Open(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reader.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cols, err := reader.Columns(ctx)
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	have := map[string]bool{}
	for _, c := range cols {
		have[strings.ToLower(c)] = true
	}
	for _, want := range []string{"id", "question", "answer", "opt", "questiontype"} {
		if !have[want] {
			t.Fatalf("column %q missing; have %v", want, cols)
		}
	}

	count, err := reader.Count(ctx, 0)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count <= 0 {
		t.Fatalf("count = %d", count)
	}

	rows, last, err := reader.Fetch(ctx, 0, 3)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if last < rows[len(rows)-1].ID {
		t.Fatalf("last = %d, rows = %+v", last, rows)
	}
	for _, row := range rows {
		if row.ID == 0 || strings.TrimSpace(row.Question) == "" || strings.TrimSpace(row.Answer) == "" {
			t.Fatalf("incomplete row: %+v", row)
		}
	}
	t.Logf("count=%d firstID=%d lastID=%d columns=%d", count, rows[0].ID, last, len(cols))
}
