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

// jieba holds a singleton segmenter because loading the dictionary is expensive.
var jieba = gojieba.NewJieba()

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
