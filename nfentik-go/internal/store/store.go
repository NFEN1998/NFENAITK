// Package store owns the PostgreSQL or MySQL database. Schema and queries
// mirror the desktop edition's database.rs so existing question banks keep
// working.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nfentik/nfentik-go/internal/match"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Question is one bank entry.
type Question struct {
	ID                  int64   `json:"id"`
	Question            string  `json:"question"`
	Options             *string `json:"options"`
	Answer              string  `json:"answer"`
	QuestionType        *string `json:"question_type"`
	CreateTime          *string `json:"create_time"`
	IsAI                bool    `json:"is_ai"`
	IsPendingCorrection bool    `json:"is_pending_correction"`
}

// Page carries a slice of questions plus the total row count.
type Page struct {
	Items []Question `json:"items"`
	Total int64      `json:"total"`
}

// QueryHit is the compact tuple returned to the OCS query handler.
type QueryHit struct {
	ID                  int64
	Question            string
	Answer              string
	IsAI                bool
	IsPendingCorrection bool
}

// Store wraps the PostgreSQL or MySQL connection pool.
type Store struct {
	db      *sql.DB
	dialect dialect

	// onChange is invoked after any bank mutation. The server uses it to
	// invalidate Redis query caches.
	onChange func()

	// trigram reports whether the pg_trgm extension and its GIN index are
	// available for ranked fuzzy candidate lookup. When false the query path
	// falls back to a bounded prefix probe.
	trigram bool
}

// OnChange registers a callback fired after every bank mutation.
func (s *Store) OnChange(fn func()) { s.onChange = fn }

// changed notifies the registered listener, if any.
func (s *Store) changed() {
	if s.onChange != nil {
		s.onChange()
	}
}

// query/queryRow/exec run a statement after rebinding placeholders for the
// active dialect.
func (s *Store) query(query string, args ...any) (*sql.Rows, error) {
	return queryOn(s.dialect, s.db, query, args...)
}

func (s *Store) queryRow(query string, args ...any) *sql.Row {
	return queryRowOn(s.dialect, s.db, query, args...)
}

func (s *Store) exec(query string, args ...any) (sql.Result, error) {
	return execOn(s.dialect, s.db, query, args...)
}

