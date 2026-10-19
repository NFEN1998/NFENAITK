package match

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// digest returns the hex SHA-256 of a normalized string.
func digest(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// QuestionHash returns a stable SHA-256 hex digest of the normalized question
// text plus its URL identity. Normalization collapses case, whitespace and
// punctuation so questions that differ only in formatting share the same hash.
// URLs are replaced by a placeholder during normalization, so the sorted URL
// set is hashed separately; without it two different image questions with the
// same text would share a bank row and answer each other's queries.
func QuestionHash(question string) string {
	urls := ExtractURLs(question)
	return digest(NormalizeQuestion(question) + "\x00" + strings.Join(urls, "\n"))
}

// OptionsHash returns the hash of normalized option text, or an empty string when
// no options are present. It is combined with QuestionHash to discriminate
// between entries that share a question but offer different choices. The
// normalization intentionally matches the historical algorithm so existing
// OptionsHash values stay valid.
func OptionsHash(options *string) string {
	if options == nil {
		return ""
	}
	normalized := NormalizeQuestion(*options)
	if normalized == "" {
		return ""
	}
	return digest(normalized)
}

// LookupKey combines the question and option hashes into the single key used by
// the bank hash column and the query cache. The two digests are joined with a
// separator so a question without options never collides with one that has them.
func LookupKey(question string, options *string) string {
	return QuestionHash(question) + "\x00" + OptionsHash(options)
}

// TrimHash normalizes a user supplied hash (lowercase, trimmed) so lookups are
// resilient to casing differences.
func TrimHash(hash string) string {
	return strings.ToLower(strings.TrimSpace(hash))
}
