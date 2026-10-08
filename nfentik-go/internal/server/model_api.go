package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nfentik/nfentik-go/internal/ai"
)

// apiError carries an HTTP status alongside a human readable message.
type apiError struct {
	status  int
	message string
}

func (e apiError) Error() string { return e.message }

func errBadRequest(msg string) error { return apiError{status: http.StatusBadRequest, message: msg} }
func errInternal(msg string) error {
	return apiError{status: http.StatusInternalServerError, message: msg}
}

// errorStatus extracts the HTTP status and message from an apiError.
func errorStatus(err error) (int, string) {
	if e, ok := err.(apiError); ok {
		return e.status, e.message
	}
	return http.StatusInternalServerError, err.Error()
}

// modelCallRequest is posted by the console when it wants a raw model answer.
type modelCallRequest struct {
	RequestID string   `json:"request_id"`
	Query     string   `json:"query"`
	Options   *string  `json:"options"`
	Type      *string  `json:"type"`
	ModelID   string   `json:"model_id"`
	Images    []string `json:"images"`
}

// handleModels returns the configured platforms and their models.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	settings, err := s.ModelSettings()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"platforms": settings.Platforms,
		"text":      settings.SelectedTextModels,
		"summary":   settings.SelectedSummaryModels,
		"vision":    settings.SelectedVisionModel,
	})
}

// resolveModelCall validates the request and returns the model, platform and
// prepared messages shared by the JSON and streaming handlers.
func (s *Server) resolveModelCall(req modelCallRequest) (ai.Model, *ai.Platform, []ai.Message, error) {
	if strings.TrimSpace(req.Query) == "" {
		return ai.Model{}, nil, nil, errBadRequest("查询内容不能为空")
	}
	settings, err := s.ModelSettings()
	if err != nil {
		return ai.Model{}, nil, nil, errInternal("模型配置读取失败: " + err.Error())
	}

	var model ai.Model
	var platform *ai.Platform
	if req.ModelID != "" {
		m, p, ok := settings.Resolve(req.ModelID)
		if !ok {
			return ai.Model{}, nil, nil, errBadRequest("未找到指定模型")
		}
		model, platform = *m, p
	} else {
		models := settings.TextModels()
		if len(models) == 0 {
			return ai.Model{}, nil, nil, errBadRequest("未配置文本模型")
		}
		model = models[0]
		p, ok := s.platformFor(settings, model.PlatformID)
		if !ok {
			return ai.Model{}, nil, nil, errBadRequest("未找到模型所属平台")
		}
		platform = p
	}

	prompt := ai.BuildTextPrompt(req.Query, req.Options, req.Type)
	if len(req.Images) > 0 {
		prompt = ai.BuildVisionPrompt(req.Query, req.Options)
	}
	return model, platform, buildMessages(prompt, req.Images), nil
}

// handleModelCall runs a single model and returns the final answer as JSON.
func (s *Server) handleModelCall(w http.ResponseWriter, r *http.Request) {
	req, err := decodeModelCall(w, r)
	if err != nil {
		return
	}
	model, platform, messages, err := s.resolveModelCall(req)
	if err != nil {
		status, msg := errorStatus(err)
		writeJSON(w, status, map[string]any{"success": false, "message": msg})
		return
	}

	appSettings := s.config.Settings()
	timeout := time.Duration(appSettings.ModelResponseTimeout) * time.Second
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	s.bus.Publish(Event{Type: EventModelCallRequest, RequestID: req.RequestID, Query: req.Query})

	var contentBuf, reasoningBuf strings.Builder
	_, _, err = s.ai.Complete(ctx, s.aiOptions(model, platform, messages, timeout), func(d ai.Delta) {
		contentBuf.WriteString(d.Content)
		reasoningBuf.WriteString(d.ReasoningContent)
		s.bus.Publish(Event{
			Type: EventModelCallProgress, RequestID: req.RequestID,
			Content: contentBuf.String(), ReasoningContent: reasoningBuf.String(),
		})
	})

	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false, "request_id": req.RequestID, "message": err.Error(),
			"content": contentBuf.String(), "reasoning_content": reasoningBuf.String(),
		})
		s.bus.Publish(Event{Type: EventModelCallResponse, RequestID: req.RequestID, IsSuccess: false, Content: err.Error()})
		return
	}

	answer := ai.ExtractAnswer(contentBuf.String())
	writeJSON(w, http.StatusOK, map[string]any{
		"success":           true,
		"request_id":        req.RequestID,
		"content":           contentBuf.String(),
		"reasoning_content": reasoningBuf.String(),
		"answer":            answer,
	})
	s.bus.Publish(Event{
		Type: EventModelCallResponse, RequestID: req.RequestID, IsSuccess: true,
		Content: contentBuf.String(), ReasoningContent: reasoningBuf.String(),
	})
}

