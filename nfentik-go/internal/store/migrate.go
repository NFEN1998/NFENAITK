package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/nfentik/nfentik-go/internal/match"
)

// migration is one ordered, forward-only schema change.
//
// Version numbers must be strictly increasing and never reused. Each migration
// runs inside a transaction together with the bookkeeping insert, so a failure
// leaves neither the schema nor the version table partially updated.
type migration struct {
	Version int
	Name    string
	// Statements are executed in order inside a single transaction.
	Statements []string
	// Baseline marks the migration that represents a database created by an
	// older release without a version table. When such a database is detected
	// every migration up to and including Baseline is recorded as already
	// applied instead of being executed.
	Baseline bool
}

// migrations is the full, ordered history of schema changes.
var migrations = []migration{
	{
		Version:  1,
		Name:     "baseline_schema",
		Baseline: true,
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS AIResponses (
				Id SERIAL PRIMARY KEY,
				Question TEXT NOT NULL,
				Options TEXT,
				QuestionType TEXT,
				Answer TEXT NOT NULL,
				CreateTime TIMESTAMP DEFAULT NOW(),
				IsAi BOOLEAN DEFAULT TRUE,
				IsPendingCorrection BOOLEAN DEFAULT FALSE
			)`,
			`CREATE TABLE IF NOT EXISTS RequestLogs (
				LogId BIGSERIAL PRIMARY KEY,
				RequestId TEXT NOT NULL,
				Timestamp TEXT NOT NULL,
				Method TEXT NOT NULL,
				Path TEXT NOT NULL,
				Status INTEGER,
				ResponseTime BIGINT,
				RequestBody TEXT,
				ResponseBody TEXT,
				Headers TEXT,
				Ip TEXT,
				UserAgent TEXT,
				Stage TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS DailyRequestCounts (
				Day TEXT PRIMARY KEY,
				Count BIGINT NOT NULL DEFAULT 0
			)`,
			`CREATE TABLE IF NOT EXISTS AppSettings (
				Key TEXT PRIMARY KEY,
				Value TEXT NOT NULL,
				UpdateTime TIMESTAMP DEFAULT NOW()
			)`,
			`CREATE INDEX IF NOT EXISTS idx_request_logs_request_id ON RequestLogs(RequestId)`,
			`CREATE INDEX IF NOT EXISTS idx_airesponses_pending ON AIResponses(IsPendingCorrection)`,
			`CREATE INDEX IF NOT EXISTS idx_airesponses_created ON AIResponses(CreateTime DESC)`,
			// Columns that were added after the very first release. They are
			// folded into the baseline so both fresh and legacy databases end up
			// with the same shape.
			`ALTER TABLE AIResponses ADD COLUMN IF NOT EXISTS QuestionType TEXT`,
			`ALTER TABLE AIResponses ADD COLUMN IF NOT EXISTS CreateTime TIMESTAMP DEFAULT NOW()`,
			`ALTER TABLE AIResponses ADD COLUMN IF NOT EXISTS IsAi BOOLEAN DEFAULT TRUE`,
			`ALTER TABLE AIResponses ADD COLUMN IF NOT EXISTS IsPendingCorrection BOOLEAN DEFAULT FALSE`,
		},
	},
	{
		Version: 2,
		Name:    "user_token_plans",
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS Users (
				Id BIGSERIAL PRIMARY KEY,
				Token TEXT NOT NULL,
				PlanType TEXT NOT NULL DEFAULT 'duration',
				PlanCode TEXT NOT NULL DEFAULT 'monthly',
				PlanLabel TEXT NOT NULL DEFAULT '',
				StartAt TEXT NOT NULL DEFAULT '',
				ExpireAt TEXT,
				TotalCount BIGINT,
				UsedCount BIGINT NOT NULL DEFAULT 0,
				RemainCount BIGINT,
				Note TEXT NOT NULL DEFAULT '',
				Enabled BOOLEAN NOT NULL DEFAULT TRUE,
				CreatedAt TIMESTAMP DEFAULT NOW(),
				UpdatedAt TIMESTAMP DEFAULT NOW()
			)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_token ON Users(Token)`,
			`CREATE TABLE IF NOT EXISTS UserRequestLogs (
				LogId BIGSERIAL PRIMARY KEY,
				UserId BIGINT NOT NULL,
				Token TEXT NOT NULL DEFAULT '',
				Timestamp TEXT NOT NULL,
				Question TEXT,
				Source TEXT,
				Status TEXT,
				ResponseTime BIGINT
			)`,
			`CREATE INDEX IF NOT EXISTS idx_user_logs_user ON UserRequestLogs(UserId, LogId DESC)`,
			`CREATE TABLE IF NOT EXISTS UserAuditLogs (
				AuditId BIGSERIAL PRIMARY KEY,
				Action TEXT NOT NULL,
				UserId BIGINT,
				UserToken TEXT NOT NULL DEFAULT '',
				Detail TEXT NOT NULL DEFAULT '',
				Actor TEXT NOT NULL DEFAULT 'admin',
				CreatedAt TIMESTAMP DEFAULT NOW()
			)`,
			`CREATE INDEX IF NOT EXISTS idx_user_audits_created ON UserAuditLogs(CreatedAt DESC)`,
		},
	},
	{
		Version: 3,
		Name:    "user_token_identity",
		Statements: []string{
			// The user token is now the only identity: drop the separate name.
			`DROP INDEX IF EXISTS idx_users_name`,
			`ALTER TABLE Users DROP COLUMN IF EXISTS Name`,
			// Audit rows keep identifying the user, now by token. Only rename
			// when the legacy column is present so fresh databases are unaffected.
			`DO $$
			BEGIN
				IF EXISTS (SELECT 1 FROM information_schema.columns
					WHERE table_name = 'userauditlogs' AND column_name = 'username') THEN
					ALTER TABLE UserAuditLogs RENAME COLUMN UserName TO UserToken;
				END IF;
			END $$`,
		},
	},
	{
		Version: 4,
		Name:    "user_log_options_answer",
		Statements: []string{
			`ALTER TABLE UserRequestLogs ADD COLUMN IF NOT EXISTS Options TEXT`,
			`ALTER TABLE UserRequestLogs ADD COLUMN IF NOT EXISTS Answer TEXT`,
		},
	},
	{
		Version: 5,
		Name:    "question_hash_lookup",
		// Only the column is added here. Backfilling the digest and building the
		// index are done in ensureQuestionHash after migrations, because both
		// must run in batches outside a single transaction to stay safe on
		// multi-million row banks.
		Statements: []string{
			`ALTER TABLE AIResponses ADD COLUMN IF NOT EXISTS QuestionHash TEXT`,
		},
	},
	{
		Version: 6,
		Name:    "answer_source",
		// Records where an answer came from (ai, external:<name>, ...) so the
		// console can show provenance. Existing rows keep an empty source.
		Statements: []string{
			`ALTER TABLE AIResponses ADD COLUMN IF NOT EXISTS Source TEXT`,
		},
	},
}

