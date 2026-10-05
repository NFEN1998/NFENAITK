// Package store owns the PostgreSQL database. Schema and queries mirror the
// desktop edition's database.rs so existing question banks keep working.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

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
	FolderID            int64   `json:"folder_id"`
	FolderName          *string `json:"folder_name"`
	CreateTime          *string `json:"create_time"`
	IsAI                bool    `json:"is_ai"`
	IsPendingCorrection bool    `json:"is_pending_correction"`
}

// Folder is a bank category node.
type Folder struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	ParentID   int64   `json:"parent_id"`
	CreateTime *string `json:"create_time"`
}

// FolderStat aggregates question counts per folder.
type FolderStat struct {
	FolderID      int64  `json:"folder_id"`
	FolderName    string `json:"folder_name"`
	QuestionCount int64  `json:"question_count"`
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

// Store wraps the PostgreSQL connection pool.
type Store struct {
	db *sql.DB

	// onChange is invoked after any question or folder mutation. The server
	// uses it to invalidate Redis query caches.
	onChange func()
}

// OnChange registers a callback fired after every bank mutation.
func (s *Store) OnChange(fn func()) { s.onChange = fn }

// changed notifies the registered listener, if any.
func (s *Store) changed() {
	if s.onChange != nil {
		s.onChange()
	}
}

// Open connects to PostgreSQL using a libpq style DSN, then ensures the schema.
func Open(dsn string) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("DATABASE_URL 未配置")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(8)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("连接 PostgreSQL 失败: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the raw handle for log persistence helpers.
func (s *Store) DB() *sql.DB { return s.db }

const questionColumns = `ar.Id, ar.Question, ar.Options, ar.Answer, ar.QuestionType,
	ar.FolderId, f.Name AS FolderName, ar.CreateTime, ar.IsAi, COALESCE(ar.IsPendingCorrection, FALSE)`

func scanQuestion(scan func(dest ...any) error) (Question, error) {
	var q Question
	err := scan(&q.ID, &q.Question, &q.Options, &q.Answer, &q.QuestionType,
		&q.FolderID, &q.FolderName, &q.CreateTime, &q.IsAI, &q.IsPendingCorrection)
	return q, err
}

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

// Query matches title/options against the bank and returns the best hits.
func (s *Store) Query(title string, options *string, scorer func(q, c string) (float64, bool)) ([]QueryHit, error) {
	rows, err := s.db.Query(`SELECT Id, Question, Options, Answer, IsAi, COALESCE(IsPendingCorrection, FALSE) FROM AIResponses`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	queryOptions := normalizeOptional(options)
	requireOption := requireOptionMatch(title)

	type scored struct {
		hit   QueryHit
		score float64
	}
	var results []scored

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
		if len(extractURLs(title)) > 0 {
			if !equalStringSlices(extractURLs(title), extractURLs(question)) {
				continue
			}
		}
		titleScore, ok := scorer(title, question)
		if !ok {
			continue
		}
		var optionScore *float64
		dbOpt := normalizeOptional(ptrOrNil(dbOptions))
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
			ID:                  id,
			Question:            question,
			Answer:              answer,
			IsAI:                isAI,
			IsPendingCorrection: isPending,
		}, final})
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
	return out, nil
}

