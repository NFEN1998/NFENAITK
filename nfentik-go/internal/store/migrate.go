package store

import (
	"database/sql"
	"fmt"
	"log"
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
			`CREATE TABLE IF NOT EXISTS Folders (
				Id SERIAL PRIMARY KEY,
				Name TEXT NOT NULL,
				ParentId INTEGER DEFAULT 0,
				CreateTime TIMESTAMP DEFAULT NOW()
			)`,
			`CREATE TABLE IF NOT EXISTS AIResponses (
				Id SERIAL PRIMARY KEY,
				Question TEXT NOT NULL,
				Options TEXT,
				QuestionType TEXT,
				Answer TEXT NOT NULL,
				CreateTime TIMESTAMP DEFAULT NOW(),
				FolderId INTEGER DEFAULT 0,
				FolderName TEXT DEFAULT '默认文件夹',
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
			`CREATE INDEX IF NOT EXISTS idx_airesponses_folder ON AIResponses(FolderId)`,
			`CREATE INDEX IF NOT EXISTS idx_airesponses_pending ON AIResponses(IsPendingCorrection)`,
			`CREATE INDEX IF NOT EXISTS idx_airesponses_created ON AIResponses(CreateTime DESC)`,
			// Columns that were added after the very first release. They are
			// folded into the baseline so both fresh and legacy databases end up
			// with the same shape.
			`ALTER TABLE Folders ADD COLUMN IF NOT EXISTS ParentId INTEGER DEFAULT 0`,
			`ALTER TABLE Folders ADD COLUMN IF NOT EXISTS CreateTime TIMESTAMP DEFAULT NOW()`,
			`ALTER TABLE AIResponses ADD COLUMN IF NOT EXISTS QuestionType TEXT`,
			`ALTER TABLE AIResponses ADD COLUMN IF NOT EXISTS CreateTime TIMESTAMP DEFAULT NOW()`,
			`ALTER TABLE AIResponses ADD COLUMN IF NOT EXISTS FolderId INTEGER DEFAULT 0`,
			`ALTER TABLE AIResponses ADD COLUMN IF NOT EXISTS FolderName TEXT DEFAULT '默认文件夹'`,
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
}

// coreTables are the tables every release expects. They are folded into the
// baseline migration, so a healthy database always has them.
var coreTables = []string{
	"folders",
	"airesponses",
	"requestlogs",
	"dailyrequestcounts",
	"appsettings",
	"users",
	"userrequestlogs",
	"userauditlogs",
}

// coreColumns maps each core table to the columns every release expects. It is
// used for integrity checks and for idempotent repair: a column can be missing
// even when the version table claims the migration that added it was applied
// (for example after a partial restore), so repair must not rely on version
// bookkeeping alone.
var coreColumns = map[string][]string{
	"folders":            {"id", "name", "parentid", "createtime"},
	"airesponses":        {"id", "question", "options", "questiontype", "answer", "createtime", "folderid", "foldername", "isai", "ispendingcorrection", "questionhash"},
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
	rows, err := s.db.Query(
		`SELECT t.name FROM unnest($1::text[]) AS t(name)
		 WHERE NOT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND lower(table_name) = t.name)`,
		pqArray(coreTables),
	)
	if err != nil {
		return report, fmt.Errorf("检查缺失表: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return report, err
		}
		report.MissingTables = append(report.MissingTables, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return report, err
	}
	rows.Close()
	report.Empty = len(report.MissingTables) == len(coreTables)

	// Which expected columns are missing from the tables that do exist.
	report.MissingColumns, err = s.missingColumns()
	if err != nil {
		return report, err
	}

	// Version table and recorded migrations.
	var hasVersion bool
	if err := s.db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		 WHERE table_schema = 'public' AND lower(table_name) = 'schemamigrations')`,
	).Scan(&hasVersion); err != nil {
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
		var exists bool
		if err := s.db.QueryRow(
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			 WHERE table_schema = 'public' AND lower(table_name) = $1)`,
			table,
		).Scan(&exists); err != nil {
			return nil, fmt.Errorf("检查表 %s: %w", table, err)
		}
		if !exists {
			continue
		}
		rows, err := s.db.Query(
			`SELECT c.name FROM unnest($1::text[]) AS c(name)
			 WHERE NOT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'public' AND lower(table_name) = $2
				  AND lower(column_name) = c.name)`,
			pqArray(cols), table,
		)
		if err != nil {
			return nil, fmt.Errorf("检查列 %s: %w", table, err)
		}
		for rows.Next() {
			var col string
			if err := rows.Scan(&col); err != nil {
				rows.Close()
				return nil, err
			}
			issues = append(issues, ColumnIssue{Table: table, Column: col})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return issues, nil
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

// pqArray renders a Go string slice as a PostgreSQL text[] literal. It is used
// for the small, static core-table list only.
func pqArray(items []string) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = `"` + strings.ReplaceAll(it, `"`, `""`) + `"`
	}
	return "{" + strings.Join(parts, ",") + "}"
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

	// Read-only integrity report, logged before any change so the startup log
	// shows exactly what the database looked like.
	if report, err := s.Inspect(); err != nil {
		log.Printf("数据库完整性检测失败: %v", err)
	} else {
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
	if err := s.repairSchema(); err != nil {
		return err
	}

	// Guarantee the implicit root folder exists and the sequence is in sync.
	if err := s.ensureRootFolder(); err != nil {
		return err
	}
	// Backfill the question hash column and build its index. Runs in batches so
	// it stays safe on banks with millions of rows.
	return s.ensureQuestionHash()
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
func (s *Store) repairSchema() error {
	// Only replay the baseline DDL when the integrity check saw a missing
	// table; the statements are idempotent but there is no reason to run them
	// on every healthy start.
	if report, err := s.Inspect(); err == nil && len(report.MissingTables) > 0 {
		for _, m := range migrations {
			if m.Version != baselineVersion() {
				continue
			}
			for _, stmt := range m.Statements {
				if _, err := s.db.Exec(stmt); err != nil {
					return fmt.Errorf("修复表结构失败: %w", err)
				}
			}
		}
		log.Printf("已重建缺失的表结构")
	}

	// Fill in any missing columns with their canonical definitions. The DDL is
	// kept here (not derived from migrations) so it stays unambiguous.
	for _, fix := range columnRepairs {
		if !fix.required(s) {
			continue
		}
		if _, err := s.db.Exec(`ALTER TABLE ` + fix.Table + ` ADD COLUMN IF NOT EXISTS ` + fix.DDL); err != nil {
			return fmt.Errorf("补齐字段 %s.%s 失败: %w", fix.Table, fix.Column, err)
		}
		log.Printf("已补齐缺失字段 %s.%s", fix.Table, fix.Column)
	}

	return nil
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
	var exists bool
	err := s.db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		 WHERE table_schema='public' AND lower(table_name)=$1 AND lower(column_name)=$2)`,
		c.Table, c.Column,
	).Scan(&exists)
	return err == nil && !exists
}

// columnRepairs lists every non-baseline column the schema may be missing. It is
// generated from the migration history so newly added columns only need to
// appear here once.
var columnRepairs = []columnRepair{
	{Table: "folders", Column: "parentid", DDL: "ParentId INTEGER DEFAULT 0"},
	{Table: "folders", Column: "createtime", DDL: "CreateTime TIMESTAMP DEFAULT NOW()"},
	{Table: "airesponses", Column: "questiontype", DDL: "QuestionType TEXT"},
	{Table: "airesponses", Column: "createtime", DDL: "CreateTime TIMESTAMP DEFAULT NOW()"},
	{Table: "airesponses", Column: "folderid", DDL: "FolderId INTEGER DEFAULT 0"},
	{Table: "airesponses", Column: "foldername", DDL: "FolderName TEXT DEFAULT '默认文件夹'"},
	{Table: "airesponses", Column: "isai", DDL: "IsAi BOOLEAN DEFAULT TRUE"},
	{Table: "airesponses", Column: "ispendingcorrection", DDL: "IsPendingCorrection BOOLEAN DEFAULT FALSE"},
	{Table: "airesponses", Column: "questionhash", DDL: "QuestionHash TEXT"},
	{Table: "userrequestlogs", Column: "options", DDL: "Options TEXT"},
	{Table: "userrequestlogs", Column: "answer", DDL: "Answer TEXT"},
}

func (s *Store) ensureMigrationTable() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS SchemaMigrations (
		Version INTEGER PRIMARY KEY,
		Name TEXT NOT NULL,
		AppliedAt TIMESTAMP DEFAULT NOW()
	)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

func (s *Store) appliedVersions() (map[int]bool, error) {
	rows, err := s.db.Query(`SELECT Version FROM SchemaMigrations`)
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
	var exists bool
	err := s.db.QueryRow(`SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema = 'public' AND lower(table_name) = 'airesponses')`).Scan(&exists)
	return exists, err
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
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("迁移 %03d %s 执行失败: %w", m.Version, m.Name, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO SchemaMigrations (Version, Name) VALUES ($1, $2)
		ON CONFLICT (Version) DO NOTHING`, m.Version, m.Name); err != nil {
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
	_, err := s.db.Exec(`INSERT INTO SchemaMigrations (Version, Name) VALUES ($1, $2)
		ON CONFLICT (Version) DO NOTHING`, m.Version, m.Name)
	return err
}

