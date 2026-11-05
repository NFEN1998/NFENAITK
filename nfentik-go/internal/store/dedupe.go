package store

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// DedupeStatus is the observable state of one question-bank dedupe run.
type DedupeStatus struct {
	Running    bool   `json:"running"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	Merged     int64  `json:"merged"`
	Message    string `json:"message,omitempty"`
	Error      string `json:"error,omitempty"`
}

// dedupeJob guards the single background dedupe pass started from the console.
type dedupeJob struct {
	mu         sync.Mutex
	running    bool
	startedAt  time.Time
	finishedAt time.Time
	merged     int64
	message    string
	errText    string
}

func (j *dedupeJob) status() DedupeStatus {
	st := DedupeStatus{
		Running: j.running,
		Merged:  j.merged,
		Message: j.message,
		Error:   j.errText,
	}
	if !j.startedAt.IsZero() {
		st.StartedAt = j.startedAt.Format(time.RFC3339)
	}
	if !j.finishedAt.IsZero() {
		st.FinishedAt = j.finishedAt.Format(time.RFC3339)
	}
	return st
}

// DedupeStatus returns the current state without blocking a running pass.
func (s *Store) DedupeStatus() DedupeStatus {
	s.dedupe.mu.Lock()
	defer s.dedupe.mu.Unlock()
	return s.dedupe.status()
}

// StartDedupe launches one background pass that merges rows sharing the
// (QuestionHash, OptionsHash) key and returns immediately. A second call while
// a pass is running reports the current status instead of starting another.
func (s *Store) StartDedupe() (DedupeStatus, error) {
	s.dedupe.mu.Lock()
	if s.dedupe.running {
		st := s.dedupe.status()
		s.dedupe.mu.Unlock()
		return st, nil
	}
	s.dedupe.running = true
	s.dedupe.startedAt = time.Now()
	s.dedupe.finishedAt = time.Time{}
	s.dedupe.merged = 0
	s.dedupe.message = "去重任务已开始"
	s.dedupe.errText = ""
	st := s.dedupe.status()
	s.dedupe.mu.Unlock()

	ctx := s.bgCtx
	if ctx == nil {
		ctx = context.Background()
	}
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		merged, err := s.dedupeBankRows(ctx)

		s.dedupe.mu.Lock()
		s.dedupe.running = false
		s.dedupe.finishedAt = time.Now()
		s.dedupe.merged = merged
		switch {
		case err != nil:
			s.dedupe.errText = err.Error()
			s.dedupe.message = "去重失败"
		case merged > 0:
			s.dedupe.message = fmt.Sprintf("去重完成，合并 %d 行", merged)
		default:
			s.dedupe.message = "去重完成，没有重复题目"
		}
		s.dedupe.mu.Unlock()

		if err != nil {
			log.Printf("手动去重失败: %v", err)
			return
		}
		if merged > 0 {
			s.changed()
		}
	}()
	return st, nil
}
