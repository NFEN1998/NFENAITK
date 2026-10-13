package externalbank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// maxBodyBytes bounds how much of a source response is read so a misbehaving
// endpoint cannot exhaust memory.
const maxBodyBytes = 1 << 20

// ErrNoAnswer reports that the response carried no extractable answer. Callers
// treat it as a miss rather than a hard failure.
var ErrNoAnswer = errors.New("no answer in response")

// Client performs a single third-party source lookup over HTTP.
type Client struct {
	HTTP *http.Client
}

// NewClient builds a Client backed by a default transport.
func NewClient() *Client { return &Client{HTTP: &http.Client{}} }

// Lookup renders the request described by src, calls the endpoint and extracts
// the answer. Every source carries its own timeout so a slow endpoint cannot
// stall the query chain.
func (c *Client) Lookup(ctx context.Context, src Source, q Query) (Result, error) {
	timeout := src.TimeoutSeconds
	if timeout <= 0 {
		timeout = DefaultTimeoutSeconds
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	method := strings.ToUpper(strings.TrimSpace(src.Method))
	if method == "" {
		method = http.MethodGet
	}

	reqURL := render(src.URL, q, url.QueryEscape)
	var body io.Reader
	switch method {
	case http.MethodPost:
		payload := src.BodyTemplate
		if strings.TrimSpace(payload) == "" {
			payload = defaultBody()
		}
		escape := url.QueryEscape
		if !strings.EqualFold(src.ContentType, "form") {
			escape = jsonStringEscape
		}
		body = strings.NewReader(render(payload, q, escape))
	default:
		method = http.MethodGet
		u, err := url.Parse(reqURL)
		if err != nil {
			return Result{}, fmt.Errorf("parse url: %w", err)
		}
		qs := u.Query()
		for key, tmpl := range src.QueryParams {
			qs.Set(key, render(tmpl, q, func(s string) string { return s }))
		}
		u.RawQuery = qs.Encode()
		reqURL = u.String()
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		ct := src.ContentType
		if ct == "" || strings.EqualFold(ct, "form") {
			ct = "application/json"
			if strings.EqualFold(src.ContentType, "form") {
				ct = "application/x-www-form-urlencoded"
			}
		}
		req.Header.Set("Content-Type", ct)
	}
	for key, value := range src.Headers {
		req.Header.Set(key, value)
	}

	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return Result{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return Result{}, fmt.Errorf("invalid json response: %w", err)
	}

	result := Result{Answer: extractAnswer(decoded, src.AnswerPath)}
	if src.QuestionPath != "" {
		result.Question = valueToString(lookupPath(decoded, src.QuestionPath))
	}
	if strings.TrimSpace(result.Answer) == "" {
		return Result{}, ErrNoAnswer
	}
	return result, nil
}

// defaultBody is the POST payload used when a source defines no body template.
func defaultBody() string {
	return `{"title":"{question}","options":"{options}","type":"{type}"}`
}

// render substitutes the query placeholders into a template. escape controls
// how substituted values are encoded for the target context.
func render(tmpl string, q Query, escape func(string) string) string {
	options := ""
	if q.Options != nil {
		options = *q.Options
	}
	qtype := ""
	if q.Type != nil {
		qtype = *q.Type
	}
	replacer := strings.NewReplacer(
		"{question}", escape(q.Title),
		"{options}", escape(options),
		"{type}", escape(qtype),
	)
	return replacer.Replace(tmpl)
}

// jsonStringEscape escapes a value for embedding inside a JSON string literal.
func jsonStringEscape(v string) string {
	encoded, err := json.Marshal(v)
	if err != nil {
		return v
	}
	s := string(encoded)
	if len(s) >= 2 {
		return s[1 : len(s)-1]
	}
	return v
}

// extractAnswer reads the answer from the decoded JSON. An empty path falls
// back to the common shapes data.answer, answer and data.
func extractAnswer(root any, path string) string {
	if strings.TrimSpace(path) != "" {
		return valueToString(lookupPath(root, path))
	}
	for _, fallback := range []string{"data.answer", "answer", "data"} {
		if v := valueToString(lookupPath(root, fallback)); v != "" {
			return v
		}
	}
	return ""
}

// lookupPath walks a dotted path through nested JSON objects.
func lookupPath(root any, path string) any {
	current := root
	for _, segment := range strings.Split(path, ".") {
		if segment == "" {
			continue
		}
		obj, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = obj[segment]
		if !ok {
			return nil
		}
	}
	return current
}

// valueToString renders a scalar JSON value as a string.
func valueToString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == math.Trunc(t) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return ""
	}
}