// ensureRootFolder creates the id 0 root folder and keeps the sequence ahead of
// it so generated ids never collide with the root row.
func (s *Store) ensureRootFolder() error {
	if _, err := s.db.Exec(`INSERT INTO Folders (Id, Name, ParentId) VALUES (0, '默认文件夹', 0)
		ON CONFLICT (Id) DO NOTHING`); err != nil {
		return fmt.Errorf("ensure root folder: %w", err)
	}
	if _, err := s.db.Exec(`SELECT setval(pg_get_serial_sequence('folders', 'id'),
		COALESCE((SELECT MAX(id) FROM folders), 0) + 1, false)`); err != nil {
		return fmt.Errorf("sync folders sequence: %w", err)
	}
	return nil
}

// ensureTrigram enables the pg_trgm extension and its GIN index when the
// database permits it. Failure is non-fatal: the query path degrades to the
// bounded prefix probe.
func (s *Store) ensureTrigram() error {
	if _, err := s.db.Exec(`CREATE EXTENSION IF NOT EXISTS pg_trgm`); err != nil {
		// A concurrent creator can win the race and surface a duplicate-key
		// error even though the extension now exists; verify before giving up.
		if !s.trigramAvailable() {
			log.Printf("pg_trgm 扩展不可用，模糊匹配将使用前缀回退: %v", err)
			return nil
		}
	}
	var exists bool
	if err := s.db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE c.relname = 'idx_airesponses_question_trgm' AND n.nspname = 'public')`,
	).Scan(&exists); err != nil {
		log.Printf("检查 trigram 索引失败，使用前缀回退: %v", err)
		return nil
	}
	if !exists {
		log.Printf("创建 trigram GIN 索引（CONCURRENTLY）")
		if _, err := s.db.Exec(
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
	err := s.db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm')`,
	).Scan(&ok)
	return err == nil && ok
}

