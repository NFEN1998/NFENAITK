// Package ai calls AI Platform-compatible chat completion endpoints.
//
// The desktop edition let users script requests with JavaScript. The web
// edition instead talks to the standard /v1/chat/completions interface, which
// every major platform exposes, and streams results back through SSE.
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message is a chat message; Content may be a string or a multimodal array.
type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// Part is one element of a multimodal user message.
type Part struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL wraps an image reference for vision models.
type ImageURL struct {
	URL string `json:"url"`
}

// Options controls a single completion request.
type Options struct {
	BaseURL     string
	APIKey      string
	Model       string
	Messages    []Message
	Temperature float64
	TopP        float64
	MaxTokens   int
	Thinking    bool
	Headers     map[string]string
	Timeout     time.Duration
}

// Delta is one streamed chunk.
type Delta struct {
	Content          string
	ReasoningContent string
}

// Client performs chat completions.
type Client struct {
	http *http.Client
}

// New creates a client with a pooled HTTP transport.
func New() *Client {
	return &Client{http: &http.Client{Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
	}}}
}

type chatRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	Stream    bool      `json:"stream"`
	MaxTokens int       `json:"max_tokens,omitempty"`
	Temp      float64   `json:"temperature,omitempty"`
	TopP      float64   `json:"top_p,omitempty"`
	Thinking  *thinkCfg `json:"thinking,omitempty"`
}

