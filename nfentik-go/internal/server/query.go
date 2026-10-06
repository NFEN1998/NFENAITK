package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/nfentik/nfentik-go/internal/ai"
	"github.com/nfentik/nfentik-go/internal/match"
	"github.com/nfentik/nfentik-go/internal/plan"
	"github.com/nfentik/nfentik-go/internal/store"
)

// QueryRequest is the payload sent by the OCS userscript.
type QueryRequest struct {
	Title   string  `json:"title"`
	Options *string `json:"options"`
	Type    *string `json:"type"`
	// Raw requests the legacy HTML question field (with the pending-correction
	// button). When false the question is returned as escaped plain text.
	Raw bool `json:"raw"`
	// Token is an optional usertoken carried in the body. It is cleared before
	// the request is logged so the secret never reaches request logs.
	Token string `json:"token"`
}

// queryData is one answer entry returned to OCS.
type queryData struct {
	ID                  int64  `json:"id"`
	Question            string `json:"question"`
	Answer              string `json:"answer"`
	IsAI                bool   `json:"is_ai"`
	IsPendingCorrection bool   `json:"is_pending_correction"`
}

// queryResponse mirrors the desktop QueryResponse: data is a single object.
type queryResponse struct {
	Code    int        `json:"code"`
	Data    *queryData `json:"data"`
	Message *string    `json:"message,omitempty"`
}

// bankAnswer is the cached, mode-independent form of a successful lookup. The
// question text is stored verbatim so it can be rendered as plain text or as
// HTML depending on the caller's `raw` flag.
type bankAnswer struct {
	ID                  int64  `json:"id"`
	Question            string `json:"question"`
	Answer              string `json:"answer"`
	IsAI                bool   `json:"is_ai"`
	IsPendingCorrection bool   `json:"is_pending_correction"`
}

// render converts a cached bank answer into the response shape for the given mode.
func (b bankAnswer) render(origin string, raw bool) queryResponse {
	return successResponse(queryData{
		ID:                  b.ID,
		Question:            renderQuestion(origin, b.ID, b.Question, b.IsPendingCorrection, raw),
		Answer:              b.Answer,
		IsAI:                b.IsAI,
		IsPendingCorrection: b.IsPendingCorrection,
	})
}

func successResponse(data queryData) queryResponse {
	return queryResponse{Code: 1, Data: &data}
}

func errorResponse(msg string) queryResponse {
	return queryResponse{Code: 0, Message: &msg}
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodHead {
		go s.bus.Publish(Event{Type: "ocs-head-received"})
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("Hello,OCS"))
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	settings := s.config.Settings()
	_ = homePageTmpl.Execute(w, homePageData{
		SiteName:     settings.SiteName,
		SiteSubtitle: settings.SiteSubtitle,
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "running", "message": "Server is running"})
}

func (s *Server) handleTime(w http.ResponseWriter, _ *http.Request) {
	now := time.Now()
	writeJSON(w, http.StatusOK, map[string]any{
		"timestamp": now.Unix(),
		"time":      now.Format(time.RFC3339),
	})
}

func (s *Server) handleEcho(w http.ResponseWriter, r *http.Request) {
	var body any
	_ = decodeJSON(r, &body)
	writeJSON(w, http.StatusOK, map[string]any{
		"echo":        body,
		"received_at": time.Now().Format(time.RFC3339),
	})
}

