package server

import (
	"sync"
	"time"
)

// Event types published on the SSE log stream.
const (
	EventRequestLog        = "request_log"
	EventModelCallRequest  = "model_call_request"
	EventModelCallProgress = "model_call_progress"
	EventModelCallResponse = "model_call_response"
)

// Event is one SSE message.
type Event struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`

	// request_log fields
	ID           string            `json:"id,omitempty"`
	Method       string            `json:"method,omitempty"`
	Path         string            `json:"path,omitempty"`
	Status       *int              `json:"status,omitempty"`
	ResponseTime *int64            `json:"response_time,omitempty"`
	RequestBody  *string           `json:"request_body,omitempty"`
	ResponseBody *string           `json:"response_body,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	IP           *string           `json:"ip,omitempty"`
	UserAgent    *string           `json:"user_agent,omitempty"`
	Stage        string            `json:"stage,omitempty"`

	// model call fields
	RequestID        string `json:"request_id,omitempty"`
	Query            string `json:"query,omitempty"`
	Content          string `json:"content,omitempty"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
	IsSuccess        bool   `json:"is_success,omitempty"`
}

// Bus is a fan-out event broker used by the SSE log stream.
type Bus struct {
	mu          sync.Mutex
	subscribers map[int]chan Event
	nextID      int
}

// NewBus creates an empty event bus.
func NewBus() *Bus {
	return &Bus{subscribers: make(map[int]chan Event)}
}

// Subscribe registers a new listener.
func (b *Bus) Subscribe() (int, <-chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	ch := make(chan Event, 256)
	b.subscribers[id] = ch
	return id, ch
}

// Unsubscribe removes a listener and closes its channel.
func (b *Bus) Unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subscribers[id]; ok {
		delete(b.subscribers, id)
		close(ch)
	}
}

// Publish delivers an event to every listener without blocking slow consumers.
func (b *Bus) Publish(evt Event) {
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subscribers {
		select {
		case ch <- evt:
		default:
		}
	}
}

// SubscriberCount reports the number of active listeners.
func (b *Bus) SubscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subscribers)
}
