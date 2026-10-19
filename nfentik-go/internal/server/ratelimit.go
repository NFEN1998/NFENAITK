package server

import (
	"sync"
	"time"
)

// rateBucket is one fixed window counter.
type rateBucket struct {
	count int
	reset time.Time
}

// rateLimiter is a small in-process fixed-window limiter. It is intentionally
// simple: the service runs as a single binary, and a multi-instance deployment
// can swap this for a Redis-backed limiter later.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]rateBucket
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{buckets: make(map[string]rateBucket)}
}

// allow increments the key and reports whether it is still within the limit.
func (l *rateLimiter) allow(key string, limit int, window time.Duration) bool {
	if limit <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.cleanupLocked(now)
	b, ok := l.buckets[key]
	if !ok || now.After(b.reset) {
		b = rateBucket{count: 0, reset: now.Add(window)}
	}
	b.count++
	l.buckets[key] = b
	return b.count <= limit
}

// cleanupLocked drops expired buckets once the map grows large.
func (l *rateLimiter) cleanupLocked(now time.Time) {
	if len(l.buckets) < 10000 {
		return
	}
	for key, b := range l.buckets {
		if now.After(b.reset) {
			delete(l.buckets, key)
		}
	}
}

// check reports whether the key is below the limit without incrementing it.
func (l *rateLimiter) check(key string, limit int, window time.Duration) bool {
	if limit <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok || time.Now().After(b.reset) {
		return true
	}
	return b.count < limit
}

// hit records a failure for the key.
func (l *rateLimiter) hit(key string, window time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.cleanupLocked(now)
	b, ok := l.buckets[key]
	if !ok || now.After(b.reset) {
		b = rateBucket{count: 0, reset: now.Add(window)}
	}
	b.count++
	l.buckets[key] = b
}

// reset clears the key after a successful attempt.
func (l *rateLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}