// handleQuery serves both GET and POST /query.
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	var req QueryRequest
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		title := q.Get("title")
		if title == "" {
			// q is accepted as an alias for title.
			title = q.Get("q")
		}
		req = QueryRequest{
			Title:   title,
			Options: optional(q.Get("options")),
			Type:    optional(q.Get("type")),
			Raw:     isTruthy(q.Get("raw")),
		}
	case http.MethodPost:
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse("请求体解析失败: "+err.Error()))
			return
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	start := time.Now()
	requestID := newID()
	origin := s.origin(r)
	ip := clientIP(r)

	// Capture the body token (if any) and strip it before the request is
	// logged so the secret is never persisted.
	bodyToken := strings.TrimSpace(req.Token)
	req.Token = ""
	var bodyStr *string
	if raw, err := json.Marshal(req); err == nil {
		v := string(raw)
		bodyStr = &v
	}
	ua := r.UserAgent()
	headers := map[string]string{}
	for k, v := range r.Header {
		headers[k] = strings.Join(v, ",")
	}

	s.bus.Publish(Event{
		Type: EventRequestLog, ID: requestID, Method: r.Method, Path: "/query",
		RequestBody: bodyStr, Headers: headers, IP: &ip, UserAgent: &ua, Stage: "started",
	})

	// User token authentication and quota check run before any lookup. The
	// enforcement mode depends on the usersEnabled / requireTokenForQuery
	// settings: when tokens are not required, anonymous queries stay allowed.
	user, denied := s.authorizeQuery(r, bodyToken)
	if denied != nil {
		status := http.StatusForbidden
		message := denied.Error()
		if errors.Is(denied, ErrUserTokenMissing) || errors.Is(denied, errInvalidToken) {
			status = http.StatusUnauthorized
		}
		resp := errorResponse(message)
		elapsed := time.Since(start).Milliseconds()
		respBody, _ := json.Marshal(resp)
		respStr := string(respBody)
		s.bus.Publish(Event{
			Type: EventRequestLog, ID: requestID, Method: r.Method, Path: "/query",
			Status: &status, ResponseTime: &elapsed, ResponseBody: &respStr, Stage: "completed",
		})
		s.logRequest(store.RequestLog{
			ID: requestID, Timestamp: time.Now().Format(time.RFC3339), Method: r.Method,
			Path: "/query", Status: &status, ResponseTime: &elapsed, RequestBody: bodyStr,
			ResponseBody: &respStr, Headers: headers, IP: &ip, UserAgent: &ua, Stage: "completed",
		})
		if user != nil {
			s.recordUserQuery(user, req, "", "denied", "", elapsed)
		}
		writeJSON(w, status, resp)
		return
	}

	s.bumpDailyRequest()

	status, resp, source := s.resolveQuery(r.Context(), req, origin)

	queryStatus := "ok"
	if resp.Code == 0 {
		queryStatus = "error"
	}
	elapsed := time.Since(start).Milliseconds()
	respBody, _ := json.Marshal(resp)
	respStr := string(respBody)
	s.bus.Publish(Event{
		Type: EventRequestLog, ID: requestID, Method: r.Method, Path: "/query",
		Status: &status, ResponseTime: &elapsed, ResponseBody: &respStr, Stage: "completed",
	})
	// Persist a single combined row carrying the request and its response.
	s.logRequest(store.RequestLog{
		ID: requestID, Timestamp: time.Now().Format(time.RFC3339), Method: r.Method,
		Path: "/query", Status: &status, ResponseTime: &elapsed, RequestBody: bodyStr,
		ResponseBody: &respStr, Headers: headers, IP: &ip, UserAgent: &ua, Stage: "completed",
	})

	// Charge the query against the user's count quota (duration and unlimited
	// plans are unaffected) and record the per-user usage log.
	if user != nil {
		if _, err := s.store.ConsumeQuota(user.ID); err != nil {
			s.PublishError("consume quota: %v", err)
		}
		answer := ""
		if resp.Data != nil {
			answer = resp.Data.Answer
		}
		s.recordUserQuery(user, req, source, queryStatus, answer, elapsed)
	}

	writeJSON(w, status, resp)
}

// authorizeQuery resolves the request's user token and validates the plan. The
// token is looked up from the Authorization header, the URL query, or the
// request body (bodyToken), in that order. When the user system is disabled or
// tokens are optional, a missing token is allowed (returns nil user, nil
// error). A non-nil error denies the query.
func (s *Server) authorizeQuery(r *http.Request, bodyToken string) (*store.User, error) {
	settings := s.config.Settings()
	if !settings.UsersEnabled {
		return nil, nil
	}
	var (
		u   *store.User
		err error
	)
	if userTokenFromRequest(r) == "" && bodyToken != "" {
		u, err = s.userFromToken(bodyToken)
	} else {
		u, err = s.userFromRequest(r)
	}
	if err != nil {
		if errors.Is(err, ErrUserTokenMissing) && !settings.RequireTokenForQuery {
			return nil, nil
		}
		if errors.Is(err, ErrUserTokenMissing) {
			return nil, err
		}
		return nil, errInvalidToken
	}
	switch userUsable(u) {
	case plan.StatusExpired:
		return u, errors.New("套餐已过期")
	case plan.StatusExhausted:
		return u, errors.New("次数已用尽")
	case plan.StatusDisabled:
		return u, errors.New("账号已停用")
	}
	return u, nil
}

