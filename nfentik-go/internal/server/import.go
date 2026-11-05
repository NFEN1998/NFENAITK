package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nfentik/nfentik-go/internal/mogo"
	"github.com/nfentik/nfentik-go/internal/mogodb"
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
	PageDelayMs   int `json:"pageDelayMs"`
	RowDelayMs    int `json:"rowDelayMs"`
	RowsPerSecond int `json:"rowsPerSecond"` // database source read rate; 0 = unlimited
	BatchDelayMs  int `json:"batchDelayMs"`  // database source pause between batches
	MaxPerRun     int `json:"maxPerRun"`     // 0 = unlimited
}

// mogoDatabase holds a database source's connection and mapping settings.
type mogoDatabase struct {
	Host      string            `json:"host"`
	Port      string            `json:"port"`
	User      string            `json:"user"`
	Password  string            `json:"password"`
	Database  string            `json:"database"`
	Table     string            `json:"table"`
	BatchSize int               `json:"batchSize"`
	Columns   map[string]string `json:"columns"`
}

// mogoSource is one configured MOGO import source plus its incremental cursor.
// Type is "http" (default) or "database".
type mogoSource struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"baseUrl"`
	AKey    string `json:"aKey"`
	Item    int    `json:"item"`
	Mode    string `json:"mode"` // incremental | backfill
	Retries int    `json:"retries"`
	// Overwrite replaces the answer of an already existing row for the same
	// normalized question and options. Off by default so imports never clobber
	// AI, third-party or manually corrected answers unless asked to.
	Overwrite    bool         `json:"overwrite"`
	Database     mogoDatabase `json:"database"`
	Schedule     mogoSchedule `json:"schedule"`
	Throttle     mogoThrottle `json:"throttle"`
	MaxID        int64        `json:"maxId"`
	LastRun      string       `json:"lastRun"`
	LastImported int64        `json:"lastImported"`
	LastUpdated  int64        `json:"lastUpdated"`
	LastError    string       `json:"lastError"`
}

// defaultMogoRetries is used when a source does not configure its own retry
// count, so a transient 502/503/504 from the MOGO host is retried by default.
const defaultMogoRetries = 3

// retryCount resolves the effective number of retries for a source, defaulting
// and clamping to a sane range.
func (src *mogoSource) retryCount() int {
	if src.Retries <= 0 {
		return defaultMogoRetries
	}
	if src.Retries > 10 {
		return 10
	}
	return src.Retries
}

// mogoImportState is the live progress of the current job.
type mogoImportState struct {
	Running    bool   `json:"running"`
	SourceID   string `json:"sourceId"`
	SourceName string `json:"sourceName"`
	Mode       string `json:"mode"`
	Imported   int64  `json:"imported"`
	Updated    int64  `json:"updated"`
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

// mogoPasswordMask is the placeholder returned to the browser in place of a
// stored database password.
const mogoPasswordMask = "***"

// mogoMaskedSources copies the sources with database passwords masked, so the
// real value never leaves the server.
func mogoMaskedSources(sources []mogoSource) []mogoSource {
	out := make([]mogoSource, len(sources))
	copy(out, sources)
	for i := range out {
		if out[i].Database.Password != "" {
			out[i].Database.Password = mogoPasswordMask
		}
	}
	return out
}

// mogoRestorePasswords fills in stored database passwords for sources whose
// submitted password is the mask placeholder.
func mogoRestorePasswords(incoming, stored []mogoSource) {
	byID := make(map[string]string, len(stored))
	for _, s := range stored {
		if s.Database.Password != "" {
			byID[s.ID] = s.Database.Password
		}
	}
	for i := range incoming {
		if incoming[i].Database.Password == mogoPasswordMask {
			if pw, ok := byID[incoming[i].ID]; ok {
				incoming[i].Database.Password = pw
			}
		}
	}
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
	case "db-test":
		s.mogoDBTest(w, r)
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
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "sources": mogoMaskedSources(s.mogoLoadSources())})
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
		// The browser only ever sees the masked password, so a submitted mask
		// means "keep the stored one".
		mogoRestorePasswords(doc.Sources, s.mogoLoadSources())
		for i := range doc.Sources {
			src := &doc.Sources[i]
			src.Name = strings.TrimSpace(src.Name)
			src.BaseURL = strings.TrimRight(strings.TrimSpace(src.BaseURL), "/")
			if src.Type == "" {
				src.Type = "http"
			}
			if src.Name == "" {
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "名称不能为空"})
				return
			}
			if src.Type == "database" {
				if src.Database.Host == "" || src.Database.User == "" || src.Database.Database == "" || src.Database.Table == "" {
					writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "「" + src.Name + "」缺少数据库连接信息（主机/用户名/库名/表名）"})
					return
				}
				if src.Database.Port != "" && !validPort(src.Database.Port) {
					writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "「" + src.Name + "」端口无效"})
					return
				}
			} else {
				if !validHTTPURL(src.BaseURL) {
					writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "「" + src.Name + "」的地址无效"})
					return
				}
				if strings.TrimSpace(src.AKey) == "" {
					writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "「" + src.Name + "」缺少 aKey"})
					return
				}
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
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "sources": mogoMaskedSources(doc.Sources)})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func normalizeSource(src *mogoSource) {
	if src.Type != "database" {
		src.Type = "http"
	}
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
	if src.Throttle.RowDelayMs < 0 {
		src.Throttle.RowDelayMs = 0
	}
	if src.Throttle.RowsPerSecond < 0 {
		src.Throttle.RowsPerSecond = 0
	}
	if src.Throttle.BatchDelayMs < 0 {
		src.Throttle.BatchDelayMs = 0
	}
	if src.Type == "http" && src.Throttle.PageDelayMs == 0 {
		src.Throttle.PageDelayMs = 1500
	}
	if src.Type == "database" {
		src.Database.Host = strings.TrimSpace(src.Database.Host)
		src.Database.Port = strings.TrimSpace(src.Database.Port)
		src.Database.User = strings.TrimSpace(src.Database.User)
		src.Database.Database = strings.TrimSpace(src.Database.Database)
		src.Database.Table = strings.TrimSpace(src.Database.Table)
		if src.Database.BatchSize <= 0 {
			src.Database.BatchSize = defaultDBBatchSize
		}
		if src.Database.BatchSize > maxDBBatchSize {
			src.Database.BatchSize = maxDBBatchSize
		}
	} else {
		// The overwrite switch only applies to the database importer.
		src.Overwrite = false
	}
	src.Retries = src.retryCount()
}

// defaultDBBatchSize and maxDBBatchSize bound the database read batch.
const (
	defaultDBBatchSize = 500
	maxDBBatchSize     = 5000
)

// validPort reports whether raw is a TCP port number in 1..65535.
func validPort(raw string) bool {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	return err == nil && n >= 1 && n <= 65535
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

// mogoDBTest validates a database connection and returns the table's columns so
// the administrator can pick a column mapping.
func (s *Server) mogoDBTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID       string            `json:"id"`
		Host     string            `json:"host"`
		Port     string            `json:"port"`
		User     string            `json:"user"`
		Password string            `json:"password"`
		Database string            `json:"database"`
		Table    string            `json:"table"`
		Columns  map[string]string `json:"columns"`
	}
	body, err := readBody(r)
	if err != nil || json.Unmarshal(body, &req) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请求体解析失败"})
		return
	}
	if strings.TrimSpace(req.Table) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请填写表名"})
		return
	}
	if req.Port != "" && !validPort(req.Port) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "端口无效"})
		return
	}
	// The browser only sees the masked password; reuse the stored one.
	if req.Password == mogoPasswordMask {
		for _, s2 := range s.mogoLoadSources() {
			if s2.ID == req.ID {
				req.Password = s2.Database.Password
				break
			}
		}
	}
	cfg := mogoDBConfig(mogoDatabase{
		Host: req.Host, Port: req.Port, User: req.User, Password: req.Password,
		Database: req.Database, Table: req.Table, Columns: req.Columns,
	})
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	reader, err := s.newMogoDBReader(cfg)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "message": "连接失败: " + err.Error()})
		return
	}
	defer reader.Close()
	columns, err := reader.Columns(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "message": "读取表结构失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "连接成功", "columns": columns})
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

// runMogoSource dispatches an import to the HTTP or database implementation.
func (s *Server) runMogoSource(ctx context.Context, job *mogoImportJob, src mogoSource) {
	if src.Type == "database" {
		s.runMogoDatabaseSource(ctx, job, src)
		return
	}
	s.runMogoHTTPSource(ctx, job, src)
}

// mogoFinish marks the job finished and persists its cursor and run metadata.
func (s *Server) mogoFinish(src mogoSource, job *mogoImportJob, errMsg string) {
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
		s2.LastUpdated = final.Updated
		s2.LastError = final.Error
	})
	s.invalidateQueryCache()
}

// runMogoHTTPSource pulls pages newest-first with throttling and updates the
// source cursor when it finishes.
func (s *Server) runMogoHTTPSource(ctx context.Context, job *mogoImportJob, src mogoSource) {
	finish := func(errMsg string) { s.mogoFinish(src, job, errMsg) }

	baseline := src.MaxID
	client := mogo.NewClient(src.BaseURL, src.AKey)
	client.Retries = src.retryCount()
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

// mogoDBReader is the read-only database surface the importer uses. It is an
// interface so tests can supply a fake reader.
type mogoDBReader interface {
	Columns(ctx context.Context) ([]string, error)
	Count(ctx context.Context, minID int64) (int64, error)
	Fetch(ctx context.Context, afterID int64, limit int) ([]mogodb.Row, int64, error)
	Close() error
}

// runMogoDatabaseSource reads rows from the MOGO database by ascending primary
// key, throttled, and writes them to the local bank under the import:mogo-db
// source tag.
func (s *Server) runMogoDatabaseSource(ctx context.Context, job *mogoImportJob, src mogoSource) {
	finish := func(errMsg string) { s.mogoFinish(src, job, errMsg) }

	cfg := mogoDBConfig(src.Database)
	reader, err := s.newMogoDBReader(cfg)
	if err != nil {
		finish("连接数据库失败: " + err.Error())
		return
	}
	defer reader.Close()

	columns, err := reader.Columns(ctx)
	if err != nil {
		finish("读取表结构失败: " + err.Error())
		return
	}
	if missing := missingColumns(cfg.EffectiveColumns(), columns); len(missing) > 0 {
		finish("缺少列: " + strings.Join(missing, ", "))
		return
	}

	if total, err := reader.Count(ctx, src.MaxID); err == nil && total > 0 {
		s.importMu.Lock()
		job.state.Total = total
		s.importMu.Unlock()
	}

	batchSize := src.Database.BatchSize
	if batchSize <= 0 {
		batchSize = defaultDBBatchSize
	}
	limiter := mogodb.NewLimiter(src.Throttle.RowsPerSecond, src.Throttle.BatchDelayMs)

	cursor := src.MaxID
	started := time.Now()
	batches := 0
	for {
		if ctx.Err() != nil {
			s.mogoPersistCursor(src.ID, job)
			finish("")
			return
		}
		rows, last, err := s.fetchDBWithRetry(ctx, reader, cursor, batchSize, src.retryCount())
		if err != nil {
			if ctx.Err() != nil {
				s.mogoPersistCursor(src.ID, job)
				finish("")
			} else {
				finish("读取数据失败: " + err.Error())
			}
			return
		}
		if len(rows) == 0 {
			break
		}

		stop := false
		for _, row := range rows {
			if ctx.Err() != nil {
				s.mogoPersistCursor(src.ID, job)
				finish("")
				return
			}
			s.importMu.Lock()
			job.state.Scanned++
			job.state.CurrentID = row.ID
			s.importMu.Unlock()

			stem, inline := mogo.SplitQuestion(row.Question)
			options := mogo.ParseOptions(row.Options)
			if len(options) == 0 {
				options = inline
			}
			created, updated, err := s.store.ImportQuestionWithOverwrite(
				stem, strings.Join(options, "\n"), row.Answer, row.QuestionType, "import:mogo-db", src.Overwrite)

			s.importMu.Lock()
			if err != nil {
				if job.state.Error == "" {
					job.state.Error = "写入题库失败: " + err.Error()
				}
			} else if created {
				job.state.Imported++
			} else if updated {
				job.state.Updated++
			} else {
				job.state.Skipped++
			}
			if row.ID > job.state.MaxID {
				job.state.MaxID = row.ID
			}
			done := job.state.Imported + job.state.Updated + job.state.Skipped
			s.importMu.Unlock()

			if src.Throttle.MaxPerRun > 0 && done >= int64(src.Throttle.MaxPerRun) {
				stop = true
				break
			}
		}

		cursor = last
		batches++
		if batches%10 == 0 {
			s.mogoPersistCursor(src.ID, job)
		}
		if stop {
			break
		}
		s.importMu.Lock()
		scanned := job.state.Scanned
		s.importMu.Unlock()
		if err := limiter.Wait(ctx, started, int(scanned)); err != nil {
			s.mogoPersistCursor(src.ID, job)
			finish("")
			return
		}
	}
	finish("")
}

// mogoDBConfig converts a stored database source into a mogodb.Config.
func mogoDBConfig(db mogoDatabase) mogodb.Config {
	cm := mogodb.DefaultColumnMap()
	if v := strings.TrimSpace(db.Columns["id"]); v != "" {
		cm.ID = v
	}
	if v := strings.TrimSpace(db.Columns["question"]); v != "" {
		cm.Question = v
	}
	if v := strings.TrimSpace(db.Columns["answer"]); v != "" {
		cm.Answer = v
	}
	if v := strings.TrimSpace(db.Columns["options"]); v != "" {
		cm.Options = v
	}
	if v := strings.TrimSpace(db.Columns["questionType"]); v != "" {
		cm.QuestionType = v
	}
	return mogodb.Config{
		Host: db.Host, Port: db.Port, User: db.User, Password: db.Password,
		Database: db.Database, Table: db.Table, Columns: cm,
	}
}

// missingColumns returns the mapped column names that are absent from actual.
func missingColumns(mapped mogodb.ColumnMap, actual []string) []string {
	have := make(map[string]bool, len(actual))
	for _, c := range actual {
		have[strings.ToLower(c)] = true
	}
	var missing []string
	seen := make(map[string]bool)
	for _, name := range []string{mapped.ID, mapped.Question, mapped.Answer, mapped.Options, mapped.QuestionType} {
		key := strings.ToLower(name)
		if have[key] || seen[key] {
			continue
		}
		seen[key] = true
		missing = append(missing, name)
	}
	return missing
}

// fetchDBWithRetry retries a Fetch on transient driver errors using the fixed
// MOGO retry delay, honoring context cancellation.
func (s *Server) fetchDBWithRetry(ctx context.Context, reader mogoDBReader, afterID int64, limit, retries int) ([]mogodb.Row, int64, error) {
	attempts := retries + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		rows, last, err := reader.Fetch(ctx, afterID, limit)
		if err == nil {
			return rows, last, nil
		}
		if ctx.Err() != nil {
			return nil, afterID, ctx.Err()
		}
		lastErr = err
		if attempt == attempts {
			break
		}
		if err := sleepContext(ctx, mogo.DefaultRetryDelay); err != nil {
			return nil, afterID, err
		}
	}
	return nil, afterID, lastErr
}

// sleepContext sleeps for d unless ctx is cancelled first.
func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