// handleModelStream runs a single model and relays every token over SSE.
func (s *Server) handleModelStream(w http.ResponseWriter, r *http.Request) {
	req, err := decodeModelCall(w, r)
	if err != nil {
		return
	}
	model, platform, messages, err := s.resolveModelCall(req)
	if err != nil {
		status, msg := errorStatus(err)
		writeJSON(w, status, map[string]any{"success": false, "message": msg})
		return
	}

	stream, ok := newSSEWriter(w)
	if !ok {
		return
	}

	appSettings := s.config.Settings()
	timeout := time.Duration(appSettings.ModelResponseTimeout) * time.Second
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	s.bus.Publish(Event{Type: EventModelCallRequest, RequestID: req.RequestID, Query: req.Query})
	stream.send("start", map[string]any{"request_id": req.RequestID, "model": model.ID})

	var contentBuf, reasoningBuf strings.Builder
	_, _, err = s.ai.Complete(ctx, s.aiOptions(model, platform, messages, timeout), func(d ai.Delta) {
		contentBuf.WriteString(d.Content)
		reasoningBuf.WriteString(d.ReasoningContent)
		evt := Event{
			Type: EventModelCallProgress, RequestID: req.RequestID,
			Content: contentBuf.String(), ReasoningContent: reasoningBuf.String(),
		}
		s.bus.Publish(evt)
		stream.send("delta", map[string]any{
			"content": d.Content, "reasoning_content": d.ReasoningContent,
			"full_content": contentBuf.String(), "full_reasoning": reasoningBuf.String(),
		})
	})

	if err != nil {
		stream.send("error", map[string]any{
			"request_id": req.RequestID, "message": err.Error(),
			"content": contentBuf.String(), "reasoning_content": reasoningBuf.String(),
		})
		s.bus.Publish(Event{Type: EventModelCallResponse, RequestID: req.RequestID, IsSuccess: false, Content: err.Error()})
		return
	}

	answer := ai.ExtractAnswer(contentBuf.String())
	stream.send("done", map[string]any{
		"request_id": req.RequestID, "content": contentBuf.String(),
		"reasoning_content": reasoningBuf.String(), "answer": answer,
	})
	s.bus.Publish(Event{
		Type: EventModelCallResponse, RequestID: req.RequestID, IsSuccess: true,
		Content: contentBuf.String(), ReasoningContent: reasoningBuf.String(),
	})
}

// aiOptions centralises the completion parameters shared by all call sites.
func (s *Server) aiOptions(model ai.Model, platform *ai.Platform, messages []ai.Message, timeout time.Duration) ai.Options {
	return ai.Options{
		BaseURL:     platform.BaseURL,
		APIKey:      platform.APIKey,
		Model:       model.ID,
		Messages:    messages,
		Temperature: model.Temperature,
		TopP:        model.TopP,
		MaxTokens:   model.MaxTokens,
		Thinking:    model.EnableThinking,
		Headers:     platform.CustomHeaders,
		Timeout:     timeout,
	}
}

// decodeModelCall parses and normalises the request body.
func decodeModelCall(w http.ResponseWriter, r *http.Request) (modelCallRequest, error) {
	var req modelCallRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return req, err
	}
	if req.RequestID == "" {
		req.RequestID = newID()
	}
	return req, nil
}

func buildMessages(prompt string, images []string) []ai.Message {
	if len(images) == 0 {
		return []ai.Message{{Role: "user", Content: prompt}}
	}
	parts := []ai.Part{{Type: "text", Text: prompt}}
	for _, img := range images {
		if img == "" {
			continue
		}
		parts = append(parts, ai.Part{Type: "image_url", ImageURL: &ai.ImageURL{URL: img}})
	}
	return []ai.Message{{Role: "user", Content: parts}}
}

// handleModelResponse stores a user supplied answer for a finished request.
func (s *Server) handleModelResponse(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RequestID string  `json:"request_id"`
		Query     string  `json:"query"`
		Answer    string  `json:"answer"`
		Options   *string `json:"options"`
		Type      *string `json:"type"`
		Save      bool    `json:"save"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	id := int64(0)
	if req.Save && strings.TrimSpace(req.Answer) != "" {
		var err error
		id, err = s.store.InsertAIResponse(req.Query, req.Answer, req.Options, req.Type, s.saveFolderID())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "id": id})
}

// handleModelProgress complements the bus based streaming interface.
func (s *Server) handleModelProgress(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "request_id": r.URL.Query().Get("request_id")})
}

// sseWriter is a small helper around the text/event-stream protocol.
type sseWriter struct {
	w  http.ResponseWriter
	fl http.Flusher
}

func newSSEWriter(w http.ResponseWriter) (*sseWriter, bool) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl.Flush()
	return &sseWriter{w: w, fl: fl}, true
}

func (s *sseWriter) send(event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if event != "" {
		fmt.Fprintf(s.w, "event: %s\n", event)
	}
	fmt.Fprintf(s.w, "data: %s\n\n", data)
	s.fl.Flush()
}

func (s *sseWriter) comment(text string) {
	fmt.Fprintf(s.w, ": %s\n\n", text)
	s.fl.Flush()
}

// handleLogStream streams request and model events over SSE.
func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	stream, ok := newSSEWriter(w)
	if !ok {
		return
	}
	id, ch := s.bus.Subscribe()
	defer s.bus.Unsubscribe(id)

	stream.send("connected", map[string]any{"message": "connected", "timestamp": time.Now()})

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case evt, open := <-ch:
			if !open {
				return
			}
			stream.send(evt.Type, evt)
		case <-heartbeat.C:
			stream.comment("keep-alive")
		}
	}
}