// recordUserQuery writes one user request log and periodically prunes the
// per-user, age and global limits.
func (s *Server) recordUserQuery(u *store.User, req QueryRequest, source, status, answer string, elapsed int64) {
	if source == "" {
		source = "bank"
	}
	options := ""
	if req.Options != nil {
		options = *req.Options
	}
	log := store.UserRequestLog{
		UserID: u.ID, Timestamp: time.Now().Format("2006-01-02 15:04:05"),
		Question: req.Title, Options: options, Answer: answer,
		Source: source, Status: status, ResponseTime: elapsed,
	}
	if err := s.store.InsertUserLog(log); err != nil {
		s.PublishError("insert user log: %v", err)
		return
	}
	settings := s.config.Settings()
	if err := s.store.PruneUserLogs(settings.UserLogPerUserLimit, settings.UserLogRetentionDays, settings.UserLogGlobalLimit); err != nil {
		s.PublishError("prune user logs: %v", err)
	}
}

// errInvalidToken marks a token that does not resolve to an enabled user.
var errInvalidToken = errors.New("令牌无效")

// resolveQuery checks the Redis cache first, then the bank, and on a miss asks
// the AI models. It also reports which source produced the answer (cache, bank
// or ai).
func (s *Server) resolveQuery(ctx context.Context, req QueryRequest, origin string) (int, queryResponse, string) {
	hasURL := containsURL(req.Title)
	if !hasURL && req.Options != nil {
		hasURL = containsURL(*req.Options)
	}

	// Serve from Redis first. Both bank and AI answers populate this cache, so a
	// repeat question is answered without touching the database. Skipped when
	// the question carries a URL because those answers can change with the
	// image. The cache stores raw data so it serves both plain-text and HTML
	// callers.
	if s.cache != nil && !hasURL {
		var cached bankAnswer
		if s.cache.GetQuery(req.Title, req.Options, &cached) {
			return http.StatusOK, cached.render(origin, req.Raw), "cache"
		}
	}

	hits, err := s.store.Query(req.Title, req.Options, match.Score)
	if err != nil {
		return http.StatusInternalServerError, errorResponse("数据库错误: " + err.Error()), "bank"
	}
	if len(hits) > 0 {
		hit := hits[0]
		entry := bankAnswer{
			ID:                  hit.ID,
			Question:            hit.Question,
			Answer:              hit.Answer,
			IsAI:                hit.IsAI,
			IsPendingCorrection: hit.IsPendingCorrection,
		}
		// Bank answers are cached too so repeated lookups skip the database.
		// Skipped for URL questions whose answers can change with the image.
		if s.cache != nil && !hasURL {
			s.cache.SetQuery(req.Title, req.Options, entry, s.queryCacheTTL())
		}
		return http.StatusOK, entry.render(origin, req.Raw), "bank"
	}

	settings, err := s.ModelSettings()
	if err != nil {
		return http.StatusInternalServerError, errorResponse("模型配置读取失败: " + err.Error()), "ai"
	}

	var content string
	if hasURL {
		content, err = s.callVision(ctx, settings, req)
	} else {
		content, err = s.callTextModels(ctx, settings, req)
	}
	if err != nil {
		return http.StatusRequestTimeout, errorResponse("模型调用失败: " + err.Error()), "ai"
	}

	if msg, isErr := ai.DetectError(content); isErr {
		return http.StatusInternalServerError, errorResponse(msg), "ai"
	}

	answer := strings.TrimSpace(ai.ExtractAnswer(content))
	if strings.Contains(content, "题目不完整,无法确定具体问题.") {
		answer = ""
	}
	if answer == "" {
		if u := firstURL(req.Title); u != "" {
			answer = "请点击链接查看图片: " + u
		}
	}
	// A model that produced no usable answer must not create a bank entry or a
	// cached result. Report the miss instead of returning an empty success.
	if answer == "" {
		return http.StatusOK, errorResponse("AI未能给出有效答案"), "ai"
	}
	// Refusals, "unable to determine" replies and low-information placeholders
	// are refused so they never pollute the bank.
	if reason, bad := ai.IsUnreliableAnswer(answer); bad {
		return http.StatusOK, errorResponse("AI未能给出有效答案：" + reason), "ai"
	}

	// Avoid storing a duplicate of a question that already exists in the bank,
	// even when the similarity lookup above missed it (e.g. formatting-only
	// differences or a concurrent insert).
	if existing, err := s.store.FindByNormalizedQuestion(req.Title); err != nil {
		s.PublishError("find duplicate question: %v", err)
	} else if existing != nil {
		entry := bankAnswer{
			ID:                  existing.ID,
			Question:            existing.Question,
			Answer:              existing.Answer,
			IsAI:                existing.IsAI,
			IsPendingCorrection: existing.IsPendingCorrection,
		}
		if s.cache != nil && !hasURL {
			s.cache.SetQuery(req.Title, req.Options, entry, s.queryCacheTTL())
		}
		return http.StatusOK, entry.render(origin, req.Raw), "ai"
	}

	id, err := s.store.InsertAIResponse(req.Title, answer, req.Options, req.Type, s.saveFolderID())
	if err != nil {
		s.PublishError("store AI answer: %v", err)
		id = 0
	}

	entry := bankAnswer{
		ID:                  id,
		Question:            req.Title,
		Answer:              answer,
		IsAI:                true,
		IsPendingCorrection: false,
	}
	if s.cache != nil && !hasURL {
		s.cache.SetQuery(req.Title, req.Options, entry, s.queryCacheTTL())
	}
	return http.StatusOK, entry.render(origin, req.Raw), "ai"
}

