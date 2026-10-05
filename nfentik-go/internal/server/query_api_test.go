package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// queryWithToken calls /query directly through the handler so the response is
// deterministic and independent of the network.
func (s *Server) queryWithToken(t *testing.T, title, token string) (int, map[string]any) {
	t.Helper()
	body := map[string]any{"title": title}
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	req := newJSONRequest(http.MethodPost, "/query", body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := newRecorder()
	s.handleQuery(rec, req)
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return rec.Code, payload
}

func TestQueryRequiresTokenWhenConfigured(t *testing.T) {
	srv, _ := testServer(t)
	srv.setTokenForQuery(t, true)

	code, payload := srv.queryWithToken(t, "1+1=", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("missing token code = %d (%v), want 401", code, payload)
	}
	code, _ = srv.queryWithToken(t, "1+1=", "invalid")
	if code != http.StatusUnauthorized {
		t.Fatalf("invalid token code = %d, want 401", code)
	}
}

func TestQueryDeniesExpiredAndExhausted(t *testing.T) {
	srv, _ := testServer(t)
	srv.setTokenForQuery(t, true)

	exhausted := srv.createTestUser(t, "exhausted", "count", 1, 0)
	code, _ := srv.queryWithToken(t, "1+1=", exhausted.Token)
	if code == http.StatusForbidden {
		t.Fatalf("first query should be allowed, got 403")
	}
	// Remain hits zero after the first consume; the next query is denied.
	code, payload := srv.queryWithToken(t, "1+1=", exhausted.Token)
	if code != http.StatusForbidden {
		t.Fatalf("exhausted code = %d (%v), want 403", code, payload)
	}
}

func TestQueryConsumesCountButNotDuration(t *testing.T) {
	srv, _ := testServer(t)
	srv.setTokenForQuery(t, true)

	count := srv.createTestUser(t, "count", "count", 3, 0)
	srv.queryWithToken(t, "1+1=", count.Token)
	got, _ := srv.store.GetUserByID(count.ID)
	if got.UsedCount != 1 || *got.RemainCount != 2 {
		t.Fatalf("count plan used=%d remain=%d, want 1/2", got.UsedCount, *got.RemainCount)
	}

	duration := srv.createTestUser(t, "dur", "monthly", 0, 0)
	srv.queryWithToken(t, "1+1=", duration.Token)
	gotDur, _ := srv.store.GetUserByID(duration.ID)
	if gotDur.TotalCount != nil || gotDur.RemainCount != nil {
		t.Fatalf("duration plan gained a count quota")
	}
	code, _ := srv.queryWithToken(t, "1+1=", duration.Token)
	if code == http.StatusForbidden {
		t.Fatalf("duration plan was denied")
	}
}

func TestQueryAnonymousAllowedWhenOptional(t *testing.T) {
	srv, _ := testServer(t)
	// usersEnabled true but requireTokenForQuery false.
	raw := []byte(`{"usersEnabled":true,"requireTokenForQuery":false}`)
	if _, err := srv.config.Patch(raw); err != nil {
		t.Fatalf("patch: %v", err)
	}
	code, _ := srv.queryWithToken(t, "1+1=", "")
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		t.Fatalf("anonymous query denied while optional: %d", code)
	}
}
