package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func nfentkGet(t *testing.T, srv *Server, query string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/nfentk?"+query, nil)
	rec := httptest.NewRecorder()
	srv.handleNfentk(rec, req)
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return rec.Code, payload
}

func nfentkQuestion(t *testing.T, srv *Server, title, answer string) int64 {
	t.Helper()
	id, err := srv.store.InsertAIResponse(title, answer, nil, nil)
	if err != nil {
		t.Fatalf("insert question: %v", err)
	}
	t.Cleanup(func() { _ = srv.store.DeleteQuestion(id) })
	return id
}

func TestNfentkReturnsMogoShape(t *testing.T) {
	srv, _ := testServer(t)
	title := uniqueQuestion("nfentk命中")
	nfentkQuestion(t, srv, title, "答案")

	code, payload := nfentkGet(t, srv, "q="+url.QueryEscape(title)+"&info="+url.QueryEscape("简答题****1"))
	if code != http.StatusOK {
		t.Fatalf("http code = %d", code)
	}
	if payload["code"] != float64(1) || payload["data"] != "答案" {
		t.Fatalf("payload = %v", payload)
	}
	if _, ok := payload["data"].(map[string]any); ok {
		t.Fatalf("data must be a string: %v", payload)
	}
}

func TestNfentkFormatsMultipleChoice(t *testing.T) {
	srv, _ := testServer(t)
	title := uniqueQuestion("nfentk多选")
	options := "A. 选项1\nB. 选项2\nC. 选项3"
	id, err := srv.store.InsertAIResponse(title, "选项1###选项3", &options, nil)
	if err != nil {
		t.Fatalf("insert question: %v", err)
	}
	t.Cleanup(func() { _ = srv.store.DeleteQuestion(id) })

	info := "多选题****[选项1###选项2###选项3]"
	code, payload := nfentkGet(t, srv, "q="+url.QueryEscape(title)+"&info="+url.QueryEscape(info))
	if code != http.StatusOK || payload["code"] != float64(1) {
		t.Fatalf("response = %d %v", code, payload)
	}
	if payload["data"] != "选项1###选项3" {
		t.Fatalf("data = %v", payload["data"])
	}
}

func TestNfentkFailureShape(t *testing.T) {
	srv := &Server{}
	code, payload := nfentkGet(t, srv, "info="+url.QueryEscape("单选题****[A###B]"))
	if code != http.StatusOK {
		t.Fatalf("http code = %d", code)
	}
	if payload["code"] != float64(-1) {
		t.Fatalf("payload = %v", payload)
	}
	if _, ok := payload["data"]; ok {
		t.Fatalf("failure must not carry data: %v", payload)
	}
}

func TestNfentkRejectsNonGet(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/nfentk?q=题目", nil)
	rec := httptest.NewRecorder()
	srv.handleNfentk(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestNfentkKeyAndTokenAreEquivalent(t *testing.T) {
	srv, _ := testServer(t)
	srv.setTokenForQuery(t, true)
	user := srv.createTestUser(t, "nfentk-key", "count", 5, 0)
	title := uniqueQuestion("nfentk认证")
	nfentkQuestion(t, srv, title, "答案")

	query := "q=" + url.QueryEscape(title)
	if code, payload := nfentkGet(t, srv, "key="+user.Token+"&"+query); code != http.StatusOK || payload["code"] != float64(1) {
		t.Fatalf("key auth: %d %v", code, payload)
	}
	if code, payload := nfentkGet(t, srv, "token="+user.Token+"&"+query); code != http.StatusOK || payload["code"] != float64(1) {
		t.Fatalf("token auth: %d %v", code, payload)
	}

	got, err := srv.store.GetUserByID(user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if got.UsedCount != 2 {
		t.Fatalf("used count = %d, want 2", got.UsedCount)
	}
}

func TestNfentkMissingTokenReturnsFailure(t *testing.T) {
	srv, _ := testServer(t)
	srv.setTokenForQuery(t, true)

	code, payload := nfentkGet(t, srv, "q=1%2B1%3D")
	if code != http.StatusOK {
		t.Fatalf("http code = %d", code)
	}
	if payload["code"] != float64(-1) {
		t.Fatalf("payload = %v", payload)
	}
}

func TestFirstQueryValueSkipsPlaceholder(t *testing.T) {
	if got := firstQueryValue([]string{"", "题目"}); got != "题目" {
		t.Fatalf("got %q", got)
	}
	if got := firstQueryValue([]string{"  ", ""}); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildNfentkQueryMapsInfo(t *testing.T) {
	info, req := buildNfentkQuery("题目", "多选题****[选项1###选项2###选项3]")
	if info.Type != "多选题" {
		t.Fatalf("info type = %q", info.Type)
	}
	if req.Title != "题目" || req.Type == nil || *req.Type != "多选题" {
		t.Fatalf("request = %+v", req)
	}
	if req.Options == nil || *req.Options != "A. 选项1\nB. 选项2\nC. 选项3" {
		t.Fatalf("options = %v", req.Options)
	}
	if req.ExtraPrompt != "" {
		t.Fatalf("extra prompt = %q", req.ExtraPrompt)
	}
}

func TestBuildNfentkQueryDiscussionContext(t *testing.T) {
	_, req := buildNfentkQuery("标题", "学习通讨论****1****正文:谈谈看法****限制长度:50")
	if req.Type == nil || *req.Type != "学习通讨论" {
		t.Fatalf("type = %v", req.Type)
	}
	if !strings.Contains(req.ExtraPrompt, "谈谈看法") || !strings.Contains(req.ExtraPrompt, "50") {
		t.Fatalf("extra prompt = %q", req.ExtraPrompt)
	}
}

func TestNfentkFailureWriter(t *testing.T) {
	rec := newRecorder()
	writeNfentkFailure(rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"code":-1}` {
		t.Fatalf("body = %q", got)
	}
}

func TestQueryShapeStaysOcsCompatible(t *testing.T) {
	srv, _ := testServer(t)
	srv.setTokenForQuery(t, true)
	user := srv.createTestUser(t, "ocs-shape", "count", 3, 0)
	title := uniqueQuestion("ocs-shape")
	nfentkQuestion(t, srv, title, "答案")

	req := httptest.NewRequest(http.MethodGet, "/query?title="+url.QueryEscape(title)+"&token="+user.Token, nil)
	rec := httptest.NewRecorder()
	srv.handleQuery(rec, req)
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("ocs data must stay an object: %v", payload)
	}
	if data["question"] != title {
		t.Fatalf("question = %v, want %v", data["question"], title)
	}
}