// queryCacheTTL resolves the configured query cache lifetime.
func (s *Server) queryCacheTTL() time.Duration {
	ttl := s.config.RedisConfig().QueryCacheTTL
	if ttl <= 0 {
		ttl = 600
	}
	return time.Duration(ttl) * time.Second
}

// bumpDailyRequest records a query for today, preferring Redis when available.
func (s *Server) bumpDailyRequest() {
	if s.cache != nil {
		s.cache.BumpDailyRequest()
		return
	}
	_ = s.store.IncrementDailyRequestCount()
}

// invalidateQueryCache drops cached query results after the bank changes.
func (s *Server) invalidateQueryCache() {
	if s.cache != nil {
		s.cache.InvalidateQueries()
	}
}

func (s *Server) saveFolderID() int64 {
	settings := s.config.Settings()
	if settings.QuestionSaveFolderID != nil && *settings.QuestionSaveFolderID > 0 {
		return *settings.QuestionSaveFolderID
	}
	return 0
}

// renderQuestion formats the question text for the response.
//
// By default the question is returned as escaped plain text, so callers can
// display it without any HTML interpretation. When raw is true the legacy
// behaviour is used: the text is HTML escaped and a pending-correction button
// is appended for embedding in the OCS userscript page.
func renderQuestion(origin string, id int64, question string, pending, raw bool) string {
	if !raw {
		return question
	}
	escaped := strings.ReplaceAll(html.EscapeString(question), "\n", "<br>")
	button := pendingCorrectionButton(origin, id, pending)
	if button == "" {
		return escaped
	}
	return `<div style="display:flex;align-items:flex-start;gap:8px;flex-wrap:wrap;">` +
		`<span style="flex:1 1 auto;min-width:0;">` + escaped + `</span>` + button + `</div>`
}

func pendingCorrectionButton(origin string, id int64, pending bool) string {
	if id <= 0 {
		return ""
	}
	if pending {
		return `<button type="button" disabled style="padding:4px 10px;border:none;border-radius:999px;background:#f59e0b;color:#fff;font-size:12px;cursor:not-allowed;opacity:0.75;white-space:nowrap;">已标记待修正</button>`
	}
	endpoint := fmt.Sprintf("%s/api/questions/%d/pending-correction", origin, id)
	return fmt.Sprintf(`<button type="button" style="padding:4px 10px;border:none;border-radius:999px;background:#ef4444;color:#fff;font-size:12px;cursor:pointer;white-space:nowrap;" onclick="(async()=>{const btn=this;if(btn.dataset.loading==='1')return;const text=btn.textContent||'标记为待修正';btn.dataset.loading='1';btn.disabled=true;btn.textContent='标记中...';try{const res=await fetch('%s',{method:'POST'});const data=await res.json().catch(()=>({success:false,message:'标记失败'}));if(!res.ok||!data.success)throw new Error(data.message||'标记失败');btn.textContent='已标记待修正';btn.style.opacity='0.75';btn.style.cursor='not-allowed';}catch(error){btn.disabled=false;btn.textContent=text;alert(error&&error.message?error.message:'标记失败');}finally{delete btn.dataset.loading;}})()">标记为待修正</button>`, endpoint)
}

