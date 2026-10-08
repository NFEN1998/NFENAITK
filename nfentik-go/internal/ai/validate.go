package ai

import "strings"

// unreliableAnswerPatterns lists substrings that mark an extracted answer as a
// refusal, an "unable to determine" reply, a low-information placeholder, or a
// fallback that should not be persisted into the question bank.
var unreliableAnswerPatterns = []string{
	"无法确定",
	"无法作答",
	"无法回答",
	"无法给出",
	"无法判断",
	"无法辨认",
	"不能确定",
	"不能作答",
	"不能回答",
	"题目不完整",
	"题目信息不完整",
	"信息不完整",
	"信息不足",
	"缺少必要",
	"缺失",
	"没有提供",
	"未提供",
	"请提供更多",
	"请补充",
	"抱歉",
	"很抱歉",
	"对不起",
	"不知道",
	"不清楚",
	"不了解",
	"无法得知",
	"请点击链接查看图片",
	"查看图片",
}

// unreliableExactAnswers matches low-information answers that are exactly one of
// these strings after trimming, so genuine short answers are not rejected.
var unreliableExactAnswers = map[string]bool{
	"无":         true,
	"none":      true,
	"null":      true,
	"nil":       true,
	"n/a":       true,
	"未知":        true,
	"不确定":       true,
	"无法回答":      true,
	"无法确定":      true,
	"no answer": true,
}

// IsUnreliableAnswer reports whether an extracted answer is too unreliable to be
// stored in the bank. It returns a short reason when rejected.
func IsUnreliableAnswer(answer string) (string, bool) {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return "答案为空", true
	}
	normalized := strings.ToLower(trimmed)
	if unreliableExactAnswers[normalized] {
		return "答案无有效信息", true
	}
	for _, p := range unreliableAnswerPatterns {
		if strings.Contains(trimmed, p) || strings.Contains(normalized, strings.ToLower(p)) {
			return "答案不可靠（" + p + "）", true
		}
	}
	return "", false
}
