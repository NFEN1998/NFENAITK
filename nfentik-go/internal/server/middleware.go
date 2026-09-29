package server

import (
	"net/http"
	"time"
)

// statusRecorder captures the response status for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	return r.ResponseWriter.Write(b)
}

// Flush keeps SSE streaming working through the wrapper.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// withLogging records non-asset requests and publishes them on the event bus.
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		// The query handler already emits a richer log entry.
		if r.URL.Path == "/query" || isAsset(r.URL.Path) {
			return
		}
		elapsed := time.Since(start).Milliseconds()
		status := rec.status
		ip := clientIP(r)
		ua := r.UserAgent()
		s.bus.Publish(Event{
			Type: EventRequestLog, ID: newID(), Method: r.Method, Path: r.URL.Path,
			Status: &status, ResponseTime: &elapsed, IP: &ip, UserAgent: &ua, Stage: "completed",
		})
	})
}

func isAsset(path string) bool {
	switch {
	case len(path) >= 8 && path[:8] == "/static/":
		return true
	case len(path) >= 8 && path[:8] == "/favicon":
		return true
	default:
		return false
	}
}