// handleQuestionAction serves POST /api/questions/{id}/pending-correction.
func (s *Server) handleQuestionAction(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/questions/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[1] != "pending-correction" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var id int64
	if _, err := fmt.Sscanf(parts[0], "%d", &id); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "题目ID无效"})
		return
	}
	if err := s.store.SetPendingCorrection(id, true); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "message": "题目已标记为待修正", "id": id,
	})
}

func containsURL(text string) bool {
	return len(match.ExtractURLs(text)) > 0
}

func optional(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return &v
}

// isTruthy interprets common boolean query parameter spellings.
func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// Text and vision model orchestration
// ---------------------------------------------------------------------------

// callTextModels runs the two phase flow used by the desktop app:
//
//	phase 1 - every selected text model answers in parallel,
//	phase 2 - the summary models consolidate the successful base answers into
//	          the final answer; without summary models the majority answer wins.
//
// The returned string is the raw model output of the chosen final provider so
// the caller can run the usual answer extraction and error detection on it.
func (s *Server) callTextModels(ctx context.Context, settings ai.ModelSettings, req QueryRequest) (string, error) {
	models := settings.TextModels()
	if len(models) == 0 {
		// Fall back to any enabled text model.
		models = settings.ModelByCategory("text")
	}
	if len(models) == 0 {
		return "", fmt.Errorf("未配置文本模型")
	}

	prompt := ai.BuildTextPrompt(req.Title, req.Options, req.Type)
	appSettings := s.config.Settings()
	timeout := time.Duration(appSettings.ModelResponseTimeout) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// ---- Phase 1: base answers in parallel --------------------------------
	results := make(chan baseResult, len(models))
	launched := 0
	for _, model := range models {
		platform, ok := s.platformFor(settings, model.PlatformID)
		if !ok {
			continue
		}
		launched++
		go func(m ai.Model, p *ai.Platform) {
			content, err := s.runModel(ctx, m, p, []ai.Message{{Role: "user", Content: prompt}})
			if err != nil {
				results <- baseResult{}
				return
			}
			if _, isErr := ai.DetectError(content); isErr {
				results <- baseResult{}
				return
			}
			answer := strings.TrimSpace(ai.ExtractAnswer(content))
			if answer == "" {
				results <- baseResult{}
				return
			}
			results <- baseResult{modelName: modelDisplayName(m), answer: answer, raw: content}
		}(model, platform)
	}
	if launched == 0 {
		return "", fmt.Errorf("未配置文本模型")
	}

	var successes []baseResult
collect:
	for i := 0; i < launched; i++ {
		select {
		case <-ctx.Done():
			break collect
		case res := <-results:
			if res.answer != "" {
				successes = append(successes, res)
			}
		}
	}
	if len(successes) == 0 {
		return "", fmt.Errorf("所有AI均查询失败")
	}

	// ---- Phase 2: summary models ------------------------------------------
	summaryModels := settings.SummaryModels()
	if len(summaryModels) > 0 {
		base := make([]ai.BaseAnswer, 0, len(successes))
		for _, r := range successes {
			base = append(base, ai.BaseAnswer{ModelName: r.modelName, Answer: r.answer})
		}
		summaryPrompt := ai.BuildSummaryPrompt(req.Title, base)
		if content, ok := s.runSummaryModels(ctx, settings, summaryModels, summaryPrompt); ok {
			return content, nil
		}
		// Fall through to majority vote when every summary model failed.
	}

	// No summary models (or all failed): use the majority base answer.
	return majorityRaw(successes), nil
}