// coreTables are the tables every release expects. They are folded into the
// baseline migration, so a healthy database always has them.
var coreTables = []string{
	"airesponses",
	"requestlogs",
	"dailyrequestcounts",
	"appsettings",
	"users",
	"userrequestlogs",
	"userauditlogs",
}

// canonicalTables maps the lowercased table names used by the integrity checks
// and repair lists to their canonical spelling, so repair DDL matches the real
// table on engines whose identifiers are case sensitive (MySQL on Linux).
var canonicalTables = map[string]string{
	"airesponses":        "AIResponses",
	"requestlogs":        "RequestLogs",
	"dailyrequestcounts": "DailyRequestCounts",
	"appsettings":        "AppSettings",
	"users":              "Users",
	"userrequestlogs":    "UserRequestLogs",
	"userauditlogs":      "UserAuditLogs",
	"schemamigrations":   "SchemaMigrations",
}

// canonicalTable returns the canonical spelling for a (possibly lowercased)
// table name, falling back to the input when unknown.
func canonicalTable(name string) string {
	if canonical, ok := canonicalTables[strings.ToLower(name)]; ok {
		return canonical
	}
	return name
}

// coreColumns maps each core table to the columns every release expects. It is
// used for integrity checks and for idempotent repair: a column can be missing
// even when the version table claims the migration that added it was applied
// (for example after a partial restore), so repair must not rely on version
// bookkeeping alone.
var coreColumns = map[string][]string{
	"airesponses":        {"id", "question", "options", "questiontype", "answer", "createtime", "isai", "ispendingcorrection", "questionhash", "source"},
	"requestlogs":        {"logid", "requestid", "timestamp", "method", "path", "status", "responsetime", "requestbody", "responsebody", "headers", "ip", "useragent", "stage"},
	"dailyrequestcounts": {"day", "count"},
	"appsettings":        {"key", "value", "updatetime"},
	"users":              {"id", "token", "plantype", "plancode", "planlabel", "startat", "expireat", "totalcount", "usedcount", "remaincount", "note", "enabled", "createdat", "updatedat"},
	"userrequestlogs":    {"logid", "userid", "token", "timestamp", "question", "options", "answer", "source", "status", "responsetime"},
	"userauditlogs":      {"auditid", "action", "userid", "usertoken", "detail", "actor", "createdat"},
}

// ColumnIssue names a single missing column on a table.
type ColumnIssue struct {
	Table  string `json:"table"`
	Column string `json:"column"`
}

