package match

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// QuestionHash returns a stable SHA-256 hex digest of the normalized question
// text. Normalization collapses case, whitespace and punctuation so questions
// that differ only in formatting share the same hash. The digest is the primary
// lookup key for the bank, turning exact-match lookups into indexed B-tree
// probes instead of full-table scans.
func QuestionHash(question string) string {
	sum := sha256.Sum256([]byte(NormalizeQuestion(question)))
	return hex.EncodeToString(sum[:])
}

// OptionsHash returns the hash of normalized option text, or an empty string when
// no options are present. It is combined with QuestionHash to discriminate
// between entries that share a question but offer different choices.
func OptionsHash(options *string) string {
	if options == nil {
		return ""
	}
	normalized := NormalizeQuestion(*options)
	if normalized == "" {
		return ""
	}
	return QuestionHash(normalized)
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
