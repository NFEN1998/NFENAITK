package mogo

import (
	"strings"
)

// FormatAnswer renders an internal answer as the MOGO `data` field. MOGO's
// data separator is "###", so multi-part answers are joined with it; free-form
// answers stay a single string so MOGO does not split them into extra answers.
func FormatAnswer(info Info, answer string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}

	switch info.Type {
	case "单选题":
		parts := splitAnswerParts(answer)
		if len(parts) == 0 {
			return answer
		}
		return mapOption(parts[0], info.Options)
	case "多选题":
		return joinMapped(splitAnswerParts(answer), info.Options)
	case "判断题":
		return normalizeMogoJudgement(answer)
	case "填空题":
		return joinParts(splitAnswerParts(answer), "###")
	case "排序题":
		return joinMapped(splitAnswerParts(answer), info.Options)
	case "连线题":
		return formatMatching(info, answer)
	case "完型填空":
		return formatCloze(info, answer)
	case "学习通讨论", "简答题", "名词解释", "论述题", "计算题", "口语题", "资料题", "问答题":
		return joinParts(splitAnswerParts(answer), "\n")
	case "智慧树互动":
		return formatInteraction(answer)
	default:
		return answer
	}
}

func splitAnswerParts(answer string) []string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return nil
	}
	for _, separator := range []string{"###", "|||"} {
		if strings.Contains(answer, separator) {
			return cleanParts(strings.Split(answer, separator))
		}
	}
	if strings.Contains(answer, "\n") {
		return cleanParts(strings.Split(answer, "\n"))
	}
	return []string{answer}
}

func cleanParts(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func joinParts(parts []string, separator string) string {
	return strings.Join(cleanParts(parts), separator)
}

func joinMapped(parts []string, options []string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, mapOption(part, options))
	}
	return strings.Join(out, "###")
}

func mapOption(part string, options []string) string {
	value := strings.TrimSpace(part)
	trimmed := strings.Trim(value, "()（）[]【】")
	if index, ok := optionIndex(trimmed); ok && index < len(options) {
		return strings.TrimSpace(options[index])
	}
	return value
}

func optionIndex(value string) (int, bool) {
	value = strings.TrimSpace(value)
	value = strings.TrimRight(value, ".、．:：)）")
	if len(value) != 1 {
		return 0, false
	}
	char := value[0]
	switch {
	case char >= 'A' && char <= 'Z':
		return int(char - 'A'), true
	case char >= 'a' && char <= 'z':
		return int(char - 'a'), true
	case char >= '1' && char <= '9':
		return int(char - '1'), true
	}
	return 0, false
}

func normalizeMogoJudgement(answer string) string {
	value := strings.ToLower(strings.TrimSpace(answer))
	if strings.Contains(value, "不错") {
		return "对"
	}
	switch {
	case strings.Contains(value, "不正确"), strings.Contains(value, "不对"),
		strings.Contains(value, "错误"), strings.Contains(value, "错"),
		value == "false", value == "f", value == "×", value == "否":
		return "错"
	case strings.Contains(value, "正确"), strings.Contains(value, "对"),
		value == "true", value == "t", value == "√", value == "是":
		return "对"
	}
	return strings.TrimSpace(answer)
}

func formatMatching(info Info, answer string) string {
	var second []string
	if len(info.Groups) >= 2 {
		second = info.Groups[1]
	}
	parts := splitAnswerParts(answer)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, matchingItem(part, second))
	}
	return strings.Join(out, "###")
}

func matchingItem(part string, second []string) string {
	value := strings.TrimSpace(part)
	if index := strings.IndexAny(value, "-:：、 "); index >= 0 {
		left := strings.TrimSpace(value[:index])
		if _, ok := optionIndex(left); ok {
			value = strings.TrimSpace(value[index+1:])
		}
	}
	value = strings.Trim(value, "()（）[]【】")
	if index, ok := optionIndex(value); ok && index < len(second) {
		return strings.TrimSpace(second[index])
	}
	return value
}

func formatCloze(info Info, answer string) string {
	parts := splitAnswerParts(answer)
	out := make([]string, 0, len(parts))
	for index, part := range parts {
		options := info.Options
		if index < len(info.Groups) {
			options = info.Groups[index]
		}
		out = append(out, mapOption(part, options))
	}
	return strings.Join(out, "###")
}

func formatInteraction(answer string) string {
	if strings.Contains(answer, "****") {
		return answer
	}
	parts := splitAnswerParts(answer)
	if len(parts) >= 2 {
		return parts[0] + "****" + strings.Join(parts[1:], " ")
	}
	return answer
}
