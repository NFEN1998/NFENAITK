package store

import (
	"testing"

	"github.com/nfentik/nfentik-go/internal/match"
)

func TestFormulaQuestionsDoNotCollide(t *testing.T) {
	s := testStore(t)
	id1, err := s.InsertAIResponse("1.5", "小数答案", nil, nil)
	if err != nil {
		t.Fatalf("insert 1.5: %v", err)
	}
	t.Cleanup(func() { _ = s.DeleteQuestion(id1) })
	id2, err := s.InsertAIResponse("15", "整数答案", nil, nil)
	if err != nil {
		t.Fatalf("insert 15: %v", err)
	}
	t.Cleanup(func() { _ = s.DeleteQuestion(id2) })

	for question, want := range map[string]string{"1.5": "小数答案", "15": "整数答案"} {
		hit, err := s.FindByNormalizedQuestion(question, nil)
		if err != nil {
			t.Fatalf("find %q: %v", question, err)
		}
		if hit == nil || hit.Answer != want {
			t.Fatalf("find %q = %+v, want %q", question, hit, want)
		}
	}
}

func TestFormulaOptionsDoNotCollide(t *testing.T) {
	s := testStore(t)
	opt1, opt2 := "A. 1.5", "A. 15"
	id1, err := s.InsertAIResponse("公式选项题", "小数", &opt1, nil)
	if err != nil {
		t.Fatalf("insert option 1.5: %v", err)
	}
	t.Cleanup(func() { _ = s.DeleteQuestion(id1) })
	id2, err := s.InsertAIResponse("公式选项题", "整数", &opt2, nil)
	if err != nil {
		t.Fatalf("insert option 15: %v", err)
	}
	t.Cleanup(func() { _ = s.DeleteQuestion(id2) })

	for options, want := range map[string]string{opt1: "小数", opt2: "整数"} {
		hit, err := s.FindByNormalizedQuestion("公式选项题", &options)
		if err != nil {
			t.Fatalf("find options %q: %v", options, err)
		}
		if hit == nil || hit.Answer != want {
			t.Fatalf("find options %q = %+v, want %q", options, hit, want)
		}
	}
}

func TestQueryFindsEquivalentFormulaOperators(t *testing.T) {
	s := testStore(t)
	cases := []struct{ stored, query, answer string }{
		{"7*8=", "7×8=", "乘法"},
		{"9<=10", "9≤10", "小于等于"},
		{"11!=12", "11≠12", "不等于"},
		{"x^2", "x²", "平方"},
		{"3-4=", "3−4=", "减法"},
	}
	for _, c := range cases {
		id, err := s.InsertAIResponse(c.stored, c.answer, nil, nil)
		if err != nil {
			t.Fatalf("insert %q: %v", c.stored, err)
		}
		hits, err := s.Query(c.query, nil, match.Score)
		if err != nil {
			t.Fatalf("query %q: %v", c.query, err)
		}
		_ = s.DeleteQuestion(id)
		if len(hits) == 0 || hits[0].Answer != c.answer {
			t.Fatalf("query %q hits=%+v, want %q", c.query, hits, c.answer)
		}
	}
}
