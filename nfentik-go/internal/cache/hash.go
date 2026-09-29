package cache

import (
	"crypto/sha1"
	"encoding/hex"
)

// hashString returns a short stable hex digest used for cache keys.
func hashString(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}