// IntegrityReport summarises the state of the database as seen at startup,
// before any repair is attempted.
type IntegrityReport struct {
	// Empty is true when none of the core tables exist yet: a brand new
	// database that the migration system will create from scratch.
	Empty bool `json:"empty"`
	// MissingTables lists core tables that do not exist.
	MissingTables []string `json:"missing_tables"`
	// MissingColumns lists columns absent from their table.
	MissingColumns []ColumnIssue `json:"missing_columns"`
	// CurrentVersion is the highest recorded migration (0 when none).
	CurrentVersion int `json:"current_version"`
	// TargetVersion is the highest migration this build knows about.
	TargetVersion int `json:"target_version"`
	// HasVersionTable reports whether SchemaMigrations exists.
	HasVersionTable bool `json:"has_version_table"`
	// PendingMigrations are the versions that still need to be applied.
	PendingMigrations []int `json:"pending_migrations"`
}

// Healthy reports whether the schema is complete and fully migrated.
func (r IntegrityReport) Healthy() bool {
	return len(r.MissingTables) == 0 && len(r.MissingColumns) == 0 && len(r.PendingMigrations) == 0
}

// Inspect performs a read-only integrity check of the database: it lists the
// core tables and columns present, reads the recorded schema version and
// computes which migrations are still pending. It never modifies the database.
func (s *Store) Inspect() (IntegrityReport, error) {
	report := IntegrityReport{TargetVersion: targetVersion()}

	// Which core tables are missing.
	for _, table := range coreTables {
		exists, err := s.tableExists(table)
		if err != nil {
			return report, fmt.Errorf("检查缺失表: %w", err)
		}
		if !exists {
			report.MissingTables = append(report.MissingTables, table)
		}
	}
	report.Empty = len(report.MissingTables) == len(coreTables)

	// Which expected columns are missing from the tables that do exist.
	var err error
	report.MissingColumns, err = s.missingColumns()
	if err != nil {
		return report, err
	}

	// Version table and recorded migrations.
	hasVersion, err := s.tableExists("schemamigrations")
	if err != nil {
		return report, fmt.Errorf("检查版本表: %w", err)
	}
	report.HasVersionTable = hasVersion

	applied := map[int]bool{}
	if hasVersion {
		applied, err = s.appliedVersions()
		if err != nil {
			return report, err
		}
		for _, m := range migrations {
			if applied[m.Version] && m.Version > report.CurrentVersion {
				report.CurrentVersion = m.Version
			}
		}
	}

	// A legacy database has core tables but no recorded migrations; the runner
	// will baseline it, so those versions are not "pending" in the usual sense.
	legacy := !hasVersion && !report.Empty
	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		if legacy && m.Baseline {
			continue
		}
		report.PendingMigrations = append(report.PendingMigrations, m.Version)
	}
	return report, nil
}

// missingColumns returns the expected columns that are absent from their table.
func (s *Store) missingColumns() ([]ColumnIssue, error) {
	var issues []ColumnIssue
	for table, cols := range coreColumns {
		// Rows for missing tables are reported by the table check; skip them.
		exists, err := s.tableExists(table)
		if err != nil {
			return nil, fmt.Errorf("检查表 %s: %w", table, err)
		}
		if !exists {
			continue
		}
		present, err := s.listColumns(table)
		if err != nil {
			return nil, fmt.Errorf("检查列 %s: %w", table, err)
		}
		for _, col := range cols {
			if !present[col] {
				issues = append(issues, ColumnIssue{Table: table, Column: col})
			}
		}
	}
	return issues, nil
}

// tableExists reports whether a table (case-insensitive) exists in the active
// schema.
func (s *Store) tableExists(table string) (bool, error) {
	var n int
	err := s.queryRow(`SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = `+s.dialect.currentSchemaExpr()+` AND lower(table_name) = $1`,
		strings.ToLower(table)).Scan(&n)
	return n > 0, err
}

// columnExists reports whether a column (case-insensitive) exists in a table.
func (s *Store) columnExists(table, column string) (bool, error) {
	var n int
	err := s.queryRow(`SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema = `+s.dialect.currentSchemaExpr()+`
		  AND lower(table_name) = $1 AND lower(column_name) = $2`,
		strings.ToLower(table), strings.ToLower(column)).Scan(&n)
	return n > 0, err
}

