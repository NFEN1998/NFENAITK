package ai

import (
	"context"
	"sync"
	"time"
)

// rpmWindow is the quota window used by upstream providers. Limits are
// expressed as requests per minute.
const rpmWindow = time.Minute

// rpmLimiter is an in-process sliding-window limiter keyed by provider account.
// A caller that arrives after the window is full sleeps until the oldest slot
// expires, so bursts are shaped instead of rejected. The wait is bounded by the
// caller's context, which keeps a sustained overload from queueing forever.
type rpmLimiter struct {
	mu      sync.Mutex
	windows map[string][]time.Time
}

func newRPMLimiter() *rpmLimiter {
	return &rpmLimiter{windows: make(map[string][]time.Time)}
}

// wait reserves one slot for key, blocking until the last minute holds fewer
// than limit requests. limit <= 0 disables the limit.
func (l *rpmLimiter) wait(ctx context.Context, key string, limit int) error {
	return l.waitWindow(ctx, key, limit, rpmWindow)
}

// waitWindow is wait with an explicit window, which tests use to avoid real
// one-minute sleeps.
func (l *rpmLimiter) waitWindow(ctx context.Context, key string, limit int, window time.Duration) error {
	if limit <= 0 {
		return nil
	}
	if window <= 0 {
		window = rpmWindow
	}
	for {
		l.mu.Lock()
		now := time.Now()
		cutoff := now.Add(-window)
		times := l.windows[key]
		first := 0
		for first < len(times) && !times[first].After(cutoff) {
			first++
		}
		if first > 0 {
			times = times[first:]
		}
		if len(times) < limit {
			times = append(times, now)
			l.windows[key] = times
			l.mu.Unlock()
			return nil
		}
		next := times[0].Add(window)
		l.windows[key] = times
		l.cleanupLocked(now, window)
		l.mu.Unlock()

		wait := next.Sub(now)
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// cleanupLocked drops expired keys once the map grows large so a deployment
// that renames platforms does not leak old windows.
func (l *rpmLimiter) cleanupLocked(now time.Time, window time.Duration) {
	if len(l.windows) < 1000 {
		return
	}
	cutoff := now.Add(-window)
	for key, times := range l.windows {
		if len(times) == 0 || !times[len(times)-1].After(cutoff) {
			delete(l.windows, key)
		}
	}
}
