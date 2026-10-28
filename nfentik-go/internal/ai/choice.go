package ai

import (
	"regexp"
	"strings"

	"github.com/nfentik/nfentik-go/internal/match"
)

// choiceOption is one parsed option from an OCS options string.
type choiceOption struct {
	Letter string
	Text   string
	Norm   string
}

var (
	// markerRe finds "A." / "A、" / "A)" style markers at the start of the
	// string or after whitespace. The preceding whitespace is part of the match
	// so inline options such as "A. 甲 B. 乙" split correctly.
	markerRe = regexp.MustCompile(`(?i)(?:^|[\s\x{3000}])([A-H])\s*[.、．:：)）]\s*`)
	// lineMarkerRe handles options without punctuation, such as "A difficult".
	lineMarkerRe = regexp.MustCompile(`^\s*([A-Ha-h])\s*[.、．:：)）]?\s+(.+)$`)
	// answerLetterRe recognises a bare option letter, optionally punctuated.
	answerLetterRe = regexp.MustCompile(`(?i)^\s*([A-H])\s*[.、．:：)）]?\s*$`)
	// compactMarkerRe is the whitespace-free fallback for CJK option strings.
	compactMarkerRe = regexp.MustCompile(`(?i)([A-H])\s*[.、．:：)）]\s*`)
	// bracketMarkerRe normalises "(A)" / "（A）" prefixes to "A. ".
	bracketMarkerRe = regexp.MustCompile(`[（(]\s*([A-Ha-h])\s*[)）]\s*`)
)

// parseInlineOptions splits raw using the supplied marker regex.
func parseInlineOptions(raw string, re *regexp.Regexp) []choiceOption {
	locs := re.FindAllStringSubmatchIndex(raw, -1)
	if len(locs) == 0 {
		return nil
	}
	var opts []choiceOption
	for i, loc := range locs {
		start := loc[1]
		end := len(raw)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		text := strings.TrimSpace(raw[start:end])
		if text == "" {
			continue
		}
		opts = append(opts, newChoiceOption(raw[loc[2]:loc[3]], text))
	}
	return opts
}

// parseChoiceOptions extracts option letters and text from the common OCS
// shapes: newline separated, inline separated, with or without punctuation.
func parseChoiceOptions(raw string) []choiceOption {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "###", "\n"))
	if raw == "" {
		return nil
	}
	raw = bracketMarkerRe.ReplaceAllString(raw, "$1. ")
	lines := strings.Split(raw, "\n")

	// Prefer one marker per line, which is the usual OCS shape.
	var lineOpts []choiceOption
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := lineMarkerRe.FindStringSubmatch(line)
		if m == nil {
			lineOpts = nil
			break
		}
		text := strings.TrimSpace(m[2])
		if text == "" {
			lineOpts = nil
			break
		}
		lineOpts = append(lineOpts, newChoiceOption(m[1], text))
	}
	if len(lineOpts) >= 2 {
		return lineOpts
	}

	// Inline markers, including a single option whose trailing part is empty.
	// Compact CJK options such as "A.甲B.乙" have no whitespace before the next
	// marker, so keep whichever parse found more options.
	inline := parseInlineOptions(raw, markerRe)
	if compact := parseInlineOptions(raw, compactMarkerRe); len(compact) > len(inline) {
		inline = compact
	}
	if len(inline) > 0 {
		return inline
	}

	// Last resort: unmarked lines are treated as options in order. This keeps
	// validation useful for option lists copied without letters.
	var plain []choiceOption
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		letter := string(rune('A' + len(plain)))
		plain = append(plain, newChoiceOption(letter, line))
	}
	if len(plain) >= 2 {
		return plain
	}
	return nil
}

func newChoiceOption(letter, text string) choiceOption {
	return choiceOption{
		Letter: strings.ToUpper(strings.TrimSpace(letter)),
		Text:   strings.TrimSpace(text),
		Norm:   match.NormalizeQuestion(text),
	}
}

// validateChoiceAnswer checks that a single/multiple choice answer refers to a
// parsed option. It returns the canonical option text, which is what the bank
// stores and OCS displays.
func validateChoiceAnswer(rawOptions, answer string, multiple bool) (string, bool) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false
	}
	opts := parseChoiceOptions(rawOptions)
	parts := []string{answer}
	if multiple {
		// A comma or semicolon may be part of a single option (for example
		// "red, blue"), so prefer an exact option match before splitting.
		if value, ok := matchOneOption(opts, answer); ok {
			return value, true
		}
		parts = splitMultiAnswer(answer)
		if len(parts) == 0 {
			return "", false
		}
	} else if strings.Contains(answer, "###") {
		// A single-choice question must not carry multiple answers.
		return "", false
	}

	canonical := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return "", false
		}
		if value, ok := matchOneOption(opts, part); ok {
			canonical = append(canonical, value)
			continue
		}
		// No parseable options: accept a concise answer that is contained in
		// the option blob. This supports letterless option lists.
		if len(opts) == 0 {
			blob := match.NormalizeQuestion(rawOptions)
			norm := match.NormalizeQuestion(part)
			if norm != "" && strings.Contains(blob, norm) {
				canonical = append(canonical, part)
				continue
			}
			if answerLetterRe.MatchString(part) {
				canonical = append(canonical, strings.ToUpper(strings.TrimSpace(part)))
				continue
			}
		}
		return "", false
	}
	if multiple {
		return strings.Join(canonical, "###"), true
	}
	return canonical[0], true
}