// SchemaVersion returns the highest applied migration version. Exposed for the
// diagnostics endpoint.
func (s *Store) SchemaVersion() (int, error) {
	var v sql.NullInt64
	err := s.db.QueryRow(`SELECT MAX(Version) FROM SchemaMigrations`).Scan(&v)
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
	// Skip the (potentially expensive) backfill scan once the column is fully
	// populated. A count with a WHERE ... IS NULL LIMIT would still scan, so we
	// probe for a single residual row.
	var pending bool
	if err := s.db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM AIResponses WHERE QuestionHash IS NULL LIMIT 1)`,
	).Scan(&pending); err != nil {
		return fmt.Errorf("检查 QuestionHash 回填: %w", err)
	}
	if pending {
		if err := s.backfillQuestionHash(); err != nil {
			return err
		}
	}
	return s.ensureQuestionHashIndex()
}

// backfillQuestionHash fills QuestionHash for every row still missing one. Rows
// are processed in key-ordered batches so each UPDATE touches a bounded set.
func (s *Store) backfillQuestionHash() error {
	const batchSize = 2000
	log.Printf("开始回填 QuestionHash（分批进行）")
	for {
		rows, err := s.db.Query(
			`SELECT Id, Question FROM AIResponses WHERE QuestionHash IS NULL ORDER BY Id LIMIT $1`,
			batchSize,
		)
		if err != nil {
			return fmt.Errorf("读取待回填题目: %w", err)
		}
		type pending struct {
			id       int64
			question string
		}
		var items []pending
		for rows.Next() {
			var it pending
			if err := rows.Scan(&it.id, &it.question); err != nil {
				rows.Close()
				return err
			}
			items = append(items, it)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(items) == 0 {
			log.Printf("QuestionHash 回填完成")
			return nil
		}

		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		for _, it := range items {
			if _, err := tx.Exec(
				`UPDATE AIResponses SET QuestionHash = $1 WHERE Id = $2`,
				match.QuestionHash(it.question), it.id,
			); err != nil {
				tx.Rollback()
				return fmt.Errorf("回填题目 %d 失败: %w", it.id, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
}

// ensureQuestionHashIndex creates the lookup index if it is missing. Unique is
// intentionally avoided: duplicate questions are tolerated by the matcher, which
// scores every candidate sharing a hash.
func (s *Store) ensureQuestionHashIndex() error {
	var exists bool
	if err := s.db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE c.relname = 'idx_airesponses_question_hash' AND n.nspname = 'public')`,
	).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	log.Printf("创建 QuestionHash 索引（CONCURRENTLY）")
	if _, err := s.db.Exec(
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_airesponses_question_hash
			ON AIResponses (QuestionHash)`,
	); err != nil {
		return fmt.Errorf("创建 QuestionHash 索引失败: %w", err)
	}
	return nil
}