// listColumns returns the lowercased column names of a table as a set.
func (s *Store) listColumns(table string) (map[string]bool, error) {
	rows, err := s.query(`SELECT lower(column_name) FROM information_schema.columns
		WHERE table_schema = `+s.dialect.currentSchemaExpr()+` AND lower(table_name) = $1`,
		strings.ToLower(table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// indexExists reports whether a named index exists in the active schema.
func (s *Store) indexExists(name string) (bool, error) {
	var n int
	if s.dialect.isPostgres() {
		err := s.queryRow(`SELECT COUNT(*) FROM pg_class c
			JOIN pg_namespace nsp ON nsp.oid = c.relnamespace
			WHERE c.relname = $1 AND nsp.nspname = current_schema()`, name).Scan(&n)
		return n > 0, err
	}
	err := s.queryRow(`SELECT COUNT(*) FROM information_schema.statistics
		WHERE table_schema = DATABASE() AND index_name = $1`, name).Scan(&n)
	return n > 0, err
}

// targetVersion returns the highest migration version known to this build.
func targetVersion() int {
	best := 0
	for _, m := range migrations {
		if m.Version > best {
			best = m.Version
		}
	}
	return best
}

// migrate applies every pending migration in order, after an integrity check.
//
// The sequence is:
//  1. Inspect the database read-only and log a report. An empty database is a
//     fresh install; a populated one is checked for missing tables/columns and
//     a stale schema version.
//  2. Run the ordered migrations. On a fresh database these create the schema;
//     on an existing one they apply only the versions still pending.
//  3. Repair any residual drift (for example a column missing even though the
//     version table says its migration ran). Repair is idempotent and does not
//     rely on version bookkeeping.
func (s *Store) migrate() error {
	if err := s.ensureMigrationTable(); err != nil {
		return err
	}
	if s.dialect.isMySQL() {
		return s.migrateMySQL()
	}

	// Read-only integrity report, logged before any change so the startup log
	// shows exactly what the database looked like. The report is reused by the
	// repair step so the schema is only scanned once.
	var initial IntegrityReport
	if report, err := s.Inspect(); err != nil {
		log.Printf("数据库完整性检测失败: %v", err)
	} else {
		initial = report
		logIntegrityReport(report)
	}

	applied, err := s.appliedVersions()
	if err != nil {
		return err
	}

	// A database created before the version table existed has tables but no
	// recorded migrations. Record everything up to the baseline as applied
	// without running it, then continue with the remaining migrations.
	if len(applied) == 0 {
		legacy, err := s.hasLegacySchema()
		if err != nil {
			return err
		}
		if legacy {
			baseline := baselineVersion()
			if baseline > 0 {
				log.Printf("检测到旧版数据库，标记基线版本 %d 为已应用（不重建数据）", baseline)
				for _, m := range migrations {
					if m.Version > baseline {
						break
					}
					if err := s.recordMigration(m); err != nil {
						return err
					}
					applied[m.Version] = true
				}
			}
		}
	}

	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		if err := s.applyMigration(m); err != nil {
			return err
		}
		log.Printf("已应用数据库迁移 %03d %s", m.Version, m.Name)
	}

	// Repair residual drift: a table or column can be missing even when the
	// version table says its migration was applied (partial restore, manual
	// changes). Repair is idempotent and independent of version bookkeeping.
	if err := s.repairSchema(initial); err != nil {
		return err
	}

	// Backfill the question hash column and build its index. Runs in batches so
	// it stays safe on banks with millions of rows.
	return s.ensureQuestionHash()
}

// migrateMySQL sets up a MySQL database. Because MySQL lacks ADD COLUMN IF NOT
// EXISTS and CREATE INDEX IF NOT EXISTS, the schema is created and repaired
// with explicit information_schema checks rather than replayed DDL statements.
// Every known migration is then recorded as applied so Inspect and
// SchemaVersion agree with the PostgreSQL path.
func (s *Store) migrateMySQL() error {
	if report, err := s.Inspect(); err != nil {
		log.Printf("数据库完整性检测失败: %v", err)
	} else {
		logIntegrityReport(report)
	}
	if err := s.ensureSchemaMySQL(); err != nil {
		return err
	}
	for _, m := range migrations {
		if err := s.recordMigration(m); err != nil {
			return err
		}
	}
	return s.ensureQuestionHash()
}

// mysqlTableDDL creates every table on a fresh MySQL database. Indexes are
// declared inline so a fresh install needs no extra steps. The statements are
// idempotent (CREATE TABLE IF NOT EXISTS).
var mysqlTableDDL = []string{
	`CREATE TABLE IF NOT EXISTS AIResponses (
		Id BIGINT NOT NULL AUTO_INCREMENT,
		Question TEXT NOT NULL,
		Options TEXT,
		QuestionType VARCHAR(64),
		Answer MEDIUMTEXT NOT NULL,
		CreateTime DATETIME DEFAULT NOW(),
		IsAi TINYINT(1) DEFAULT 1,
		IsPendingCorrection TINYINT(1) DEFAULT 0,
		QuestionHash VARCHAR(64),
		Source VARCHAR(64) DEFAULT '',
		PRIMARY KEY (Id),
		KEY idx_airesponses_pending (IsPendingCorrection),
		KEY idx_airesponses_created (CreateTime),
		KEY idx_airesponses_question_hash (QuestionHash)
	)` + mysqlTableOptions,
	`CREATE TABLE IF NOT EXISTS RequestLogs (
		LogId BIGINT NOT NULL AUTO_INCREMENT,
		RequestId VARCHAR(64) NOT NULL,
		Timestamp VARCHAR(32) NOT NULL,
		Method VARCHAR(16) NOT NULL,
		Path VARCHAR(1024) NOT NULL,
		Status INT,
		ResponseTime BIGINT,
		RequestBody MEDIUMTEXT,
		ResponseBody MEDIUMTEXT,
		Headers MEDIUMTEXT,
		Ip VARCHAR(64),
		UserAgent VARCHAR(512),
		Stage VARCHAR(32) NOT NULL,
		PRIMARY KEY (LogId),
		KEY idx_request_logs_request_id (RequestId)
	)` + mysqlTableOptions,
	`CREATE TABLE IF NOT EXISTS DailyRequestCounts (
		Day VARCHAR(10) NOT NULL,
		Count BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (Day)
	)` + mysqlTableOptions,
	"CREATE TABLE IF NOT EXISTS AppSettings (" +
		"`Key` VARCHAR(255) NOT NULL, " +
		"`Value` MEDIUMTEXT NOT NULL, " +
		"UpdateTime DATETIME DEFAULT NOW(), " +
		"PRIMARY KEY (`Key`)" +
		")" + mysqlTableOptions,
	`CREATE TABLE IF NOT EXISTS Users (
		Id BIGINT NOT NULL AUTO_INCREMENT,
		Token VARCHAR(128) NOT NULL,
		PlanType VARCHAR(32) NOT NULL DEFAULT 'duration',
		PlanCode VARCHAR(64) NOT NULL DEFAULT 'monthly',
		PlanLabel VARCHAR(128) NOT NULL DEFAULT '',
		StartAt VARCHAR(32) NOT NULL DEFAULT '',
		ExpireAt VARCHAR(32),
		TotalCount BIGINT,
		UsedCount BIGINT NOT NULL DEFAULT 0,
		RemainCount BIGINT,
		Note VARCHAR(255) NOT NULL DEFAULT '',
		Enabled TINYINT(1) NOT NULL DEFAULT 1,
		CreatedAt DATETIME DEFAULT NOW(),
		UpdatedAt DATETIME DEFAULT NOW(),
		PRIMARY KEY (Id),
		UNIQUE KEY idx_users_token (Token)
	)` + mysqlTableOptions,
	`CREATE TABLE IF NOT EXISTS UserRequestLogs (
		LogId BIGINT NOT NULL AUTO_INCREMENT,
		UserId BIGINT NOT NULL,
		Token VARCHAR(128) NOT NULL DEFAULT '',
		Timestamp VARCHAR(32) NOT NULL,
		Question TEXT,
		Options TEXT,
		Answer TEXT,
		Source VARCHAR(64),
		Status VARCHAR(32),
		ResponseTime BIGINT,
		PRIMARY KEY (LogId),
		KEY idx_user_logs_user (UserId, LogId)
	)` + mysqlTableOptions,
	`CREATE TABLE IF NOT EXISTS UserAuditLogs (
		AuditId BIGINT NOT NULL AUTO_INCREMENT,
		Action VARCHAR(64) NOT NULL,
		UserId BIGINT,
		UserToken VARCHAR(128) NOT NULL DEFAULT '',
		Detail TEXT NOT NULL,
		Actor VARCHAR(64) NOT NULL DEFAULT 'admin',
		CreatedAt DATETIME DEFAULT NOW(),
		PRIMARY KEY (AuditId),
		KEY idx_user_audits_created (CreatedAt)
	)` + mysqlTableOptions,
}

// mysqlColumnRepairs lists columns a partially created MySQL database might be
// missing. Types match the CREATE TABLE definitions above.
var mysqlColumnRepairs = []columnRepair{
	{Table: "airesponses", Column: "questiontype", DDL: "QuestionType VARCHAR(64)"},
	{Table: "airesponses", Column: "createtime", DDL: "CreateTime DATETIME DEFAULT NOW()"},
	{Table: "airesponses", Column: "isai", DDL: "IsAi TINYINT(1) DEFAULT 1"},
	{Table: "airesponses", Column: "ispendingcorrection", DDL: "IsPendingCorrection TINYINT(1) DEFAULT 0"},
	{Table: "airesponses", Column: "questionhash", DDL: "QuestionHash VARCHAR(64)"},
	{Table: "airesponses", Column: "source", DDL: "Source VARCHAR(64) DEFAULT ''"},
	{Table: "userrequestlogs", Column: "options", DDL: "Options TEXT"},
	{Table: "userrequestlogs", Column: "answer", DDL: "Answer TEXT"},
}

// ensureSchemaMySQL creates any missing tables, columns and indexes.
func (s *Store) ensureSchemaMySQL() error {
	for _, ddl := range mysqlTableDDL {
		if _, err := s.exec(ddl); err != nil {
			return fmt.Errorf("初始化 MySQL 表结构失败: %w", err)
		}
	}
	for _, fix := range mysqlColumnRepairs {
		if !fix.required(s) {
			continue
		}
		if _, err := s.exec("ALTER TABLE " + canonicalTable(fix.Table) + " ADD COLUMN " + fix.DDL); err != nil {
			return fmt.Errorf("补齐字段 %s.%s 失败: %w", fix.Table, fix.Column, err)
		}
		log.Printf("已补齐缺失字段 %s.%s", fix.Table, fix.Column)
	}
	return nil
}

// logIntegrityReport prints a human readable summary of the startup check.
func logIntegrityReport(r IntegrityReport) {
	switch {
	case r.Empty:
		log.Printf("数据库完整性检测: 空库，将初始化全部表结构（目标版本 %d）", r.TargetVersion)
	case r.Healthy():
		log.Printf("数据库完整性检测: 通过（版本 %d/%d，结构与迁移均为最新）",
			r.CurrentVersion, r.TargetVersion)
	default:
		if !r.HasVersionTable {
			log.Printf("数据库完整性检测: 未发现版本表，将按旧版数据库处理并补齐结构")
		}
		if len(r.MissingTables) > 0 {
			log.Printf("数据库完整性检测: 缺失表 %v，将自动创建", r.MissingTables)
		}
		if len(r.MissingColumns) > 0 {
			items := make([]string, len(r.MissingColumns))
			for i, c := range r.MissingColumns {
				items[i] = c.Table + "." + c.Column
			}
			log.Printf("数据库完整性检测: 缺失字段 %v，将自动补齐", items)
		}
		if len(r.PendingMigrations) > 0 {
			log.Printf("数据库完整性检测: 待应用迁移 %v（当前版本 %d，目标版本 %d）",
				r.PendingMigrations, r.CurrentVersion, r.TargetVersion)
		}
	}
}

// repairSchema recreates any missing core table and adds any missing core
// column. It uses CREATE TABLE IF NOT EXISTS / ADD COLUMN IF NOT EXISTS so it is
// safe to run on every startup and never touches existing data.
//
// The report captured before migrations ran is passed in so the schema is only
// inspected once; repair does not re-scan the database.
func (s *Store) repairSchema(report IntegrityReport) error {
	// Only replay DDL when the integrity check saw a missing table; the
	// statements are idempotent but there is no reason to run them on every
	// healthy start. Every migration's CREATE TABLE is replayed (not just the
	// baseline) so a table introduced in V2+ is also recovered.
	if len(report.MissingTables) > 0 {
		for _, m := range migrations {
			for _, stmt := range m.Statements {
				if !isCreateTable(stmt) {
					continue
				}
				if _, err := s.exec(stmt); err != nil {
					return fmt.Errorf("修复表结构失败: %w", err)
				}
			}
		}
		log.Printf("已重建缺失的表结构: %v", report.MissingTables)
	}

	// Fill in any missing columns with their canonical definitions. The DDL is
	// kept here (not derived from migrations) so it stays unambiguous.
	for _, fix := range columnRepairs {
		if !fix.required(s) {
			continue
		}
		if _, err := s.exec(`ALTER TABLE ` + canonicalTable(fix.Table) + ` ADD COLUMN IF NOT EXISTS ` + fix.DDL); err != nil {
			return fmt.Errorf("补齐字段 %s.%s 失败: %w", fix.Table, fix.Column, err)
		}
		log.Printf("已补齐缺失字段 %s.%s", fix.Table, fix.Column)
	}

	return nil
}

// isCreateTable reports whether a migration statement creates a table. Only
// these are replayed during repair, so index/column maintenance is not repeated
// needlessly. The match is case-insensitive after trimming leading whitespace.
func isCreateTable(stmt string) bool {
	upper := strings.ToUpper(strings.TrimSpace(stmt))
	return strings.HasPrefix(upper, "CREATE TABLE ")
}

// columnRepair describes one idempotent column addition.
type columnRepair struct {
	Table  string
	Column string
	DDL    string // column definition, e.g. "QuestionHash TEXT"
}

// required reports whether the column is currently missing, so we only log and
// execute the repair when it is actually needed.
func (c columnRepair) required(s *Store) bool {
	exists, err := s.columnExists(c.Table, c.Column)
	return err == nil && !exists
}

// columnRepairs lists every non-baseline column the schema may be missing. It is
// generated from the migration history so newly added columns only need to
// appear here once.
var columnRepairs = []columnRepair{
	{Table: "airesponses", Column: "questiontype", DDL: "QuestionType TEXT"},
	{Table: "airesponses", Column: "createtime", DDL: "CreateTime TIMESTAMP DEFAULT NOW()"},
	{Table: "airesponses", Column: "isai", DDL: "IsAi BOOLEAN DEFAULT TRUE"},
	{Table: "airesponses", Column: "ispendingcorrection", DDL: "IsPendingCorrection BOOLEAN DEFAULT FALSE"},
	{Table: "airesponses", Column: "questionhash", DDL: "QuestionHash TEXT"},
	{Table: "airesponses", Column: "source", DDL: "Source TEXT"},
	{Table: "userrequestlogs", Column: "options", DDL: "Options TEXT"},
	{Table: "userrequestlogs", Column: "answer", DDL: "Answer TEXT"},
}

func (s *Store) ensureMigrationTable() error {
	var ddl string
	if s.dialect.isMySQL() {
		ddl = `CREATE TABLE IF NOT EXISTS SchemaMigrations (
			Version INTEGER NOT NULL,
			Name VARCHAR(128) NOT NULL,
			AppliedAt DATETIME DEFAULT NOW(),
			PRIMARY KEY (Version)
		)` + mysqlTableOptions
	} else {
		ddl = `CREATE TABLE IF NOT EXISTS SchemaMigrations (
			Version INTEGER PRIMARY KEY,
			Name TEXT NOT NULL,
			AppliedAt TIMESTAMP DEFAULT NOW()
		)`
	}
	if _, err := s.exec(ddl); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

func (s *Store) appliedVersions() (map[int]bool, error) {
	rows, err := s.query(`SELECT Version FROM SchemaMigrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// hasLegacySchema reports whether the database already contains the core tables
// but no recorded migrations, meaning it predates the migration system.
func (s *Store) hasLegacySchema() (bool, error) {
	return s.tableExists("airesponses")
}

// baselineVersion returns the highest migration marked as a baseline.
func baselineVersion() int {
	best := 0
	for _, m := range migrations {
		if m.Baseline && m.Version > best {
			best = m.Version
		}
	}
	return best
}

// applyMigration runs one migration and records it atomically.
func (s *Store) applyMigration(m migration) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range m.Statements {
		if _, err := execOn(s.dialect, tx, stmt); err != nil {
			return fmt.Errorf("迁移 %03d %s 执行失败: %w", m.Version, m.Name, err)
		}
	}
	if _, err := execOn(s.dialect, tx, `INSERT INTO SchemaMigrations (Version, Name) VALUES ($1, $2)`+
		s.dialect.recordVersionSuffix(), m.Version, m.Name); err != nil {
		return fmt.Errorf("记录迁移 %03d 失败: %w", m.Version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交迁移 %03d 失败: %w", m.Version, err)
	}
	return nil
}

// recordMigration marks a migration as applied without executing it. Used when
// baselining an existing database.
func (s *Store) recordMigration(m migration) error {
	_, err := s.exec(`INSERT INTO SchemaMigrations (Version, Name) VALUES ($1, $2)`+
		s.dialect.recordVersionSuffix(), m.Version, m.Name)
	return err
}

// ensureTrigram enables the pg_trgm extension and its GIN index when the
// database permits it. It is a no-op on MySQL, whose fuzzy candidates use the
// bounded prefix probe. Failure is non-fatal: the query path degrades to that
// same prefix probe.
func (s *Store) ensureTrigram() error {
	if s.dialect.isMySQL() {
		return nil
	}
	if _, err := s.exec(`CREATE EXTENSION IF NOT EXISTS pg_trgm`); err != nil {
		// A concurrent creator can win the race and surface a duplicate-key
		// error even though the extension now exists; verify before giving up.
		if !s.trigramAvailable() {
			log.Printf("pg_trgm 扩展不可用，模糊匹配将使用前缀回退: %v", err)
			return nil
		}
	}
	exists, err := s.indexExists("idx_airesponses_question_trgm")
	if err != nil {
		log.Printf("检查 trigram 索引失败，使用前缀回退: %v", err)
		return nil
	}
	if !exists {
		log.Printf("创建 trigram GIN 索引（CONCURRENTLY）")
		if _, err := s.exec(
			`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_airesponses_question_trgm
				ON AIResponses USING gin (Question gin_trgm_ops)`,
		); err != nil {
			log.Printf("创建 trigram 索引失败，使用前缀回退: %v", err)
			return nil
		}
	}
	s.trigram = true
	return nil
}

// trigramAvailable reports whether the pg_trgm extension is installed.
func (s *Store) trigramAvailable() bool {
	var ok bool
	err := s.queryRow(
		`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm')`,
	).Scan(&ok)
	return err == nil && ok
}

// SchemaVersion returns the highest applied migration version. Exposed for the
// diagnostics endpoint.
func (s *Store) SchemaVersion() (int, error) {
	var v sql.NullInt64
	err := s.queryRow(`SELECT MAX(Version) FROM SchemaMigrations`).Scan(&v)
	if err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

// ensureQuestionHash backfills the QuestionHash column and ensures its index
// exists. Both steps are idempotent and safe to run on every startup.
//
// The backfill is done in bounded batches so a multi-million row bank never
// holds a long transaction; the index is built with CONCURRENTLY so it does not
// block reads or writes. CONCURRENTLY cannot run inside a transaction, so the
// single statements below are issued on the pool directly.
func (s *Store) ensureQuestionHash() error {
	// Recompute every digest once when the normalization algorithm changes.
	// Stored hashes from an older algorithm no longer match lookups computed
	// with the current one, so a stale version forces a single rehash pass.
	if err := s.ensureQuestionHashVersion(); err != nil {
		return err
	}
	// Skip the (potentially expensive) backfill scan once the column is fully
	// populated. A count with a WHERE ... IS NULL LIMIT would still scan, so we
	// probe for a single residual row.
	var pendingCount int
	if err := s.queryRow(
		`SELECT COUNT(*) FROM AIResponses WHERE QuestionHash IS NULL`,
	).Scan(&pendingCount); err != nil {
		return fmt.Errorf("检查 QuestionHash 回填: %w", err)
	}
	if pendingCount > 0 {
		if err := s.backfillQuestionHash(); err != nil {
			return err
		}
	}
	return s.ensureQuestionHashIndex()
}

// questionHashVersionKey stores the NormalizeQuestion version used to build the
// QuestionHash column. When the algorithm changes the stored value falls behind
// and every row is rehashed exactly once.
const questionHashVersionKey = "question_hash_version"

// ensureQuestionHashVersion rehashes the whole bank when the stored
// normalization version is missing or older than match.NormalizerVersion, then
// records the current version.
func (s *Store) ensureQuestionHashVersion() error {
	raw, found, err := s.GetSetting(questionHashVersionKey)
	if err != nil {
		return fmt.Errorf("读取 QuestionHash 版本: %w", err)
	}
	if found && decodeNormalizerVersion(raw) == match.NormalizerVersion {
		return nil
	}
	if err := s.rehashQuestionHash(); err != nil {
		return err
	}
	return s.SetSetting(questionHashVersionKey, json.RawMessage(strconv.Itoa(match.NormalizerVersion)))
}

// decodeNormalizerVersion parses the stored version, tolerating both a bare
// number and a quoted string form. An unparsable value reports version 0 so it
// triggers a rehash.
func decodeNormalizerVersion(raw json.RawMessage) int {
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			return n
		}
	}
	return 0
}

// pendingHash is one row awaiting a recomputed digest.
type pendingHash struct {
	id       int64
	question string
}

// rehashQuestionHash recomputes QuestionHash for every row in bounded, key
// ordered batches. It is used when the normalization algorithm changes and,
// unlike backfillQuestionHash, does not restrict itself to NULL digests.
func (s *Store) rehashQuestionHash() error {
	const batchSize = 2000
	log.Printf("归一化算法已更新，开始重算 QuestionHash（分批进行）")
	lastID := int64(0)
	for {
		rows, err := s.query(
			`SELECT Id, Question FROM AIResponses WHERE Id > $1 ORDER BY Id LIMIT $2`,
			lastID, batchSize,
		)
		if err != nil {
			return fmt.Errorf("读取待重算题目: %w", err)
		}
		items, err := scanPendingHash(rows)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			log.Printf("QuestionHash 重算完成")
			return nil
		}
		if err := s.writeQuestionHashBatch(items); err != nil {
			return err
		}
		lastID = items[len(items)-1].id
	}
}

// backfillQuestionHash fills QuestionHash for every row still missing one. Rows
// are processed in key-ordered batches so each UPDATE touches a bounded set.
func (s *Store) backfillQuestionHash() error {
	const batchSize = 2000
	log.Printf("开始回填 QuestionHash（分批进行）")
	for {
		rows, err := s.query(
			`SELECT Id, Question FROM AIResponses WHERE QuestionHash IS NULL ORDER BY Id LIMIT $1`,
			batchSize,
		)
		if err != nil {
			return fmt.Errorf("读取待回填题目: %w", err)
		}
		items, err := scanPendingHash(rows)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			log.Printf("QuestionHash 回填完成")
			return nil
		}
		if err := s.writeQuestionHashBatch(items); err != nil {
			return err
		}
	}
}

