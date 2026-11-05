package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/nfentik/nfentik-go/internal/match"
)

// TestWriteHashBatchUpdatesEveryRow verifies the single-statement batch writer
// assigns each digest to the correct row and leaves unlisted rows untouched.
func TestWriteHashBatchUpdatesEveryRow(t *testing.T) {
	s := testStore(t)

	const n = 2500
	marker := "批量重算测试题 batch rehash"
	args := make([]any, 0, n*5)
	var ph strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			ph.WriteString(", ")
		}
		fmt.Fprintf(&ph, "($%d, $%d, $%d, $%d, 'A')", i*4+1, i*4+2, i*4+3, i*4+4)
		args = append(args,
			fmt.Sprintf("%s %d", marker, i),
			fmt.Sprintf("A. %d B. %d", i, i+1),
			fmt.Sprintf("stale-q-%d", i),
			fmt.Sprintf("stale-o-%d", i),
		)
	}
	if _, err := s.exec(
		`INSERT INTO AIResponses (Question, Options, QuestionHash, OptionsHash, Answer) VALUES `+ph.String(),
		args...,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	defer func() {
		_, _ = s.exec(`DELETE FROM AIResponses WHERE Question LIKE $1`, marker+" %")
	}()

	rows, err := s.query(
		`SELECT Id, Question, COALESCE(Options, '') FROM AIResponses WHERE Question LIKE $1 ORDER BY Id`,
		marker+" %",
	)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	var questions []pendingHash
	var options []pendingOptions
	for rows.Next() {
		var q pendingHash
		var o pendingOptions
		if err := rows.Scan(&q.id, &q.question, &o.options); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		o.id = q.id
		questions = append(questions, q)
		options = append(options, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(questions) != n {
		t.Fatalf("inserted rows = %d, want %d", len(questions), n)
	}

	// The last row stays out of both batches and must keep its stale digests.
	sentinel := questions[n-1]
	if err := s.writeQuestionHashBatch(s.db, questions[:n-1]); err != nil {
		t.Fatalf("write question batch: %v", err)
	}
	if err := s.writeOptionsHashBatch(s.db, options[:n-1]); err != nil {
		t.Fatalf("write options batch: %v", err)
	}

	for i := 0; i < n-1; i++ {
		var qh, oh string
		if err := s.queryRow(
			`SELECT QuestionHash, OptionsHash FROM AIResponses WHERE Id = $1`, questions[i].id,
		).Scan(&qh, &oh); err != nil {
			t.Fatalf("read row %d: %v", i, err)
		}
		if want := match.QuestionHash(questions[i].question); qh != want {
			t.Fatalf("row %d QuestionHash = %s, want %s", i, qh, want)
		}
		opts := options[i].options
		if want := match.OptionsHash(&opts); oh != want {
			t.Fatalf("row %d OptionsHash = %s, want %s", i, oh, want)
		}
	}

	var qh, oh string
	if err := s.queryRow(
		`SELECT QuestionHash, OptionsHash FROM AIResponses WHERE Id = $1`, sentinel.id,
	).Scan(&qh, &oh); err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if qh != fmt.Sprintf("stale-q-%d", n-1) || oh != fmt.Sprintf("stale-o-%d", n-1) {
		t.Fatalf("sentinel row modified: %s / %s", qh, oh)
	}
}

// TestEnsureHashesRehashesOnVersionBump drives the full startup path: a stale
// normalizer version must recompute both digest columns, record the new version
// and leave the lookup plus unique indexes in place.
func TestEnsureHashesRehashesOnVersionBump(t *testing.T) {
	s := testStore(t)

	marker := "版本重算测试题 version bump"
	if _, err := s.exec(
		`INSERT INTO AIResponses (Question, Options, Answer, QuestionHash, OptionsHash)
		 VALUES ($1, $2, 'A', 'outdated-q', 'outdated-o')`,
		marker, "A. 甲 B. 乙",
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	defer func() {
		_, _ = s.exec(`DELETE FROM AIResponses WHERE Question = $1`, marker)
	}()

	// Force both version keys behind the current algorithm.
	for _, key := range []string{questionHashVersionKey, optionsHashVersionKey} {
		if err := s.SetSetting(key, json.RawMessage("1")); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}

	if err := s.ensureHashes(); err != nil {
		t.Fatalf("ensureHashes: %v", err)
	}

	var qh, oh string
	if err := s.queryRow(
		`SELECT QuestionHash, OptionsHash FROM AIResponses WHERE Question = $1`, marker,
	).Scan(&qh, &oh); err != nil {
		t.Fatalf("read: %v", err)
	}
	if want := match.QuestionHash(marker); qh != want {
		t.Fatalf("QuestionHash = %s, want %s", qh, want)
	}
	opts := "A. 甲 B. 乙"
	if want := match.OptionsHash(&opts); oh != want {
		t.Fatalf("OptionsHash = %s, want %s", oh, want)
	}

	for _, key := range []string{questionHashVersionKey, optionsHashVersionKey} {
		raw, found, err := s.GetSetting(key)
		if err != nil || !found {
			t.Fatalf("get %s: %v found=%v", key, err, found)
		}
		if decodeNormalizerVersion(raw) != match.NormalizerVersion {
			t.Fatalf("%s not advanced: %s", key, raw)
		}
	}
	for _, idx := range []string{"idx_airesponses_question_hash", "idx_airesponses_question_options"} {
		if exists, err := s.indexExists(idx); err != nil || !exists {
			t.Fatalf("index %s missing: %v", idx, err)
		}
	}
}

// TestRehashHashesWritesOnlyChangedRows verifies the differential path: rows
// whose digests already match the current algorithm are left untouched, while
// stale question or options digests are corrected in one batch.
func TestRehashHashesWritesOnlyChangedRows(t *testing.T) {
	s := testStore(t)

	marker := "差分重算测试 diff rehash"
	opts := "A. 甲 B. 乙"
	wantOptions := match.OptionsHash(&opts)

	rows := []struct {
		question     string
		questionHash string
		optionsHash  string
	}{
		{marker + " 0", match.QuestionHash(marker + " 0"), wantOptions},
		{marker + " 1", "stale-question", wantOptions},
		{marker + " 2", match.QuestionHash(marker + " 2"), "stale-options"},
	}
	for _, r := range rows {
		if _, err := s.exec(
			`INSERT INTO AIResponses (Question, Options, Answer, QuestionHash, OptionsHash)
			 VALUES ($1, $2, 'A', $3, $4)`,
			r.question, opts, r.questionHash, r.optionsHash,
		); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	defer func() {
		_, _ = s.exec(`DELETE FROM AIResponses WHERE Question LIKE $1`, marker+" %")
	}()

	selected, err := s.query(
		`SELECT Id, Question, COALESCE(Options, ''), COALESCE(QuestionHash, ''), COALESCE(OptionsHash, '')
		 FROM AIResponses WHERE Question LIKE $1 ORDER BY Id`, marker+" %",
	)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	items, err := scanPendingHashRows(selected)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(items) != len(rows) {
		t.Fatalf("rows = %d, want %d", len(items), len(rows))
	}

	written, err := s.writePendingHashes(s.db, items, true, true)
	if err != nil {
		t.Fatalf("writePendingHashes: %v", err)
	}
	if written != 2 {
		t.Fatalf("written = %d, want 2", written)
	}

	for i, r := range rows {
		var qh, oh string
		if err := s.queryRow(
			`SELECT QuestionHash, OptionsHash FROM AIResponses WHERE Question = $1`, r.question,
		).Scan(&qh, &oh); err != nil {
			t.Fatalf("read row %d: %v", i, err)
		}
		if qh != match.QuestionHash(r.question) || oh != wantOptions {
			t.Fatalf("row %d = %s / %s, want %s / %s", i, qh, oh,
				match.QuestionHash(r.question), wantOptions)
		}
	}
}
