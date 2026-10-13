package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nfentik/nfentik-go/internal/mogo"
)

// mogoSourcesKey is the AppSettings key holding the configured MOGO sources.
const mogoSourcesKey = "mogo_sources"

// mogoSchedule describes when a source is imported automatically.
type mogoSchedule struct {
	Type         string `json:"type"` // manual | interval | daily
	EveryMinutes int    `json:"everyMinutes"`
	AtTime       string `json:"atTime"` // "HH:MM" for daily
}

// mogoThrottle keeps the import gentle so it does not load the MOGO host.
type mogoThrottle struct {
	PageDelayMs int `json:"pageDelayMs"`
	RowDelayMs  int `json:"rowDelayMs"`
	MaxPerRun   int `json:"maxPerRun"` // 0 = unlimited
}

// mogoSource is one configured MOGO import source plus its incremental cursor.
type mogoSource struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Enabled      bool         `json:"enabled"`
	BaseURL      string       `json:"baseUrl"`
	AKey         string       `json:"aKey"`
	Item         int          `json:"item"`
	Mode         string       `json:"mode"` // incremental | backfill
	Schedule     mogoSchedule `json:"schedule"`
	Throttle     mogoThrottle `json:"throttle"`
	MaxID        int64        `json:"maxId"`
	LastRun      string       `json:"lastRun"`
	LastImported int64        `json:"lastImported"`
	LastError    string       `json:"lastError"`
}

// mogoImportState is the live progress of the current job.
type mogoImportState struct {
	Running    bool   `json:"running"`
	SourceID   string `json:"sourceId"`
	SourceName string `json:"sourceName"`
	Mode       string `json:"mode"`
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

// ---------------------------------------------------------------------------
// source store
// ---------------------------------------------------------------------------

func (s *Server) mogoLoadSources() []mogoSource {
	raw, found, err := s.store.GetSetting(mogoSourcesKey)
	if err != nil || !found {
		return nil
	}
	var doc struct {
		Sources []mogoSource `json:"sources"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	return doc.Sources
}

func (s *Server) mogoSaveSources(sources []mogoSource) error {
	if sources == nil {
		sources = []mogoSource{}
	}
	raw, err := json.Marshal(map[string]any{"sources": sources})
	if err != nil {
		return err
	}
	return s.store.SetSetting(mogoSourcesKey, raw)
}

// mogoUpdateSource loads the sources, mutates the matching one and saves.
func (s *Server) mogoUpdateSource(id string, fn func(*mogoSource)) {
	s.importMu.Lock()
	defer s.importMu.Unlock()
	sources := s.mogoLoadSources()
	for i := range sources {
		if sources[i].ID == id {
			fn(&sources[i])
			break
		}
	}
	_ = s.mogoSaveSources(sources)
}

// ---------------------------------------------------------------------------
// routing
// ---------------------------------------------------------------------------

// adminMogo routes the MOGO import endpoints under /api/admin/mogo/*.
func (s *Server) adminMogo(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		http.NotFound(w, r)
		return
	}
	switch rest[0] {
	case "sources":
		s.mogoSources(w, r, rest[1:])
	case "status":
		s.mogoStatus(w, r)
	case "stop":
		s.mogoStop(w, r)
	case "test":
		s.mogoTest(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) mogoSources(w http.ResponseWriter, r *http.Request, rest []string) {
	// /sources            GET list, PUT replace
	// /sources/{id}/run   POST run now
	if len(rest) >= 2 && rest[1] == "run" {
		s.mogoRunSource(w, r, rest[0])
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "sources": s.mogoLoadSources()})
	case http.MethodPut, http.MethodPost:
		body, err := readBody(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
			return
		}
		var doc struct {
			Sources []mogoSource `json:"sources"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "配置解析失败"})
			return
		}
		for i := range doc.Sources {
			src := &doc.Sources[i]
			src.Name = strings.TrimSpace(src.Name)
			src.BaseURL = strings.TrimRight(strings.TrimSpace(src.BaseURL), "/")
			if src.Name == "" {
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "名称不能为空"})
				return
			}
			if !validHTTPURL(src.BaseURL) {
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "「" + src.Name + "」的地址无效"})
				return
			}
			if strings.TrimSpace(src.AKey) == "" {
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "「" + src.Name + "」缺少 aKey"})
				return
			}
			if src.ID == "" {
				src.ID = "mogo-" + time.Now().Format("20060102150405.000000")
			}
			normalizeSource(src)
		}
		if err := s.mogoSaveSources(doc.Sources); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "sources": doc.Sources})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func normalizeSource(src *mogoSource) {
	if src.Item <= 0 {
		src.Item = 50
	}
	if src.Item > 500 {
		src.Item = 500
	}
	if src.Mode != "backfill" {
		src.Mode = "incremental"
	}
	switch src.Schedule.Type {
	case "interval", "daily":
	default:
		src.Schedule.Type = "manual"
	}
	if src.Schedule.Type == "interval" && src.Schedule.EveryMinutes <= 0 {
		src.Schedule.EveryMinutes = 60
	}
	if src.Throttle.PageDelayMs < 0 {
		src.Throttle.PageDelayMs = 0
	}
	if src.Throttle.PageDelayMs == 0 {
		src.Throttle.PageDelayMs = 1500
	}
	if src.Throttle.RowDelayMs < 0 {
		src.Throttle.RowDelayMs = 0
	}
}

func validHTTPURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (s *Server) mogoStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.importMu.Lock()
	state := mogoImportState{}
	if s.importJob != nil {
		state = s.importJob.state
	}
	s.importMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "state": state})
}

func (s *Server) mogoTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		BaseURL string `json:"base_url"`
		AKey    string `json:"a_key"`
	}
	body, err := readBody(r)
	if err != nil || json.Unmarshal(body, &req) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请求体解析失败"})
		return
	}
	if !validHTTPURL(req.BaseURL) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "地址无效"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	schema, err := mogo.NewClient(req.BaseURL, req.AKey).Header(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "message": "连接失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "连接成功", "fields": len(schema.TableHeaderKey), "dbType": schema.DBType})
}

// ---------------------------------------------------------------------------
// run / stop
// ---------------------------------------------------------------------------

func (s *Server) mogoRunSource(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var target *mogoSource
	for _, src := range s.mogoLoadSources() {
		if src.ID == id {
			cp := src
			target = &cp
			break
		}
	}
	if target == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "源不存在"})
		return
	}
	if err := s.startMogoJob(*target); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "导入已开始"})
}

func (s *Server) mogoStop(w http.ResponseWriter, r *http.Request) {
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

// startMogoJob begins a background import unless one is already running.
func (s *Server) startMogoJob(src mogoSource) error {
	s.importMu.Lock()
	if s.importJob != nil && s.importJob.state.Running {
		s.importMu.Unlock()
		return errMogoBusy
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &mogoImportJob{
		cancel: cancel,
		state: mogoImportState{
			Running:    true,
			SourceID:   src.ID,
			SourceName: src.Name,
			Mode:       src.Mode,
			MaxID:      src.MaxID,
			StartedAt:  time.Now().Format(time.RFC3339),
		},
	}
	s.importJob = job
	s.importMu.Unlock()

	go s.runMogoSource(ctx, job, src)
	return nil
}

type mogoBusyError struct{}

func (mogoBusyError) Error() string { return "已有导入任务在运行" }

var errMogoBusy = mogoBusyError{}

// runMogoSource pulls pages newest-first with throttling and updates the source
// cursor when it finishes.
func (s *Server) runMogoSource(ctx context.Context, job *mogoImportJob, src mogoSource) {
	finish := func(errMsg string) {
		s.importMu.Lock()
		job.state.Running = false
		job.state.FinishedAt = time.Now().Format(time.RFC3339)
		if errMsg != "" && job.state.Error == "" {
			job.state.Error = errMsg
		}
		final := job.state
		s.importMu.Unlock()

		s.mogoUpdateSource(src.ID, func(s2 *mogoSource) {
			if final.MaxID > s2.MaxID {
				s2.MaxID = final.MaxID
			}
			s2.LastRun = final.FinishedAt
			s2.LastImported = final.Imported
			s2.LastError = final.Error
		})
		s.invalidateQueryCache()
	}

	baseline := src.MaxID
	client := mogo.NewClient(src.BaseURL, src.AKey)
	schema, err := client.Header(ctx)
	if err != nil {
		finish("读取字段定义失败: " + err.Error())
		return
	}

	lastID := int64(-1)
	pages := 0
	for {
		if ctx.Err() != nil {
			finish("")
			return
		}
		page, err := client.FetchPage(ctx, schema, lastID, src.Item)
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
			if ctx.Err() != nil {
				s.mogoPersistCursor(src.ID, job)
				finish("")
				return
			}
			s.importMu.Lock()
			job.state.Scanned++
			job.state.CurrentID = row.ID
			s.importMu.Unlock()

			if src.Mode == "incremental" && baseline > 0 && row.ID <= baseline {
				stop = true
				break
			}

			stem, inline := mogo.SplitQuestion(row.Question)
			options := mogo.ParseOptions(row.Opt)
			if len(options) == 0 {
				options = inline
			}
			created, err := s.store.ImportQuestion(stem, strings.Join(options, "\n"), row.Answer, row.QuestionType, "import:mogo")

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
			done := job.state.Imported + job.state.Skipped
			s.importMu.Unlock()

			if src.Throttle.RowDelayMs > 0 {
				time.Sleep(time.Duration(src.Throttle.RowDelayMs) * time.Millisecond)
			}
			if src.Throttle.MaxPerRun > 0 && done >= int64(src.Throttle.MaxPerRun) {
				stop = true
				break
			}
		}

		pages++
		if pages%10 == 0 {
			s.mogoPersistCursor(src.ID, job)
		}
		if stop {
			break
		}
		lastID = page.LastID
		if src.Throttle.PageDelayMs > 0 {
			select {
			case <-ctx.Done():
				s.mogoPersistCursor(src.ID, job)
				finish("")
				return
			case <-time.After(time.Duration(src.Throttle.PageDelayMs) * time.Millisecond):
			}
		}
	}
	finish("")
}

func (s *Server) mogoPersistCursor(id string, job *mogoImportJob) {
	s.importMu.Lock()
	maxID := job.state.MaxID
	s.importMu.Unlock()
	s.mogoUpdateSource(id, func(src *mogoSource) {
		if maxID > src.MaxID {
			src.MaxID = maxID
		}
	})
}

// ---------------------------------------------------------------------------
// scheduler
// ---------------------------------------------------------------------------

// startMogoScheduler launches the periodic scheduler exactly once.
func (s *Server) startMogoScheduler() {
	s.importOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				s.runDueMogoSources()
			}
		}()
	})
}

