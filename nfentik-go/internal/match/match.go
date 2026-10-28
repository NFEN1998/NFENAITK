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

// NormalizeQuestion reduces a question to lowercase letters and digits,
// dropping whitespace and text punctuation. Formula questions keep the symbols
// that change their meaning (decimal points, ratios, factorials, roots,
// absolute-value bars, superscripts, brackets and so on), so "1.5" and "15"
// no longer share a digest. Equivalent operator spellings are compared
// separately by operatorsCompatible.
func NormalizeQuestion(text string) string {
	runes := []rune(strings.ToLower(normalizeURLs(text)))
	formula := looksLikeFormulaRunes(runes)
	var b strings.Builder
	bracketDepth := 0
	for i, r := range runes {
		if r >= 0xFF01 && r <= 0xFF5E {
			r -= 0xFEE0
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		if unicode.IsMark(r) {
			if formula {
				b.WriteRune(r)
			}
			continue
		}
		if isFormulaSymbol(r) {
			b.WriteRune(r)
			switch r {
			case '(', '[', '{':
				bracketDepth++
			case ')', ']', '}':
				if bracketDepth > 0 {
					bracketDepth--
				}
			}
			continue
		}
		if !formula {
			continue
		}
		switch {
		case r == '=':
			b.WriteRune(r)
		case r == '.' || r == ':':
			if betweenDigits(runes, i) {
				b.WriteRune(r)
			}
		case r == '!':
			b.WriteRune(r)
		case r == ',':
			if bracketDepth > 0 {
				b.WriteRune(r)
			}
		case r == '[' || r == '{':
			b.WriteRune(r)
			bracketDepth++
		case r == ']' || r == '}':
			b.WriteRune(r)
			if bracketDepth > 0 {
				bracketDepth--
			}
		case isFormulaModeSymbol(r):
			b.WriteRune(r)
		}
	}
	out := b.String()
	if formula {
		out = trimDecorativeEquals(out)
	}
	return out
}

// NormalizerVersion identifies the current NormalizeQuestion algorithm. Bump it
// whenever normalization changes so stored question and option digests are
// recomputed once on the next startup.
const NormalizerVersion = 4

// looksLikeFormula decides whether the broader formula alphabet should be
// preserved. Ordinary prose keeps the historical punctuation-insensitive hash.
func looksLikeFormula(text string) bool {
	return looksLikeFormulaRunes([]rune(strings.ToLower(normalizeURLs(text))))
}

// IsFormula reports whether text uses math-significant symbols. The store uses
// it to widen candidate retrieval for equivalent operator spellings.
func IsFormula(text string) bool { return looksLikeFormula(text) }

// FormulaPrefix returns the leading alphanumeric run of a normalized formula.
// It is used as a bounded candidate prefix so "1×2" can find a stored "1*2".
func FormulaPrefix(normalized string) string {
	runes := []rune(normalized)
	for i, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		if i > 0 {
			return string(runes[:i])
		}
		for j := 0; j < len(runes); j++ {
			if unicode.IsLetter(runes[j]) || unicode.IsDigit(runes[j]) {
				return string(runes[j : j+1])
			}
		}
		return ""
	}
	return normalized
}

func looksLikeFormulaRunes(runes []rune) bool {
	var hasDigit, hasLetter bool
	for _, r := range runes {
		if unicode.IsDigit(r) {
			hasDigit = true
		}
		if unicode.IsLetter(r) {
			hasLetter = true
		}
	}
	for i, r := range runes {
		r = foldWidthRune(r)
		if isStrongMathSymbol(r) {
			return true
		}
		if unicode.Is(unicode.No, r) {
			return true
		}
		switch r {
		case '=':
			if (hasDigit || hasLetter) && alnumBefore(runes, i) && alnumAfter(runes, i) {
				return true
			}
		case '<', '>':
			if next, ok := runeAfter(runes, i); ok && next == '=' {
				if (hasDigit || hasLetter) && alnumBefore(runes, i) && alnumAfterEquals(runes, i) {
					return true
				}
			} else if betweenAlnum(runes, i) {
				return true
			}
		case '!':
			if next, ok := runeAfter(runes, i); ok && next == '=' {
				if alnumBefore(runes, i) && alnumAfterEquals(runes, i) {
					return true
				}
			} else if prev, ok := runeBefore(runes, i); ok && unicode.IsDigit(prev) {
				return true
			}
		case '.', ':':
			if betweenDigits(runes, i) {
				return true
			}
		case '+', '-', '*', '/', '^', '%':
			if betweenDigits(runes, i) {
				return true
			}
		case '−', '–', '—', '·', '⋅':
			if betweenDigits(runes, i) {
				return true
			}
		case '°', '‰':
			if prev, ok := runeBefore(runes, i); ok && unicode.IsDigit(prev) {
				return true
			}
		}
	}
	if (hasDigit || hasLetter) && countRune(runes, '|') >= 2 {
		return true
	}
	if (hasDigit || hasLetter) && commaInsideBrackets(runes) {
		return true
	}
	return false
}

