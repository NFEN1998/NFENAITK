package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nfentik/nfentik-go/internal/mogodb"
)

// fakeDBReader is a deterministic in-memory mogoDBReader for tests.
type fakeDBReader struct {
	rows     []mogodb.Row
	columns  []string
	openErr  error
	colsErr  error
	fetchErr error
	afterIDs []int64
	fetches  int
}

func (f *fakeDBReader) Columns(context.Context) ([]string, error)   { return f.columns, f.colsErr }
func (f *fakeDBReader) Count(context.Context, int64) (int64, error) { return int64(len(f.rows)), nil }
func (f *fakeDBReader) Close() error                                { return nil }
func (f *fakeDBReader) Fetch(_ context.Context, afterID int64, limit int) ([]mogodb.Row, int64, error) {
	if f.fetchErr != nil {
		return nil, afterID, f.fetchErr
	}
	f.fetches++
	f.afterIDs = append(f.afterIDs, afterID)
	var out []mogodb.Row
	last := afterID
	for _, r := range f.rows {
		if r.ID > afterID {
			out = append(out, r)
			last = r.ID
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, last, nil
}

func fullColumns() []string { return []string{"id", "question", "answer", "opt", "questionType"} }

func TestNormalizeSourceDatabaseDefaults(t *testing.T) {
	httpSrc := mogoSource{}
	normalizeSource(&httpSrc)
	if httpSrc.Type != "http" || httpSrc.Throttle.PageDelayMs != 1500 {
		t.Fatalf("http defaults wrong: %+v", httpSrc)
	}

	dbSrc := mogoSource{Type: "database"}
	normalizeSource(&dbSrc)
	if dbSrc.Database.BatchSize != defaultDBBatchSize {
		t.Fatalf("db batch default = %d, want %d", dbSrc.Database.BatchSize, defaultDBBatchSize)
	}
	if dbSrc.Throttle.PageDelayMs == 1500 {
		t.Fatalf("database source got an HTTP page delay")
	}

	clamp := mogoSource{Type: "database", Database: mogoDatabase{BatchSize: 99999}, Throttle: mogoThrottle{RowsPerSecond: -5}}
	normalizeSource(&clamp)
	if clamp.Database.BatchSize != maxDBBatchSize {
		t.Fatalf("batch size not clamped: %d", clamp.Database.BatchSize)
	}
	if clamp.Throttle.RowsPerSecond != 0 {
		t.Fatalf("negative rows-per-second not clamped: %d", clamp.Throttle.RowsPerSecond)
	}
}

func TestMogoPasswordMaskRoundTrip(t *testing.T) {
	stored := []mogoSource{{ID: "a", Type: "database", Database: mogoDatabase{Password: "secret"}}}
	masked := mogoMaskedSources(stored)
	if masked[0].Database.Password != mogoPasswordMask {
		t.Fatalf("password not masked: %q", masked[0].Database.Password)
	}
	if stored[0].Database.Password != "secret" {
		t.Fatalf("mask mutated the original slice")
	}

	incoming := []mogoSource{{ID: "a", Type: "database", Database: mogoDatabase{Password: mogoPasswordMask}}}
	mogoRestorePasswords(incoming, stored)
	if incoming[0].Database.Password != "secret" {
		t.Fatalf("stored password not restored: %q", incoming[0].Database.Password)
	}
}

func TestMissingColumns(t *testing.T) {
	mapped := mogodb.DefaultColumnMap()
	if got := missingColumns(mapped, fullColumns()); len(got) != 0 {
		t.Fatalf("expected no missing columns, got %v", got)
	}
	if got := missingColumns(mapped, []string{"id", "question", "answer", "opt"}); len(got) != 1 || got[0] != "questionType" {
		t.Fatalf("missingColumns = %v, want [questionType]", got)
	}
}

func dbTestSource(maxID int64) mogoSource {
	return mogoSource{
		ID: "db1", Name: "db", Type: "database", Mode: "incremental", MaxID: maxID,
		Database: mogoDatabase{Host: "h", User: "u", Database: "d", Table: "t", BatchSize: 2},
	}
}

func TestRunMogoDatabaseImports(t *testing.T) {
	srv, _ := testServer(t)
	q := "数据库导入测试 unique db import"
	reader := &fakeDBReader{
		columns: fullColumns(),
		rows:    []mogodb.Row{{ID: 10, Question: q, Answer: "A", QuestionType: "单选题"}},
	}
	srv.newMogoDBReader = func(mogodb.Config) (mogoDBReader, error) { return reader, nil }
	defer srv.store.DB().Exec(`DELETE FROM AIResponses WHERE Question = $1`, q)

	src := dbTestSource(5)
	job := &mogoImportJob{state: mogoImportState{Running: true, SourceID: "db1", MaxID: 5}}
	srv.runMogoDatabaseSource(context.Background(), job, src)

	if reader.afterIDs[0] != 5 {
		t.Fatalf("first fetch afterID = %d, want the cursor 5", reader.afterIDs[0])
	}
	if job.state.Imported != 1 || job.state.Scanned != 1 {
		t.Fatalf("counts wrong: imported=%d scanned=%d", job.state.Imported, job.state.Scanned)
	}
	var source string
	if err := srv.store.DB().QueryRow(`SELECT Source FROM AIResponses WHERE Question = $1`, q).Scan(&source); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if source != "import:mogo-db" {
		t.Fatalf("source = %q, want import:mogo-db", source)
	}
}

func TestRunMogoDatabaseOverwriteExisting(t *testing.T) {
	srv, _ := testServer(t)
	q := "数据库覆盖测试 unique db overwrite"
	if _, err := srv.store.InsertAIResponse(q, "AI答案", nil, nil); err != nil {
		t.Fatalf("seed ai answer: %v", err)
	}
	defer srv.store.DB().Exec(`DELETE FROM AIResponses WHERE Question = $1`, q)

	reader := &fakeDBReader{
		columns: fullColumns(),
		rows:    []mogodb.Row{{ID: 10, Question: q, Answer: "MOGO答案", QuestionType: "单选题"}},
	}
	srv.newMogoDBReader = func(mogodb.Config) (mogoDBReader, error) { return reader, nil }

	src := dbTestSource(0)
	src.Overwrite = true
	job := &mogoImportJob{state: mogoImportState{Running: true, SourceID: "db1"}}
	srv.runMogoDatabaseSource(context.Background(), job, src)

	if job.state.Updated != 1 || job.state.Imported != 0 || job.state.Skipped != 0 {
		t.Fatalf("counts wrong: imported=%d updated=%d skipped=%d", job.state.Imported, job.state.Updated, job.state.Skipped)
	}
	var answer, source string
	var isAI bool
	if err := srv.store.DB().QueryRow(`SELECT Answer, Source, IsAi FROM AIResponses WHERE Question = $1`, q).
		Scan(&answer, &source, &isAI); err != nil {
		t.Fatalf("read overwritten row: %v", err)
	}
	if answer != "MOGO答案" || source != "import:mogo-db" || isAI {
		t.Fatalf("overwrite result: answer=%q source=%q isAI=%v", answer, source, isAI)
	}
}

func TestRunMogoDatabaseKeepsExistingWithoutOverwrite(t *testing.T) {
	srv, _ := testServer(t)
	q := "数据库默认不覆盖测试 unique db no overwrite"
	if _, err := srv.store.InsertAIResponse(q, "AI答案", nil, nil); err != nil {
		t.Fatalf("seed ai answer: %v", err)
	}
	defer srv.store.DB().Exec(`DELETE FROM AIResponses WHERE Question = $1`, q)

	reader := &fakeDBReader{
		columns: fullColumns(),
		rows:    []mogodb.Row{{ID: 10, Question: q, Answer: "MOGO答案"}},
	}
	srv.newMogoDBReader = func(mogodb.Config) (mogoDBReader, error) { return reader, nil }

	job := &mogoImportJob{state: mogoImportState{Running: true, SourceID: "db1"}}
	srv.runMogoDatabaseSource(context.Background(), job, dbTestSource(0))

	if job.state.Skipped != 1 || job.state.Updated != 0 || job.state.Imported != 0 {
		t.Fatalf("counts wrong: imported=%d updated=%d skipped=%d", job.state.Imported, job.state.Updated, job.state.Skipped)
	}
	var answer, source string
	var isAI bool
	if err := srv.store.DB().QueryRow(`SELECT Answer, Source, IsAi FROM AIResponses WHERE Question = $1`, q).
		Scan(&answer, &source, &isAI); err != nil {
		t.Fatalf("read existing row: %v", err)
	}
	if answer != "AI答案" || source != "ai" || !isAI {
		t.Fatalf("existing row changed: answer=%q source=%q isAI=%v", answer, source, isAI)
	}
}

func TestRunMogoDatabaseMaxPerRunStops(t *testing.T) {
	srv, _ := testServer(t)
	q := "数据库上限测试 unique db maxperrun"
	reader := &fakeDBReader{
		columns: fullColumns(),
		rows: []mogodb.Row{
			{ID: 10, Question: q + "-a", Answer: "A"},
			{ID: 11, Question: q + "-b", Answer: "B"},
			{ID: 12, Question: q + "-c", Answer: "C"},
		},
	}
	srv.newMogoDBReader = func(mogodb.Config) (mogoDBReader, error) { return reader, nil }
	defer srv.store.DB().Exec(`DELETE FROM AIResponses WHERE Question LIKE $1`, q+"%")

	src := dbTestSource(0)
	src.Throttle.MaxPerRun = 2
	job := &mogoImportJob{state: mogoImportState{Running: true, SourceID: "db1"}}
	srv.runMogoDatabaseSource(context.Background(), job, src)

	if got := job.state.Imported + job.state.Skipped; got != 2 {
		t.Fatalf("processed %d rows, want 2 (MaxPerRun)", got)
	}
	if job.state.CurrentID != 11 {
		t.Fatalf("current id = %d, want 11", job.state.CurrentID)
	}
}

func TestRunMogoDatabaseConnectError(t *testing.T) {
	srv, _ := testServer(t)
	srv.newMogoDBReader = func(mogodb.Config) (mogoDBReader, error) { return nil, context.DeadlineExceeded }
	job := &mogoImportJob{state: mogoImportState{Running: true, SourceID: "db1"}}
	srv.runMogoDatabaseSource(context.Background(), job, dbTestSource(0))
	if !strings.Contains(job.state.Error, "连接数据库失败") {
		t.Fatalf("error = %q, want a connection failure", job.state.Error)
	}
}

func TestRunMogoDatabaseMissingColumn(t *testing.T) {
	srv, _ := testServer(t)
	reader := &fakeDBReader{columns: []string{"id", "question"}}
	srv.newMogoDBReader = func(mogodb.Config) (mogoDBReader, error) { return reader, nil }
	job := &mogoImportJob{state: mogoImportState{Running: true, SourceID: "db1"}}
	srv.runMogoDatabaseSource(context.Background(), job, dbTestSource(0))
	if !strings.Contains(job.state.Error, "缺少列") || !strings.Contains(job.state.Error, "answer") {
		t.Fatalf("error = %q, want a missing-column error", job.state.Error)
	}
}

func TestMogoDBTestEndpoint(t *testing.T) {
	srv, _ := testServer(t)
	srv.newMogoDBReader = func(mogodb.Config) (mogoDBReader, error) {
		return &fakeDBReader{columns: fullColumns()}, nil
	}
	req := newJSONRequest(http.MethodPost, "/api/admin/mogo/db-test", map[string]any{
		"host": "h", "user": "u", "database": "d", "table": "t",
	})
	rec := newRecorder()
	srv.adminMogo(rec, req, []string{"db-test"})
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["success"] != true {
		t.Fatalf("payload = %v", payload)
	}
	cols, _ := payload["columns"].([]any)
	if len(cols) != len(fullColumns()) {
		t.Fatalf("columns = %v", payload["columns"])
	}

	// A reader error surfaces as a failed test.
	srv.newMogoDBReader = func(mogodb.Config) (mogoDBReader, error) { return nil, context.DeadlineExceeded }
	rec = newRecorder()
	srv.adminMogo(rec, newJSONRequest(http.MethodPost, "/api/admin/mogo/db-test", map[string]any{
		"host": "h", "user": "u", "database": "d", "table": "t",
	}), []string{"db-test"})
	payload = map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	if payload["success"] == true {
		t.Fatalf("expected failure payload, got %v", payload)
	}
}