// runDueMogoSources starts the first due, enabled source when idle.
func (s *Server) runDueMogoSources() {
	s.importMu.Lock()
	busy := s.importJob != nil && s.importJob.state.Running
	s.importMu.Unlock()
	if busy {
		return
	}
	now := time.Now()
	for _, src := range s.mogoLoadSources() {
		if !src.Enabled {
			continue
		}
		if !scheduleDue(src.Schedule, src.LastRun, now) {
			continue
		}
		if err := s.startMogoJob(src); err == nil {
			return
		}
	}
}

// scheduleDue reports whether a source's schedule is due at now.
func scheduleDue(sched mogoSchedule, lastRun string, now time.Time) bool {
	switch sched.Type {
	case "interval":
		if sched.EveryMinutes <= 0 {
			return false
		}
		last, err := time.Parse(time.RFC3339, lastRun)
		if err != nil {
			return true // never ran: run once now
		}
		return now.Sub(last) >= time.Duration(sched.EveryMinutes)*time.Minute
	case "daily":
		hh, mm, ok := parseHHMM(sched.AtTime)
		if !ok {
			return false
		}
		today := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
		if now.Before(today) {
			return false
		}
		last, err := time.Parse(time.RFC3339, lastRun)
		if err != nil {
			return true
		}
		return last.Before(today)
	default:
		return false
	}
}

func parseHHMM(value string) (int, int, bool) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	var hh, mm int
	if _, err := fmt.Sscanf(parts[0], "%d", &hh); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &mm); err != nil {
		return 0, 0, false
	}
	if hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, false
	}
	return hh, mm, true
}