// insertReturningID runs an INSERT (without a RETURNING clause) and returns the
// generated id. PostgreSQL appends RETURNING Id; MySQL uses LastInsertId.
func (s *Store) insertReturningID(query string, args ...any) (int64, error) {
	if s.dialect.isPostgres() {
		text, a := s.dialect.rebind(query+" RETURNING Id", args)
		var id int64
		if err := s.db.QueryRow(text, a...).Scan(&id); err != nil {
			return 0, err
		}
		return id, nil
	}
	res, err := s.exec(query, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// insertReturningIDTx is insertReturningID for a transaction.
func (s *Store) insertReturningIDTx(tx *sql.Tx, query string, args ...any) (int64, error) {
	if s.dialect.isPostgres() {
		text, a := s.dialect.rebind(query+" RETURNING Id", args)
		var id int64
		if err := tx.QueryRow(text, a...).Scan(&id); err != nil {
			return 0, err
		}
		return id, nil
	}
	res, err := execOn(s.dialect, tx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Connection pool defaults. The pool is intentionally larger than the old
// fixed 16 so a burst of concurrent AI-backed queries does not serialize on a
// handful of connections. Both bounds can be overridden from the environment
// for deployments that need to match a specific PostgreSQL max_connections.
const (
	defaultMaxOpenConns = 50
	defaultMaxIdleConns = 25
	connMaxLifetime     = time.Hour
	connMaxIdleTime     = 10 * time.Minute
)

// Open connects to PostgreSQL or MySQL using a libpq or mysql style DSN, then
// ensures the schema.
func Open(dsn string) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("DATABASE_URL 未配置")
	}
	d, err := detectDialect(dsn)
	if err != nil {
		return nil, err
	}
	db, err := openDB(d, dsn)
	if err != nil {
		return nil, err
	}
	maxOpen := envInt("DB_MAX_OPEN_CONNS", defaultMaxOpenConns)
	maxIdle := envInt("DB_MAX_IDLE_CONNS", defaultMaxIdleConns)
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	// Recycle connections periodically so a long-running process never holds a
	// stale backend past a server restart, failover or idle timeout.
	db.SetConnMaxLifetime(connMaxLifetime)
	db.SetConnMaxIdleTime(connMaxIdleTime)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	s := &Store{db: db, dialect: d}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.ensureTrigram(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// envInt reads a positive integer from the environment, falling back to def.
func envInt(key string, def int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return def
	}
	return v
}

// Close releases the database handle.

// InspectDSN opens a short-lived connection, runs the read-only integrity check
// and closes it without applying any migration. It backs the check-db command.
func InspectDSN(dsn string) (IntegrityReport, error) {
	if strings.TrimSpace(dsn) == "" {
		return IntegrityReport{}, errors.New("DATABASE_URL 未配置")
	}
	d, err := detectDialect(dsn)
	if err != nil {
		return IntegrityReport{}, err
	}
	db, err := openDB(d, dsn)
	if err != nil {
		return IntegrityReport{}, err
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return IntegrityReport{}, fmt.Errorf("连接数据库失败: %w", err)
	}
	return (&Store{db: db, dialect: d}).Inspect()
}
func (s *Store) Close() error { return s.db.Close() }

// Ping verifies the database connection is still alive.
func (s *Store) Ping() error { return s.db.Ping() }

// DB exposes the raw handle for log persistence helpers.
func (s *Store) DB() *sql.DB { return s.db }

const questionColumns = `ar.Id, ar.Question, ar.Options, ar.Answer, ar.QuestionType,
	ar.CreateTime, ar.IsAi, COALESCE(ar.IsPendingCorrection, FALSE)`

// hitColumns is the compact projection shared by the query hot path.
const hitColumns = `Id, Question, Answer, IsAi, COALESCE(IsPendingCorrection, FALSE)`

func scanQuestion(scan func(dest ...any) error) (Question, error) {
	var q Question
	err := scan(&q.ID, &q.Question, &q.Options, &q.Answer, &q.QuestionType,
		&q.CreateTime, &q.IsAI, &q.IsPendingCorrection)
	return q, err
}

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

// candidateLimit bounds how many bank rows are pulled into memory for
// fuzzy scoring. The exact-hash stage usually answers without touching it; the
// limit keeps the fallback bounded regardless of bank size.
const candidateLimit = 500

// Query matches title/options against the bank and returns the best hits.
//
// It works in two stages so it scales to very large banks:
//
//  1. Exact stage: probe the indexed QuestionHash column for rows whose
//     normalized question equals the query. This answers the common case with a
//     single B-tree lookup and no full scan.
//  2. Fallback stage: when nothing matches exactly, pull a bounded candidate
//     window (indexed trigram search when available, otherwise a prefix probe)
//     and score only those rows with the supplied scorer.
//
// The returned slice is capped at 50 entries ordered by descending score.
func (s *Store) Query(title string, options *string, scorer func(q, c string) (float64, bool)) ([]QueryHit, error) {
	queryOptions := normalizeOptional(options)
	requireOption := requireOptionMatch(title)

	// Stage 1: exact normalized-question matches via the hash index.
	exact, err := s.queryByHash(title, queryOptions, scorer)
	if err != nil {
		return nil, err
	}
	if len(exact) > 0 {
		return exact, nil
	}

	// Stage 2: bounded fuzzy candidates, scored in memory.
	candidates, err := s.fuzzyCandidates(title)
	if err != nil {
		return nil, err
	}
	return scoreCandidates(title, queryOptions, requireOption, candidates, scorer), nil
}

// queryByHash returns exact normalized-question matches. When the query carries
// options the row's options must also match.
func (s *Store) queryByHash(title string, queryOptions *string, scorer func(q, c string) (float64, bool)) ([]QueryHit, error) {
	hash := match.QuestionHash(title)
	if hash == "" {
		return nil, nil
	}
	rows, err := s.query(`SELECT Id, Question, Options, Answer, IsAi, COALESCE(IsPendingCorrection, FALSE)
		FROM AIResponses WHERE QuestionHash = $1 LIMIT $2`, hash, candidateLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []QueryHit
	for rows.Next() {
		var (
			id               int64
			question, answer string
			dbOptions        sql.NullString
			isAI, isPending  bool
		)
		if err := rows.Scan(&id, &question, &dbOptions, &answer, &isAI, &isPending); err != nil {
			return nil, err
		}
		// When the query carries URLs, require an identical URL set.
		if len(extractURLs(title)) > 0 && !equalStringSlices(extractURLs(title), extractURLs(question)) {
			continue
		}
		// The normalized questions are identical here, so the title score is
		// exact and options never gate the match (mirroring the fallback rule
		// where "exact" bypasses the option requirement).
		if queryOptions != nil && normalizeOptional(ptrOrNil(dbOptions)) != nil {
			dbOpt := normalizeOptional(ptrOrNil(dbOptions))
			if _, ok := scorer(*queryOptions, *dbOpt); !ok {
				continue
			}
		}
		results = append(results, QueryHit{
			ID:                  id,
			Question:            question,
			Answer:              answer,
			IsAI:                isAI,
			IsPendingCorrection: isPending,
		})
	}
	return results, rows.Err()
}

// fuzzyCandidates pulls a bounded window of rows for fuzzy scoring. It prefers
// a trigram (pg_trgm) index scan that ranks rows by similarity; if the
// extension is unavailable the probe degrades to a prefix match that still uses
// the hash index's avoidance of a full scan.
func (s *Store) fuzzyCandidates(title string) ([]candidate, error) {
	normalized := match.NormalizeQuestion(title)
	if normalized == "" {
		return nil, nil
	}
	if s.trigram {
		return s.trigramCandidates(normalized)
	}
	return s.prefixCandidates(normalized)
}

// candidate is one row considered for fuzzy scoring.
type candidate struct {
	id              int64
	question        string
	answer          string
	dbOptions       sql.NullString
	isAI, isPending bool
}

// trigramCandidates uses pg_trgm's similarity operator to rank candidates in the
// database, so only the top window is transferred. The `%` operator is the one
// backed by the GIN trigram index; similarity() only orders the resulting rows.
func (s *Store) trigramCandidates(normalized string) ([]candidate, error) {
	rows, err := s.query(`SELECT Id, Question, Options, Answer, IsAi, COALESCE(IsPendingCorrection, FALSE)
		FROM AIResponses
		WHERE Question % $1
		ORDER BY similarity(Question, $1) DESC
		LIMIT $2`, normalized, candidateLimit)
	if err != nil {
		return nil, err
	}
	return scanCandidates(rows)
}

// prefixCandidates falls back to a prefix probe on the normalized column. It
// uses a generated-free LIKE on Question; the hash index is used by the exact
// stage, and this bounded probe keeps the fallback from scanning the whole
// table.
func (s *Store) prefixCandidates(normalized string) ([]candidate, error) {
	prefix := normalized
	if len([]rune(prefix)) > 12 {
		prefix = string([]rune(prefix)[:12])
	}
	pattern := prefix + "%"
	rows, err := s.query(`SELECT Id, Question, Options, Answer, IsAi, COALESCE(IsPendingCorrection, FALSE)
		FROM AIResponses WHERE `+s.dialect.ilike("Question", "$1")+` LIMIT $2`, pattern, candidateLimit)
	if err != nil {
		return nil, err
	}
	return scanCandidates(rows)
}

func scanCandidates(rows *sql.Rows) ([]candidate, error) {
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.question, &c.dbOptions, &c.answer, &c.isAI, &c.isPending); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// scoreCandidates applies the matcher to the bounded candidate window, returning
// the best hits ordered by score.
func scoreCandidates(title string, queryOptions *string, requireOption bool, candidates []candidate, scorer func(q, c string) (float64, bool)) []QueryHit {
	type scored struct {
		hit   QueryHit
		score float64
	}
	results := make([]scored, 0, len(candidates))
	for _, c := range candidates {
		if len(extractURLs(title)) > 0 && !equalStringSlices(extractURLs(title), extractURLs(c.question)) {
			continue
		}
		titleScore, ok := scorer(title, c.question)
		if !ok {
			continue
		}
		var optionScore *float64
		dbOpt := normalizeOptional(ptrOrNil(c.dbOptions))
		if queryOptions != nil && dbOpt != nil {
			if sc, ok := scorer(*queryOptions, *dbOpt); ok {
				optionScore = &sc
			}
		}
		exact := titleScore >= 1.0-1e-9
		if !exact && (requireOption || queryOptions != nil) && optionScore == nil {
			continue
		}
		final := titleScore
		if optionScore != nil {
			final = titleScore*0.7 + (*optionScore)*0.3
		}
		results = append(results, scored{QueryHit{
			ID:                  c.id,
			Question:            c.question,
			Answer:              c.answer,
			IsAI:                c.isAI,
			IsPendingCorrection: c.isPending,
		}, final})
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	limit := 50
	if len(results) < limit {
		limit = len(results)
	}
	out := make([]QueryHit, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, results[i].hit)
	}
	return out
}

// FindByNormalizedQuestion returns an existing hit whose question is equivalent
// to title after normalization (case, whitespace and punctuation removed). Used
// at insert time to avoid storing a duplicate of a question that was just added.
// The lookup is an indexed probe on QuestionHash, so it stays O(log n) even on
// banks with tens of millions of rows.
func (s *Store) FindByNormalizedQuestion(title string) (*QueryHit, error) {
	hash := match.QuestionHash(title)
	if hash == "" {
		return nil, nil
	}
	row := s.queryRow(`SELECT `+hitColumns+` FROM AIResponses
		WHERE QuestionHash = $1 ORDER BY Id LIMIT 1`, hash)

	var (
		id               int64
		question, answer string
		isAI, isPending  bool
	)
	if err := row.Scan(&id, &question, &answer, &isAI, &isPending); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &QueryHit{
		ID:                  id,
		Question:            question,
		Answer:              answer,
		IsAI:                isAI,
		IsPendingCorrection: isPending,
	}, nil
}

// PaginatedQuestions returns a page of questions, optionally limited to those
// flagged for correction.
func (s *Store) PaginatedQuestions(pendingOnly bool, page, pageSize int64, sortOrder string) (Page, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}
	offset := (page - 1) * pageSize
	direction := "DESC"
	if strings.EqualFold(sortOrder, "asc") {
		direction = "ASC"
	}

	countSQL := `SELECT COUNT(*) FROM AIResponses`
	filter := ""
	if pendingOnly {
		filter = ` WHERE COALESCE(IsPendingCorrection, FALSE) = TRUE`
		countSQL += filter
	}

	var total int64
	if err := s.queryRow(countSQL).Scan(&total); err != nil {
		return Page{}, err
	}

	query := fmt.Sprintf(`SELECT %s FROM AIResponses ar%s
		ORDER BY ar.CreateTime %s LIMIT $1 OFFSET $2`, questionColumns, filter, direction)
	rows, err := s.query(query, pageSize, offset)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	items := []Question{}
	for rows.Next() {
		q, err := scanQuestion(rows.Scan)
		if err != nil {
			return Page{}, err
		}
		items = append(items, q)
	}
	return Page{Items: items, Total: total}, rows.Err()
}

// PendingCorrectionQuestions returns all flagged questions.
func (s *Store) PendingCorrectionQuestions() ([]Question, error) {
	rows, err := s.query(fmt.Sprintf(`SELECT %s FROM AIResponses ar
		WHERE COALESCE(ar.IsPendingCorrection, FALSE) = TRUE
		ORDER BY ar.CreateTime DESC`, questionColumns))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Question
	for rows.Next() {
		q, err := scanQuestion(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// PendingCorrectionCount returns how many questions are flagged.
func (s *Store) PendingCorrectionCount() (int64, error) {
	var n int64
	err := s.queryRow(`SELECT COUNT(*) FROM AIResponses WHERE COALESCE(IsPendingCorrection, FALSE) = TRUE`).Scan(&n)
	return n, err
}

// Search performs a token based substring search over the whole bank.
func (s *Store) Search(keyword string) ([]Question, error) {
	rows, err := s.query(fmt.Sprintf(`SELECT %s FROM AIResponses ar`, questionColumns))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(keyword)))
	out := []Question{}
	for rows.Next() {
		q, err := scanQuestion(rows.Scan)
		if err != nil {
			return nil, err
		}
		if len(terms) == 0 {
			out = append(out, q)
			continue
		}
		haystack := strings.ToLower(q.Question)
		if q.Options != nil {
			haystack += " " + strings.ToLower(*q.Options)
		}
		haystack += " " + strings.ToLower(q.Answer)
		ok := true
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, q)
		}
	}
	return out, rows.Err()
}

// GetQuestion fetches a single question by id.
func (s *Store) GetQuestion(id int64) (Question, error) {
	row := s.queryRow(fmt.Sprintf(`SELECT %s FROM AIResponses ar
		WHERE ar.Id = $1`, questionColumns), id)
	q, err := scanQuestion(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Question{}, ErrNotFound
	}
	return q, err
}

// AddQuestion inserts a manual question, optionally bypassing the blank-answer guard.
func (s *Store) AddQuestion(question string, options *string, answerStr, questionType *string, isAI bool) (Question, error) {
	if strings.TrimSpace(question) == "" {
		return Question{}, errors.New("题目不能为空")
	}
	answer := ""
	if answerStr != nil {
		answer = *answerStr
	}
	if isAI && strings.TrimSpace(answer) == "" {
		return Question{}, errors.New("AI处理结果答案为空，不保存题目")
	}
	id, err := s.insertReturningID(`INSERT INTO AIResponses (Question, Options, Answer, QuestionType, IsAi, CreateTime, QuestionHash)
		VALUES ($1, $2, $3, $4, $5, NOW(), $6)`,
		question, options, answer, questionType, isAI, match.QuestionHash(question))
	if err != nil {
		return Question{}, err
	}
	s.changed()
	return s.GetQuestion(id)
}

// InsertAIResponse stores an AI produced answer.
func (s *Store) InsertAIResponse(question, answer string, options, questionType *string) (int64, error) {
	return s.insertBankResponse(question, answer, options, questionType, true, "ai")
}

// InsertExternalResponse stores a third-party question bank answer. The row is
// marked as not AI and tagged with the source name so the console can show its
// provenance.
func (s *Store) InsertExternalResponse(question, answer string, options, questionType *string, source string) (int64, error) {
	return s.insertBankResponse(question, answer, options, questionType, false, source)
}

// insertBankResponse is the shared guarded insert used by the AI and external
// bank paths. It reuses an existing row for the same normalized question and
// options so a repeated answer never creates a duplicate, and records the
// source (ai, external:<name>, ...) for auditing.
func (s *Store) insertBankResponse(question, answer string, options, questionType *string, isAI bool, source string) (int64, error) {
	if strings.TrimSpace(question) == "" {
		return 0, errors.New("题目为空，不保存")
	}
	if strings.TrimSpace(answer) == "" {
		return 0, errors.New("AI处理结果答案为空，不保存题目")
	}
	questionHash := match.QuestionHash(question)

	if existing, err := s.idByLookup(questionHash, options); err != nil {
		return 0, err
	} else if existing != 0 {
		return existing, nil
	}

	from := ""
	if s.dialect.isMySQL() {
		from = " FROM DUAL"
	}
	insertSQL := `INSERT INTO AIResponses
		(Question, Answer, Options, QuestionType, IsAi, IsPendingCorrection, CreateTime, QuestionHash, Source)
		SELECT $1, $2, $3, $4, $5, FALSE, NOW(), $6, $7` + from + `
		WHERE NOT EXISTS (
			SELECT 1 FROM AIResponses
			WHERE QuestionHash = $6 AND ` + s.dialect.nullSafeEqual("Options", "$3") + `
		)`
	insertArgs := []any{question, answer, options, questionType, isAI, questionHash, source}

	lookupExisting := func() (int64, error) {
		return s.idByLookup(questionHash, options)
	}

	var id int64
	if s.dialect.isPostgres() {
		if err := s.queryRow(insertSQL+` RETURNING Id`, insertArgs...).Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return lookupExisting()
			}
			return 0, err
		}
	} else {
		res, err := s.exec(insertSQL, insertArgs...)
		if err != nil {
			return 0, err
		}
		inserted, _ := res.LastInsertId()
		if inserted == 0 {
			return lookupExisting()
		}
		id = inserted
	}
	s.changed()
	return id, nil
}

