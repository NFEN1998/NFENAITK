package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func uniqueQuestion(prefix string) string {
	return fmt.Sprintf("%s %d", prefix, time.Now().UnixNano())
}

func saveExternalBanks(t *testing.T, srv *Server, sources []map[string]any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"sources": sources})
	if err != nil {
		t.Fatalf("marshal external banks: %v", err)
	}
	if err := srv.config.SaveExternalBanks(raw); err != nil {
		t.Fatalf("save external banks: %v", err)
	}
	t.Cleanup(func() { _ = srv.store.DeleteSetting("external_banks") })
}

func TestExternalBankHitIsReturnedAndWrittenBack(t *testing.T) {
	srv, _ := testServer(t)
	question := uniqueQuestion("外部链路命中测试")
	answer := "外部答案"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fmt.Sprintf(`{"data":{"question":%q,"answer":%q}}`, question, answer)))
	}))
	defer upstream.Close()

	saveExternalBanks(t, srv, []map[string]any{{
		"id": "s1", "name": "demo", "enabled": true, "priority": 10,
		"url": upstream.URL, "method": "GET",
		"answerPath": "data.answer", "questionPath": "data.question",
		"writeBack": true, "timeoutSeconds": 5,
	}})

	code, payload := srv.queryWithToken(t, question, "")
	if code != http.StatusOK {
		t.Fatalf("code = %d (%v)", code, payload)
	}
	data, _ := payload["data"].(map[string]any)
	if data == nil || data["answer"] != answer {
		t.Fatalf("payload = %v", payload)
	}

	hit, err := srv.store.FindByNormalizedQuestion(question)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if hit == nil || hit.Answer != answer {
		t.Fatalf("answer not written back: %+v", hit)
	}
	t.Cleanup(func() {
		if hit != nil {
			_ = srv.store.DeleteQuestion(hit.ID)
		}
	})
}

func TestExternalBankRejectsMismatchedQuestion(t *testing.T) {
	srv, _ := testServer(t)
	question := uniqueQuestion("外部链路校验测试")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"question":"完全不同的另一道题目 xyz","answer":"不应采信"}}`))
	}))
	defer upstream.Close()

	saveExternalBanks(t, srv, []map[string]any{{
		"id": "s1", "name": "demo", "enabled": true, "priority": 10,
		"url": upstream.URL, "method": "GET",
		"answerPath": "data.answer", "questionPath": "data.question",
		"writeBack": true, "timeoutSeconds": 5,
	}})

	_, payload := srv.queryWithToken(t, question, "")
	if payload["data"] != nil {
		t.Fatalf("mismatched answer was accepted: %v", payload)
	}
	msg, _ := payload["message"].(string)
	if !strings.Contains(msg, "模型调用失败") {
		t.Fatalf("expected fallback to model, got %v", payload)
	}
	hit, err := srv.store.FindByNormalizedQuestion(question)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if hit != nil {
		t.Fatalf("rejected answer was written back: %+v", hit)
	}
}

func TestExternalBankTrustModeAcceptsAnyQuestion(t *testing.T) {
	srv, _ := testServer(t)
	question := uniqueQuestion("外部链路信任模式测试")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"answer":"信任采信"}}`))
	}))
	defer upstream.Close()

	saveExternalBanks(t, srv, []map[string]any{{
		"id": "s1", "name": "trusted", "enabled": true, "priority": 10,
		"url": upstream.URL, "method": "GET",
		"answerPath": "data.answer", "trust": true, "writeBack": false, "timeoutSeconds": 5,
	}})

	code, payload := srv.queryWithToken(t, question, "")
	if code != http.StatusOK {
		t.Fatalf("code = %d (%v)", code, payload)
	}
	data, _ := payload["data"].(map[string]any)
	if data == nil || data["answer"] != "信任采信" {
		t.Fatalf("payload = %v", payload)
	}
	// writeBack is false, so nothing should be persisted under this question.
	hit, err := srv.store.FindByNormalizedQuestion(question)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if hit != nil {
		t.Fatalf("non-writeBack source persisted a row: %+v", hit)
	}
}

func TestQueryRejectsEmptyTitle(t *testing.T) {
	srv, _ := testServer(t)
	code, payload := srv.queryWithToken(t, "   ", "")
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d (%v), want 400", code, payload)
	}
}

func TestAdminExternalBanksValidation(t *testing.T) {
	srv, _ := testServer(t)
	defer srv.store.DeleteSetting("external_banks")

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"empty name", map[string]any{"sources": []map[string]any{{"name": "", "url": "https://x.example/q"}}}, "显示名称不能为空"},
		{"empty url", map[string]any{"sources": []map[string]any{{"name": "n", "url": ""}}}, "请求地址不能为空"},
		{"invalid url", map[string]any{"sources": []map[string]any{{"name": "n", "url": "ftp://x"}}}, "请求地址无效"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRecorder()
			req := newJSONRequest(http.MethodPut, "/api/admin/external-banks", tc.body)
			srv.adminExternalBanks(rec, req, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400 (%s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("body %q missing %q", rec.Body.String(), tc.want)
			}
		})
	}

	rec := newRecorder()
	req := newJSONRequest(http.MethodPut, "/api/admin/external-banks", map[string]any{
		"sources": []map[string]any{{"name": "n", "url": "https://x.example/q", "enabled": true}},
	})
	srv.adminExternalBanks(rec, req, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid save code = %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestAdminExternalBankTestEndpoint(t *testing.T) {
	srv, _ := testServer(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"answer":"连通答案"}}`))
	}))
	defer upstream.Close()

	rec := newRecorder()
	req := newJSONRequest(http.MethodPost, "/api/admin/external-banks/test", map[string]any{
		"source": map[string]any{
			"name": "t", "url": upstream.URL + "/q?title={question}",
			"method": "GET", "answerPath": "data.answer",
		},
		"question": "1+1=",
	})
	srv.adminExternalBanks(rec, req, []string{"test"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "连通答案") {
		t.Fatalf("test endpoint = %d %s", rec.Code, rec.Body.String())
	}

	rec = newRecorder()
	req = newJSONRequest(http.MethodPost, "/api/admin/external-banks/test", map[string]any{
		"source": map[string]any{"url": "ftp://x"}, "question": "1+1=",
	})
	srv.adminExternalBanks(rec, req, []string{"test"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid url code = %d", rec.Code)
	}
}
