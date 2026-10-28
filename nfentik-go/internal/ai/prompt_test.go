package ai

import (
	"strings"
	"testing"
)

func TestDetectQuestionKind(t *testing.T) {
	cases := map[string]questionKind{
		"single_choice":   kindSingle,
		"单选":              kindSingle,
		"multiple_choice": kindMultiple,
		"多选":              kindMultiple,
		"judgement":       kindJudgement,
		"判断":              kindJudgement,
		"completion":      kindCompletion,
		"填空":              kindCompletion,
		"essay":           kindEssay,
		"short_answer":    kindEssay,
		"简答":              kindEssay,
		"问答题":             kindEssay,
		"论述":              kindEssay,
		"":                kindUnknown,
		"未知类型":            kindUnknown,
	}
	for raw, want := range cases {
		if got := detectQuestionKind(raw); got != want {
			t.Errorf("detectQuestionKind(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestQuestionKindEssayNameAndHint(t *testing.T) {
	if kindEssay.chineseName() != "简答" {
		t.Errorf("kindEssay chineseName = %q, want 简答", kindEssay.chineseName())
	}
	if kindEssay.hint() == "" {
		t.Errorf("kindEssay hint is empty")
	}
}

func TestBuildTextPromptWithContext(t *testing.T) {
	plain := BuildTextPrompt("题目", nil, nil)
	withContext := BuildTextPromptWithContext("题目", nil, nil, "补充")
	if plain == withContext {
		t.Fatalf("context did not change the prompt")
	}
	if !strings.Contains(withContext, "【补充信息】\n补充") {
		t.Fatalf("context missing: %s", withContext)
	}
	if strings.Index(withContext, "【补充信息】") > strings.Index(withContext, "请先分析") {
		t.Fatalf("context must precede the final instruction")
	}
}
