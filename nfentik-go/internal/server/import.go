package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nfentik/nfentik-go/internal/mogo"
)

// mogoImportKey is the AppSettings key holding the incremental import cursor.
const mogoImportKey = "mogo_import_state"

// mogoStoredState persists the incremental cursor so a later run only pulls
// questions newer than the previous import.
type mogoStoredState struct {
	BaseURL  string `json:"baseUrl"`
	MaxID    int64  `json:"maxId"`
	LastRun  string `json:"lastRun"`
	Imported int64  `json:"imported"`
}

// mogoImportState is the live progress reported to the console.
type mogoImportState struct {
	Running    bool   `json:"running"`
	Mode       string `json:"mode"`
	BaseURL    string `json:"baseUrl"`
	FolderID   int64  `json:"folderId"`
	Imported   int64  `json:"imported"`
	Skipped    int64  `json:"skipped"`
	Scanned    int64  `json:"scanned"`
	CurrentID  int64  `json:"currentId"`
	MaxID      int64  `json:"maxId"`
	Total      int64  `json:"total"`
	Error      string `json:"error"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
}

// mogoImportJob guards a single running import and exposes its progress.
type mogoImportJob struct {
	state  mogoImportState
	cancel context.CancelFunc
}

// adminImport routes the MOGO import endpoints.
func (s *Server) adminImport(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) < 2 || rest[0] != "mogo" {
		http.NotFound(w, r)
		return
	}
	switch rest[1] {
	case "status":
		s.mogoImportStatus(w, r)
	case "start":
		s.mogoImportStart(w, r)
	case "stop":
		s.mogoImportStop(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) mogoImportReadState() mogoStoredState {
	var st mogoStoredState
	raw, found, err := s.store.GetSetting(mogoImportKey)
	if err != nil || !found {
		return st
	}
	_ = json.Unmarshal(raw, &st)
	return st
}

func (s *Server) mogoImportWriteState(st mogoStoredState) {
	if raw, err := json.Marshal(st); err == nil {
		_ = s.store.SetSetting(mogoImportKey, raw)
	}
}

func (s *Server) mogoImportSnapshot() mogoImportState {
	s.importMu.Lock()
	defer s.importMu.Unlock()
	if s.importJob != nil {
		return s.importJob.state
	}
	stored := s.mogoImportReadState()
	return mogoImportState{Mode: "incremental", BaseURL: stored.BaseURL, MaxID: stored.MaxID}
}

func (s *Server) mogoImportStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	state := s.mogoImportSnapshot()
	stored := s.mogoImportReadState()
	writeJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"state":       state,
		"last_run":    stored.LastRun,
		"last_import": stored.Imported,
	})
}

func (s *Server) mogoImportStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.importMu.Lock()
	job := s.importJob
	s.importMu.Unlock()
	if job != nil && job.cancel != nil {
		job.cancel()
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "已请求停止"})
}

func (s *Server) mogoImportStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		BaseURL  string `json:"base_url"`
		AKey     string `json:"a_key"`
		FolderID int64  `json:"folder_id"`
		Item     int    `json:"item"`
		Mode     string `json:"mode"`
	}
	body, err := readBody(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请求体解析失败"})
		return
	}
	base := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "MOGO 地址无效"})
		return
	}
	if strings.TrimSpace(req.AKey) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请填写 aKey"})
		return
	}
	item := req.Item
	if item <= 0 {
		item = 100
	}
	if item > 500 {
		item = 500
	}
	mode := req.Mode
	if mode != "backfill" {
		mode = "incremental"
	}

	s.importMu.Lock()
	if s.importJob != nil && s.importJob.state.Running {
		s.importMu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "已有导入任务在运行"})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &mogoImportJob{
		cancel: cancel,
		state: mogoImportState{
			Running:   true,
			Mode:      mode,
			BaseURL:   base,
			FolderID:  req.FolderID,
			Total:     0,
			StartedAt: time.Now().Format(time.RFC3339),
		},
	}
	s.importJob = job
	s.importMu.Unlock()

	go s.runMogoImport(ctx, job, base, strings.TrimSpace(req.AKey), req.FolderID, item, mode)

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "导入已开始"})
}

// runMogoImport pulls pages newest-first and stops once an already-imported id
// is reached (incremental mode). Progress is stored on the job so the console
// can poll it.
func (s *Server) runMogoImport(ctx context.Context, job *mogoImportJob, baseURL, aKey string, folderID int64, item int, mode string) {
	finish := func(errMsg string) {
		s.importMu.Lock()
		job.state.Running = false
		job.state.FinishedAt = time.Now().Format(time.RFC3339)
		if errMsg != "" && job.state.Error == "" {
			job.state.Error = errMsg
		}
		state := job.state
		s.importMu.Unlock()

		stored := s.mogoImportReadState()
		if state.MaxID > stored.MaxID {
			stored.MaxID = state.MaxID
		}
		stored.BaseURL = baseURL
		stored.LastRun = state.FinishedAt
		stored.Imported = state.Imported
		s.mogoImportWriteState(stored)
		s.invalidateQueryCache()
	}

	baseline := s.mogoImportReadState().MaxID
	client := mogo.NewClient(baseURL, aKey)
	schema, err := client.Header(ctx)
	if err != nil {
		finish("读取 MOGO 字段定义失败: " + err.Error())
		return
	}

	lastID := int64(-1)
	pages := 0
	for {
		if ctx.Err() != nil {
			finish("")
			return
		}
		page, err := client.FetchPage(ctx, schema, lastID, item)
		if err != nil {
			if ctx.Err() != nil {
				finish("")
			} else {
				finish("拉取数据失败: " + err.Error())
			}
			return
		}
		if len(page.Rows) == 0 {
			break
		}

		s.importMu.Lock()
		if page.TotalCount > 0 {
			job.state.Total = page.TotalCount
		}
		s.importMu.Unlock()

		stop := false
		for _, row := range page.Rows {
			s.importMu.Lock()
			job.state.Scanned++
			job.state.CurrentID = row.ID
			s.importMu.Unlock()

			if mode == "incremental" && baseline > 0 && row.ID <= baseline {
				stop = true
				break
			}

			stem, inline := mogo.SplitQuestion(row.Question)
			options := mogo.ParseOptions(row.Opt)
			if len(options) == 0 {
				options = inline
			}
			created, err := s.store.ImportQuestion(stem, strings.Join(options, "\n"), row.Answer, row.QuestionType, folderID, "import:mogo")

			s.importMu.Lock()
			if err != nil {
				if job.state.Error == "" {
					job.state.Error = "写入题库失败: " + err.Error()
				}
			} else if created {
				job.state.Imported++
			} else {
				job.state.Skipped++
			}
			if row.ID > job.state.MaxID {
				job.state.MaxID = row.ID
			}
			s.importMu.Unlock()
		}

		if stop {
			break
		}
		lastID = page.LastID
		pages++
		if pages%10 == 0 {
			// Persist the cursor periodically so an interrupted run resumes
			// roughly where it stopped.
			s.importMu.Lock()
			maxID := job.state.MaxID
			s.importMu.Unlock()
			stored := s.mogoImportReadState()
			if maxID > stored.MaxID {
				stored.MaxID = maxID
				stored.BaseURL = baseURL
				s.mogoImportWriteState(stored)
			}
		}
		time.Sleep(120 * time.Millisecond)
	}
	finish("")
}
