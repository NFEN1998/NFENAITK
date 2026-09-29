package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/nfentik/nfentik-go/internal/config"
	"github.com/nfentik/nfentik-go/internal/store"
)

// handleAdmin routes every /api/admin/* endpoint.
func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin"), "/")
	if path == "" {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "nfentik-go admin API"})
		return
	}
	if !s.requireAdmin(r) {
		s.unauthorized(w)
		return
	}

	parts := strings.Split(path, "/")
	switch parts[0] {
	case "settings":
		s.adminSettings(w, r)
	case "questions":
		s.adminQuestions(w, r, parts[1:])
	case "folders":
		s.adminFolders(w, r, parts[1:])
	case "logs":
		s.adminLogs(w, r, parts[1:])
	case "model-config":
		s.adminModelConfig(w, r)
	case "stats":
		s.adminStats(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) adminSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "settings": s.config.Settings()})
	case http.MethodPut, http.MethodPost:
		body, err := readBody(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
			return
		}
		updated, err := s.config.Patch(body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "settings": updated})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) adminQuestions(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			s.listQuestions(w, r)
		case http.MethodPost:
			s.createQuestion(w, r)
		case http.MethodDelete:
			s.deleteQuestions(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	if rest[0] == "search" && r.Method == http.MethodGet {
		keyword := r.URL.Query().Get("keyword")
		var folderID *int64
		if v := r.URL.Query().Get("folder_id"); v != "" {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil {
				folderID = &id
			}
		}
		items, err := s.store.Search(keyword, folderID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "items": items})
		return
	}

	id, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if len(rest) > 1 {
		switch rest[1] {
		case "pending-correction":
			if r.Method == http.MethodPost {
				pending, _ := strconv.ParseBool(defaultString(r.URL.Query().Get("value"), "true"))
				if err := s.store.SetPendingCorrection(id, pending); err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"success": true})
				return
			}
		case "move":
			if r.Method == http.MethodPost {
				var req struct {
					TargetFolderID int64 `json:"target_folder_id"`
				}
				_ = decodeJSON(r, &req)
				if err := s.store.MoveQuestion(id, req.TargetFolderID); err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"success": true})
				return
			}
		case "copy":
			if r.Method == http.MethodPost {
				var req struct {
					TargetFolderID int64 `json:"target_folder_id"`
				}
				_ = decodeJSON(r, &req)
				if err := s.store.CopyQuestion(id, req.TargetFolderID); err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"success": true})
				return
			}
		}
	}

	switch r.Method {
	case http.MethodGet:
		q, err := s.store.GetQuestion(id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "item": q})
	case http.MethodPut:
		var req struct {
			Question     *string `json:"question"`
			Options      *string `json:"options"`
			Answer       *string `json:"answer"`
			QuestionType *string `json:"question_type"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
			return
		}
		if err := s.store.UpdateQuestion(id, req.Question, req.Options, req.Answer, req.QuestionType); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	case http.MethodDelete:
		if err := s.store.DeleteQuestion(id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) listQuestions(w http.ResponseWriter, r *http.Request) {
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	pageSize := parseIntDefault(r.URL.Query().Get("page_size"), 20)
	pendingOnly := r.URL.Query().Get("pending") == "true"
	sortOrder := defaultString(r.URL.Query().Get("sort"), "desc")

	var folderID *int64
	if v := r.URL.Query().Get("folder_id"); v != "" && v != "all" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			folderID = &id
		}
	}
	result, err := s.store.PaginatedQuestions(folderID, pendingOnly, page, pageSize, sortOrder)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "items": result.Items, "total": result.Total, "page": page, "page_size": pageSize,
	})
}

func (s *Server) createQuestion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Question     string  `json:"question"`
		Options      *string `json:"options"`
		Answer       *string `json:"answer"`
		QuestionType *string `json:"question_type"`
		FolderID     int64   `json:"folder_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	if strings.TrimSpace(req.Question) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "题目内容不能为空"})
		return
	}
	q, err := s.store.AddQuestion(req.Question, req.Options, req.Answer, req.QuestionType, req.FolderID, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "item": q})
}

func (s *Server) deleteQuestions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	if err := s.store.DeleteQuestions(req.IDs); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "deleted": len(req.IDs)})
}

func (s *Server) adminFolders(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			folders, err := s.store.Folders()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
				return
			}
			stats, _ := s.store.FolderStats()
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "folders": folders, "stats": stats})
		case http.MethodPost:
			var req struct {
				Name     string `json:"name"`
				ParentID int64  `json:"parent_id"`
			}
			if err := decodeJSON(r, &req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
				return
			}
			if strings.TrimSpace(req.Name) == "" {
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "文件夹名称不能为空"})
				return
			}
			id, err := s.store.AddFolder(req.Name, req.ParentID)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "id": id})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	id, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		questions, err := s.store.RecursiveQuestions(id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		path, _ := s.store.FolderPath(id)
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "items": questions, "path": path})
	case http.MethodPut:
		var req struct {
			Name     *string `json:"name"`
			ParentID *int64  `json:"parent_id"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
			return
		}
		if req.Name != nil {
			if err := s.store.RenameFolder(id, *req.Name); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
				return
			}
		}
		if req.ParentID != nil {
			if err := s.store.MoveFolder(id, *req.ParentID); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	case http.MethodDelete:
		deleteQuestions := r.URL.Query().Get("delete_questions") == "true"
		if err := s.store.DeleteFolder(id, deleteQuestions); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) adminLogs(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) > 0 && rest[0] == "clear" && r.Method == http.MethodDelete {
		if err := s.store.ClearRequestLogs(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := int(parseIntDefault(r.URL.Query().Get("limit"), 200))
	logs, err := s.store.RequestLogs(limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	counts, _ := s.store.DailyRequestCounts()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "logs": logs, "daily": counts})
}

func (s *Server) adminModelConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		raw, err := s.config.ModelConfig()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "config": json.RawMessage(raw)})
	case http.MethodPut, http.MethodPost:
		body, err := readBody(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
			return
		}
		if !json.Valid(body) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "模型配置不是有效的 JSON"})
			return
		}
		if err := s.config.SaveModelConfig(body); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) adminStats(w http.ResponseWriter, r *http.Request) {
	pending, _ := s.store.PendingCorrectionCount()
	counts, _ := s.store.DailyRequestCounts()
	var total int64
	for _, c := range counts {
		total += c.Count
	}
	folders, _ := s.store.FolderStats()
	redisStatus := map[string]any{"enabled": false}
	if s.cache != nil {
		redisStatus = map[string]any{
			"enabled":        true,
			"reachable":      s.cache.Ping(),
			"log_queue":      s.cache.QueueLength(),
			"cache_hit_info": "查询结果缓存已启用",
		}
	}
	schemaVersion, _ := s.store.SchemaVersion()
	writeJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"pending":        pending,
		"daily":          counts,
		"total_requests": total,
		"folder_stats":   folders,
		"subscribers":    s.bus.SubscriberCount(),
		"data_path":      s.DataPath(),
		"redis":          redisStatus,
		"schema_version": schemaVersion,
	})
}

func readBody(r *http.Request) ([]byte, error) {
	body := make([]byte, 0)
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			if n == 0 {
				break
			}
		}
	}
	return body, nil
}

func parseIntDefault(v string, def int64) int64 {
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

func defaultString(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// ensure the store package remains referenced by admin wiring helpers.
var _ = store.Question{}
var _ = config.Settings{}
var _ = fmt.Sprintf
