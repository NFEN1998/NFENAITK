package ai

import (
	"fmt"
	"strings"
)

// questionKind categorises the incoming OCS question type.
type questionKind int

const (
	kindUnknown questionKind = iota
	kindSingle
	kindMultiple
	kindJudgement
	kindCompletion
	kindEssay
)

func (k questionKind) chineseName() string {
	switch k {
	case kindSingle:
		return "单选"
	case kindMultiple:
		return "多选"
	case kindJudgement:
		return "判断"
	case kindCompletion:
		return "填空"
	case kindEssay:
		return "简答"
	}
	return ""
}

func (k questionKind) hint() string {
	switch k {
	case kindSingle:
		return "这是单选题,只有一个正确答案.请返回正确选项的完整文字内容,不要返回A/B/C/D等选项字母或序号."
	case kindMultiple:
		return "这是多选题,可能有多个正确答案.请返回所有正确选项的完整文字内容,多个答案用\"###\"连接,不要返回A/B/C/D等选项字母."
	case kindJudgement:
		return "这是判断题,请只回答正确或错误,不要添加任何其他文字或标点."
	case kindCompletion:
		return "这是填空题.如果有多空请用\"###\"连接每个空的答案,只有一个空则直接返回答案内容,不要加序号."
	case kindEssay:
		return "这是简答题.请给出完整、准确的答案,分点作答时要点之间用\"###\"连接,不要输出与答案无关的内容."
	}
	return ""
}

func detectQuestionKind(queryType string) questionKind {
	trimmed := strings.TrimSpace(queryType)
	if trimmed == "" {
		return kindUnknown
	}
	normalized := strings.ToLower(trimmed)
	switch {
	case strings.Contains(normalized, "single") || strings.Contains(trimmed, "单选") || strings.Contains(trimmed, "单项选择"):
		return kindSingle
	case strings.Contains(normalized, "multiple") || strings.Contains(trimmed, "多选") || strings.Contains(trimmed, "多项选择"):
		return kindMultiple
	case strings.Contains(normalized, "judgement") || strings.Contains(normalized, "judgment") || strings.Contains(trimmed, "判断"):
		return kindJudgement
	case strings.Contains(normalized, "completion") || strings.Contains(trimmed, "填空"):
		return kindCompletion
	case strings.Contains(normalized, "essay") || strings.Contains(normalized, "short_answer") ||
		strings.Contains(normalized, "short-answer") || strings.Contains(normalized, "shortanswer") ||
		strings.Contains(trimmed, "简答") || strings.Contains(trimmed, "问答") || strings.Contains(trimmed, "论述"):
		return kindEssay
	}
	return kindUnknown
}

// BuildTextPrompt mirrors build_model_query_prompt from the desktop app.
func BuildTextPrompt(title string, options, queryType *string) string {
	var b strings.Builder
	b.WriteString("你是一个专业的答题助手。我会给你一道题目，请先分别做两件事：\n")
	b.WriteString("1. 仔细分析题目，理解题目在问什么，如果有选项则逐一分析每个选项是否正确。\n")
	b.WriteString("2. 输出最终答案，**只输出一个JSON对象**，不要加代码块标记，不要加任何其他文字。\n\n")
	b.WriteString("格式：{\"answer\": \"你的最终答案\"}\n\n")

	if queryType != nil {
		raw := strings.TrimSpace(*queryType)
		if raw != "" {
			if kind := detectQuestionKind(raw); kind != kindUnknown {
				fmt.Fprintf(&b, "【题目类型：%s题】\n", kind.chineseName())
				fmt.Fprintf(&b, "提示：%s\n", kind.hint())
			} else {
				fmt.Fprintf(&b, "【题目类型字段：%s】\n", raw)
			}
		}
	}

	fmt.Fprintf(&b, "【题目】\n%s\n", title)
	if options != nil {
		if opts := strings.TrimSpace(*options); opts != "" {
			fmt.Fprintf(&b, "【选项】\n%s\n", opts)
		}
	}
	b.WriteString("\n请先分析，再输出答案JSON：")
	return b.String()
}

// BuildSummaryPrompt mirrors the desktop summary phase prompt. It asks the
// summary model to consolidate the base models' answers into one final answer.
func BuildSummaryPrompt(title string, baseAnswers []BaseAnswer) string {
	var b strings.Builder
	b.WriteString("你是一个总结专家。下面是用户的问题以及AI模型的回答。请根据回答内容，整理并总结出一个最准确的答案。\n\n")
	fmt.Fprintf(&b, "用户原始问题：\n%s\n\n", title)
	b.WriteString("模型回答内容：\n")
	for _, a := range baseAnswers {
		fmt.Fprintf(&b, "[%s]\n%s\n\n", a.ModelName, a.Answer)
	}
	b.WriteString("请直接给出最终总结答案：")
	return b.String()
}

// BaseAnswer pairs one base model's answer with its display name.
type BaseAnswer struct {
	ModelName string
	Answer    string
}

// MajorityAnswer returns the most frequent answer among the candidates.
// Comparison is case-insensitive and whitespace-trimmed; the original spelling
// of the first occurrence is returned. It returns "" when there are none.
func MajorityAnswer(answers []string) string {
	counts := map[string]int{}
	original := map[string]string{}
	best := ""
	bestCount := 0
	for _, a := range answers {
		key := strings.ToLower(strings.TrimSpace(a))
		if key == "" {
			continue
		}
		counts[key]++
		if _, ok := original[key]; !ok {
			original[key] = a
		}
		if counts[key] > bestCount {
			bestCount = counts[key]
			best = key
		}
	}
	return original[best]
}

// BuildVisionPrompt wraps a URL based question for the vision model.
func BuildVisionPrompt(title string, options *string) string {
	var b strings.Builder
	b.WriteString("请阅读图片内容并回答题目。\n")
	b.WriteString("先分析题目，再只输出一个JSON对象：{\"answer\": \"你的最终答案\"}\n\n")
	fmt.Fprintf(&b, "【题目】\n%s\n", title)
	if options != nil {
		if opts := strings.TrimSpace(*options); opts != "" {
			fmt.Fprintf(&b, "【选项】\n%s\n", opts)
		}
	}
	return b.String()
}
