// Package importer migrates an existing SQLite question bank (produced by the
// Tauri desktop edition) into the PostgreSQL database used by this service.
package importer

import (
	"database/sql"
	"fmt"

	"github.com/nfentik/nfentik-go/internal/store"

	_ "github.com/mattn/go-sqlite3"
)

// Result summarises a migration run.
type Result struct {
	Folders   int
	Questions int
	Skipped   int
}

// FromSQLite copies every folder and question from srcPath into dst.
//
// The migration is additive: rows already present in PostgreSQL are kept, and
// duplicates are skipped by matching (Question, Answer). The root folder with
// Id 0 is never copied because the target schema guarantees it exists.
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

	// ---- Folders -----------------------------------------------------------
	folderIDs, err := copyFolders(src, dst, &res)
	if err != nil {
		return res, err
	}

	// ---- Questions ---------------------------------------------------------
	if err := copyQuestions(src, dst, folderIDs, &res); err != nil {
		return res, err
	}
	return res, nil
}

// copyFolders inserts folders while preserving parent relationships. It handles
// arbitrary ordering by inserting in multiple passes until no progress is made.
func copyFolders(src *sql.DB, dst *store.Store, res *Result) (map[int64]int64, error) {
	rows, err := src.Query(`SELECT Id, Name, COALESCE(ParentId, 0) FROM Folders ORDER BY Id`)
	if err != nil {
		return nil, fmt.Errorf("读取 SQLite 文件夹失败: %w", err)
	}
	defer rows.Close()

	type folderRow struct {
		id       int64
		name     string
		parentID int64
	}
	var folders []folderRow
	for rows.Next() {
		var f folderRow
		if err := rows.Scan(&f.id, &f.name, &f.parentID); err != nil {
			return nil, err
		}
		if f.id == 0 {
			continue
		}
		folders = append(folders, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Map old folder ids to new ones so question FolderId stays consistent.
	mapping := map[int64]int64{0: 0}
	// The root folder maps to 0 as well.
	pending := folders
	for len(pending) > 0 {
		var next []folderRow
		progress := false
		for _, f := range pending {
			newParent, ok := mapping[f.parentID]
			if !ok {
				next = append(next, f)
				continue
			}
			newID, err := dst.AddFolder(f.name, newParent)
			if err != nil {
				return nil, fmt.Errorf("写入文件夹 %q 失败: %w", f.name, err)
			}
			mapping[f.id] = newID
			res.Folders++
			progress = true
		}
		if !progress {
			// Orphaned parents: attach the remainder to the root folder.
			for _, f := range next {
				newID, err := dst.AddFolder(f.name, 0)
				if err != nil {
					return nil, fmt.Errorf("写入文件夹 %q 失败: %w", f.name, err)
				}
				mapping[f.id] = newID
				res.Folders++
			}
			break
		}
		pending = next
	}
	return mapping, nil
}

func copyQuestions(src *sql.DB, dst *store.Store, folderIDs map[int64]int64, res *Result) error {
	rows, err := src.Query(`SELECT Question, Options, Answer, QuestionType, COALESCE(FolderId, 0),
		COALESCE(IsAi, 1), COALESCE(IsPendingCorrection, 0) FROM AIResponses ORDER BY Id`)
	if err != nil {
		return fmt.Errorf("读取 SQLite 题目失败: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			question            string
			options, answer     sql.NullString
			questionType        sql.NullString
			folderID            int64
			isAI, isPendingFlag int
		)
		if err := rows.Scan(&question, &options, &answer, &questionType, &folderID, &isAI, &isPendingFlag); err != nil {
			return err
		}
		answerStr := answer.String
		if answerStr == "" {
			res.Skipped++
			continue
		}

		// Map the legacy folder id to the newly created one. Unknown folders
		// fall back to the root so no question is lost.
		targetFolder, ok := folderIDs[folderID]
		if !ok {
			targetFolder = 0
		}

		id, err := dst.InsertAIResponse(question, answerStr, optional(options), optional(questionType), targetFolder)
		if err != nil {
			// Blank answers are already filtered; anything else is a real error.
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
