package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// maxLogBody caps how many bytes of a request or response body are kept in a
// log entry. Larger bodies are truncated to protect the database.
const maxLogBody = 16 * 1024

// sensitiveKeys are JSON field names whose values are replaced with "***".
var sensitiveKeys = map[string]bool{
	"apikey": true, "api_key": true, "token": true, "password": true,
	"passwd": true, "secret": true, "authorization": true,
	"admintoken": true, "admin_token": true, "access_token": true,
	"refresh_token": true, "client_secret": true,
}

// sensitiveHeaders are header names whose values are replaced with "***".
var sensitiveHeaders = map[string]bool{
	"authorization": true, "proxy-authorization": true, "cookie": true,
	"set-cookie": true, "x-admin-token": true, "x-api-key": true,
	"x-auth-token": true,
}

// skipBodyPaths are paths whose request body is never captured (credentials).
var skipBodyPaths = map[string]bool{
	"/api/login": true,
}

// skipLogPaths are paths that are not logged at all.
func skipLogPaths(path string) bool {
	switch path {
	case "/", "/console", "/api/logs/stream", "/api/admin/logs":
		return true
	}
	return isAsset(path)
}

// sanitizeHeaders converts request headers into a flat map with sensitive
// values masked.
func sanitizeHeaders(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		if sensitiveHeaders[strings.ToLower(k)] {
			out[k] = "***"
			continue
		}
		out[k] = strings.Join(v, ",")
	}
	return out
}

// captureBodyString redacts and truncates a captured body for logging. It
// returns nil for empty input.
func captureBodyString(contentType string, data []byte, truncated bool) *string {
	if len(data) == 0 {
		return nil
	}
	if isJSONContentType(contentType) && json.Valid(data) {
		data = redactJSON(data)
	}
	s := string(data)
	if truncated {
		s += "\n…(内容过长已截断)"
	}
	return &s
}

func isJSONContentType(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.Contains(ct, "json") || strings.Contains(ct, "javascript")
}

// redactJSON recursively replaces values of sensitive fields with "***".
func redactJSON(data []byte) []byte {
	var v any
	if json.Unmarshal(data, &v) != nil {
		return data
	}
	redactValue(v)
	out, err := json.Marshal(v)
	if err != nil {
		return data
	}
	return out
}

func redactValue(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if sensitiveKeys[strings.ToLower(k)] {
				t[k] = "***"
				continue
			}
			redactValue(val)
		}
	case []any:
		for _, val := range t {
			redactValue(val)
		}
	}
}

// readAndRestoreBody consumes r.Body (up to the cap) and replaces it so
// downstream handlers can read it again. The returned body is already
// truncated and redacted for logging.
func readAndRestoreBody(r *http.Request) *string {
	if r.Body == nil || skipBodyPaths[r.URL.Path] {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxLogBody+1))
	_ = r.Body.Close()
	// Always restore the body, even when reading failed, so the handler runs.
	r.Body = io.NopCloser(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	truncated := len(data) > maxLogBody
	if truncated {
		data = data[:maxLogBody]
	}
	return captureBodyString(r.Header.Get("Content-Type"), data, truncated)
}

// bodyRecorder wraps a ResponseWriter, forwarding writes while buffering the
// first maxLogBody bytes of the response for logging.
type bodyRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
	buf    bytes.Buffer
}

func (w *bodyRecorder) WriteHeader(code int) {
	if !w.wrote {
		w.status = code
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *bodyRecorder) Write(b []byte) (int, error) {
	if !w.wrote {
		w.status = http.StatusOK
		w.wrote = true
	}
	if w.buf.Len() < maxLogBody {
		remaining := maxLogBody - w.buf.Len()
		if remaining > len(b) {
			remaining = len(b)
		}
		w.buf.Write(b[:remaining])
	}
	return w.ResponseWriter.Write(b)
}

// Flush keeps SSE streaming working through the wrapper.
func (w *bodyRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// capturedResponse redacts and truncates the buffered response body.
func (w *bodyRecorder) capturedResponse(contentType string) *string {
	if w.buf.Len() == 0 {
		return nil
	}
	return captureBodyString(contentType, w.buf.Bytes(), w.buf.Len() >= maxLogBody)
}