type thinkCfg struct {
	Type string `json:"type"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func normalizeBaseURL(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/chat/completions"
	}
	// Many consoles expose the API under an /api path already.
	if strings.Contains(base, "/api") {
		return base + "/v1/chat/completions"
	}
	return base + "/v1/chat/completions"
}

// Complete streams a completion and invokes onDelta for every token chunk.
// It returns the accumulated content and reasoning text.
func (c *Client) Complete(ctx context.Context, opt Options, onDelta func(Delta)) (string, string, error) {
	if opt.BaseURL == "" {
		return "", "", errors.New("平台未配置 Base URL")
	}
	if opt.Model == "" {
		return "", "", errors.New("模型 ID 为空")
	}
	endpoint := normalizeBaseURL(opt.BaseURL)

	payload := chatRequest{
		Model:     opt.Model,
		Messages:  opt.Messages,
		Stream:    true,
		MaxTokens: opt.MaxTokens,
		Temp:      opt.Temperature,
		TopP:      opt.TopP,
	}
	if opt.Thinking {
		payload.Thinking = &thinkCfg{Type: "enabled"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}

	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if opt.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+opt.APIKey)
	}
	for k, v := range opt.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("请求模型失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return "", "", fmt.Errorf("API 错误 %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var content, reasoning strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return content.String(), reasoning.String(), errors.New(chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		d := chunk.Choices[0].Delta
		rc := d.ReasoningContent
		if rc == "" {
			rc = d.Reasoning
		}
		if d.Content == "" && rc == "" {
			continue
		}
		if d.Content != "" {
			content.WriteString(d.Content)
		}
		if rc != "" {
			reasoning.WriteString(rc)
		}
		if onDelta != nil {
			onDelta(Delta{Content: d.Content, ReasoningContent: rc})
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			return content.String(), reasoning.String(), errors.New("模型响应超时")
		}
		return content.String(), reasoning.String(), fmt.Errorf("读取模型响应失败: %w", err)
	}
	return content.String(), reasoning.String(), nil
}

// ExtractAnswer pulls the answer field out of a model response, tolerating
// markdown fences, trailing prose and the common "anwser" typo.
func ExtractAnswer(raw string) string {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	if ans, ok := answerFromJSON(cleaned); ok {
		return ans
	}
	if fragment := lastBalancedJSON(cleaned); fragment != "" {
		if ans, ok := answerFromJSON(fragment); ok {
			return ans
		}
	}

	// Regex fallback for "answer": "..." embedded in prose.
	if v := regexAnswer(cleaned); v != "" {
		return v
	}
	// Chinese/English "答案:" prefix.
	if v := textAnswer(cleaned); v != "" {
		return v
	}
	// Option lists such as "A. ..." or "正确答案是 B" are accepted verbatim.
	if v := optionAnswer(cleaned); v != "" {
		return v
	}
	// Short plain-text answers (a single concise line) are used verbatim.
	trimmed := strings.TrimSpace(cleaned)
	if isPlausibleAnswer(trimmed) {
		return trimmed
	}
	// The model produced prose or an unrecognised structure. Returning the raw
	// text here would store nonsense in the question bank, so signal "no answer"
	// and let the caller reject the response.
	return ""
}

func answerFromJSON(text string) (string, bool) {
	var obj map[string]any
	if err := json.Unmarshal([]byte(text), &obj); err != nil {
		return "", false
	}
	for _, key := range []string{"answer", "anwser"} {
		if v, ok := obj[key]; ok {
			if s, ok := v.(string); ok {
				return s, true
			}
		}
	}
	return "", false
}

func lastBalancedJSON(text string) string {
	depth := 0
	end := -1
	for i := len(text) - 1; i >= 0; i-- {
		switch text[i] {
		case '}':
			if end == -1 {
				end = i
			}
			depth++
		case '{':
			if end != -1 {
				depth--
				if depth == 0 {
					return text[i : end+1]
				}
			}
		}
	}
	return ""
}

func regexAnswer(text string) string {
	re := answerRe
	if m := re.FindStringSubmatch(text); len(m) > 1 {
		return m[1]
	}
	return ""
}

func textAnswer(text string) string {
	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(line)
		for _, prefix := range []string{"答案:", "答案：", "answer:", "answer："} {
			if idx := strings.Index(lower, prefix); idx >= 0 {
				return strings.TrimSpace(line[idx+len(prefix):])
			}
		}
	}
	return ""
}

// optionAnswer recognises single-line answers that reference an option, such as
// "A"、"B. 连接" or "正确答案是 A".
func optionAnswer(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.Contains(trimmed, "\n") {
		return ""
	}
	for _, prefix := range []string{"正确答案是", "正确答案为", "正确答案：", "正确答案:", "答案是", "答：", "选"} {
		if idx := strings.Index(trimmed, prefix); idx >= 0 {
			if v := strings.TrimSpace(trimmed[idx+len(prefix):]); v != "" {
				return v
			}
		}
	}
	return ""
}

// refusalPhrases mark model responses that decline to answer. They must never
// be stored as a question answer.
var refusalPhrases = []string{
	"无法回答", "无法确定", "无法判断", "不能回答", "信息不足", "题目不完整",
	"抱歉", "对不起", "请提供", "无法获知", "不清楚",
	"i cannot", "i can't", "unable to answer", "i'm sorry", "sorry,",
	"insufficient information", "not enough information",
}

// isPlausibleAnswer decides whether a plain-text response is concise enough to
// be treated as an answer. Prose, refusals and multi-sentence explanations are
// rejected so they are never persisted as a question answer.
func isPlausibleAnswer(text string) bool {
	if text == "" || len(text) > 200 {
		return false
	}
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		return false
	}
	lower := strings.ToLower(text)
	for _, phrase := range refusalPhrases {
		if strings.Contains(lower, phrase) {
			return false
		}
	}
	lines := 0
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) != "" {
			lines++
		}
	}
	if lines > 2 {
		return false
	}
	// More than one sentence-ending punctuation marks the text as an
	// explanation rather than a concise answer.
	if strings.Count(text, "。") > 0 || strings.Count(text, "；") > 0 {
		return false
	}
	if strings.Count(text, ". ") > 0 || strings.Count(text, "! ") > 0 || strings.Count(text, "? ") > 0 {
		return false
	}
	if strings.Count(text, ",") > 2 {
		return false
	}
	return true
}

// DetectError reports a model-side error message, if any.
func DetectError(raw string) (string, bool) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	if cleaned == "所有AI均查询失败" {
		return cleaned, true
	}
	if strings.HasPrefix(cleaned, "错误:") || strings.HasPrefix(cleaned, "Error:") || strings.HasPrefix(cleaned, "API 错误") {
		return cleaned, true
	}
	if strings.Contains(cleaned, `"error"`) {
		var obj map[string]any
		if err := json.Unmarshal([]byte(cleaned), &obj); err == nil {
			if e, ok := obj["error"]; ok {
				if m, ok := e.(map[string]any); ok {
					if msg, ok := m["message"].(string); ok {
						return msg, true
					}
				}
				return fmt.Sprint(e), true
			}
		}
		if fragment := lastBalancedJSON(cleaned); fragment != "" {
			if err := json.Unmarshal([]byte(fragment), &obj); err == nil {
				if e, ok := obj["error"]; ok {
					if m, ok := e.(map[string]any); ok {
						if msg, ok := m["message"].(string); ok {
							return msg, true
						}
					}
					return fmt.Sprint(e), true
				}
			}
		}
	}
	return "", false
}
