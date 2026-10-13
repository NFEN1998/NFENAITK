package mogo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseOptions(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"单选题****[A###B###C###D]", []string{"A", "B", "C", "D"}},
		{"判断题****[对###错]", []string{"对", "错"}},
		{"****[甲###乙]", []string{"甲", "乙"}},
		{"甲###乙###丙", []string{"甲", "乙", "丙"}},
		{"", nil},
	}
	for _, tc := range cases {
		got := ParseOptions(tc.raw)
		if len(got) != len(tc.want) {
			t.Fatalf("ParseOptions(%q) = %v, want %v", tc.raw, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("ParseOptions(%q)[%d] = %q, want %q", tc.raw, i, got[i], tc.want[i])
			}
		}
	}
}

func TestSplitQuestion(t *testing.T) {
	stem, options := SplitQuestion("下列说法哪个正确？#####选项一###选项二")
	if stem != "下列说法哪个正确？" || len(options) != 2 || options[0] != "选项一" {
		t.Fatalf("SplitQuestion = %q %v", stem, options)
	}
	stem, options = SplitQuestion("普通题目")
	if stem != "普通题目" || options != nil {
		t.Fatalf("SplitQuestion plain = %q %v", stem, options)
	}
}

func TestFetchPageMapsRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/getQuestionHeader":
			if r.URL.Query().Get("aKey") != "secret" {
				_, _ = w.Write([]byte(`{"code":-1,"msg":"访问失败!"}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":1,"msg":"ok","data":{"dbType":"question","table_header":["id","题目","答案","题型","选项"],"table_header_key":["id","question","answer","questionType","opt"]}}`))
		case "/getData":
			_, _ = w.Write([]byte(`{"code":1,"msg":"ok","totalCount":2,"dataLength":2,"data":[[2,"题目二","答案二","单选题","单选题****[A###B]"],[1,"题目一","答案一","判断题","判断题****[对###错]"]]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "secret")
	schema, err := client.Header(context.Background())
	if err != nil {
		t.Fatalf("Header: %v", err)
	}
	page, err := client.FetchPage(context.Background(), schema, -1, 100)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if len(page.Rows) != 2 || page.TotalCount != 2 {
		t.Fatalf("page = %+v", page)
	}
	if page.Rows[0].ID != 2 || page.Rows[0].Question != "题目二" || page.Rows[0].QuestionType != "单选题" {
		t.Fatalf("row0 = %+v", page.Rows[0])
	}
	if page.LastID != 1 {
		t.Fatalf("lastID = %d, want 1", page.LastID)
	}
}

func TestHeaderRejectsBadKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":-1,"msg":"访问失败!"}`))
	}))
	defer server.Close()

	if _, err := NewClient(server.URL, "bad").Header(context.Background()); err == nil {
		t.Fatalf("expected an error for a rejected key")
	}
}
