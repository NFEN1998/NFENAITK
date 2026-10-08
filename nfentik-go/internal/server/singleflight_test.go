package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockModelSSE returns an SSE endpoint that replies with a canned answer and
// counts how many requests reached it. A short delay widens the window during
// which concurrent callers can observe the in-flight call.
func mockModelSSE(t *testing.T, calls *int64, delay time.Duration) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(calls, 1)
		if delay > 0 {
			time.Sleep(delay)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		chunk := `data: {"choices":[{"delta":{"content":"{\"answer\":\"42\"}"}}]}` + "\n\n"
		fmt.Fprint(w, chunk)
		if flusher != nil {
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

// configureMockModel points the selected text model at the mock SSE server.
func configureMockModel(t *testing.T, srv *Server, baseURL string) {
	t.Helper()
	cfg := map[string]any{
		"selectedTextModels": []string{"mock-model"},
		"platforms": []map[string]any{{
			"id":          "mock",
			"name":        "mock",
			"displayName": "mock",
			"baseUrl":     baseURL,
			"apiKey":      "test",
			"enabled":     true,
			"models": []map[string]any{{
				"id": "mock-model", "name": "mock-model", "displayName": "mock-model",
				"platformId": "mock", "enabled": true, "category": "text", "maxTokens": 128,
			}},
		}},
	}
	raw, _ := json.Marshal(cfg)
	if err := srv.config.SaveModelConfig(raw); err != nil {
		t.Fatalf("save model config: %v", err)
	}
}

// TestResolveSingleFlightCoalescesConcurrentQueries verifies that identical
// questions arriving at the same time trigger exactly one AI request.
func TestResolveSingleFlightCoalescesConcurrentQueries(t *testing.T) {
	srv, _ := testServer(t)

	var calls int64
	mock := mockModelSSE(t, &calls, 150*time.Millisecond)
	defer mock.Close()
	configureMockModel(t, srv, mock.URL)

	// A unique question guarantees a cache/bank miss and forces the AI path.
	title := fmt.Sprintf("single-flight-%d", time.Now().UnixNano())
	req := QueryRequest{Title: title}

	const n = 20
	var wg sync.WaitGroup
	statuses := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			statuses[idx], _, _ = srv.resolveQuery(context.Background(), req, "http://test")
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("AI upstream calls = %d, want 1 (single-flight failed)", got)
	}
	for i, code := range statuses {
		if code != http.StatusOK {
			t.Fatalf("caller %d status = %d, want 200", i, code)
		}
	}
}

// TestResolveSingleFlightDistinctQuestionsNotCoalesced verifies different
// questions still run their own AI calls.
func TestResolveSingleFlightDistinctQuestionsNotCoalesced(t *testing.T) {
	srv, _ := testServer(t)

	var calls int64
	mock := mockModelSSE(t, &calls, 80*time.Millisecond)
	defer mock.Close()
	configureMockModel(t, srv, mock.URL)

	base := time.Now().UnixNano()
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := QueryRequest{Title: fmt.Sprintf("distinct-%d-%d", base, idx)}
			srv.resolveQuery(context.Background(), req, "http://test")
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt64(&calls); got != 3 {
		t.Fatalf("AI upstream calls = %d, want 3", got)
	}
}

// TestResolveSharedClearsInflight ensures a completed call is removed so the
// next identical question runs (and hits the cache) rather than reusing a stale
// in-flight entry.
func TestResolveSharedClearsInflight(t *testing.T) {
	srv, _ := testServer(t)

	var calls int64
	mock := mockModelSSE(t, &calls, 0)
	defer mock.Close()
	configureMockModel(t, srv, mock.URL)

	title := fmt.Sprintf("clear-%d", time.Now().UnixNano())
	req := QueryRequest{Title: title}

	srv.resolveQuery(context.Background(), req, "http://test")
	srv.pendingMu.Lock()
	pending := len(srv.pending)
	srv.pendingMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending map not cleared, has %d entries", pending)
	}

	// Second call is served from cache, so no extra upstream call. Cache is
	// nil in tests, so it re-resolves but must not error.
	code, _, _ := srv.resolveQuery(context.Background(), req, "http://test")
	if code != http.StatusOK {
		t.Fatalf("second resolve status = %d, want 200", code)
	}
}
