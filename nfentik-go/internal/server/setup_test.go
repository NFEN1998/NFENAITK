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
		{"no host", `{"db_host":"","db_user":"u","db_name":"d","admin_token":"x"}`, "主机"},
		{"no token", `{"db_host":"127.0.0.1","db_port":"5432","db_user":"u","db_name":"d","admin_token":""}`, "管理员令牌"},
		{"bad port", `{"db_host":"127.0.0.1","db_port":"abc","db_user":"u","db_name":"d","admin_token":"x"}`, "端口"},
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

func TestDBFieldsDSN(t *testing.T) {
	f := dbFields{Host: "127.0.0.1", Port: "5432", User: "zerror", Password: "p@ss:w0rd", Name: "zerror", SSLMode: "disable"}
	got := f.dsn()
	if !strings.HasPrefix(got, "postgres://zerror:") {
		t.Fatalf("dsn %q missing user", got)
	}
	if !strings.Contains(got, "@127.0.0.1:5432/zerror") {
		t.Fatalf("dsn %q missing host/path", got)
	}
	if !strings.Contains(got, "sslmode=disable") {
		t.Fatalf("dsn %q missing sslmode", got)
	}
	// Round-trip: parsing must recover the same fields.
	back := parseDSN(got)
	if back.Host != f.Host || back.Port != f.Port || back.User != f.User ||
		back.Password != f.Password || back.Name != f.Name || back.SSLMode != f.SSLMode {
		t.Fatalf("round-trip mismatch: %+v vs %+v", back, f)
	}
}

func TestDBFieldsDSNDefaults(t *testing.T) {
	got := dbFields{}.dsn()
	if !strings.Contains(got, "@127.0.0.1:5432") {
		t.Fatalf("default dsn %q should point at local postgres", got)
	}
	if !strings.Contains(got, "sslmode=disable") {
		t.Fatalf("default dsn %q should default sslmode=disable", got)
	}
}

func TestSetupTestDBRequiresFields(t *testing.T) {
	h := newSetupServer(t).Handler()
	req := httptest.NewRequest(http.MethodPost, "/setup/test-db", strings.NewReader(`{"db_host":"","db_user":"","db_name":""}`))
	req.Header.Set("Content-Type", "application/json")
	rec := newRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
