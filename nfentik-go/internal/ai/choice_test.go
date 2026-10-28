package ai

import "testing"

func strptr(v string) *string { return &v }

func TestValidateChoiceAnswerRejectsTranslations(t *testing.T) {
	options := "A. difficult\nB. complicated\nC. simple\nD. invalid"
	single := strptr("单选题")
	if got, ok := ValidateAnswer("However intricate...", &options, single, "C. simple"); !ok || got != "simple" {
		t.Fatalf("valid option = %q ok=%v", got, ok)
	}
	if got, ok := ValidateAnswer("However intricate...", &options, single, "simple"); !ok || got != "simple" {
		t.Fatalf("bare option text = %q ok=%v", got, ok)
	}
	if _, ok := ValidateAnswer("However intricate...", &options, single, "动物之间的交流方式无论多么复杂"); ok {
		t.Fatalf("translation was accepted as a choice answer")
	}
	// Untagged questions with options still get choice validation.
	if _, ok := ValidateAnswer("However intricate...", &options, nil, "动物之间的交流方式无论多么复杂"); ok {
		t.Fatalf("translation was accepted without an explicit type")
	}
}

func TestValidateChoiceAnswerMalformedOptions(t *testing.T) {
	options := "A. salvage\nB."
	if got, ok := ValidateAnswer("Let all our neighbors know...", &options, strptr("单选题"), "salvage"); !ok || got != "salvage" {
		t.Fatalf("valid option = %q ok=%v", got, ok)
	}
	if _, ok := ValidateAnswer("Let all our neighbors know...", &options, strptr("单选题"), "让我们所有的邻国知道"); ok {
		t.Fatalf("translation was accepted for malformed options")
	}
}

func TestValidateChoiceFormats(t *testing.T) {
	cases := []struct {
		options string
		answer  string
		want    string
	}{
		{"A. difficult\nB. complicated\nC. simple\nD. invalid", "C", "simple"},
		{"A、difficult\nB、complicated\nC、simple\nD、invalid", "C、simple", "simple"},
		{"A. difficult B. complicated C. simple D. invalid", "simple", "simple"},
		{"difficult\ncomplicated\nsimple\ninvalid", "simple", "simple"},
		{"A 甲\nB 乙", "甲", "甲"},
		{"A.甲B.乙C.丙D.丁", "C", "丙"},
		{"(A) difficult\n(B) complicated\n(C) simple\n(D) invalid", "C", "simple"},
		{"A. difficult\nB. complicated\nC. simple\nD. invalid", "答案是 C", "simple"},
		{"A. difficult\nB. complicated\nC. simple\nD. invalid", "（C）", "simple"},
	}
	for _, c := range cases {
		got, ok := ValidateAnswer("question", &c.options, strptr("单选题"), c.answer)
		if !ok || got != c.want {
			t.Fatalf("options=%q answer=%q = %q ok=%v, want %q", c.options, c.answer, got, ok, c.want)
		}
	}
}

func TestValidateMultipleChoice(t *testing.T) {
	options := "A. difficult B. complicated C. simple D. invalid"
	if got, ok := ValidateAnswer("question", &options, strptr("多选题"), "A###C"); !ok || got != "difficult###simple" {
		t.Fatalf("multiple answer = %q ok=%v", got, ok)
	}
	if _, ok := ValidateAnswer("question", &options, strptr("单选题"), "A###C"); ok {
		t.Fatalf("single choice accepted multiple answers")
	}
}

func TestValidateJudgement(t *testing.T) {
	cases := map[string]string{
		"正确":    "正确",
		"对":     "正确",
		"不错":    "正确",
		"true":  "正确",
		"不正确":   "错误",
		"不对":    "错误",
		"错误":    "错误",
		"错":     "错误",
		"false": "错误",
	}
	for in, want := range cases {
		got, ok := ValidateAnswer("question", nil, strptr("判断题"), in)
		if !ok || got != want {
			t.Fatalf("judgement %q = %q ok=%v, want %q", in, got, ok, want)
		}
	}
}

func TestValidateMultipleChoicePrefersWholeOption(t *testing.T) {
	options := "A. red, blue\nB. green\nC. yellow"
	got, ok := ValidateAnswer("choose colors", &options, strptr("多选题"), "red, blue")
	if !ok || got != "red, blue" {
		t.Fatalf("whole option = %q ok=%v", got, ok)
	}
	got, ok = ValidateAnswer("choose colors", &options, strptr("多选题"), "A###C")
	if !ok || got != "red, blue###yellow" {
		t.Fatalf("multiple answer = %q ok=%v", got, ok)
	}
}
