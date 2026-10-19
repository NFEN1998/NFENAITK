// Package match implements the question similarity algorithm used to decide
// whether an incoming OCS query matches an existing bank entry.
//
// It is a faithful port of the Rust implementation in database.rs:
//   - URLs are normalised to a placeholder and must match exactly when present
//   - a character level Levenshtein ratio gates candidates (>0.72)
//   - keyword coverage (jieba search-mode tokens minus stopwords) refines the score
//   - titles with option hints ("以下/下列/...") require the options to match too
package match

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/yanyiwu/gojieba"
)

// stopwords mirrors QUERY_STOPWORDS from the desktop app.
var stopwords = map[string]struct{}{
	"的": {}, "地": {}, "得": {}, "了": {}, "着": {}, "吗": {}, "呢": {}, "啊": {},
	"呀": {}, "吧": {}, "么": {}, "嘛": {}, "在": {}, "是": {}, "和": {}, "与": {},
	"及": {}, "或": {}, "并": {}, "且": {}, "将": {}, "把": {}, "被": {}, "由": {},
	"对": {}, "于": {}, "中": {}, "上": {}, "下": {}, "请问": {}, "哪里": {}, "哪儿": {},
	"哪个": {}, "哪种": {}, "哪项": {}, "哪些": {}, "什么": {}, "怎么": {}, "怎样": {},
	"如何": {}, "为何": {}, "为什么": {}, "多少": {}, "几": {}, "一下": {},
	"以下": {}, "下列": {}, "下面": {}, "下叙": {}, "题目": {}, "选项": {}, "答案": {},
	"内容": {}, "说法": {}, "图片": {}, "图中": {}, "名字": {}, "名称": {}, "城市": {},
	"国家": {}, "地区": {}, "地方": {},
}

// optionMatchKeywords mirrors QUESTION_AND_OPTIONS_MATCH_KEYWORDS.
var optionMatchKeywords = []string{"以下", "下列", "下面", "下叙"}

var (
	urlRe          = regexp.MustCompile(`https?://[^\s]+`)
	trailingPuncts = "，。！？、；：\u300c\u300d\u300e\u300f（）【】"
)

// jiebaDictFiles are the dictionary files gojieba needs, in the order its
// NewJieba constructor expects them.
var jiebaDictFiles = []string{
	"jieba.dict.utf8",
	"hmm_model.utf8",
	"user.dict.utf8",
	"idf.utf8",
	"stop_words.utf8",
}

// jieba holds a singleton segmenter because loading the dictionary is expensive.
var jieba = newSegmenter()

// newSegmenter builds the singleton segmenter.
//
// gojieba resolves its dictionaries from the path of its own source file at
// build time (via runtime.Caller), which only exists on the machine that
// compiled the binary. That makes a released binary tied to the build host and
// unusable on a machine without the Go module cache - notably a cross-compiled
// Windows build, whose compiled-in path is a Linux path.
//
// To keep releases portable, the dictionary is looked up in this order:
//  1. the JIEBA_DICT_DIR environment variable,
//  2. a "dict" directory next to the running executable (shipped in release packages),
//  3. gojieba's compiled-in default (the build host module cache).
func newSegmenter() *gojieba.Jieba {
	if dir := resolveDictDir(); dir != "" {
		paths := make([]string, len(jiebaDictFiles))
		for i, name := range jiebaDictFiles {
			paths[i] = filepath.Join(dir, name)
		}
		return gojieba.NewJieba(paths...)
	}
	return gojieba.NewJieba()
}

// resolveDictDir returns the first usable dictionary directory, or "" to fall
// back to gojieba's compiled-in default.
func resolveDictDir() string {
	if dir := strings.TrimSpace(os.Getenv("JIEBA_DICT_DIR")); dir != "" && isDictDir(dir) {
		return dir
	}
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "dict")
		if isDictDir(candidate) {
			return candidate
		}
	}
	return ""
}