// ImportQuestion inserts a question pulled from an external source (for example
// a MOGO import). Rows that already exist for the same normalized question and
// options are skipped. It does not invalidate the query cache per row; the
// caller does that once when a bulk import finishes.
func (s *Store) ImportQuestion(question, options, answer, questionType, source string) (bool, error) {
	question = strings.TrimSpace(question)
	answer = strings.TrimSpace(answer)
	if question == "" || answer == "" {
		return false, nil
	}
	var opts *string
	if strings.TrimSpace(options) != "" {
		opts = &options
	}
	var qtype *string
	if strings.TrimSpace(questionType) != "" {
		qtype = &questionType
	}
	hash := match.QuestionHash(question)
	existing, err := s.idByLookup(hash, opts)
	if err != nil {
		return false, err
	}
	if existing != 0 {
		return false, nil
	}
	if _, err := s.insertReturningID(`INSERT INTO AIResponses
		(Question, Options, Answer, QuestionType, IsAi, IsPendingCorrection, CreateTime, QuestionHash, Source)
		VALUES ($1, $2, $3, $4, FALSE, FALSE, NOW(), $5, $6)`,
		question, opts, answer, qtype, hash, source); err != nil {
		return false, err
	}
	return true, nil
}

// idByLookup returns the id of the row matching the question hash and the exact
// stored options, or zero when none exists.
func (s *Store) idByLookup(questionHash string, options *string) (int64, error) {
	var id int64
	err := s.queryRow(`SELECT Id FROM AIResponses
		WHERE QuestionHash = $1 AND `+s.dialect.nullSafeEqual("Options", "$2")+`
		ORDER BY Id LIMIT 1`, questionHash, options).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return id, nil
}

