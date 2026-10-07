package store

import (
	"testing"

	"github.com/nfentik/nfentik-go/internal/match"
)

func strp(v string) *string { return &v }

// TestInsertAIResponseDeduplicatesSameFolder verifies that storing the same
// question and options twice in one folder reuses the existing row instead of
// inserting a duplicate.
func TestInsertAIResponseDeduplicatesSameFolder(t *testing.T) {
	s := testStore(t)

	question := "去重测试题目 unique dedupe"
	options := strp("A. 甲 B. 乙 C. 丙 D. 丁")

	first, err := s.InsertAIResponse(question, "A", options, nil, 0)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	second, err := s.InsertAIResponse(question, "A", options, nil, 0)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if first != second {
		t.Fatalf("second insert returned %d, want reuse of %d", second, first)
	}

	var count int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM AIResponses WHERE QuestionHash = $1 AND FolderId = 0`,
		match.QuestionHash(question),
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count = %d, want 1", count)
	}
	_, _ = s.db.Exec(`DELETE FROM AIResponses WHERE QuestionHash = $1`, match.QuestionHash(question))
}

// TestInsertAIResponseAllowsDifferentOptions verifies that the same question
// with different options is stored as a distinct entry.
func TestInsertAIResponseAllowsDifferentOptions(t *testing.T) {
	s := testStore(t)

	question := "去重测试题目 不同选项"
	a, err := s.InsertAIResponse(question, "A", strp("A. 甲 B. 乙"), nil, 0)
	if err != nil {
		t.Fatalf("insert a: %v", err)
	}
	b, err := s.InsertAIResponse(question, "B", strp("A. 丙 B. 丁"), nil, 0)
	if err != nil {
		t.Fatalf("insert b: %v", err)
	}
	if a == b {
		t.Fatalf("different options reused the same row %d", a)
	}
	_, _ = s.db.Exec(`DELETE FROM AIResponses WHERE QuestionHash = $1`, match.QuestionHash(question))
}
