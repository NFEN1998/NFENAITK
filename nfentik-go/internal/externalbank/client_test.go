package externalbank

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLookupGetWithURLPlaceholder(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("title")
		_, _ = w.Write([]byte(`{"data":{"answer":"A"}}`))
	}))
	defer srv.Close()

	c := NewClient()
	res, err := c.Lookup(context.Background(), Source{
		URL:        srv.URL + "/q?title={question}",
		Method:     "GET",
		AnswerPath: "data.answer",
	}, Query{Title: "1+1="})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if res.Answer != "A" {
		t.Fatalf("answer = %q, want A", res.Answer)
	}
	if gotQuery != "1+1=" {
		t.Fatalf("server saw title %q, want 1+1=", gotQuery)
	}
}

func TestLookupGetQueryParams(t *testing.T) {
	var gotQ, gotOptions string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.Query().Get("q")
		gotOptions = r.URL.Query().Get("options")
		_, _ = w.Write([]byte(`{"answer":"B"}`))
	}))
	defer srv.Close()

	options := "A. 甲 B. 乙"
	c := NewClient()
	res, err := c.Lookup(context.Background(), Source{
		URL:         srv.URL + "/q",
		Method:      "GET",
		QueryParams: map[string]string{"q": "{question}", "options": "{options}"},
	}, Query{Title: "题目", Options: &options})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if res.Answer != "B" {
		t.Fatalf("answer = %q, want B", res.Answer)
	}
	if gotQ != "题目" || gotOptions != options {
		t.Fatalf("params q=%q options=%q", gotQ, gotOptions)
	}
}

func TestLookupPostJSONBodyAndQuestionPath(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"data":{"question":"题目","answer":"C"}}`))
	}))
	defer srv.Close()

	c := NewClient()
	res, err := c.Lookup(context.Background(), Source{
		URL:          srv.URL + "/search",
		Method:       "POST",
		BodyTemplate: `{"q":"{question}"}`,
		AnswerPath:   "data.answer",
		QuestionPath: "data.question",
	}, Query{Title: "题目"})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if res.Answer != "C" || res.Question != "题目" {
		t.Fatalf("result = %+v", res)
	}
	if body["q"] != "题目" {
		t.Fatalf("body q = %v", body["q"])
	}
}

func TestLookupAnswerFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"answer":"fallback"}`))
	}))
	defer srv.Close()

	c := NewClient()
	res, err := c.Lookup(context.Background(), Source{URL: srv.URL, Method: "GET"}, Query{Title: "x"})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if res.Answer != "fallback" {
		t.Fatalf("answer = %q", res.Answer)
	}
}

func TestLookupAutoAdaptsAnswer(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		answerPath string
		want       string
	}{
		{"data string", `{"code":1,"msg":"success","data":"北京","answer":"北京"}`, "", "北京"},
		{"nested data.answer", `{"data":{"answer":"A"}}`, "", "A"},
		{"top-level answer", `{"answer":"B"}`, "", "B"},
		{"result.content", `{"result":{"content":"C"}}`, "", "C"},
		{"array join", `{"data":["甲","乙"]}`, "", "甲###乙"},
		{"case-insensitive key", `{"Answer":"Z"}`, "", "Z"},
		{"explicit path wins", `{"data":{"answer":"X","other":"Y"}}`, "data.answer", "X"},
		{"explicit path falls back to auto", `{"data":{"answer":"X"}}`, "data.missing", "X"},
		{"status only is a miss", `{"success":true,"message":"ok","code":1}`, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := NewClient()
			res, err := c.Lookup(context.Background(), Source{
				URL: srv.URL, Method: "GET", AnswerPath: tc.answerPath,
			}, Query{Title: "x"})
			if tc.want == "" {
				if !errors.Is(err, ErrNoAnswer) {
					t.Fatalf("err = %v, want ErrNoAnswer", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Lookup: %v", err)
			}
			if res.Answer != tc.want {
				t.Fatalf("answer = %q, want %q", res.Answer, tc.want)
			}
		})
	}
}

func TestLookupAutoDetectsQuestion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":1,"q":"地球的卫星是","data":"月球"}`))
	}))
	defer srv.Close()

	c := NewClient()
	res, err := c.Lookup(context.Background(), Source{URL: srv.URL, Method: "GET"}, Query{Title: "地球的卫星是"})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if res.Answer != "月球" || res.Question != "地球的卫星是" {
		t.Fatalf("result = %+v", res)
	}
}

func TestLookupNoAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"other":1}}`))
	}))
	defer srv.Close()

	c := NewClient()
	_, err := c.Lookup(context.Background(), Source{URL: srv.URL, Method: "GET"}, Query{Title: "x"})
	if !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("err = %v, want ErrNoAnswer", err)
	}
}

func TestLookupInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := NewClient()
	if _, err := c.Lookup(context.Background(), Source{URL: srv.URL}, Query{Title: "x"}); err == nil {
		t.Fatalf("expected an error for invalid JSON")
	}
}

func TestLookupNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := NewClient()
	if _, err := c.Lookup(context.Background(), Source{URL: srv.URL}, Query{Title: "x"}); err == nil {
		t.Fatalf("expected an error for a non-2xx status")
	}
}

func TestLookupTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(1200 * time.Millisecond)
		_, _ = w.Write([]byte(`{"answer":"late"}`))
	}))
	defer srv.Close()

	c := NewClient()
	start := time.Now()
	_, err := c.Lookup(context.Background(), Source{URL: srv.URL, TimeoutSeconds: 1}, Query{Title: "x"})
	if err == nil {
		t.Fatalf("expected a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 1100*time.Millisecond {
		t.Fatalf("lookup took %v, timeout was not enforced", elapsed)
	}
}

func TestLookupNumberAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"answer":42}}`))
	}))
	defer srv.Close()

	c := NewClient()
	res, err := c.Lookup(context.Background(), Source{URL: srv.URL, AnswerPath: "data.answer"}, Query{Title: "x"})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if res.Answer != "42" {
		t.Fatalf("answer = %q, want 42", res.Answer)
	}
}
