package server

import (
	"net/http"
	"testing"

	"github.com/nfentik/nfentik-go/internal/externalbank"
)

func sp(v string) *string { return &v }

func TestModelResponseSaveRejectsTranslation(t *testing.T) {
	srv, ts := testServer(t)
	question := "However intricate the ways in which animals communicate with each other"
	options := "A. difficult\nB. complicated\nC. simple\nD. invalid"
	code, payload := doJSON(t, http.MethodPost, ts.URL+"/api/model/response", map[string]any{
		"query": question, "options": options, "type": "单选题",
		"answer": "动物之间的交流方式无论多么复杂", "save": true,
	}, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("save translation = %d %v, want 400", code, payload)
	}
	hit, err := srv.store.FindByNormalizedQuestion(question, &options)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if hit != nil {
		t.Fatalf("translation was stored: %+v", hit)
	}
}

func TestAdminCreateQuestionRejectsTranslation(t *testing.T) {
	_, ts := testServer(t)
	code, payload := doJSON(t, http.MethodPost, ts.URL+"/api/admin/questions", map[string]any{
		"question":      "However intricate the ways in which animals communicate",
		"options":       "A. difficult\nB. complicated\nC. simple\nD. invalid",
		"answer":        "动物之间的交流方式无论多么复杂",
		"question_type": "单选题",
	}, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("create translation = %d %v, want 400", code, payload)
	}
}

func TestAcceptExternalRequiresExactAndValidAnswer(t *testing.T) {
	s := &Server{}
	question := "However intricate the ways in which animals communicate with each other, they do not indulge in anything that"
	req := QueryRequest{
		Title:   question,
		Options: sp("A. difficult\nB. complicated\nC. simple\nD. invalid"),
		Type:    sp("单选题"),
	}

	answer, exact, ok := s.acceptExternal(externalbank.Source{Trust: false}, req, externalbank.Result{
		Question: question, Answer: "C. simple",
	})
	if !ok || !exact || answer != "simple" {
		t.Fatalf("exact answer = %q exact=%v ok=%v", answer, exact, ok)
	}

	if _, _, ok := s.acceptExternal(externalbank.Source{Trust: false}, req, externalbank.Result{
		Question: question, Answer: "动物之间的交流方式无论多么复杂",
	}); ok {
		t.Fatalf("translation was accepted from an external source")
	}

	answer, exact, ok = s.acceptExternal(externalbank.Source{Trust: true}, req, externalbank.Result{
		Question: "However intricate the ways animals communicate with each other",
		Answer:   "simple",
	})
	if !ok || exact {
		t.Fatalf("near match = %q exact=%v ok=%v, want accepted but not exact", answer, exact, ok)
	}

	answer, exact, ok = s.acceptExternal(externalbank.Source{Trust: true}, req, externalbank.Result{
		Answer: "simple",
	})
	if !ok || exact || answer != "simple" {
		t.Fatalf("trusted empty question = %q exact=%v ok=%v", answer, exact, ok)
	}

	imageReq := QueryRequest{Title: "请看图片 https://example.com/a.jpg"}
	if _, _, ok := s.acceptExternal(externalbank.Source{Trust: true}, imageReq, externalbank.Result{
		Question: "请看图片 https://example.com/b.jpg", Answer: "A",
	}); ok {
		t.Fatalf("different image URLs were accepted as the same question")
	}
}
