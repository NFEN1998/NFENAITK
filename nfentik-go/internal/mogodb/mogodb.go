// Package mogodb reads a MOGO host's question bank directly from its
// MySQL/MariaDB database. It is an alternative to the HTTP importer, for hosts
// whose HTTP API times out under load. All access is read-only: the package
// only ever issues SELECT statements.
package mogodb

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// ColumnMap maps the local fields to the MOGO question table's columns.
type ColumnMap struct {
	ID           string
	Question     string
	Answer       string
	Options      string
	QuestionType string
}

// DefaultColumnMap matches the field names reported by the MOGO HTTP API's
// getQuestionHeader, which the database table shares.
func DefaultColumnMap() ColumnMap {
	return ColumnMap{ID: "id", Question: "question", Answer: "answer", Options: "opt", QuestionType: "questionType"}
}

// Config describes one MOGO database connection.
type Config struct {
	Host        string
	Port        string
	User        string
	Password    string
	Database    string
	Table       string
	Columns     ColumnMap
	ConnTimeout time.Duration
	ReadTimeout time.Duration
}

// Row is one question read from the MOGO database.
type Row struct {
	ID           int64
	Question     string
	Answer       string
	Options      string
	QuestionType string
}

// Reader reads rows from one MOGO question table.
type Reader struct {
	db          *sql.DB
	table       string
	colID       string
	colQuestion string
	colAnswer   string
	colOptions  string
	colType     string
}

// identifierRe is the safe subset of MySQL identifiers accepted for table and
// column names. Everything else is rejected so identifiers can be interpolated
// without allowing SQL injection.
var identifierRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// QuoteIdentifier validates and backtick-quotes a MySQL identifier.
func QuoteIdentifier(name string) (string, error) {
	if !identifierRe.MatchString(name) {
		return "", fmt.Errorf("非法的标识符: %q", name)
	}
	return "`" + name + "`", nil
}

// DSN builds the MySQL DSN for the config. Exposed for testing.
func (cfg Config) DSN() string {
	port := strings.TrimSpace(cfg.Port)
	if port == "" {
		port = "3306"
	}
	c := mysql.NewConfig()
	c.User = cfg.User
	c.Passwd = cfg.Password
	c.Net = "tcp"
	c.Addr = net.JoinHostPort(strings.TrimSpace(cfg.Host), port)
	c.DBName = cfg.Database
	c.ParseTime = false
	c.Timeout = cfg.connTimeout()
	c.ReadTimeout = cfg.readTimeout()
	c.Params = map[string]string{"charset": "utf8mb4"}
	return c.FormatDSN()
}

func (cfg Config) connTimeout() time.Duration {
	if cfg.ConnTimeout > 0 {
		return cfg.ConnTimeout
	}
	return 5 * time.Second
}

func (cfg Config) readTimeout() time.Duration {
	if cfg.ReadTimeout > 0 {
		return cfg.ReadTimeout
	}
	return 30 * time.Second
}

// columnMap fills empty mapping entries with the defaults.
func (cfg Config) columnMap() ColumnMap {
	def := DefaultColumnMap()
	cm := cfg.Columns
	if strings.TrimSpace(cm.ID) == "" {
		cm.ID = def.ID
	}
	if strings.TrimSpace(cm.Question) == "" {
		cm.Question = def.Question
	}
	if strings.TrimSpace(cm.Answer) == "" {
		cm.Answer = def.Answer
	}
	if strings.TrimSpace(cm.Options) == "" {
		cm.Options = def.Options
	}
	if strings.TrimSpace(cm.QuestionType) == "" {
		cm.QuestionType = def.QuestionType
	}
	return cm
}

// EffectiveColumns returns the column names that will actually be read.
func (cfg Config) EffectiveColumns() ColumnMap { return cfg.columnMap() }

// Open validates the config, connects to the MOGO database and returns a
// Reader. The connection is verified with a Ping so configuration errors
// surface immediately.
func Open(cfg Config) (*Reader, error) {
	table, err := QuoteIdentifier(strings.TrimSpace(cfg.Table))
	if err != nil {
		return nil, err
	}
	cm := cfg.columnMap()
	col := func(field, name string) (string, error) {
		q, err := QuoteIdentifier(strings.TrimSpace(name))
		if err != nil {
			return "", fmt.Errorf("%s: %w", field, err)
		}
		return q, nil
	}
	colID, err := col("主键列", cm.ID)
	if err != nil {
		return nil, err
	}
	colQuestion, err := col("题目列", cm.Question)
	if err != nil {
		return nil, err
	}
	colAnswer, err := col("答案列", cm.Answer)
	if err != nil {
		return nil, err
	}
	colOptions, err := col("选项列", cm.Options)
	if err != nil {
		return nil, err
	}
	colType, err := col("题型列", cm.QuestionType)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Reader{
		db:          db,
		table:       table,
		colID:       colID,
		colQuestion: colQuestion,
		colAnswer:   colAnswer,
		colOptions:  colOptions,
		colType:     colType,
	}, nil
}

// Close releases the database handle.
func (r *Reader) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

// Columns returns the column names of the configured table, used by the
// connection test to help the administrator pick a mapping.
func (r *Reader) Columns(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT column_name FROM information_schema.columns
		WHERE table_schema = DATABASE() AND table_name = ? ORDER BY ordinal_position`, strings.Trim(r.table, "`"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// Count returns how many rows have a primary key greater than minID.
func (r *Reader) Count(ctx context.Context, minID int64) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM `+r.table+` WHERE `+r.colID+` > ?`, minID).Scan(&n)
	return n, err
}

// Fetch returns up to limit rows whose primary key is greater than afterID, in
// ascending key order, together with the last key read.
func (r *Reader) Fetch(ctx context.Context, afterID int64, limit int) ([]Row, int64, error) {
	if limit <= 0 {
		limit = 500
	}
	q := `SELECT ` + r.colID + `, ` + r.colQuestion + `, ` + r.colAnswer + `, ` +
		r.colOptions + `, ` + r.colType + ` FROM ` + r.table +
		` WHERE ` + r.colID + ` > ? ORDER BY ` + r.colID + ` ASC LIMIT ?`
	rows, err := r.db.QueryContext(ctx, q, afterID, limit)
	if err != nil {
		return nil, afterID, err
	}
	defer rows.Close()
	var out []Row
	last := afterID
	for rows.Next() {
		var (
			id                        int64
			question, answer, opt, qt sql.NullString
		)
		if err := rows.Scan(&id, &question, &answer, &opt, &qt); err != nil {
			return nil, last, err
		}
		out = append(out, Row{
			ID:           id,
			Question:     question.String,
			Answer:       answer.String,
			Options:      opt.String,
			QuestionType: qt.String,
		})
		last = id
	}
	return out, last, rows.Err()
}