// UpdateQuestion patches the provided fields and clears the pending flag.
func (s *Store) UpdateQuestion(id int64, question, options, answer, questionType *string) error {
	sets := []string{}
	args := []any{}
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if question != nil {
		add("Question", *question)
		add("QuestionHash", match.QuestionHash(*question))
	}
	if options != nil {
		add("Options", *options)
	}
	if answer != nil {
		add("Answer", *answer)
	}
	if questionType != nil {
		add("QuestionType", *questionType)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "IsPendingCorrection = FALSE")
	args = append(args, id)
	res, err := s.exec("UPDATE AIResponses SET "+strings.Join(sets, ", ")+
		fmt.Sprintf(" WHERE Id = $%d", len(args)), args...)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	s.changed()
	return nil
}

// SetPendingCorrection toggles the pending-correction flag.
func (s *Store) SetPendingCorrection(id int64, pending bool) error {
	res, err := s.exec(`UPDATE AIResponses SET IsPendingCorrection = $1 WHERE Id = $2`, pending, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	s.changed()
	return nil
}

// DeleteQuestion removes a single question.
func (s *Store) DeleteQuestion(id int64) error {
	if _, err := s.exec(`DELETE FROM AIResponses WHERE Id = $1`, id); err != nil {
		return err
	}
	s.changed()
	return nil
}

// DeleteQuestions removes a batch of questions.
func (s *Store) DeleteQuestions(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := execOn(s.dialect, tx, `DELETE FROM AIResponses WHERE Id = $1`, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.changed()
	return nil
}

// ErrNotFound signals a missing row.
var ErrNotFound = errors.New("记录不存在")

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func normalizeOptional(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func ptrOrNil(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func requireOptionMatch(title string) bool {
	for _, kw := range optionMatchKeywords {
		if strings.Contains(title, kw) {
			return true
		}
	}
	return false
}

var optionMatchKeywords = []string{"以下", "下列", "下面", "下叙"}

func extractURLs(text string) []string {
	return match.ExtractURLs(text)
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
