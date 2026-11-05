package cache

import "github.com/nfentik/nfentik-go/internal/match"

// queryCacheKey returns the canonical cache key suffix for a query. It delegates
// to the matcher's lookup hash so cached results share the same identity used by
// the bank's hash index, keeping cache hits and invalidation consistent.
func queryCacheKey(title string, options *string) string {
	return match.LookupKey(title, options)
}
