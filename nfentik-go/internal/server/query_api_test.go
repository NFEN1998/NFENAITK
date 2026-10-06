package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nfentik/nfentik-go/internal/store"
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

func (s *Server) queryWithBodyToken(t *testing.T, title, token string) (int, map[string]any) {
	t.Helper()
	body := map[string]any{"title": title, "token": token}
	req := newJSONRequest(http.MethodPost, "/query", body)
	rec := newRecorder()
	s.handleQuery(rec, req)
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return rec.Code, payload
}

func TestQueryAcceptsBodyToken(t *testing.T) {
	srv, _ := testServer(t)
	srv.setTokenForQuery(t, true)

	u := srv.createTestUser(t, "bodytok", "count", 2, 0)
	code, payload := srv.queryWithBodyToken(t, "1+1=", u.Token)
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		t.Fatalf("body token denied: %d (%v)", code, payload)
	}
	got, _ := srv.store.GetUserByID(u.ID)
	if got.UsedCount != 1 || *got.RemainCount != 1 {
		t.Fatalf("body token did not consume quota: used=%d remain=%d", got.UsedCount, *got.RemainCount)
	}

	// A missing or invalid body token must still be rejected.
	if code, _ := srv.queryWithBodyToken(t, "1+1=", ""); code != http.StatusUnauthorized {
		t.Fatalf("missing body token code = %d, want 401", code)
	}
	if code, _ := srv.queryWithBodyToken(t, "1+1=", "nope"); code != http.StatusUnauthorized {
		t.Fatalf("invalid body token code = %d, want 401", code)
	}
}

// TestQueryBodyTokenNotLogged ensures the secret never reaches request logs.
func TestQueryBodyTokenNotLogged(t *testing.T) {
	srv, _ := testServer(t)
	srv.setTokenForQuery(t, true)

	u := srv.createTestUser(t, "secret", "count", 2, 0)
	srv.queryWithBodyToken(t, "1+1=", u.Token)

	logs, _, err := srv.store.RequestLogsFiltered(store.RequestLogFilter{Path: "/query", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	for _, l := range logs {
		if l.RequestBody != nil && strings.Contains(*l.RequestBody, u.Token) {
			t.Fatalf("token leaked into request body log: %s", *l.RequestBody)
		}
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
