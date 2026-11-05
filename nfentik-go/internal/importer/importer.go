// Package importer migrates an existing SQLite question bank (produced by the
// Tauri desktop edition) into the database used by this service.
package importer

import (
	"database/sql"
	"fmt"

	"github.com/nfentik/nfentik-go/internal/store"

	_ "github.com/mattn/go-sqlite3"
)

// Result summarises a migration run.
type Result struct {
	Questions int
	Skipped   int
}

// FromSQLite copies every question from srcPath into dst.
//
// The migration is additive: rows already present are kept, and duplicates are
// skipped by matching the normalized question and options. Folder structure
// from the legacy database is ignored (folders are no longer supported).
func FromSQLite(srcPath string, dst *store.Store) (Result, error) {
	src, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro&_busy_timeout=5000", srcPath))
	if err != nil {
		return Result{}, err
	}
	defer src.Close()
	if err := src.Ping(); err != nil {
		return Result{}, fmt.Errorf("打开 SQLite 数据库失败: %w", err)
	}

	var res Result
	if err := copyQuestions(src, dst, &res); err != nil {
		return res, err
	}
	return res, nil
}

func copyQuestions(src *sql.DB, dst *store.Store, res *Result) error {
	rows, err := src.Query(`SELECT Question, Options, Answer, QuestionType,
		COALESCE(IsPendingCorrection, 0) FROM AIResponses ORDER BY Id`)
	if err != nil {
		return fmt.Errorf("读取 SQLite 题目失败: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			question        string
			options, answer sql.NullString
			questionType    sql.NullString
			isPendingFlag   int
		)
		if err := rows.Scan(&question, &options, &answer, &questionType, &isPendingFlag); err != nil {
			return err
		}
		answerStr := answer.String
		if answerStr == "" {
			res.Skipped++
			continue
		}

		id, err := dst.InsertAIResponse(question, answerStr, optional(options), optional(questionType))
		if err != nil {
			return fmt.Errorf("写入题目失败: %w", err)
		}
		if isPendingFlag == 1 {
			_ = dst.SetPendingCorrection(id, true)
		}
		res.Questions++
	}
	return rows.Err()
}

func optional(v sql.NullString) *string {
	if !v.Valid || v.String == "" {
		return nil
	}
	s := v.String
	return &s
}
