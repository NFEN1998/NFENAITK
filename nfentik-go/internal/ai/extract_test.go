package ai

import "testing"

func TestExtractAnswerAcceptsValidForms(t *testing.T) {
	cases := map[string]string{
		`{"answer": "A.连接"}`:                 "A.连接",
		"```json\n{\"answer\": \"80\"}\n```": "80",
		"答案是 A":                              "A",
		"正确答案是 B. 网络层":                       "B. 网络层",
		"A":                                  "A",
		"1949年10月1日":                         "1949年10月1日",
	}
	for raw, want := range cases {
		if got := ExtractAnswer(raw); got != want {
			t.Errorf("ExtractAnswer(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestExtractAnswerRejectsProse(t *testing.T) {
	rejects := []string{
		"这个问题涉及很多方面。首先，我们需要考虑……其次，还要看……最后得出结论。",
		"很抱歉，我无法回答这个问题，因为信息不足，请提供更多上下文。",
		"Based on the information provided, there are multiple considerations here. First, we must evaluate the context. Second, the answer depends on several factors.",
	}
	for _, raw := range rejects {
		if got := ExtractAnswer(raw); got != "" {
			t.Errorf("ExtractAnswer(%q) = %q, want empty", raw, got)
		}
	}
}

func TestExtractAnswerEmptyInput(t *testing.T) {
	if got := ExtractAnswer(""); got != "" {
		t.Errorf("ExtractAnswer(\"\") = %q, want empty", got)
	}
}
