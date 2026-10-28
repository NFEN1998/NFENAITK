package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDecodeQueryValue(t *testing.T) {
	cases := map[string]string{
		"1+1=":               "1+1=",
		"what+is+1+1=":       "what is 1+1=",
		"what+is+the+answer": "what is the answer",
		"1%2B1%3D":           "1+1=",
		"A.+1+1":             "A. 1+1",
		"2+(3)":              "2+(3)",
		"(1)+2":              "(1)+2",
		"x+1=":               "x+1=",
	}
	for raw, want := range cases {
		if got := decodeQueryValue(raw); got != want {
			t.Fatalf("decodeQueryValue(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestQueryValueDuplicatePlaceholder(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/query?q=&q=1+1=", nil)
	if got := queryValue(req, "q"); got != "1+1=" {
		t.Fatalf("queryValue = %q", got)
	}
}

func TestQueryGetPreservesMathPlus(t *testing.T) {
	srv, _ := testServer(t)
	title := fmt.Sprintf("1+1=%d", time.Now().UnixNano())
	id, err := srv.store.InsertAIResponse(title, "2", nil, nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { _ = srv.store.DeleteQuestion(id) })
	req := httptest.NewRequest(http.MethodGet, "/query", nil)
	// Build a raw query with an unencoded plus to reproduce the bug.
	req.URL.RawQuery = "title=" + title + "&token="
	rec := newRecorder()
	srv.handleQuery(rec, req)
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, _ := payload["data"].(map[string]any)
	if data == nil || data["question"] != title {
		t.Fatalf("payload = %v", payload)
	}
}

func TestQueryGetMixedTextAndPlus(t *testing.T) {
	srv, _ := testServer(t)
	stamp := time.Now().UnixNano()
	title := fmt.Sprintf("what is 1+1= %d", stamp)
	id, err := srv.store.InsertAIResponse(title, "2", nil, nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { _ = srv.store.DeleteQuestion(id) })
	req := httptest.NewRequest(http.MethodGet, "/query", nil)
	req.URL.RawQuery = fmt.Sprintf("q=what+is+1+1=+%d", stamp)
	rec := newRecorder()
	srv.handleQuery(rec, req)
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, _ := payload["data"].(map[string]any)
	if data == nil || data["question"] != title {
		t.Fatalf("payload = %v", payload)
	}
}

func TestQueryGetKeepsSpaceEncoding(t *testing.T) {
	srv, _ := testServer(t)
	title := "what is the capital of France"
	id, err := srv.store.InsertAIResponse(title, "Paris", nil, nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { _ = srv.store.DeleteQuestion(id) })
	req := httptest.NewRequest(http.MethodGet, "/query?q=what+is+the+capital+of+France", nil)
	rec := newRecorder()
	srv.handleQuery(rec, req)
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, _ := payload["data"].(map[string]any)
	if data == nil || data["question"] != title {
		t.Fatalf("payload = %v", payload)
	}
}

func TestQueryGetPreservesPlusInOptions(t *testing.T) {
	srv, _ := testServer(t)
	title := "9+1=?"
	options := "A. 9+1\nB. 10"
	id, err := srv.store.InsertAIResponse(title, "9+1", &options, nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { _ = srv.store.DeleteQuestion(id) })
	req := httptest.NewRequest(http.MethodGet, "/query?title=9+1=?&options=A.+9+1%0AB.+10", nil)
	rec := newRecorder()
	srv.handleQuery(rec, req)
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, _ := payload["data"].(map[string]any)
	if data == nil || data["answer"] != "9+1" {
		t.Fatalf("payload = %v", payload)
	}
}

func TestNfentkPreservesMathPlus(t *testing.T) {
	srv, _ := testServer(t)
	title := fmt.Sprintf("1+1=%d", time.Now().UnixNano())
	id, err := srv.store.InsertAIResponse(title, "2", nil, nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { _ = srv.store.DeleteQuestion(id) })
	req := httptest.NewRequest(http.MethodGet, "/nfentk", nil)
	req.URL.RawQuery = "q=" + title
	rec := newRecorder()
	srv.handleNfentk(rec, req)
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["code"] != float64(1) || payload["data"] != "2" {
		t.Fatalf("payload = %v", payload)
	}
}
