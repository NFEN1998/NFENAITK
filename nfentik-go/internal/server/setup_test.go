package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/nfentik/nfentik-go/internal/config"
)

// newSetupServer builds a wizard with no database, mirroring a true first run.
func newSetupServer(t *testing.T) *SetupServer {
	t.Helper()
	s, err := NewSetupServer(os.DirFS("../../web"), config.BootstrapFile{}, nil, nil, "尚未配置数据库连接")
	if err != nil {
		t.Fatalf("NewSetupServer: %v", err)
	}
	return s
}

func TestSetupRedirectsUnknownPaths(t *testing.T) {
	h := newSetupServer(t).Handler()
	rec := newRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/setup" {
		t.Fatalf("root = %d %q, want 302 /setup", rec.Code, rec.Header().Get("Location"))
	}
}

func TestSetupStateIsPrefilled(t *testing.T) {
	s := newSetupServer(t)
	h := s.Handler()
	rec := newRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/setup/state", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"success":true`, config.DefaultSiteName, config.DefaultSiteSubtitle} {
		if !strings.Contains(body, want) {
			t.Fatalf("state %q missing %q", body, want)
		}
	}
}

func TestSetupRejectsMissingFields(t *testing.T) {
	h := newSetupServer(t).Handler()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"no database", `{"database_url":"","admin_token":"x"}`, "PostgreSQL"},
		{"no token", `{"database_url":"postgres://x","admin_token":""}`, "管理员令牌"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/setup", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := newRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("body %q missing %q", rec.Body.String(), tc.want)
			}
		})
	}
}

func TestSetupPageRenders(t *testing.T) {
	h := newSetupServer(t).Handler()
	rec := newRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/setup", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "安装配置向导") {
		t.Fatalf("setup page missing wizard heading")
	}
}
