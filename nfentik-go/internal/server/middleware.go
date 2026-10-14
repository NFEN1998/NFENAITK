package server

import (
	"net/http"
	"time"

	"github.com/nfentik/nfentik-go/internal/store"
)

// withLogging captures requests/responses and records them as request logs.
// The /query handler emits its own richer entry, so it is skipped here.
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/query" || skipLogPaths(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		requestID := newID()
		ip := clientIP(r)
		ua := r.UserAgent()
		headers := sanitizeHeaders(r.Header)
		reqBody := readAndRestoreBody(r)

		rec := &bodyRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		elapsed := time.Since(start).Milliseconds()
		status := rec.status
		respBody := rec.capturedResponse(rec.Header().Get("Content-Type"))
		entry := store.RequestLog{
			ID: requestID, Timestamp: time.Now().Format(time.RFC3339), Method: r.Method,
			Path: r.URL.Path, Status: &status, ResponseTime: &elapsed, RequestBody: reqBody,
			ResponseBody: respBody, Headers: headers, IP: &ip, UserAgent: &ua, Stage: "completed",
		}
		s.bus.Publish(Event{
			Type: EventRequestLog, ID: requestID, Method: r.Method, Path: r.URL.Path,
			Status: &status, ResponseTime: &elapsed, RequestBody: reqBody, ResponseBody: respBody,
			Headers: headers, IP: &ip, UserAgent: &ua, Stage: "completed",
		})
		s.logRequest(entry)
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
