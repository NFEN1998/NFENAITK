package mogo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

// TestClientRetriesTransientStatus verifies a 504 is retried until it succeeds.
func TestClientRetriesTransientStatus(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		_, _ = w.Write([]byte(`{"code":1,"msg":"ok","data":{"dbType":"question","table_header":["id"],"table_header_key":["id"]}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "secret")
	client.Retries = 3
	client.RetryDelay = time.Millisecond

	if _, err := client.Header(context.Background()); err != nil {
		t.Fatalf("Header after retries: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

// TestClientDoesNotRetryClientError verifies a 4xx fails on the first attempt.
func TestClientDoesNotRetryClientError(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewClient(server.URL, "secret")
	client.Retries = 5
	client.RetryDelay = time.Millisecond

	if _, err := client.Header(context.Background()); err == nil {
		t.Fatalf("expected an error for a 400")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry on 4xx)", attempts)
	}
}

// TestClientRetryStopsOnContextCancel verifies a cancelled context aborts the
// retry wait instead of blocking for the full delay.
func TestClientRetryStopsOnContextCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := NewClient(server.URL, "secret")
	client.Retries = 3
	client.RetryDelay = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Header(ctx); err == nil {
		t.Fatalf("expected the cancelled context to abort the retry")
	}
}

// TestQuestionBankSampleFormat covers the real questionbank.data shape:
// the stem and inline options are joined with #####, while opt carries the
// type prefix and the authoritative option list.
func TestQuestionBankSampleFormat(t *testing.T) {
	question := "下列关于急救知识说法正确的是()。#####对毛细血管出血患者在近心端压迫止血###心跳骤停时可以利用自动体外除颤器(AED)对患者进行除颤和心肺复苏###洗胃最佳的时间应该尽量控制在患者服毒后6-8小时之内"
	opt := "单选题****[洗胃最佳的时间应该尽量控制在患者服毒后6-8小时之内###对毛细血管出血患者在近心端压迫止血###胸外心脏按压的频率为每分钟30~50次###心跳骤停时可以利用自动体外除颤器(AED)对患者进行除颤和心肺复苏]"

	stem, inline := SplitQuestion(question)
	if stem != "下列关于急救知识说法正确的是()。" {
		t.Fatalf("stem = %q", stem)
	}
	if len(inline) != 3 {
		t.Fatalf("inline options = %d, want 3", len(inline))
	}
	options := ParseOptions(opt)
	if len(options) != 4 {
		t.Fatalf("opt options = %v", options)
	}
	if options[0] != "洗胃最佳的时间应该尽量控制在患者服毒后6-8小时之内" {
		t.Fatalf("first option = %q", options[0])
	}

	stem, inline = SplitQuestion("心肺复苏术简称CPR,是针对骤停的心脏和呼吸采取的救命技术。")
	if stem == "" || len(inline) != 0 {
		t.Fatalf("judgement split = %q %v", stem, inline)
	}
	if got := ParseOptions("判断题****[对###错]"); len(got) != 2 || got[0] != "对" || got[1] != "错" {
		t.Fatalf("judgement options = %v", got)
	}
}