// runSummaryModels queries each summary model in parallel and returns the first
// successful raw response.
func (s *Server) runSummaryModels(ctx context.Context, settings ai.ModelSettings, models []ai.Model, prompt string) (string, bool) {
	type result struct {
		raw string
		ok  bool
	}
	results := make(chan result, len(models))
	launched := 0
	for _, model := range models {
		platform, ok := s.platformFor(settings, model.PlatformID)
		if !ok {
			continue
		}
		launched++
		go func(m ai.Model, p *ai.Platform) {
			content, err := s.runModel(ctx, m, p, []ai.Message{{Role: "user", Content: prompt}})
			if err != nil {
				results <- result{}
				return
			}
			if _, isErr := ai.DetectError(content); isErr {
				results <- result{}
				return
			}
			if strings.TrimSpace(ai.ExtractAnswer(content)) == "" {
				results <- result{}
				return
			}
			results <- result{raw: content, ok: true}
		}(model, platform)
	}
	if launched == 0 {
		return "", false
	}
	for i := 0; i < launched; i++ {
		select {
		case <-ctx.Done():
			return "", false
		case res := <-results:
			if res.ok {
				return res.raw, true
			}
		}
	}
	return "", false
}

// baseResult is one successful phase-1 answer together with its raw output.
type baseResult struct {
	modelName string
	answer    string
	raw       string
}

// majorityRaw picks the majority base answer and returns the raw model output of
// the first base model that produced it, so extraction and reasoning stay intact.
func majorityRaw(successes []baseResult) string {
	answers := make([]string, 0, len(successes))
	for _, r := range successes {
		answers = append(answers, r.answer)
	}
	winner := ai.MajorityAnswer(answers)
	for _, r := range successes {
		if strings.EqualFold(strings.TrimSpace(r.answer), strings.TrimSpace(winner)) {
			return r.raw
		}
	}
	return successes[0].raw
}

// modelDisplayName resolves a human readable label for logs and prompts.
func modelDisplayName(m ai.Model) string {
	if strings.TrimSpace(m.DisplayName) != "" {
		return m.DisplayName
	}
	if strings.TrimSpace(m.Name) != "" {
		return m.Name
	}
	return m.ID
}

// callVision runs the configured vision model against an image URL.
func (s *Server) callVision(ctx context.Context, settings ai.ModelSettings, req QueryRequest) (string, error) {
	model, platform, ok := settings.VisionModel()
	if !ok {
		// No vision model: degrade to the text models with the raw question.
		return s.callTextModels(ctx, settings, req)
	}
	imageURL := firstURL(req.Title)
	if imageURL == "" && req.Options != nil {
		imageURL = firstURL(*req.Options)
	}
	if imageURL == "" {
		return s.callTextModels(ctx, settings, req)
	}

	prompt := ai.BuildVisionPrompt(strings.ReplaceAll(req.Title, imageURL, ""), req.Options)
	appSettings := s.config.Settings()
	timeout := time.Duration(appSettings.ModelResponseTimeout) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	messages := []ai.Message{{
		Role: "user",
		Content: []ai.Part{
			{Type: "text", Text: prompt},
			{Type: "image_url", ImageURL: &ai.ImageURL{URL: imageURL}},
		},
	}}
	return s.runModel(ctx, model, platform, messages)
}

func (s *Server) platformFor(settings ai.ModelSettings, platformID string) (*ai.Platform, bool) {
	for i := range settings.Platforms {
		if settings.Platforms[i].ID == platformID {
			return &settings.Platforms[i], true
		}
	}
	return nil, false
}

func (s *Server) runModel(ctx context.Context, model ai.Model, platform *ai.Platform, messages []ai.Message) (string, error) {
	if platform == nil {
		return "", fmt.Errorf("未找到模型所属平台")
	}
	if platform.BaseURL == "" {
		return "", fmt.Errorf("平台 %s 未配置 Base URL", platform.DisplayName)
	}
	appSettings := s.config.Settings()
	timeout := time.Duration(appSettings.ModelResponseTimeout) * time.Second

	content, _, err := s.ai.Complete(ctx, ai.Options{
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
	}, nil)
	return content, err
}

func firstURL(text string) string {
	urls := match.ExtractURLs(text)
	if len(urls) == 0 {
		return ""
	}
	return urls[0]
}