// FindByNormalizedQuestion returns an existing hit whose question is equivalent
// to title after normalization (case, whitespace and punctuation removed). Used
// at insert time to avoid storing a duplicate of a question that was just added.
func (s *Store) FindByNormalizedQuestion(title string) (*QueryHit, error) {
	target := match.NormalizeQuestion(title)
	if target == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT Id, Question, Options, Answer, IsAi, COALESCE(IsPendingCorrection, FALSE) FROM AIResponses`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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
		if match.NormalizeQuestion(question) != target {
			continue
		}
		return &QueryHit{
			ID:                  id,
			Question:            question,
			Answer:              answer,
			IsAI:                isAI,
			IsPendingCorrection: isPending,
		}, nil
	}
	return nil, rows.Err()
}

// Folders returns every folder ordered by name.
func (s *Store) Folders() ([]Folder, error) {
	rows, err := s.db.Query(`SELECT Id, Name, ParentId, CreateTime FROM Folders ORDER BY Name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Folder
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.ID, &f.Name, &f.ParentID, &f.CreateTime); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FolderPath returns the ancestor chain (root first) for a folder.
func (s *Store) FolderPath(folderID int64) ([]Folder, error) {
	rows, err := s.db.Query(`
		WITH RECURSIVE folder_path AS (
			SELECT Id, Name, ParentId, 0 AS level FROM Folders WHERE Id = $1
			UNION ALL
			SELECT f.Id, f.Name, f.ParentId, fp.level + 1 FROM Folders f
			INNER JOIN folder_path fp ON f.Id = fp.ParentId
			WHERE f.Id != fp.Id
		)
		SELECT Id, Name, ParentId FROM folder_path ORDER BY level DESC`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Folder
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.ID, &f.Name, &f.ParentID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FolderStats lists all folders with their direct question counts.
func (s *Store) FolderStats() ([]FolderStat, error) {
	rows, err := s.db.Query(`
		SELECT f.Id, COALESCE(f.Name, '[未分类]'), COUNT(ar.Id)
		FROM Folders f
		LEFT JOIN AIResponses ar ON f.Id = ar.FolderId
		GROUP BY f.Id, f.Name
		ORDER BY COUNT(ar.Id) DESC, f.Name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FolderStat
	for rows.Next() {
		var st FolderStat
		if err := rows.Scan(&st.FolderID, &st.FolderName, &st.QuestionCount); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// PaginatedQuestions returns a page of questions filtered by folder or pending state.
func (s *Store) PaginatedQuestions(folderID *int64, pendingOnly bool, page, pageSize int64, sortOrder string) (Page, error) {
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

	var (
		total    int64
		query    string
		args     []any
		countSQL string
		countArg []any
	)

	switch {
	case pendingOnly:
		countSQL = `SELECT COUNT(*) FROM AIResponses WHERE COALESCE(IsPendingCorrection, FALSE) = TRUE`
		query = fmt.Sprintf(`SELECT %s FROM AIResponses ar
			LEFT JOIN Folders f ON ar.FolderId = f.Id
			WHERE COALESCE(ar.IsPendingCorrection, FALSE) = TRUE
			ORDER BY ar.CreateTime %s LIMIT $1 OFFSET $2`, questionColumns, direction)
		args = []any{pageSize, offset}
	case folderID != nil && *folderID == 0:
		countSQL = `SELECT COUNT(*) FROM AIResponses WHERE FolderId = 0`
		query = fmt.Sprintf(`SELECT %s FROM AIResponses ar
			INNER JOIN Folders f ON ar.FolderId = f.Id
			WHERE ar.FolderId = 0
			ORDER BY ar.CreateTime %s LIMIT $1 OFFSET $2`, questionColumns, direction)
		args = []any{pageSize, offset}
	case folderID != nil:
		countSQL = `WITH RECURSIVE folder_tree AS (
			SELECT Id FROM Folders WHERE Id = $1
			UNION ALL
			SELECT f.Id FROM Folders f INNER JOIN folder_tree ft ON f.ParentId = ft.Id
		) SELECT COUNT(*) FROM AIResponses ar INNER JOIN folder_tree ft ON ar.FolderId = ft.Id`
		countArg = []any{*folderID}
		query = fmt.Sprintf(`WITH RECURSIVE folder_tree AS (
				SELECT Id FROM Folders WHERE Id = $1
				UNION ALL
				SELECT f.Id FROM Folders f INNER JOIN folder_tree ft ON f.ParentId = ft.Id
			)
			SELECT %s FROM AIResponses ar
			INNER JOIN folder_tree ft ON ar.FolderId = ft.Id
			INNER JOIN Folders f ON ar.FolderId = f.Id
			ORDER BY ar.CreateTime %s LIMIT $2 OFFSET $3`, questionColumns, direction)
		args = []any{*folderID, pageSize, offset}
	default:
		countSQL = `SELECT COUNT(*) FROM AIResponses`
		query = fmt.Sprintf(`SELECT %s FROM AIResponses ar
			LEFT JOIN Folders f ON ar.FolderId = f.Id
			ORDER BY ar.CreateTime %s LIMIT $1 OFFSET $2`, questionColumns, direction)
		args = []any{pageSize, offset}
	}

	if err := s.db.QueryRow(countSQL, countArg...).Scan(&total); err != nil {
		return Page{}, err
	}

	rows, err := s.db.Query(query, args...)
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

// RecursiveQuestions returns every question inside a folder subtree.
func (s *Store) RecursiveQuestions(folderID int64) ([]Question, error) {
	var (
		query string
		args  []any
	)
	if folderID == 0 {
		query = fmt.Sprintf(`SELECT %s FROM AIResponses ar
			INNER JOIN Folders f ON ar.FolderId = f.Id
			WHERE ar.FolderId = 0 ORDER BY ar.CreateTime DESC`, questionColumns)
	} else {
		query = fmt.Sprintf(`WITH RECURSIVE folder_tree AS (
				SELECT Id FROM Folders WHERE Id = $1
				UNION ALL
				SELECT f.Id FROM Folders f INNER JOIN folder_tree ft ON f.ParentId = ft.Id
			)
			SELECT %s FROM AIResponses ar
			INNER JOIN folder_tree ft ON ar.FolderId = ft.Id
			INNER JOIN Folders f ON ar.FolderId = f.Id
			ORDER BY ar.CreateTime DESC`, questionColumns)
		args = []any{folderID}
	}
	rows, err := s.db.Query(query, args...)
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

// PendingCorrectionQuestions returns all flagged questions.
func (s *Store) PendingCorrectionQuestions() ([]Question, error) {
	rows, err := s.db.Query(fmt.Sprintf(`SELECT %s FROM AIResponses ar
		LEFT JOIN Folders f ON ar.FolderId = f.Id
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
	err := s.db.QueryRow(`SELECT COUNT(*) FROM AIResponses WHERE COALESCE(IsPendingCorrection, FALSE) = TRUE`).Scan(&n)
	return n, err
}

// FolderQuestionCount returns the direct question count for a folder.
func (s *Store) FolderQuestionCount(folderID int64) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM AIResponses WHERE FolderId = $1`, folderID).Scan(&n)
	return n, err
}

// Search performs a token based substring search inside a folder scope.
func (s *Store) Search(keyword string, folderID *int64) ([]Question, error) {
	var (
		query string
		args  []any
	)
	switch {
	case folderID != nil && *folderID == 0:
		query = fmt.Sprintf(`SELECT %s FROM AIResponses ar
			INNER JOIN Folders f ON ar.FolderId = f.Id
			WHERE ar.FolderId = 0`, questionColumns)
	case folderID != nil:
		query = fmt.Sprintf(`WITH RECURSIVE folder_tree AS (
				SELECT Id FROM Folders WHERE Id = $1
				UNION ALL
				SELECT f.Id FROM Folders f INNER JOIN folder_tree ft ON f.ParentId = ft.Id
			)
			SELECT %s FROM AIResponses ar
			INNER JOIN folder_tree ft ON ar.FolderId = ft.Id
			INNER JOIN Folders f ON ar.FolderId = f.Id`, questionColumns)
		args = []any{*folderID}
	default:
		query = fmt.Sprintf(`SELECT %s FROM AIResponses ar
			LEFT JOIN Folders f ON ar.FolderId = f.Id`, questionColumns)
	}

	rows, err := s.db.Query(query, args...)
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
	row := s.db.QueryRow(fmt.Sprintf(`SELECT %s FROM AIResponses ar
		LEFT JOIN Folders f ON ar.FolderId = f.Id WHERE ar.Id = $1`, questionColumns), id)
	q, err := scanQuestion(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Question{}, ErrNotFound
	}
	return q, err
}

// AddQuestion inserts a manual question, optionally bypassing the blank-answer guard.
func (s *Store) AddQuestion(question string, options *string, answerStr, questionType *string, folderID int64, isAI bool) (Question, error) {
	answer := ""
	if answerStr != nil {
		answer = *answerStr
	}
	if isAI && strings.TrimSpace(answer) == "" {
		return Question{}, errors.New("AI处理结果答案为空，不保存题目")
	}
	target, err := s.targetFolder(folderID)
	if err != nil {
		return Question{}, err
	}
	var id int64
	err = s.db.QueryRow(`INSERT INTO AIResponses (Question, Options, Answer, QuestionType, FolderId, IsAi, CreateTime)
		VALUES ($1, $2, $3, $4, $5, $6, NOW()) RETURNING Id`,
		question, options, answer, questionType, target, isAI).Scan(&id)
	if err != nil {
		return Question{}, err
	}
	s.changed()
	return s.GetQuestion(id)
}

// InsertAIResponse stores an AI produced answer in the configured folder.
func (s *Store) InsertAIResponse(question, answer string, options, questionType *string, folderID int64) (int64, error) {
	if strings.TrimSpace(answer) == "" {
		return 0, errors.New("AI处理结果答案为空，不保存题目")
	}
	target, err := s.targetFolder(folderID)
	if err != nil {
		return 0, err
	}
	folderName := "默认文件夹"
	_ = s.db.QueryRow(`SELECT Name FROM Folders WHERE Id = $1`, target).Scan(&folderName)
	var id int64
	err = s.db.QueryRow(`INSERT INTO AIResponses
		(Question, Answer, Options, QuestionType, IsAi, IsPendingCorrection, CreateTime, FolderId, FolderName)
		VALUES ($1, $2, $3, $4, TRUE, FALSE, NOW(), $5, $6) RETURNING Id`,
		question, answer, options, questionType, target, folderName).Scan(&id)
	if err != nil {
		return 0, err
	}
	s.changed()
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
	res, err := s.db.Exec("UPDATE AIResponses SET "+strings.Join(sets, ", ")+
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
	res, err := s.db.Exec(`UPDATE AIResponses SET IsPendingCorrection = $1 WHERE Id = $2`, pending, id)
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

// MoveQuestion relocates a question into another folder.
func (s *Store) MoveQuestion(questionID, targetFolderID int64) error {
	target, err := s.targetFolder(targetFolderID)
	if err != nil {
		return err
	}
	if _, err = s.db.Exec(`UPDATE AIResponses SET FolderId = $1 WHERE Id = $2`, target, questionID); err != nil {
		return err
	}
	s.changed()
	return nil
}

// CopyQuestion duplicates a question into another folder.
func (s *Store) CopyQuestion(questionID, targetFolderID int64) error {
	target, err := s.targetFolder(targetFolderID)
	if err != nil {
		return err
	}
	q, err := s.GetQuestion(questionID)
	if err != nil {
		return err
	}
	if _, err = s.db.Exec(`INSERT INTO AIResponses
		(Question, Options, Answer, QuestionType, FolderId, IsAi, IsPendingCorrection, CreateTime)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())`,
		q.Question, q.Options, q.Answer, q.QuestionType, target, q.IsAI, q.IsPendingCorrection); err != nil {
		return err
	}
	s.changed()
	return nil
}

// DeleteQuestion removes a single question.
func (s *Store) DeleteQuestion(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM AIResponses WHERE Id = $1`, id); err != nil {
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
	stmt, err := tx.Prepare(`DELETE FROM AIResponses WHERE Id = $1`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, id := range ids {
		if _, err := stmt.Exec(id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.changed()
	return nil
}

// ClearFolderQuestions removes every question inside a folder subtree.
func (s *Store) ClearFolderQuestions(folderID int64) error {
	ids, err := s.subtreeIDs(folderID)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.Exec(`DELETE FROM AIResponses WHERE FolderId = $1`, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.changed()
	return nil
}

// AddFolder creates a folder and returns its id.
func (s *Store) AddFolder(name string, parentID int64) (int64, error) {
	var id int64
	err := s.db.QueryRow(`INSERT INTO Folders (Name, ParentId, CreateTime) VALUES ($1, $2, NOW()) RETURNING Id`,
		name, parentID).Scan(&id)
	if err != nil {
		return 0, err
	}
	s.changed()
	return id, nil
}

// RenameFolder updates a folder name.
func (s *Store) RenameFolder(id int64, name string) error {
	if _, err := s.db.Exec(`UPDATE Folders SET Name = $1 WHERE Id = $2`, name, id); err != nil {
		return err
	}
	s.changed()
	return nil
}

// MoveFolder reparents a folder, refusing self-parenting.
func (s *Store) MoveFolder(id, parentID int64) error {
	if id == parentID {
		return errors.New("cannot move folder into itself")
	}
	if _, err := s.db.Exec(`UPDATE Folders SET ParentId = $1 WHERE Id = $2`, parentID, id); err != nil {
		return err
	}
	s.changed()
	return nil
}

// DeleteFolder removes a folder subtree, either with or without its questions.
func (s *Store) DeleteFolder(id int64, deleteQuestions bool) error {
	ids, err := s.subtreeIDs(id)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var parentID int64
	_ = tx.QueryRow(`SELECT ParentId FROM Folders WHERE Id = $1`, id).Scan(&parentID)

	for _, fid := range ids {
		if deleteQuestions {
			if _, err := tx.Exec(`DELETE FROM AIResponses WHERE FolderId = $1`, fid); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(`UPDATE AIResponses SET FolderId = $1 WHERE FolderId = $2`, parentID, fid); err != nil {
				return err
			}
		}
	}
	for _, fid := range ids {
		if _, err := tx.Exec(`DELETE FROM Folders WHERE Id = $1`, fid); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.changed()
	return nil
}

// subtreeIDs returns a folder id together with all descendants.
func (s *Store) subtreeIDs(folderID int64) ([]int64, error) {
	rows, err := s.db.Query(`WITH RECURSIVE folder_tree AS (
		SELECT Id FROM Folders WHERE Id = $1
		UNION ALL
		SELECT f.Id FROM Folders f INNER JOIN folder_tree ft ON f.ParentId = ft.Id
	) SELECT Id FROM folder_tree`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// targetFolder resolves the actual folder for inserts, creating a
// "[未分类]" child when the requested folder already contains subfolders.
func (s *Store) targetFolder(parentID int64) (int64, error) {
	if parentID == 0 {
		return 0, nil
	}
	var hasSub bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM Folders WHERE ParentId = $1)`, parentID).Scan(&hasSub); err != nil {
		return 0, err
	}
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM Folders WHERE Id = $1`, parentID).Scan(&one)
	exists := err == nil
	if !exists {
		return 0, nil
	}
	if !hasSub {
		return parentID, nil
	}
	var uncategorized int64
	err = s.db.QueryRow(`SELECT Id FROM Folders WHERE ParentId = $1 AND Name = '[未分类]'`, parentID).Scan(&uncategorized)
	if err == nil {
		return uncategorized, nil
	}
	var id int64
	err = s.db.QueryRow(`INSERT INTO Folders (Name, ParentId, CreateTime) VALUES ('[未分类]', $1, NOW()) RETURNING Id`,
		parentID).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
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
