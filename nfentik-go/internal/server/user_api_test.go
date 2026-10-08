package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/nfentik/nfentik-go/internal/config"
	"github.com/nfentik/nfentik-go/internal/store"
)

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func newJSONRequest(method, target string, body any) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewReader(mustJSON(body)))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func newRecorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }

// testServer wires a Server backed by PostgreSQL and the on-disk web assets.
// It skips when no database is reachable. Settings keys it mutates are
// snapshotted and restored so the shared test database keeps working.
func testServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://zerror:zerror@127.0.0.1:5432/zerror?sslmode=disable"
	}
	st, err := store.Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}

	snapApp, hadApp, _ := st.GetSetting("app_settings")
	snapModel, hadModel, _ := st.GetSetting("model_config")
	if err := st.DeleteSetting("app_settings"); err != nil {
		t.Fatalf("reset app_settings: %v", err)
	}
	if err := st.DeleteSetting("model_config"); err != nil {
		t.Fatalf("reset model_config: %v", err)
	}

	cfg, err := config.New(st, config.BootstrapFile{})
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	srv, err := New(Deps{Store: st, Config: cfg, WebFS: os.DirFS("../../web"), StorageInfo: "test"})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())

	t.Cleanup(func() {
		ts.Close()
		st.DeleteSetting("app_settings")
		st.DeleteSetting("model_config")
		if hadApp {
			_ = st.SetSetting("app_settings", snapApp)
		}
		if hadModel {
			_ = st.SetSetting("model_config", snapModel)
		}
		_, _ = st.DB().Exec(`DELETE FROM UserRequestLogs`)
		_, _ = st.DB().Exec(`DELETE FROM UserAuditLogs`)
		_, _ = st.DB().Exec(`DELETE FROM Users`)
		st.Close()
	})
	return srv, ts
}

func doJSON(t *testing.T, method, url string, body any, headers map[string]string) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var payload map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	return resp.StatusCode, payload
}

// setTokenForQuery is a helper that flips the token requirement on.
func (s *Server) setTokenForQuery(t *testing.T, required bool) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"requireTokenForQuery": required, "usersEnabled": true})
	if _, err := s.config.Patch(raw); err != nil {
		t.Fatalf("patch settings: %v", err)
	}
}

func (s *Server) createTestUser(t *testing.T, label, planCode string, total int64, days int) store.User {
	t.Helper()
	body := map[string]any{"plan_code": planCode}
	if total > 0 {
		body["total_count"] = total
	}
	if days > 0 {
		body["days"] = days
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users", bytes.NewReader(mustJSON(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.adminCreateUser(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create user %s: %d %s", label, rec.Code, rec.Body.String())
	}
	var out struct {
		User store.User `json:"user"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.User
}

func mustJSON(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}
