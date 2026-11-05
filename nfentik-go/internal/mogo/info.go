package mogo

import (
	"fmt"
	"strconv"
	"strings"
)

const infoSeparator = "****"

// Info is the parsed MOGO `info` query parameter. MOGO sends the question type
// and its payload separated by "****", with option values separated by "###"
// (or sometimes "|||").
type Info struct {
	Raw        string
	Type       string
	Options    []string
	Groups     [][]string
	BlankCount int
	Body       string
	Limit      int
}

// ParseInfo decodes the info parameter into the fields used by the query
// chain. Malformed input is accepted on a best-effort basis so a single bad
// field never blocks a query.
func ParseInfo(raw string) Info {
	info := Info{Raw: strings.TrimSpace(raw)}
	if info.Raw == "" {
		return info
	}

	parts := strings.Split(info.Raw, infoSeparator)
	info.Type = canonicalInfoType(parts[0])
	payloads := parts[1:]

	switch info.Type {
	case "学习通讨论":
		for _, payload := range payloads {
			payload = strings.TrimSpace(payload)
			switch {
			case strings.HasPrefix(payload, "正文:"):
				info.Body = strings.TrimSpace(strings.TrimPrefix(payload, "正文:"))
			case strings.HasPrefix(payload, "限制长度:"):
				info.Limit, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(payload, "限制长度:")))
			}
		}
	case "智慧树互动":
		return info
	case "连线题", "完型填空":
		info.Groups = parseGroups(strings.Join(payloads, infoSeparator))
		for _, group := range info.Groups {
			info.Options = append(info.Options, group...)
		}
	default:
		payload := strings.TrimSpace(strings.Join(payloads, infoSeparator))
		if payload == "" {
			return info
		}
		if count, err := strconv.Atoi(payload); err == nil {
			info.BlankCount = count
			return info
		}
		info.Options = parseOptionList(payload)
	}

	if info.Type == "" && len(info.Options) > 0 {
		if len(info.Options) > 1 {
			info.Type = "多选题"
		} else {
			info.Type = "单选题"
		}
	}
	return info
}

func canonicalInfoType(raw string) string {
	typeName := strings.TrimSpace(raw)
	if typeName == "" {
		return ""
	}
	switch {
	case strings.Contains(typeName, "学习通讨论"):
		return "学习通讨论"
	case strings.Contains(typeName, "智慧树互动"):
		return "智慧树互动"
	case strings.Contains(typeName, "连线"):
		return "连线题"
	case strings.Contains(typeName, "排序"):
		return "排序题"
	case strings.Contains(typeName, "完型") || strings.Contains(typeName, "完形"):
		return "完型填空"
	case strings.Contains(typeName, "单选") || strings.Contains(typeName, "单项选择"):
		return "单选题"
	case strings.Contains(typeName, "多选") || strings.Contains(typeName, "多项选择"):
		return "多选题"
	case strings.Contains(typeName, "判断"):
		return "判断题"
	case strings.Contains(typeName, "名词解释"):
		return "名词解释"
	case strings.Contains(typeName, "论述"):
		return "论述题"
	case strings.Contains(typeName, "计算"):
		return "计算题"
	case strings.Contains(typeName, "口语"):
		return "口语题"
	case strings.Contains(typeName, "资料"):
		return "资料题"
	case strings.Contains(typeName, "问答") || strings.Contains(typeName, "简答"):
		return "简答题"
	case strings.Contains(typeName, "填空"):
		return "填空题"
	}
	return typeName
}

func parseOptionList(payload string) []string {
	body := strings.TrimSpace(payload)
	body = strings.TrimPrefix(body, "[")
	body = strings.TrimSuffix(body, "]")
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	separator := detectListSeparator(body)
	if separator == "" {
		return []string{body}
	}
	parts := strings.Split(body, separator)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func detectListSeparator(value string) string {
	switch {
	case strings.Contains(value, "###"):
		return "###"
	case strings.Contains(value, "|||"):
		return "|||"
	case strings.Contains(value, "\n"):
		return "\n"
	}
	return ""
}

func parseGroups(payload string) [][]string {
	body := strings.TrimSpace(payload)
	body = strings.TrimPrefix(body, "[")
	body = strings.TrimSuffix(body, "]")
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}

	var groups [][]string
	depth := 0
	start := -1
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '[':
			if depth == 0 {
				start = i + 1
			}
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
			if depth == 0 && start >= 0 {
				if options := parseOptionList(body[start:i]); len(options) > 0 {
					groups = append(groups, options)
				}
				start = -1
			}
		}
	}
	if len(groups) == 0 {
		if options := parseOptionList(body); len(options) > 0 {
			groups = append(groups, options)
		}
	}
	return groups
}

// InternalType maps a MOGO type to the type understood by the query chain.
// Subjective variants share the essay handling; special types keep their name.
func (i Info) InternalType() *string {
	typeName := strings.TrimSpace(i.Type)
	if typeName == "" {
		return nil
	}
	switch typeName {
	case "名词解释", "论述题", "计算题", "口语题", "资料题", "问答题":
		typeName = "简答题"
	}
	return &typeName
}

// QueryOptions returns OCS-style options for the types that benefit from
// option-aware matching and answer validation. Special types receive their
// structured context through ExtraPrompt instead.
func (i Info) QueryOptions() *string {
	if len(i.Options) == 0 {
		return nil
	}
	switch i.Type {
	case "单选题", "多选题":
		value := formatOptions(i.Options)
		return &value
	default:
		return nil
	}
}

// ExtraPrompt returns MOGO-only context for the AI prompt. It is empty for
// ordinary choice questions, which already carry their options.
func (i Info) ExtraPrompt() string {
	var lines []string
	switch i.Type {
	case "填空题":
		if i.BlankCount > 0 {
			lines = append(lines, fmt.Sprintf("空格数量：%d", i.BlankCount))
		}
	case "连线题":
		if len(i.Groups) >= 2 {
			lines = append(lines, "第一组："+strings.Join(i.Groups[0], "###"))
			lines = append(lines, "第二组："+strings.Join(i.Groups[1], "###"))
			lines = append(lines, "请按第一组的顺序返回第二组的正确内容，多个答案用###连接，不要返回题号或字母。")
		}
	case "排序题":
		if len(i.Options) > 0 {
			lines = append(lines, "待排序选项："+strings.Join(i.Options, "###"))
			lines = append(lines, "请按正确顺序返回选项文字，用###连接，不要返回题号或字母。")
		}
	case "完型填空":
		for index, group := range i.Groups {
			lines = append(lines, fmt.Sprintf("第%d组选项：%s", index+1, strings.Join(group, "###")))
		}
		lines = append(lines, "请按空顺序返回每个空的答案，用###连接。")
	case "学习通讨论":
		if i.Body != "" {
			lines = append(lines, "正文："+i.Body)
		}
		if i.Limit > 0 {
			lines = append(lines, fmt.Sprintf("限制长度：%d", i.Limit))
		}
		lines = append(lines, "请直接返回讨论内容。")
	case "智慧树互动":
		lines = append(lines, "请返回格式：新的提问****回答")
	}
	return strings.Join(lines, "\n")
}

func formatOptions(options []string) string {
	var builder strings.Builder
	for index, option := range options {
		if index > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteRune(rune('A' + index))
		builder.WriteString(". ")
		builder.WriteString(option)
	}
	return builder.String()
}
