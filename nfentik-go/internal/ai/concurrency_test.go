package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// streamOnce writes a minimal SSE completion then DONE.
func streamOnce(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"{\"answer\":\"ok\"}"}}]}`+"\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// TestCompleteRespectsConcurrencyLimit verifies the client never runs more
// completions at once than its configured cap.
func TestCompleteRespectsConcurrencyLimit(t *testing.T) {
	const limit = 3
	var inFlight, peak int64

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt64(&inFlight, 1)
		for {
			old := atomic.LoadInt64(&peak)
			if cur <= old || atomic.CompareAndSwapInt64(&peak, old, cur) {
				break
			}
		}
		time.Sleep(120 * time.Millisecond)
		streamOnce(w)
		atomic.AddInt64(&inFlight, -1)
	}))
	defer upstream.Close()

	client := NewWithLimits(limit)
	opt := Options{BaseURL: upstream.URL, Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = client.Complete(context.Background(), opt, nil)
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&peak); got > limit {
		t.Fatalf("peak concurrency = %d, want <= %d", got, limit)
	}
	if atomic.LoadInt64(&peak) == 0 {
		t.Fatalf("upstream never observed a call")
	}
}

// TestCompleteUnlimitedByDefault verifies New() applies no cap.
func TestCompleteUnlimitedByDefault(t *testing.T) {
	var peak int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt64(&inFlightCounter, 1)
		for {
			old := atomic.LoadInt64(&peak)
			if cur <= old || atomic.CompareAndSwapInt64(&peak, old, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		streamOnce(w)
		atomic.AddInt64(&inFlightCounter, -1)
	}))
	defer upstream.Close()

	client := New()
	opt := Options{BaseURL: upstream.URL, Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = client.Complete(context.Background(), opt, nil)
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&peak); got < 2 {
		t.Fatalf("expected concurrent calls without a cap, peak = %d", got)
	}
}

var inFlightCounter int64
