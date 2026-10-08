package server

import (
	"net/http"
	"strings"
	"testing"
)

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
