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
	"sort"
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
	if strings.TrimSpace(src.QuestionPath) != "" {
		result.Question = valueToString(lookupPath(decoded, src.QuestionPath))
	} else {
		// No explicit question path: try to discover an echoed question so the
		// answer can still be verified against the query.
		result.Question = autoExtractQuestion(decoded)
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

// extractAnswer reads the answer from the decoded JSON. An explicit path wins;
// when it is empty, or yields nothing, the answer is auto-detected from the
// response shape so arbitrary third-party formats can be consumed without
// hand-configuring a path.
func extractAnswer(root any, path string) string {
	if strings.TrimSpace(path) != "" {
		if s := valueToString(lookupPath(root, path)); strings.TrimSpace(s) != "" {
			return s
		}
	}
	return autoExtractAnswer(root)
}

// autoExtractAnswer searches the response tree for a value under a well-known
// answer key, preferring the shallowest and left-most match.
func autoExtractAnswer(root any) string {
	return searchByKeys(root, answerKeys, autoMaxDepth)
}

// autoExtractQuestion searches for an echoed question under a well-known key.
func autoExtractQuestion(root any) string {
	return searchByKeys(root, questionKeys, autoMaxDepth)
}

const autoMaxDepth = 5

// answerKeys are the keys commonly used to carry an answer. Container values
// under these keys are still traversed, so nested shapes like data.answer work.
var answerKeys = []string{
	"answer", "anwser", "answertext", "answer_text", "answerstr",
	"correctanswer", "correct_answer", "correct", "correctanswertext",
	"data", "result", "results", "content", "text", "value", "output", "body",
	"detail", "details", "answers", "solution",
}

// questionKeys are the keys commonly used to echo the query back.
var questionKeys = []string{"q", "question", "query", "title", "questiontext", "question_text"}

// metaKeys are bookkeeping keys whose values must never be mistaken for an
// answer (status codes, timings, counters and the like).
var metaKeys = map[string]bool{
	"code": true, "status": true, "state": true, "msg": true, "message": true,
	"errmsg": true, "error": true, "err": true, "errcode": true, "errno": true,
	"success": true, "ok": true, "time": true, "timestamp": true, "date": true,
	"total": true, "calls": true, "count": true, "page": true, "pagesize": true,
	"type": true, "id": true, "requestid": true, "traceid": true, "cost": true,
	"elapsed": true, "spend": true, "took": true, "duration": true, "token": true,
}

// searchByKeys performs a breadth-first search for the first value held under
// any of keys. It descends through objects and arrays (skipping meta subtrees)
// up to maxDepth levels.
func searchByKeys(root any, keys []string, maxDepth int) string {
	type item struct {
		value any
		depth int
	}
	queue := []item{{root, 0}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.depth > maxDepth {
			continue
		}
		switch node := current.value.(type) {
		case map[string]any:
			// Direct (and case-insensitive) hits at this level, tried in the
			// configured key order for determinism.
			for _, wanted := range keys {
				if raw, ok := node[wanted]; ok {
					if s := stringifyAnswer(raw); s != "" {
						return s
					}
				}
			}
			for key, raw := range node {
				lower := strings.ToLower(key)
				for _, wanted := range keys {
					if lower == wanted {
						if s := stringifyAnswer(raw); s != "" {
							return s
						}
					}
				}
			}
			for _, key := range sortedKeys(node) {
				if metaKeys[strings.ToLower(key)] {
					continue
				}
				queue = append(queue, item{node[key], current.depth + 1})
			}
		case []any:
			for _, element := range node {
				queue = append(queue, item{element, current.depth + 1})
			}
		}
	}
	return ""
}

// stringifyAnswer renders a scalar (or a list of scalars) as an answer string.
// Objects return "" so the search continues into them.
func stringifyAnswer(raw any) string {
	switch value := raw.(type) {
	case string:
		return filterStatusWord(strings.TrimSpace(value))
	case float64:
		return valueToString(value)
	case []any:
		parts := make([]string, 0, len(value))
		for _, element := range value {
			if s := stringifyAnswer(element); s != "" {
				parts = append(parts, s)
			}
		}
		switch len(parts) {
		case 0:
			return ""
		case 1:
			return parts[0]
		default:
			return strings.Join(parts, "###")
		}
	default:
		return ""
	}
}

// filterStatusWord drops boolean/status-like placeholders so they are not
// treated as answers. Real answers are returned unchanged.
func filterStatusWord(value string) string {
	switch strings.ToLower(value) {
	case "", "success", "ok", "true", "false", "null", "none", "nil",
		"成功", "失败", "操作成功":
		return ""
	}
	return value
}

// sortedKeys returns a map's keys in sorted order for deterministic traversal.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
