package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRPMLimiterShapesBursts(t *testing.T) {
	l := newRPMLimiter()
	ctx := context.Background()
	window := 150 * time.Millisecond
	start := time.Now()

	if err := l.waitWindow(ctx, "acct", 2, window); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := l.waitWindow(ctx, "acct", 2, window); err != nil {
		t.Fatalf("second: %v", err)
	}
	// The third request must wait for the first slot to age out.
	if err := l.waitWindow(ctx, "acct", 2, window); err != nil {
		t.Fatalf("third: %v", err)
	}
	if elapsed := time.Since(start); elapsed < window-time.Millisecond {
		t.Fatalf("third call returned after %v, want at least %v", elapsed, window)
	}
}

func TestRPMLimiterKeepsAccountsIndependent(t *testing.T) {
	l := newRPMLimiter()
	ctx := context.Background()
	if err := l.waitWindow(ctx, "a", 1, time.Hour); err != nil {
		t.Fatalf("a: %v", err)
	}
	start := time.Now()
	if err := l.waitWindow(ctx, "b", 1, time.Hour); err != nil {
		t.Fatalf("b: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("key b blocked on key a for %v", elapsed)
	}
}

func TestRPMLimiterHonoursContext(t *testing.T) {
	l := newRPMLimiter()
	if err := l.waitWindow(context.Background(), "acct", 1, time.Hour); err != nil {
		t.Fatalf("first: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := l.waitWindow(ctx, "acct", 1, time.Hour); err == nil {
		t.Fatal("expected a context error when the quota is exhausted")
	}
}

func TestPlatformEffectiveRPMLimit(t *testing.T) {
	cases := []struct {
		name string
		p    Platform
		want int
	}{
		{"default is free", Platform{}, RPMFree},
		{"free", Platform{Tier: "free"}, RPMFree},
		{"enterprise", Platform{Tier: "enterprise"}, RPMEnterprise},
		{"explicit override", Platform{Tier: "enterprise", RPMLimit: 5}, 5},
		{"unlimited", Platform{RPMLimit: -1}, -1},
	}
	for _, c := range cases {
		if got := c.p.EffectiveRPMLimit(); got != c.want {
			t.Errorf("%s: EffectiveRPMLimit() = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestCompleteWaitsForRPMQuota verifies the client wires the per-platform
// quota into every completion: once the window is full, a call with a short
// deadline fails instead of hitting the provider.
func TestCompleteWaitsForRPMQuota(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streamOnce(w)
	}))
	defer upstream.Close()

	client := New()
	opt := Options{
		BaseURL:  upstream.URL,
		Model:    "m",
		Messages: []Message{{Role: "user", Content: "hi"}},
		RPMLimit: 1,
		RateKey:  "acct",
	}
	if _, _, err := client.Complete(context.Background(), opt, nil); err != nil {
		t.Fatalf("first call: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := client.Complete(ctx, opt, nil); err == nil {
		t.Fatal("second call should have waited for the RPM window and timed out")
	}
}