// isDictDir reports whether dir contains every dictionary file.
func isDictDir(dir string) bool {
	for _, name := range jiebaDictFiles {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
}

// CloseSegmenter releases the jieba dictionary. Call once on shutdown.
func CloseSegmenter() {
	jieba.Free()
}

// ExtractURLs returns the sorted set of URLs found in text with trailing
// Chinese punctuation trimmed off.
func ExtractURLs(text string) []string {
	matches := urlRe.FindAllString(text, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, strings.TrimRight(m, trailingPuncts))
	}
	sort.Strings(out)
	return out
}

func normalizeURLs(text string) string {
	return urlRe.ReplaceAllString(text, "__URL__")
}

// NormalizeQuestion reduces a question to lowercase letters and digits, dropping
// whitespace and text punctuation, so questions that differ only in formatting
// compare as equal. Formula symbols (operators and grouping) are preserved
// because they change the meaning of an expression: without them "3+3=" and
// "3×3=" would collapse onto the same digest and return each other's answers.
func NormalizeQuestion(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(normalizeURLs(text)) {
		if r >= 0xFF01 && r <= 0xFF5E {
			r -= 0xFEE0
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || isFormulaSymbol(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NormalizerVersion identifies the current NormalizeQuestion algorithm. Bump it
// whenever normalization changes so stored question digests are recomputed once
// on the next startup (see Store.ensureQuestionHash).
const NormalizerVersion = 3

// isMathOperator reports whether r is an operator that changes the value of a
// numeric expression. It is the discriminator used to keep "3+3=" and "3×3="
// apart; parentheses and the equals sign are excluded because they are often
// decorative (blank markers, equation formatting) rather than meaningful.
func isMathOperator(r rune) bool {
	switch r {
	case '+', '-', '*', '/', '×', '÷', '^', '%', '≠', '≤', '≥', '<', '>':
		return true
	default:
		return false
	}
}

// isFormulaSymbol reports whether r is preserved during normalization: math
// operators plus grouping parentheses, whose presence changes a formula. The
// equals sign is treated as formatting so "3+3=" and "3+3" still compare equal.
func isFormulaSymbol(r rune) bool {
	return isMathOperator(r) || r == '(' || r == ')'
}

// operatorsCompatible reports whether two strings may be treated as fuzzy
// matches. When both look like numeric expressions (a digit together with an
// operator) their operator multisets must agree, so a plus question never
// fuzzy-matches a multiplication row. Plain prose and decorative parentheses
// are left untouched.
func operatorsCompatible(a, b string) bool {
	if !looksLikeExpression(a) || !looksLikeExpression(b) {
		return true
	}
	return equalOperatorCounts(operatorCounts(a), operatorCounts(b))
}

// looksLikeExpression reports whether s contains both a digit and an operator,
// the shape of an arithmetic or comparison expression.
func looksLikeExpression(s string) bool {
	hasDigit, hasOperator := false, false
	for _, r := range foldWidth(s) {
		if unicode.IsDigit(r) {
			hasDigit = true
		}
		if isMathOperator(r) {
			hasOperator = true
		}
	}
	return hasDigit && hasOperator
}

// operatorCounts tallies the math operators in s after width folding.
func operatorCounts(s string) map[rune]int {
	counts := map[rune]int{}
	for _, r := range foldWidth(s) {
		if isMathOperator(r) {
			counts[r]++
		}
	}
	return counts
}

// equalOperatorCounts compares two operator tallies.
func equalOperatorCounts(a, b map[rune]int) bool {
	if len(a) != len(b) {
		return false
	}
	for r, n := range a {
		if b[r] != n {
			return false
		}
	}
	return true
}

// foldWidth lowercases s and maps full-width ASCII forms to their half-width
// equivalents so "＋" compares equal to "+".
func foldWidth(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 0xFF01 && r <= 0xFF5E {
			r -= 0xFEE0
		}
		b.WriteRune(r)
	}
	return b.String()
}

// RequireOptionMatch reports whether the title asks about the listed options.
func RequireOptionMatch(title string) bool {
	for _, kw := range optionMatchKeywords {
		if strings.Contains(title, kw) {
			return true
		}
	}
	return false
}

func isPunctuationOrSpace(r rune) bool {
	if unicode.IsSpace(r) || unicode.IsPunct(r) {
		return true
	}
	switch r {
	case '，', '。', '！', '？', '、', '；', '：', '（', '）', '【', '】',
		'《', '》', '“', '”', '‘', '’', '—', '…', '·':
		return true
	}
	return false
}

func isMeaningfulToken(token string) bool {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return false
	}
	if _, ok := stopwords[trimmed]; ok {
		return false
	}
	allPunct := true
	for _, r := range trimmed {
		if !isPunctuationOrSpace(r) {
			allPunct = false
			break
		}
	}
	if allPunct {
		return false
	}

	runes := []rune(trimmed)
	allDigit, allAlpha := true, true
	for _, r := range runes {
		if !unicode.IsDigit(r) {
			allDigit = false
		}
		if !unicode.IsLetter(r) || r > unicode.MaxASCII {
			allAlpha = false
		}
	}
	if allDigit {
		return true
	}
	if allAlpha {
		return len(runes) > 1
	}
	return len(runes) > 1
}

// ExtractKeywords returns the meaningful jieba search tokens of text.
func ExtractKeywords(text string) map[string]struct{} {
	words := jieba.CutForSearch(text, true)
	set := make(map[string]struct{}, len(words))
	for _, w := range words {
		token := strings.ToLower(strings.TrimSpace(w))
		if isMeaningfulToken(token) {
			set[token] = struct{}{}
		}
	}
	return set
}

func keywordCoverage(query, candidate map[string]struct{}) float64 {
	if len(query) == 0 {
		return 1.0
	}
	matched := 0
	for token := range query {
		if _, ok := candidate[token]; ok {
			matched++
		}
	}
	return float64(matched) / float64(len(query))
}

func minKeywordCoverage(queryLen int, charSimilarity float64) float64 {
	if queryLen <= 2 {
		return 1.0
	}
	if charSimilarity >= 0.88 && queryLen <= 8 {
		return 1.0
	}
	if queryLen <= 4 {
		return 1.0
	}
	if queryLen <= 8 {
		return 0.9
	}
	return 0.75
}

// levenshteinSimilarity returns 1 - distance/max(len) over runes.
func levenshteinSimilarity(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 && len(rb) == 0 {
		return 1.0
	}
	if len(ra) == 0 || len(rb) == 0 {
		return 0.0
	}
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	maxLen := len(ra)
	if len(rb) > maxLen {
		maxLen = len(rb)
	}
	return 1.0 - float64(prev[len(rb)])/float64(maxLen)
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// Score computes a similarity score in [0,1]. A zero second return means the
// candidate is rejected.
func Score(query, candidate string) (float64, bool) {
	nq := strings.ToLower(strings.TrimSpace(normalizeURLs(query)))
	nc := strings.ToLower(strings.TrimSpace(normalizeURLs(candidate)))
	if nq == "" || nc == "" {
		return 0, false
	}
	if nq == nc {
		return 1.0, true
	}
	// A numeric expression must never fuzzy-match one that carries different
	// operators (e.g. "3+3=" against "3×3=").
	if !operatorsCompatible(nq, nc) {
		return 0, false
	}
	charSim := levenshteinSimilarity(nq, nc)
	if charSim < 0.72 {
		return 0, false
	}
	queryKeywords := ExtractKeywords(nq)
	if len(queryKeywords) == 0 {
		return charSim, true
	}
	candidateKeywords := ExtractKeywords(nc)
	coverage := keywordCoverage(queryKeywords, candidateKeywords)
	if coverage+1e-9 < minKeywordCoverage(len(queryKeywords), charSim) {
		return 0, false
	}
	return charSim*0.7 + coverage*0.3, true
}

// IsExact reports whether a score represents an exact match.
func IsExact(score float64) bool {
	return score >= 1.0-1e-9
}
