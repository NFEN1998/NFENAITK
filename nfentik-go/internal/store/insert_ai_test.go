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
	if err := s.queryRow(
		`SELECT COUNT(*) FROM AIResponses WHERE QuestionHash = $1 AND FolderId = 0`,
		match.QuestionHash(question),
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count = %d, want 1", count)
	}
	_, _ = s.exec(`DELETE FROM AIResponses WHERE QuestionHash = $1`, match.QuestionHash(question))
}

// TestAddQuestionPersistsOptionsAndHash locks in the admin "add question" path,
// which regressed once when an options-hash column was added and removed.
func TestAddQuestionPersistsOptionsAndHash(t *testing.T) {
	s := testStore(t)

	options := strp("A. 一 B. 二")
	created, err := s.AddQuestion("管理端新增题目 add-question", options, nil, nil, 0, false)
	if err != nil {
		t.Fatalf("add question: %v", err)
	}
	if created.ID == 0 {
		t.Fatalf("expected a persisted id")
	}
	if created.Options == nil || *created.Options != *options {
		t.Fatalf("options not persisted: %v", created.Options)
	}
	var hash string
	if err := s.queryRow(`SELECT QuestionHash FROM AIResponses WHERE Id = $1`, created.ID).Scan(&hash); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if hash == "" {
		t.Fatalf("question hash not persisted")
	}
	_, _ = s.exec(`DELETE FROM AIResponses WHERE Id = $1`, created.ID)
}

// TestQueryMatchesInsertedQuestion exercises the two-stage query path (exact
// hash probe plus fuzzy fallback) on the active dialect.
func TestQueryMatchesInsertedQuestion(t *testing.T) {
	s := testStore(t)

	question := "查询匹配测试题 unique query match"
	answer := "A"
	if _, err := s.AddQuestion(question, strp("A. 甲 B. 乙"), &answer, nil, 0, false); err != nil {
		t.Fatalf("add question: %v", err)
	}
	defer s.exec(`DELETE FROM AIResponses WHERE Question = $1`, question)

	hits, err := s.Query(question, nil, match.Score)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("expected a hit for the inserted question")
	}
	if hits[0].Answer != "A" {
		t.Fatalf("answer = %q, want A", hits[0].Answer)
	}
}

// TestInsertExternalResponseStoresSource verifies third-party answers are
// deduplicated and tagged with their source while staying out of the AI bucket.
func TestInsertExternalResponseStoresSource(t *testing.T) {
	s := testStore(t)

	question := "第三方题库来源测试 unique external source"
	options := strp("A. 甲 B. 乙")
	id, err := s.InsertExternalResponse(question, "甲", options, nil, 0, "external:demo")
	if err != nil {
		t.Fatalf("insert external: %v", err)
	}
	again, err := s.InsertExternalResponse(question, "甲", options, nil, 0, "external:demo")
	if err != nil {
		t.Fatalf("second insert external: %v", err)
	}
	if again != id {
		t.Fatalf("second insert returned %d, want reuse of %d", again, id)
	}

	var (
		source string
		isAI   bool
	)
	if err := s.queryRow(`SELECT Source, IsAi FROM AIResponses WHERE Id = $1`, id).Scan(&source, &isAI); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if source != "external:demo" {
		t.Fatalf("source = %q, want external:demo", source)
	}
	if isAI {
		t.Fatalf("external row must not be marked as ai")
	}
	_, _ = s.exec(`DELETE FROM AIResponses WHERE Id = $1`, id)
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
	_, _ = s.exec(`DELETE FROM AIResponses WHERE QuestionHash = $1`, match.QuestionHash(question))
}