func isStrongMathSymbol(r rune) bool {
	switch r {
	case '×', '÷', '≤', '≥', '≠', '±', '≈', '∞', '∑', '∏', '∫', '∂', '∇',
		'√', '∠', '△', '⊥', '∥', '≌', '∽', '∈', '∉', '⊂', '⊃', '∪', '∩',
		'∅', '→', '⇒', '⇔', '⁄':
		return true
	}
	return false
}

func isFormulaModeSymbol(r rune) bool {
	switch r {
	case '.', ':', '!', ',', '[', ']', '{', '}', '|', '_', '\'', '′', '″',
		'~', '∼', '°', '‰', '−', '–', '—', '·', '⋅', '⁄', '±', '≈', '∞',
		'∑', '∏', '∫', '∂', '∇', '√', '∠', '△', '⊥', '∥', '≌', '∽', '∈',
		'∉', '⊂', '⊃', '∪', '∩', '∅', '→', '⇒', '⇔':
		return true
	}
	return unicode.Is(unicode.No, r)
}

func foldWidthRune(r rune) rune {
	if r >= 0xFF01 && r <= 0xFF5E {
		return r - 0xFEE0
	}
	return r
}

func prevNonSpace(runes []rune, i int) (rune, bool) {
	for j := i - 1; j >= 0; j-- {
		if !unicode.IsSpace(runes[j]) {
			return runes[j], true
		}
	}
	return 0, false
}

func nextNonSpace(runes []rune, i int) (rune, bool) {
	for j := i + 1; j < len(runes); j++ {
		if !unicode.IsSpace(runes[j]) {
			return runes[j], true
		}
	}
	return 0, false
}

func runeBefore(runes []rune, i int) (rune, bool) { return prevNonSpace(runes, i) }
func runeAfter(runes []rune, i int) (rune, bool)  { return nextNonSpace(runes, i) }

func betweenDigits(runes []rune, i int) bool {
	prev, pok := prevNonSpace(runes, i)
	next, nok := nextNonSpace(runes, i)
	return pok && nok && unicode.IsDigit(prev) && unicode.IsDigit(next)
}

func betweenAlnum(runes []rune, i int) bool {
	prev, pok := prevNonSpace(runes, i)
	next, nok := nextNonSpace(runes, i)
	return pok && nok && isAlnumRune(prev) && isAlnumRune(next)
}

func alnumBefore(runes []rune, i int) bool {
	prev, ok := prevNonSpace(runes, i)
	return ok && isAlnumRune(prev)
}

func alnumAfter(runes []rune, i int) bool {
	next, ok := nextNonSpace(runes, i)
	return ok && isAlnumRune(next)
}

func alnumAfterEquals(runes []rune, i int) bool {
	for j := i + 1; j < len(runes); j++ {
		if unicode.IsSpace(runes[j]) {
			continue
		}
		if runes[j] == '=' {
			for k := j + 1; k < len(runes); k++ {
				if unicode.IsSpace(runes[k]) {
					continue
				}
				return isAlnumRune(runes[k])
			}
			return false
		}
		return isAlnumRune(runes[j])
	}
	return false
}

func isAlnumRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func countRune(runes []rune, target rune) int {
	n := 0
	for _, r := range runes {
		if r == target {
			n++
		}
	}
	return n
}

func commaInsideBrackets(runes []rune) bool {
	depth := 0
	for _, r := range runes {
		switch r {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth > 0 {
				return true
			}
		}
	}
	return false
}

func trimDecorativeEquals(s string) string {
	runes := []rune(s)
	for len(runes) > 0 && runes[len(runes)-1] == '=' {
		if len(runes) == 1 {
			break
		}
		prev := runes[len(runes)-2]
		if prev == '<' || prev == '>' || prev == '!' || prev == '=' {
			break
		}
		if unicode.IsDigit(prev) || prev == ')' || prev == ']' || prev == '}' {
			runes = runes[:len(runes)-1]
			continue
		}
		break
	}
	return string(runes)
}

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
	fa := looksLikeFormula(a)
	fb := looksLikeFormula(b)
	if !fa && !fb {
		return true
	}
	if fa != fb {
		return false
	}
	return formulaSignature(a) == formulaSignature(b)
}

