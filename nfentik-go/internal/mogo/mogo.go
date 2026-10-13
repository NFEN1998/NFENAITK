// Package mogo reads the question bank of a MOGO console over its internal
// HTTP API. It is used by the importer to pull questions into the local bank.
package mogo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxBodyBytes = 16 << 20

// Client talks to one MOGO instance.
type Client struct {
	BaseURL string
	AKey    string
	HTTP    *http.Client
}

// NewClient builds a client. baseURL is the MOGO origin (for example
// http://host:9988); aKey is the site access key.
func NewClient(baseURL, aKey string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		AKey:    strings.TrimSpace(aKey),
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Schema describes the question table as reported by getQuestionHeader.
type Schema struct {
	DBType         string   `json:"dbType"`
	TableHeader    []string `json:"table_header"`
	TableHeaderKey []string `json:"table_header_key"`
}

// Row is one question row, mapped from the positional arrays MOGO returns.
type Row struct {
	ID           int64
	Course       string
	Question     string
	Answer       string
	QuestionID   string
	QuestionType string
	Opt          string
	AddTime      string
}

// Page is one page of rows plus the cursor needed for the next page. Rows are
// ordered by descending id, so LastID is the id to pass as last_id next time.
type Page struct {
	Rows       []Row
	LastID     int64
	TotalCount int64
}

// Header fetches and caches the question table schema.
func (c *Client) Header(ctx context.Context) (Schema, error) {
	raw, err := c.get(ctx, "/getQuestionHeader", url.Values{})
	if err != nil {
		return Schema{}, err
	}
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data Schema `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Schema{}, fmt.Errorf("解析字段定义失败: %w", err)
	}
	if resp.Code != 1 {
		return Schema{}, fmt.Errorf("获取字段定义失败: %s", resp.Msg)
	}
	return resp.Data, nil
}

// FetchPage returns one page of questions starting after lastID (pass -1 for
// the newest page). item is the page size.
func (c *Client) FetchPage(ctx context.Context, schema Schema, lastID int64, item int) (Page, error) {
	params := url.Values{}
	params.Set("dbType", "question")
	params.Set("page", "1")
	params.Set("item", strconv.Itoa(item))
	params.Set("last_id", strconv.FormatInt(lastID, 10))
	params.Set("zt", "1")
	params.Set("ty", "0")
	params.Set("Dlen", "0")

	raw, err := c.get(ctx, "/getData", params)
	if err != nil {
		return Page{}, err
	}
	var resp struct {
		Code       int                 `json:"code"`
		Msg        string              `json:"msg"`
		TotalCount int64               `json:"totalCount"`
		DataLength int                 `json:"dataLength"`
		Data       [][]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Page{}, fmt.Errorf("解析题库数据失败: %w", err)
	}
	if resp.Code != 1 {
		return Page{}, fmt.Errorf("获取题库数据失败: %s", resp.Msg)
	}

	index := indexOf(schema.TableHeaderKey)
	page := Page{TotalCount: resp.TotalCount}
	for _, fields := range resp.Data {
		row := Row{
			ID:           fieldInt(fields, index, "id"),
			Course:       fieldString(fields, index, "course"),
			Question:     fieldString(fields, index, "question"),
			Answer:       fieldString(fields, index, "answer"),
			QuestionID:   fieldString(fields, index, "questionId"),
			QuestionType: fieldString(fields, index, "questionType"),
			Opt:          fieldString(fields, index, "opt"),
			AddTime:      fieldString(fields, index, "addTime"),
		}
		page.Rows = append(page.Rows, row)
	}
	if n := len(page.Rows); n > 0 {
		page.LastID = page.Rows[n-1].ID
	}
	return page, nil
}

func (c *Client) get(ctx context.Context, path string, params url.Values) ([]byte, error) {
	if c.AKey != "" {
		params.Set("aKey", c.AKey)
	}
	endpoint := c.BaseURL + path
	if encoded := params.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mogo 返回状态 %d", resp.StatusCode)
	}
	return raw, nil
}

func indexOf(keys []string) map[string]int {
	index := make(map[string]int, len(keys))
	for i, key := range keys {
		index[strings.ToLower(key)] = i
	}
	return index
}

func fieldRaw(fields []json.RawMessage, index map[string]int, key string) json.RawMessage {
	i, ok := index[strings.ToLower(key)]
	if !ok || i < 0 || i >= len(fields) {
		return nil
	}
	return fields[i]
}

func fieldString(fields []json.RawMessage, index map[string]int, key string) string {
	raw := fieldRaw(fields, index, key)
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.Trim(string(raw), `"`)
}

func fieldInt(fields []json.RawMessage, index map[string]int, key string) int64 {
	raw := fieldRaw(fields, index, key)
	if len(raw) == 0 {
		return 0
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		n, _ = strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	}
	return n
}

// ParseOptions extracts the option list from MOGO's opt field, which looks like
// "单选题****[选项一###选项二###…]".
func ParseOptions(opt string) []string {
	inner := strings.TrimSpace(opt)
	if i := strings.Index(inner, "["); i >= 0 {
		inner = inner[i+1:]
		if j := strings.LastIndex(inner, "]"); j >= 0 {
			inner = inner[:j]
		}
	} else if i := strings.Index(inner, "****"); i >= 0 {
		inner = inner[i+4:]
	}
	return splitOptions(inner)
}

// SplitQuestion separates an inline option block. MOGO joins the stem and the
// options with "#####" and separates options with "###".
func SplitQuestion(question string) (stem string, options []string) {
	if i := strings.Index(question, "#####"); i >= 0 {
		return strings.TrimSpace(question[:i]), splitOptions(question[i+5:])
	}
	return strings.TrimSpace(question), nil
}

func splitOptions(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, "###")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