// matchOneOption resolves a bare letter or option text to its canonical text.
func matchOneOption(opts []choiceOption, answer string) (string, bool) {
	answer = stripAnswerPrefix(answer)
	if m := answerLetterRe.FindStringSubmatch(answer); m != nil {
		letter := strings.ToUpper(m[1])
		for _, opt := range opts {
			if opt.Letter == letter {
				return opt.Text, true
			}
		}
		// The options may be unmarked; a bare letter is still a valid choice.
		return letter, true
	}

	norm := match.NormalizeQuestion(answer)
	if norm == "" {
		return "", false
	}
	for _, opt := range opts {
		if opt.Norm == "" {
			continue
		}
		if norm == opt.Norm {
			return opt.Text, true
		}
		// Allow "The answer is simple." style answers while keeping a bound on
		// how much extra text can surround an option.
		if len(opt.Norm) >= 2 && strings.Contains(norm, opt.Norm) && len(norm) <= len(opt.Norm)*3 {
			return opt.Text, true
		}
	}
	return "", false
}

// stripAnswerPrefix removes common "答案是" style prefixes and wrapping
// brackets so letter and text matching see the payload only.
func stripAnswerPrefix(answer string) string {
	trimmed := strings.TrimSpace(answer)
	trimmed = strings.Trim(trimmed, "()（）[]【】")
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{
		"正确答案是", "正确答案为", "答案是", "答案：", "答案:",
		"the answer is", "answer:", "answer：", "选",
	} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(trimmed[len(prefix):])
		}
	}
	return trimmed
}

// splitMultiAnswer splits a multiple-choice answer on the documented separator
// and common alternatives.
func splitMultiAnswer(answer string) []string {
	if strings.Contains(answer, "###") {
		return strings.Split(answer, "###")
	}
	if strings.Contains(answer, "\n") {
		var lines []string
		for _, line := range strings.Split(answer, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
		if len(lines) > 1 {
			return lines
		}
	}
	// "A,B" / "A，B" are common model outputs; split only when every part looks
	// like a short option reference or text.
	fields := strings.FieldsFunc(answer, func(r rune) bool {
		return r == ',' || r == '，' || r == '、' || r == ';' || r == '；'
	})
	if len(fields) > 1 {
		out := make([]string, 0, len(fields))
		for _, f := range fields {
			f = strings.TrimSpace(f)
			if f == "" {
				return []string{answer}
			}
			out = append(out, f)
		}
		return out
	}
	// Compact letter lists such as "AC" or "ac".
	trimmed := strings.TrimSpace(answer)
	if len(trimmed) > 1 {
		letters := make([]string, 0, len(trimmed))
		allLetters := true
		for _, r := range trimmed {
			if r >= 'A' && r <= 'H' || r >= 'a' && r <= 'h' {
				letters = append(letters, strings.ToUpper(string(r)))
				continue
			}
			allLetters = false
			break
		}
		if allLetters {
			return letters
		}
	}
	return []string{answer}
}

// ValidateAnswer validates and canonicalises an AI or external answer for the
// question type. Choice questions must resolve to one of the supplied options;
// judgement answers are normalised; free-form answers pass through the
// unreliable-answer filter.
func ValidateAnswer(question string, options, queryType *string, answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false
	}
	if _, bad := IsUnreliableAnswer(answer); bad {
		return "", false
	}
	kind := detectQuestionKind(valueOf(queryType))
	opts := valueOf(options)

	switch kind {
	case kindSingle:
		if opts == "" {
			return answer, true
		}
		return validateChoiceAnswer(opts, answer, false)
	case kindMultiple:
		if opts == "" {
			return answer, true
		}
		return validateChoiceAnswer(opts, answer, true)
	case kindJudgement:
		return normalizeJudgement(answer)
	case kindCompletion, kindEssay:
		return answer, true
	default:
		// An untagged question with options is overwhelmingly a choice question;
		// validating it prevents translations and prose from entering the bank.
		if opts != "" {
			return validateChoiceAnswer(opts, answer, strings.Contains(answer, "###"))
		}
		return answer, true
	}
}

func valueOf(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func normalizeJudgement(answer string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(answer))
	switch {
	case strings.Contains(lower, "不正确"), strings.Contains(lower, "不对"):
		return "错误", true
	case strings.Contains(lower, "不错"):
		return "正确", true
	}
	hasTrue := strings.Contains(lower, "正确") || strings.Contains(lower, "对") ||
		lower == "true" || lower == "t" || lower == "√" || lower == "是"
	hasFalse := strings.Contains(lower, "错误") || strings.Contains(lower, "错") ||
		lower == "false" || lower == "f" || lower == "×" || lower == "否"
	switch {
	case hasTrue && !hasFalse:
		return "正确", true
	case hasFalse && !hasTrue:
		return "错误", true
	}
	return "", false
}