// formulaSignature keeps only the operator/grouping skeleton of a formula and
// canonicalises equivalent spellings: × and * both become *, ≤ and <= both
// become <=, and so on. It lets fuzzy matching treat equivalent formulas as
// the same while NormalizeQuestion keeps their hashes distinct.
func formulaSignature(text string) string {
	runes := []rune(strings.ToLower(normalizeURLs(text)))
	var b strings.Builder
	for i := 0; i < len(runes); i++ {
		r := foldWidthRune(runes[i])
		switch r {
		case '+':
			b.WriteByte('+')
		case '-', '−', '–', '—':
			b.WriteByte('-')
		case '*', '×', '·', '⋅':
			b.WriteByte('*')
		case '/', '÷', '⁄':
			b.WriteByte('/')
		case '^':
			b.WriteByte('^')
			if next, ok := runeAfter(runes, i); ok && unicode.IsDigit(next) {
				b.WriteRune(next)
				i++
			}
		case '%':
			b.WriteByte('%')
		case '=':
			b.WriteByte('=')
		case '<':
			if next, ok := runeAfter(runes, i); ok && next == '=' {
				b.WriteString("<=")
				i++
			} else {
				b.WriteByte('<')
			}
		case '>':
			if next, ok := runeAfter(runes, i); ok && next == '=' {
				b.WriteString(">=")
				i++
			} else {
				b.WriteByte('>')
			}
		case '!':
			if next, ok := runeAfter(runes, i); ok && next == '=' {
				b.WriteString("!=")
				i++
			} else {
				b.WriteByte('!')
			}
		case '≤':
			b.WriteString("<=")
		case '≥':
			b.WriteString(">=")
		case '≠':
			b.WriteString("!=")
		case '_':
			b.WriteByte('_')
			if next, ok := runeAfter(runes, i); ok && unicode.IsDigit(next) {
				b.WriteRune(next)
				i++
			}
		case '.', ':', '(', ')', '[', ']', '{', '}', '|', '√', '±', '≈',
			'∞', '∑', '∏', '∫', '∂', '∇', '∠', '△', '⊥', '∥', '≌', '∽', '∈',
			'∉', '⊂', '⊃', '∪', '∩', '∅', '→', '⇒', '⇔', '~', '∼', '°', '‰',
			'\'', '′', '″':
			b.WriteRune(r)
		default:
			if digit, ok := superscriptDigit(r); ok {
				b.WriteByte('^')
				b.WriteByte(byte('0' + digit))
			} else if digit, ok := subscriptDigit(r); ok {
				b.WriteByte('_')
				b.WriteByte(byte('0' + digit))
			}
		}
	}
	return trimSignatureEquals(b.String())
}

func superscriptDigit(r rune) (int, bool) {
	switch r {
	case '⁰':
		return 0, true
	case '¹':
		return 1, true
	case '²':
		return 2, true
	case '³':
		return 3, true
	case '⁴':
		return 4, true
	case '⁵':
		return 5, true
	case '⁶':
		return 6, true
	case '⁷':
		return 7, true
	case '⁸':
		return 8, true
	case '⁹':
		return 9, true
	}
	return 0, false
}

func subscriptDigit(r rune) (int, bool) {
	switch r {
	case '₀':
		return 0, true
	case '₁':
		return 1, true
	case '₂':
		return 2, true
	case '₃':
		return 3, true
	case '₄':
		return 4, true
	case '₅':
		return 5, true
	case '₆':
		return 6, true
	case '₇':
		return 7, true
	case '₈':
		return 8, true
	case '₉':
		return 9, true
	}
	return 0, false
}

func trimSignatureEquals(sig string) string {
	for len(sig) > 0 && sig[len(sig)-1] == '=' {
		if len(sig) == 1 {
			break
		}
		prev := sig[len(sig)-2]
		if prev == '<' || prev == '>' || prev == '!' || prev == '=' {
			break
		}
		sig = sig[:len(sig)-1]
	}
	return sig
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

// comparisonForm canonicalises equivalent operator spellings for similarity
// scoring. NormalizeQuestion keeps the original symbols so their hashes stay
// distinct; comparison is where × and * or ≤ and <= should be treated alike.
func comparisonForm(text string) string {
	runes := []rune(strings.ToLower(normalizeURLs(text)))
	var b strings.Builder
	for _, r := range runes {
		r = foldWidthRune(r)
		switch r {
		case '×', '·', '⋅':
			b.WriteByte('*')
		case '÷', '⁄':
			b.WriteByte('/')
		case '≤':
			b.WriteString("<=")
		case '≥':
			b.WriteString(">=")
		case '≠':
			b.WriteString("!=")
		case '−', '–', '—':
			b.WriteByte('-')
		default:
			if digit, ok := superscriptDigit(r); ok {
				b.WriteByte('^')
				b.WriteByte(byte('0' + digit))
			} else if digit, ok := subscriptDigit(r); ok {
				b.WriteByte('_')
				b.WriteByte(byte('0' + digit))
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// Score computes a similarity score in [0,1]. A zero second return means the
// candidate is rejected.
func Score(query, candidate string) (float64, bool) {
	nq := comparisonForm(strings.TrimSpace(query))
	nc := comparisonForm(strings.TrimSpace(candidate))
	if nq == "" || nc == "" {
		return 0, false
	}
	if nq == nc {
		return 1.0, true
	}
	// A numeric expression must never fuzzy-match one that carries different
	// operators (e.g. "3+3=" against "3×3=").
	if !operatorsCompatible(query, candidate) {
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
