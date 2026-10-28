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
	if err := tmpl.ExecuteTemplate(&buf, "user.html", map[string]any{"SiteName": "test"}); err != nil {
		t.Fatalf("execute user.html: %v", err)
	}
	page := buf.String()
	for _, want := range []string{"URL 模式1调用", "URL 模式2调用", `{"code":-1}`, "user-url-mogo"} {
		if !strings.Contains(page, want) {
			t.Fatalf("user page missing %q", want)
		}
	}
}

func TestUserPageRenders(t *testing.T) {
	srv, _ := testServer(t)
	req := newJSONRequest(http.MethodGet, "/user", nil)
	rec := newRecorder()
	srv.handleUserPage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "用户中心") {
		t.Fatalf("page missing title")
	}
}
