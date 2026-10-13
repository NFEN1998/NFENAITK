package store

import (
	"testing"

	"github.com/nfentik/nfentik-go/internal/match"
)

func strp(v string) *string { return &v }

// TestInsertAIResponseDeduplicates verifies that storing the same question
// and options twice reuses the existing row instead of inserting a duplicate.
func TestInsertAIResponseDeduplicates(t *testing.T) {
	s := testStore(t)

	question := "去重测试题目 unique dedupe"
	options := strp("A. 甲 B. 乙 C. 丙 D. 丁")

	first, err := s.InsertAIResponse(question, "A", options, nil)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	second, err := s.InsertAIResponse(question, "A", options, nil)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if first != second {
		t.Fatalf("second insert returned %d, want reuse of %d", second, first)
	}

	var count int
	if err := s.queryRow(
		`SELECT COUNT(*) FROM AIResponses WHERE QuestionHash = $1`,
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
	created, err := s.AddQuestion("管理端新增题目 add-question", options, nil, nil, false)
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
	if _, err := s.AddQuestion(question, strp("A. 甲 B. 乙"), &answer, nil, false); err != nil {
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
	id, err := s.InsertExternalResponse(question, "甲", options, nil, "external:demo")
	if err != nil {
		t.Fatalf("insert external: %v", err)
	}
	again, err := s.InsertExternalResponse(question, "甲", options, nil, "external:demo")
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

// TestImportQuestionDeduplicates ensures a repeated import row is skipped and
// the imported row carries the import source tag.
func TestImportQuestionDeduplicates(t *testing.T) {
	s := testStore(t)
	question := "导入去重测试 unique import dedupe"

	created, err := s.ImportQuestion(question, "A\nB", "A", "单选题", "import:mogo")
	if err != nil || !created {
		t.Fatalf("first import: created=%v err=%v", created, err)
	}
	again, err := s.ImportQuestion(question, "A\nB", "A", "单选题", "import:mogo")
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if again {
		t.Fatalf("second import should have been skipped")
	}

	var source string
	if err := s.queryRow(`SELECT Source FROM AIResponses WHERE Question = $1`, question).Scan(&source); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if source != "import:mogo" {
		t.Fatalf("source = %q, want import:mogo", source)
	}
	_, _ = s.exec(`DELETE FROM AIResponses WHERE Question = $1`, question)
}

// TestRejectsEmptyQuestion ensures a blank question is never persisted, which
// would otherwise let a third-party bank plant an answer with no question.
func TestRejectsEmptyQuestion(t *testing.T) {
	s := testStore(t)
	if _, err := s.AddQuestion("   ", nil, strp("a"), nil, false); err == nil {
		t.Fatalf("AddQuestion accepted an empty question")
	}
	if _, err := s.InsertExternalResponse("  ", "a", nil, nil, "external:x"); err == nil {
		t.Fatalf("InsertExternalResponse accepted an empty question")
	}
}

// TestQueryDistinguishesOperators guards the fix for the bug where "3+3=" and
// "3×3=" collapsed onto the same normalized question and returned each other's
// answers. The two questions differ only in the operator, so they must stay
// distinct through both insert dedup and the exact-match query stage.
func TestQueryDistinguishesOperators(t *testing.T) {
	s := testStore(t)

	plusQ := "98127634+123="
	timesQ := "98127634×123="
	if _, err := s.AddQuestion(plusQ, nil, strp("答案加"), nil, false); err != nil {
		t.Fatalf("add plus: %v", err)
	}
	if _, err := s.AddQuestion(timesQ, nil, strp("答案乘"), nil, false); err != nil {
		t.Fatalf("add times: %v", err)
	}
	defer s.exec(`DELETE FROM AIResponses WHERE Question IN ($1, $2)`, plusQ, timesQ)

	plusHits, err := s.Query(plusQ, nil, match.Score)
	if err != nil {
		t.Fatalf("query plus: %v", err)
	}
	if len(plusHits) == 0 || plusHits[0].Answer != "答案加" {
		t.Fatalf("plus query returned %+v, want answer 答案加", plusHits)
	}

	timesHits, err := s.Query(timesQ, nil, match.Score)
	if err != nil {
		t.Fatalf("query times: %v", err)
	}
	if len(timesHits) == 0 || timesHits[0].Answer != "答案乘" {
		t.Fatalf("times query returned %+v, want answer 答案乘", timesHits)
	}
}

// TestQueryRejectsDifferentOperatorFuzzy covers the fallback path: when only the
// multiplication row exists, a plus query must not fuzzy-match it.
func TestQueryRejectsDifferentOperatorFuzzy(t *testing.T) {
	s := testStore(t)

	timesQ := "98765432×123="
	if _, err := s.AddQuestion(timesQ, nil, strp("答案乘"), nil, false); err != nil {
		t.Fatalf("add: %v", err)
	}
	defer s.exec(`DELETE FROM AIResponses WHERE Question = $1`, timesQ)

	hits, err := s.Query("98765432+123=", nil, match.Score)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for _, h := range hits {
		if h.Answer == "答案乘" {
			t.Fatalf("plus query fuzzy-matched the multiplication row: %+v", h)
		}
	}
}

// TestInsertAIResponseAllowsDifferentOptions verifies that the same question
// with different options is stored as a distinct entry.
func TestInsertAIResponseAllowsDifferentOptions(t *testing.T) {
	s := testStore(t)

	question := "去重测试题目 不同选项"
	a, err := s.InsertAIResponse(question, "A", strp("A. 甲 B. 乙"), nil)
	if err != nil {
		t.Fatalf("insert a: %v", err)
	}
	b, err := s.InsertAIResponse(question, "B", strp("A. 丙 B. 丁"), nil)
	if err != nil {
		t.Fatalf("insert b: %v", err)
	}
	if a == b {
		t.Fatalf("different options reused the same row %d", a)
	}
	_, _ = s.exec(`DELETE FROM AIResponses WHERE QuestionHash = $1`, match.QuestionHash(question))
}