// scanPendingHash reads the (id, question) pairs from a batch query, closing the
// rows before returning.
func scanPendingHash(rows *sql.Rows) ([]pendingHash, error) {
	defer rows.Close()
	var items []pendingHash
	for rows.Next() {
		var it pendingHash
		if err := rows.Scan(&it.id, &it.question); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// writeQuestionHashBatch stores recomputed digests for one batch inside a single
// transaction.
func (s *Store) writeQuestionHashBatch(items []pendingHash) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, it := range items {
		if _, err := execOn(s.dialect, tx,
			`UPDATE AIResponses SET QuestionHash = $1 WHERE Id = $2`,
			match.QuestionHash(it.question), it.id,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("回填题目 %d 失败: %w", it.id, err)
		}
	}
	return tx.Commit()
}

// ensureQuestionHashIndex creates the lookup index if it is missing. Unique is
// intentionally avoided: duplicate questions are tolerated by the matcher, which
// scores every candidate sharing a hash.
func (s *Store) ensureQuestionHashIndex() error {
	exists, err := s.indexExists("idx_airesponses_question_hash")
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if s.dialect.isMySQL() {
		log.Printf("创建 QuestionHash 索引")
		if _, err := s.exec(
			`CREATE INDEX idx_airesponses_question_hash ON AIResponses (QuestionHash)`,
		); err != nil {
			return fmt.Errorf("创建 QuestionHash 索引失败: %w", err)
		}
		return nil
	}
	log.Printf("创建 QuestionHash 索引（CONCURRENTLY）")
	if _, err := s.exec(
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_airesponses_question_hash
			ON AIResponses (QuestionHash)`,
	); err != nil {
		return fmt.Errorf("创建 QuestionHash 索引失败: %w", err)
	}
	return nil
}
