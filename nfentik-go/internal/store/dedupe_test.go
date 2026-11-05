package store

import (
	"testing"
	"time"
)

// TestStartDedupeCompletes drives the manual dedupe lifecycle: StartDedupe must
// return immediately with a running status, then settle into a finished state.
func TestStartDedupeCompletes(t *testing.T) {
	s := testStore(t)

	status, err := s.StartDedupe()
	if err != nil {
		t.Fatalf("StartDedupe: %v", err)
	}
	if !status.Running {
		t.Fatalf("status.Running = false immediately after start")
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		current := s.DedupeStatus()
		if !current.Running {
			if current.Error != "" {
				t.Fatalf("dedupe error: %s", current.Error)
			}
			if current.FinishedAt == "" {
				t.Fatalf("finished run has no FinishedAt: %+v", current)
			}
			if current.Merged < 0 {
				t.Fatalf("merged = %d", current.Merged)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("dedupe did not finish in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
