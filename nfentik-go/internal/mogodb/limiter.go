package mogodb

import (
	"context"
	"time"
)

// Limiter throttles reads to a maximum rows-per-second rate plus an optional
// fixed delay between batches, so a direct database read does not load the MOGO
// host. A zero rate and zero delay mean no throttling.
type Limiter struct {
	rowsPerSecond int
	batchDelay    time.Duration
}

// NewLimiter builds a Limiter. rowsPerSecond <= 0 disables the rate limit.
func NewLimiter(rowsPerSecond, batchDelayMs int) Limiter {
	if rowsPerSecond < 0 {
		rowsPerSecond = 0
	}
	if batchDelayMs < 0 {
		batchDelayMs = 0
	}
	return Limiter{
		rowsPerSecond: rowsPerSecond,
		batchDelay:    time.Duration(batchDelayMs) * time.Millisecond,
	}
}

// Wait blocks until reading rows rows since t0 is allowed by the configured
// rate, then adds the batch delay. It returns ctx.Err() if the context is
// cancelled while waiting.
func (l Limiter) Wait(ctx context.Context, t0 time.Time, rows int) error {
	var wait time.Duration
	if l.rowsPerSecond > 0 && rows > 0 {
		expected := time.Duration(float64(rows) / float64(l.rowsPerSecond) * float64(time.Second))
		if elapsed := time.Since(t0); elapsed < expected {
			wait = expected - elapsed
		}
	}
	wait += l.batchDelay
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
