package server

import (
	"bytes"
	"html/template"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestUserTemplateHasBothUrlModes(t *testing.T) {
	tmpl, err := template.ParseFS(os.DirFS("../../web"), "templates/*.html")
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "user.html", map[string]any{"SiteName": "test", "Page": "integrate"}); err != nil {
		t.Fatalf("execute user.html: %v", err)
	}
	page := buf.String()
	for _, want := range []string{"URL 模式1", "URL 模式2", `{"code":-1}`, "user-url-mogo"} {
		if !strings.Contains(page, want) {
			t.Fatalf("user page missing %q", want)
		}
	}
}

func TestUserPageRenders(t *testing.T) {
	srv, _ := testServer(t)
	cases := map[string]string{
		"/user":           "最近调用",
		"/user/":          "最近调用",
		"/user/plan":      "权益明细",
		"/user/account":   "修改密码",
		"/user/redeem":    "输入卡密",
		"/user/integrate": "URL 模式1",
		"/user/logs":      "搜索题目或答案",
		"/user/login":     "欢迎回来",
	}
	for path, marker := range cases {
		req := newJSONRequest(http.MethodGet, path, nil)
		rec := newRecorder()
		srv.handleUserPage(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s code = %d: %s", path, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, "用户中心") {
			t.Fatalf("%s page missing title", path)
		}
		if !strings.Contains(body, marker) {
			t.Fatalf("%s page missing marker %q", path, marker)
		}
	}
}

func TestUserPageRejectsUnknownPath(t *testing.T) {
	srv, _ := testServer(t)
	req := newJSONRequest(http.MethodGet, "/user/nope", nil)
	rec := newRecorder()
	srv.handleUserPage(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown page code = %d, want 404", rec.Code)
	}
}

// TestUserPagesShareTheShell ensures every page renders the shared navigation
// and assets, and that no template action leaked into the output. The login
// page is standalone but must still expose the three login forms.
func TestUserPagesShareTheShell(t *testing.T) {
	srv, _ := testServer(t)

	shellPages := []string{"/user", "/user/plan", "/user/account", "/user/redeem", "/user/integrate", "/user/logs"}
	for _, path := range shellPages {
		req := newJSONRequest(http.MethodGet, path, nil)
		rec := newRecorder()
		srv.handleUserPage(rec, req)
		body := rec.Body.String()
		for _, want := range []string{"shell-user-name", "user-alert", "user-logout", "/user/static/user.css", "/user/static/user.js"} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s missing %q", path, want)
			}
		}
		if strings.Contains(body, "{{") {
			t.Fatalf("%s contains a leaked template action", path)
		}
	}

	req := newJSONRequest(http.MethodGet, "/user/login", nil)
	rec := newRecorder()
	srv.handleUserPage(rec, req)
	body := rec.Body.String()
	for _, want := range []string{"user-login-btn", "account-login-btn", "register-btn", "data-login-tabs"} {
		if !strings.Contains(body, want) {
			t.Fatalf("login page missing %q", want)
		}
	}
}

// TestUserStaticAssets ensures the /user/ page routes do not shadow the CSS and
// JavaScript served from the embedded static directory.
func TestUserStaticAssets(t *testing.T) {
	_, ts := testServer(t)
	for _, path := range []string{"/user/static/user.css", "/user/static/user.js"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, resp.StatusCode)
		}
	}
}
