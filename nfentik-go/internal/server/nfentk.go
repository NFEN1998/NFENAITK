package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nfentik/nfentik-go/internal/mogo"
	"github.com/nfentik/nfentik-go/internal/store"
)

type nfentkSuccess struct {
	Code int    `json:"code"`
	Data string `json:"data"`
}

type nfentkFailure struct {
	Code int `json:"code"`
}

func writeNfentkFailure(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, nfentkFailure{Code: -1})
}

// handleNfentk serves the MOGO question bank contract. It shares the query
// chain, token checks, quota and request logging with /query while rendering a
// compact {"code":1,"data":"..."} / {"code":-1} response.
func (s *Server) handleNfentk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	title := queryValue(r, "q")
	if title == "" {
		title = queryValue(r, "title")
	}
	if title == "" {
		writeNfentkFailure(w)
		return
	}

	info, req := buildNfentkQuery(title, queryValue(r, "info"))

	start := time.Now()
	requestID := newID()
	ip := clientIP(r)
	ua := r.UserAgent()
	headers := sanitizeHeaders(r.Header)
	bodyStr := marshalNfentkRequest(req)

	s.bus.Publish(Event{
		Type: EventRequestLog, ID: requestID, Method: r.Method, Path: "/nfentk",
		RequestBody: bodyStr, Headers: headers, IP: &ip, UserAgent: &ua, Stage: "started",
	})

	user, quotaKind, denied := s.authorizeQuery(r, "")
	if denied != nil {
		status := http.StatusForbidden
		if errors.Is(denied, ErrUserTokenMissing) || errors.Is(denied, errInvalidToken) {
			status = http.StatusUnauthorized
		}
		elapsed := time.Since(start).Milliseconds()
		response := nfentkFailure{Code: -1}
		responseBody := marshalNfentkResponse(response)
		s.publishNfentkLog(requestID, r, status, elapsed, bodyStr, responseBody, headers, ip, ua)
		s.logRequest(store.RequestLog{
			ID: requestID, Timestamp: time.Now().Format(time.RFC3339), Method: r.Method,
			Path: "/nfentk", Status: &status, ResponseTime: &elapsed, RequestBody: bodyStr,
			ResponseBody: responseBody, Headers: headers, IP: &ip, UserAgent: &ua, Stage: "completed",
		})
		if user != nil {
			s.recordUserQuery(user, req, "", "denied", "", elapsed)
		}
		writeNfentkFailure(w)
		return
	}

	s.bumpDailyRequest()

	result := s.resolveShared(r.Context(), req)
	answer := ""
	if result.errMsg == "" {
		answer = mogo.FormatAnswer(info, result.entry.Answer)
	}

	status := result.status
	if status == 0 {
		status = http.StatusOK
	}
	elapsed := time.Since(start).Milliseconds()

	var response any
	if answer == "" {
		response = nfentkFailure{Code: -1}
	} else {
		response = nfentkSuccess{Code: 1, Data: answer}
	}
	responseBody := marshalNfentkResponse(response)

	s.publishNfentkLog(requestID, r, status, elapsed, bodyStr, responseBody, headers, ip, ua)
	s.logRequest(store.RequestLog{
		ID: requestID, Timestamp: time.Now().Format(time.RFC3339), Method: r.Method,
		Path: "/nfentk", Status: &status, ResponseTime: &elapsed, RequestBody: bodyStr,
		ResponseBody: responseBody, Headers: headers, IP: &ip, UserAgent: &ua, Stage: "completed",
	})

	if user != nil {
		s.consumeQueryQuota(user, quotaKind)
		queryStatus := "ok"
		if answer == "" {
			queryStatus = "error"
		}
		s.recordUserQuery(user, req, result.source, queryStatus, answer, elapsed)
	}

	writeJSON(w, http.StatusOK, response)
}

// firstQueryValue returns the first non-empty value. MOGO may keep a trailing
// placeholder such as q= and append the real value as a second q parameter.
func firstQueryValue(values []string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func buildNfentkQuery(title, infoRaw string) (mogo.Info, QueryRequest) {
	info := mogo.ParseInfo(infoRaw)
	return info, QueryRequest{
		Title:       title,
		Type:        info.InternalType(),
		Options:     info.QueryOptions(),
		ExtraPrompt: info.ExtraPrompt(),
	}
}

func (s *Server) publishNfentkLog(requestID string, r *http.Request, status int, elapsed int64, requestBody, responseBody *string, headers map[string]string, ip, ua string) {
	s.bus.Publish(Event{
		Type: EventRequestLog, ID: requestID, Method: r.Method, Path: "/nfentk",
		Status: &status, ResponseTime: &elapsed, RequestBody: requestBody,
		ResponseBody: responseBody, Headers: headers, IP: &ip, UserAgent: &ua, Stage: "completed",
	})
}

func marshalNfentkRequest(req QueryRequest) *string {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil
	}
	value := string(raw)
	return &value
}

func marshalNfentkResponse(response any) *string {
	raw, err := json.Marshal(response)
	if err != nil {
		return nil
	}
	value := string(raw)
	return &value
}
