package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

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
		// index are done in ensureHashes after migrations, because both
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
	{
		Version: 7,
		Name:    "options_hash_uniqueness",
		// OptionsHash is the normalized options digest. Together with
		// QuestionHash it forms the uniqueness key that stops concurrent
		// writers (an import and a query write-back) from inserting the same
		// question twice with different answers. Only the column is added here;
		// the backfill, duplicate merge and unique index run in
		// ensureHashes so they can be batched.
		Statements: []string{
			`ALTER TABLE AIResponses ADD COLUMN IF NOT EXISTS OptionsHash TEXT`,
		},
	},
	{
		Version: 8,
		Name:    "account_login_recharge",
		// Accounts give the web portal a username/password identity, sessions
		// keep that login state server side, and redemption codes carry the
		// quota a user buys. Tokens stay untouched and remain the /query
		// credential. Timestamps are TEXT so the same scan code works on both
		// PostgreSQL and MySQL without driver-level time parsing.
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS Accounts (
				Id BIGSERIAL PRIMARY KEY,
				Username VARCHAR(32) NOT NULL,
				UsernameKey VARCHAR(32) NOT NULL,
				PasswordHash VARCHAR(100) NOT NULL,
				UserId BIGINT,
				Status VARCHAR(16) NOT NULL DEFAULT 'enabled',
				CreatedAt TIMESTAMP DEFAULT NOW(),
				UpdatedAt TIMESTAMP DEFAULT NOW(),
				LastLoginAt TIMESTAMP,
				UNIQUE (UsernameKey),
				UNIQUE (UserId)
			)`,
			`CREATE TABLE IF NOT EXISTS AccountSessions (
				Id BIGSERIAL PRIMARY KEY,
				AccountId BIGINT NOT NULL,
				TokenHash CHAR(64) NOT NULL,
				ExpiresAt TEXT NOT NULL,
				CreatedAt TEXT NOT NULL,
				LastSeenAt TEXT NOT NULL,
				IP VARCHAR(64) NOT NULL DEFAULT '',
				UserAgent VARCHAR(255) NOT NULL DEFAULT '',
				UNIQUE (TokenHash)
			)`,
			`CREATE INDEX IF NOT EXISTS idx_account_sessions_account ON AccountSessions (AccountId)`,
			`CREATE TABLE IF NOT EXISTS RedemptionBatches (
				Id BIGSERIAL PRIMARY KEY,
				BatchNo VARCHAR(32) NOT NULL,
				Kind VARCHAR(16) NOT NULL,
				PlanCode VARCHAR(32) NOT NULL,
				Amount BIGINT NOT NULL DEFAULT 0,
				Days INTEGER NOT NULL DEFAULT 0,
				Quantity INTEGER NOT NULL,
				Note VARCHAR(255) NOT NULL DEFAULT '',
				CreatedAt TIMESTAMP DEFAULT NOW(),
				UNIQUE (BatchNo)
			)`,
			`CREATE TABLE IF NOT EXISTS RedemptionCodes (
				Id BIGSERIAL PRIMARY KEY,
				BatchId BIGINT NOT NULL,
				CodeHash CHAR(64) NOT NULL,
				Kind VARCHAR(16) NOT NULL,
				Amount BIGINT NOT NULL DEFAULT 0,
				Days INTEGER NOT NULL DEFAULT 0,
				ExpiresAt TEXT,
				Status VARCHAR(16) NOT NULL DEFAULT 'unused',
				UsedBy BIGINT,
				UsedAt TEXT,
				CreatedAt TIMESTAMP DEFAULT NOW(),
				UNIQUE (CodeHash)
			)`,
			`CREATE INDEX IF NOT EXISTS idx_redeem_codes_batch ON RedemptionCodes (BatchId)`,
			`CREATE INDEX IF NOT EXISTS idx_redeem_codes_status ON RedemptionCodes (Status)`,
		},
	},

	{
		Version: 9,
		Name:    "plans_catalogue",
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS Plans (
				Code VARCHAR(32) PRIMARY KEY,
				Label VARCHAR(64) NOT NULL,
				Kind VARCHAR(16) NOT NULL,
				Days INTEGER NOT NULL DEFAULT 0,
				QuotaCount BIGINT NOT NULL DEFAULT 0,
				Unlimited BOOLEAN NOT NULL DEFAULT FALSE,
				Custom BOOLEAN NOT NULL DEFAULT FALSE,
				ExpiryDays INTEGER NOT NULL DEFAULT 0,
				Enabled BOOLEAN NOT NULL DEFAULT TRUE,
				Sort INTEGER NOT NULL DEFAULT 0,
				PriceCents BIGINT NOT NULL DEFAULT 0,
				Currency VARCHAR(8) NOT NULL DEFAULT 'CNY',
				Description VARCHAR(255) NOT NULL DEFAULT '',
				Badge VARCHAR(32) NOT NULL DEFAULT '',
				CreatedAt TIMESTAMP DEFAULT NOW(),
				UpdatedAt TIMESTAMP DEFAULT NOW()
			)`,
			`CREATE INDEX IF NOT EXISTS idx_plans_sort ON Plans(Sort, Code)`,
		},
	},
	{
		Version: 10,
		Name:    "user_entitlements",
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS UserEntitlements (
				UserId BIGINT NOT NULL,
				Kind VARCHAR(16) NOT NULL,
				PlanCode VARCHAR(64) NOT NULL DEFAULT '',
				PlanLabel VARCHAR(128) NOT NULL DEFAULT '',
				StartAt TEXT NOT NULL DEFAULT '',
				ExpireAt TEXT,
				Unlimited BOOLEAN NOT NULL DEFAULT FALSE,
				TotalCount BIGINT,
				UsedCount BIGINT NOT NULL DEFAULT 0,
				RemainCount BIGINT,
				UpdatedAt TIMESTAMP DEFAULT NOW(),
				PRIMARY KEY (UserId, Kind)
			)`,
			`CREATE INDEX IF NOT EXISTS idx_user_entitlements_plan ON UserEntitlements(PlanCode)`,
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
	"accounts",
	"accountsessions",
	"redemptionbatches",
	"redemptioncodes",
	"plans",
	"userentitlements",
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
	"accounts":           "Accounts",
	"accountsessions":    "AccountSessions",
	"redemptionbatches":  "RedemptionBatches",
	"redemptioncodes":    "RedemptionCodes",
	"plans":              "Plans",
	"userentitlements":   "UserEntitlements",
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
	"airesponses":        {"id", "question", "options", "questiontype", "answer", "createtime", "isai", "ispendingcorrection", "questionhash", "optionshash", "source"},
	"requestlogs":        {"logid", "requestid", "timestamp", "method", "path", "status", "responsetime", "requestbody", "responsebody", "headers", "ip", "useragent", "stage"},
	"dailyrequestcounts": {"day", "count"},
	"appsettings":        {"key", "value", "updatetime"},
	"users":              {"id", "token", "plantype", "plancode", "planlabel", "startat", "expireat", "totalcount", "usedcount", "remaincount", "note", "enabled", "createdat", "updatedat"},
	"userrequestlogs":    {"logid", "userid", "token", "timestamp", "question", "options", "answer", "source", "status", "responsetime"},
	"userauditlogs":      {"auditid", "action", "userid", "usertoken", "detail", "actor", "createdat"},
	"accounts":           {"id", "username", "usernamekey", "passwordhash", "userid", "status", "createdat", "updatedat", "lastloginat"},
	"accountsessions":    {"id", "accountid", "tokenhash", "expiresat", "createdat", "lastseenat", "ip", "useragent"},
	"redemptionbatches":  {"id", "batchno", "kind", "plancode", "amount", "days", "quantity", "note", "createdat"},
	"redemptioncodes":    {"id", "batchid", "codehash", "kind", "amount", "days", "expiresat", "status", "usedby", "usedat", "createdat"},
	"plans":              {"code", "label", "kind", "days", "quotacount", "unlimited", "custom", "expirydays", "enabled", "sort", "pricecents", "currency", "description", "badge", "createdat", "updatedat"},
	"userentitlements":   {"userid", "kind", "plancode", "planlabel", "startat", "expireat", "unlimited", "totalcount", "usedcount", "remaincount", "updatedat"},
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

	// Backfill the hash columns and build their indexes. Runs in batches so
	// it stays safe on banks with millions of rows.
	return s.ensureHashes()
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
	return s.ensureHashes()
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
		OptionsHash VARCHAR(64),
		Source VARCHAR(64) DEFAULT '',
		PRIMARY KEY (Id),
		KEY idx_airesponses_pending (IsPendingCorrection),
		KEY idx_airesponses_created (CreateTime),
		KEY idx_airesponses_question_hash (QuestionHash),
		UNIQUE KEY idx_airesponses_question_options (QuestionHash, OptionsHash)
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
	`CREATE TABLE IF NOT EXISTS Accounts (
		Id BIGINT NOT NULL AUTO_INCREMENT,
		Username VARCHAR(32) NOT NULL,
		UsernameKey VARCHAR(32) NOT NULL,
		PasswordHash VARCHAR(100) NOT NULL,
		UserId BIGINT NULL,
		Status VARCHAR(16) NOT NULL DEFAULT 'enabled',
		CreatedAt DATETIME DEFAULT NOW(),
		UpdatedAt DATETIME DEFAULT NOW(),
		LastLoginAt DATETIME NULL,
		PRIMARY KEY (Id),
		UNIQUE KEY idx_accounts_username_key (UsernameKey),
		UNIQUE KEY idx_accounts_user_id (UserId)
	)` + mysqlTableOptions,
	`CREATE TABLE IF NOT EXISTS AccountSessions (
		Id BIGINT NOT NULL AUTO_INCREMENT,
		AccountId BIGINT NOT NULL,
		TokenHash CHAR(64) NOT NULL,
		ExpiresAt VARCHAR(32) NOT NULL,
		CreatedAt VARCHAR(32) NOT NULL,
		LastSeenAt VARCHAR(32) NOT NULL,
		IP VARCHAR(64) NOT NULL DEFAULT '',
		UserAgent VARCHAR(255) NOT NULL DEFAULT '',
		PRIMARY KEY (Id),
		UNIQUE KEY idx_account_sessions_token (TokenHash),
		KEY idx_account_sessions_account (AccountId)
	)` + mysqlTableOptions,
	`CREATE TABLE IF NOT EXISTS RedemptionBatches (
		Id BIGINT NOT NULL AUTO_INCREMENT,
		BatchNo VARCHAR(32) NOT NULL,
		Kind VARCHAR(16) NOT NULL,
		PlanCode VARCHAR(32) NOT NULL,
		Amount BIGINT NOT NULL DEFAULT 0,
		Days INT NOT NULL DEFAULT 0,
		Quantity INT NOT NULL,
		Note VARCHAR(255) NOT NULL DEFAULT '',
		CreatedAt DATETIME DEFAULT NOW(),
		PRIMARY KEY (Id),
		UNIQUE KEY idx_redeem_batches_no (BatchNo)
	)` + mysqlTableOptions,
	`CREATE TABLE IF NOT EXISTS RedemptionCodes (
		Id BIGINT NOT NULL AUTO_INCREMENT,
		BatchId BIGINT NOT NULL,
		CodeHash CHAR(64) NOT NULL,
		Kind VARCHAR(16) NOT NULL,
		Amount BIGINT NOT NULL DEFAULT 0,
		Days INT NOT NULL DEFAULT 0,
		ExpiresAt VARCHAR(32) NULL,
		Status VARCHAR(16) NOT NULL DEFAULT 'unused',
		UsedBy BIGINT NULL,
		UsedAt VARCHAR(32) NULL,
		CreatedAt DATETIME DEFAULT NOW(),
		PRIMARY KEY (Id),
		UNIQUE KEY idx_redeem_codes_hash (CodeHash),
		KEY idx_redeem_codes_batch (BatchId),
		KEY idx_redeem_codes_status (Status)
	)` + mysqlTableOptions,
	`CREATE TABLE IF NOT EXISTS Plans (
		Code VARCHAR(32) NOT NULL,
		Label VARCHAR(64) NOT NULL,
		Kind VARCHAR(16) NOT NULL,
		Days INT NOT NULL DEFAULT 0,
		QuotaCount BIGINT NOT NULL DEFAULT 0,
		Unlimited TINYINT(1) NOT NULL DEFAULT 0,
		Custom TINYINT(1) NOT NULL DEFAULT 0,
		ExpiryDays INT NOT NULL DEFAULT 0,
		Enabled TINYINT(1) NOT NULL DEFAULT 1,
		Sort INT NOT NULL DEFAULT 0,
		PriceCents BIGINT NOT NULL DEFAULT 0,
		Currency VARCHAR(8) NOT NULL DEFAULT 'CNY',
		Description VARCHAR(255) NOT NULL DEFAULT '',
		Badge VARCHAR(32) NOT NULL DEFAULT '',
		CreatedAt DATETIME DEFAULT NOW(),
		UpdatedAt DATETIME DEFAULT NOW(),
		PRIMARY KEY (Code),
		KEY idx_plans_sort (Sort, Code)
	)` + mysqlTableOptions,
	`CREATE TABLE IF NOT EXISTS UserEntitlements (
		UserId BIGINT NOT NULL,
		Kind VARCHAR(16) NOT NULL,
		PlanCode VARCHAR(64) NOT NULL DEFAULT '',
		PlanLabel VARCHAR(128) NOT NULL DEFAULT '',
		StartAt VARCHAR(32) NOT NULL DEFAULT '',
		ExpireAt VARCHAR(32) NULL,
		Unlimited TINYINT(1) NOT NULL DEFAULT 0,
		TotalCount BIGINT NULL,
		UsedCount BIGINT NOT NULL DEFAULT 0,
		RemainCount BIGINT NULL,
		UpdatedAt DATETIME DEFAULT NOW(),
		PRIMARY KEY (UserId, Kind),
		KEY idx_user_entitlements_plan (PlanCode)
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
	{Table: "airesponses", Column: "optionshash", DDL: "OptionsHash TEXT"},
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
	s.trigram.Store(true)
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

// ensureHashes keeps the digest columns and their indexes consistent with the
// current normalization algorithm. Small banks are repaired inline; large banks
// hand the work to a background worker so a deploy restores service in seconds
// instead of waiting hours for a full rehash.
func (s *Store) ensureHashes() error {
	if err := s.refreshCapabilityFlags(); err != nil {
		return err
	}
	needed, err := s.hashMaintenanceNeeded()
	if err != nil {
		return err
	}
	if !needed {
		return nil
	}
	if s.bankIsLarge() {
		log.Printf("题库较大，哈希维护将在后台进行，服务立即启动")
		s.startHashMaintenance()
		return nil
	}
	return s.runHashMaintenance()
}

// hashMaintenanceSyncMaxRows is the largest bank still repaired inline. Above
// it the work moves to the background worker.
const hashMaintenanceSyncMaxRows = 20000

// bankIsLarge reports whether the bank is above the inline maintenance limit.
// It probes a bounded offset instead of counting millions of rows.
func (s *Store) bankIsLarge() bool {
	var one int
	err := s.queryRow(
		`SELECT 1 FROM AIResponses LIMIT 1 OFFSET $1`, hashMaintenanceSyncMaxRows,
	).Scan(&one)
	if err == nil {
		return true
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	// A probe error must never make startup block: assume large and defer.
	return true
}

// startHashMaintenance launches the background worker. It is cancelled by
// Close; a failed or interrupted pass retries on the next startup.
func (s *Store) startHashMaintenance() {
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		if err := s.runHashMaintenance(); err != nil {
			log.Printf("后台哈希维护未完成（下次启动继续）: %v", err)
		}
	}()
}

// refreshCapabilityFlags mirrors optional index presence into atomics read by
// the query and insert paths.
func (s *Store) refreshCapabilityFlags() error {
	hasUnique, err := s.indexExists("idx_airesponses_question_options")
	if err != nil {
		return err
	}
	s.uniqueQuestion.Store(hasUnique)
	if s.dialect.isPostgres() {
		hasTrigram, err := s.indexExists("idx_airesponses_question_trgm")
		if err != nil {
			return err
		}
		s.trigram.Store(hasTrigram)
	}
	return nil
}

// hashMaintenanceNeeded reports whether digest or index work remains. Version
// keys and catalog lookups are cheap, so the usual "already up to date"
// startup performs no table scan at all.
func (s *Store) hashMaintenanceNeeded() (bool, error) {
	for _, key := range []string{questionHashVersionKey, optionsHashVersionKey} {
		stale, err := s.hashVersionStale(key)
		if err != nil {
			return false, err
		}
		if stale {
			return true, nil
		}
	}
	hasLookup, err := s.indexExists("idx_airesponses_question_hash")
	if err != nil {
		return false, err
	}
	if !hasLookup || !s.uniqueQuestion.Load() {
		return true, nil
	}
	if s.dialect.isPostgres() && !s.trigram.Load() {
		return true, nil
	}
	return false, nil
}

// runHashMaintenance performs every hash-related repair in dependency order.
func (s *Store) runHashMaintenance() error {
	ctx := s.bgCtx
	if ctx == nil {
		ctx = context.Background()
	}
	// Build the lookup index first so reads stay fast while a long rehash runs.
	// Differential writes keep the incremental index-maintenance cost small.
	if err := s.ensureQuestionHashIndex(); err != nil {
		return err
	}
	questionStale, err := s.hashVersionStale(questionHashVersionKey)
	if err != nil {
		return err
	}
	optionsStale, err := s.hashVersionStale(optionsHashVersionKey)
	if err != nil {
		return err
	}
	if questionStale || optionsStale {
		if err := s.rehashBank(ctx, questionStale, optionsStale); err != nil {
			return err
		}
	}
	// Merging only needs to run while the unique index is missing; once it
	// exists the database guarantees there are no duplicates.
	if exists, err := s.indexExists("idx_airesponses_question_options"); err != nil {
		return err
	} else if !exists {
		s.dedupeBankRows()
	}
	if err := s.ensureOptionsUniqueIndex(); err != nil {
		return err
	}
	return s.ensureTrigram()
}

// questionHashVersionKey stores the NormalizeQuestion version used to build the
// QuestionHash column. When the algorithm changes the stored value falls behind
// and every row is rehashed exactly once.
const questionHashVersionKey = "question_hash_version"

// hashVersionStale reports whether the stored normalization version for one
// hash column is missing or older than match.NormalizerVersion.
func (s *Store) hashVersionStale(key string) (bool, error) {
	raw, found, err := s.GetSetting(key)
	if err != nil {
		return false, fmt.Errorf("读取 %s 版本: %w", key, err)
	}
	return !found || decodeNormalizerVersion(raw) != match.NormalizerVersion, nil
}

// rehashBank recomputes stale digest columns. The unique index is dropped for
// the duration because a rehash can make an old row collide with a row that
// already carries the new digest; duplicates are merged before the index is
// rebuilt. The lookup index stays in place so reads remain fast while the
// background pass runs.
func (s *Store) rehashBank(ctx context.Context, questionStale, optionsStale bool) error {
	s.dropIndexIfExists("idx_airesponses_question_options")

	if err := s.rehashHashes(ctx, questionStale, optionsStale); err != nil {
		return err
	}
	// Version keys are recorded only after a full pass, so an interrupted run
	// simply repeats on the next startup and already-updated rows are skipped
	// by the digest comparison.
	if questionStale {
		if err := s.SetSetting(questionHashVersionKey, json.RawMessage(strconv.Itoa(match.NormalizerVersion))); err != nil {
			return err
		}
	}
	if optionsStale {
		if err := s.SetSetting(optionsHashVersionKey, json.RawMessage(strconv.Itoa(match.NormalizerVersion))); err != nil {
			return err
		}
	}
	return nil
}

// dropIndexIfExists removes an AIResponses index, tolerating absence. A failure
// is logged and skipped: the rehash still works with the index in place, just
// slower.
func (s *Store) dropIndexIfExists(name string) {
	exists, err := s.indexExists(name)
	if err != nil || !exists {
		return
	}
	query := "DROP INDEX " + name
	if s.dialect.isMySQL() {
		query = "DROP INDEX " + name + " ON AIResponses"
	}
	if _, err := s.exec(query); err != nil {
		log.Printf("警告: 删除索引 %s 失败: %v", name, err)
	}
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

// hashProgress emits periodic progress logs during long rehash/backfill passes
// so a multi-million row migration never looks stuck. It reports every 100k
// rows with a percentage and an ETA derived from the observed throughput.
type hashProgress struct {
	mu      sync.Mutex
	label   string
	total   int64
	done    int64
	start   time.Time
	nextLog int64
}

func newHashProgress(label string, total int64) *hashProgress {
	return &hashProgress{label: label, total: total, start: time.Now(), nextLog: 100000}
}

func (p *hashProgress) add(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done += int64(n)
	if p.done < p.nextLog {
		return
	}
	elapsed := time.Since(p.start).Round(time.Second)
	if p.total > 0 {
		percent := float64(p.done) / float64(p.total) * 100
		if percent > 100 {
			percent = 100
		}
		eta := ""
		if p.done < p.total {
			remain := time.Duration(float64(time.Since(p.start)) / float64(p.done) * float64(p.total-p.done))
			eta = fmt.Sprintf("，预计剩余 %s", remain.Round(time.Second))
		}
		log.Printf("%s进度: %d/%d (%.1f%%)，已耗时 %s%s", p.label, p.done, p.total, percent, elapsed, eta)
	} else {
		log.Printf("%s进度: %d 行，已耗时 %s", p.label, p.done, elapsed)
	}
	p.nextLog = p.done + 100000
}

func (p *hashProgress) finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	log.Printf("%s完成，共 %d 行，耗时 %s", p.label, p.done, time.Since(p.start).Round(time.Second))
}

// hashBatchSize is the number of rows per rehash/backfill statement.
// PostgreSQL's unnest form binds only two array parameters regardless of
// size, so larger batches amortize the per-statement overhead. MySQL's CASE
// form binds three placeholders per row and stays smaller to limit parse
// cost while remaining under the driver parameter limit.
func (s *Store) hashBatchSize() int {
	if s.dialect.isPostgres() {
		return 10000
	}
	return 2000
}

// pendingHashRow is one row read during maintenance together with its stored
// digests. The stored values let the pass rewrite only rows whose digest
// actually changed instead of updating every row unconditionally.
type pendingHashRow struct {
	id           int64
	question     string
	options      string
	questionHash string
	optionsHash  string
}

func scanPendingHashRows(rows *sql.Rows) ([]pendingHashRow, error) {
	defer rows.Close()
	var items []pendingHashRow
	for rows.Next() {
		var it pendingHashRow
		if err := rows.Scan(&it.id, &it.question, &it.options, &it.questionHash, &it.optionsHash); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// rehashHashes scans every row once and rewrites only the digests that changed.
// The scan is split into disjoint Id ranges so several workers can hash text in
// parallel; the write volume stays proportional to the real delta.
func (s *Store) rehashHashes(ctx context.Context, questionStale, optionsStale bool) error {
	var total int64
	if err := s.queryRow(`SELECT COUNT(*) FROM AIResponses`).Scan(&total); err != nil {
		return fmt.Errorf("统计题目总数: %w", err)
	}
	if total == 0 {
		log.Printf("哈希重算完成，共 0 行")
		return nil
	}
	var maxID int64
	if err := s.queryRow(`SELECT COALESCE(MAX(Id), 0) FROM AIResponses`).Scan(&maxID); err != nil {
		return fmt.Errorf("读取题目最大 ID: %w", err)
	}
	workers := envInt("HASH_REHASH_WORKERS", 4)
	if workers > 16 {
		workers = 16
	}
	if int64(workers) > total {
		workers = int(total)
	}
	if workers < 1 {
		workers = 1
	}
	log.Printf("哈希重算开始：%d 行，%d 个并发分片", total, workers)

	progress := newHashProgress("哈希重算", total)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	for i := 0; i < workers; i++ {
		lo := maxID * int64(i) / int64(workers)
		hi := maxID * int64(i+1) / int64(workers)
		if i == workers-1 {
			hi = maxID
		}
		wg.Add(1)
		go func(lo, hi int64) {
			defer wg.Done()
			if err := s.rehashRange(ctx, lo, hi, questionStale, optionsStale, progress); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
				cancel()
			}
		}(lo, hi)
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	progress.finish()
	return nil
}

// rehashRange processes one Id range on its own connection so a worker can relax
// the PostgreSQL WAL flush latency without affecting the shared pool.
func (s *Store) rehashRange(ctx context.Context, lo, hi int64, questionStale, optionsStale bool, progress *hashProgress) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if s.dialect.isPostgres() {
			_, _ = conn.ExecContext(context.Background(), "RESET synchronous_commit")
		}
		conn.Close()
	}()
	if s.dialect.isPostgres() {
		if _, err := conn.ExecContext(ctx, "SET synchronous_commit = off"); err != nil {
			log.Printf("警告: 设置 synchronous_commit 失败: %v", err)
		}
	}
	q := connQueryer{conn: conn, ctx: ctx}
	batchSize := s.hashBatchSize()
	lastID := lo
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := queryOn(s.dialect, q,
			`SELECT Id, Question, COALESCE(Options, ''), COALESCE(QuestionHash, ''), COALESCE(OptionsHash, '')
			 FROM AIResponses WHERE Id > $1 AND Id <= $2 ORDER BY Id LIMIT $3`,
			lastID, hi, batchSize,
		)
		if err != nil {
			return fmt.Errorf("读取待重算题目: %w", err)
		}
		items, err := scanPendingHashRows(rows)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		if _, err := s.writePendingHashes(q, items, questionStale, optionsStale); err != nil {
			return err
		}
		progress.add(len(items))
		lastID = items[len(items)-1].id
	}
}

// hashRow is one (row id, digests) assignment produced by a rehash batch.
type hashRow struct {
	id           int64
	questionHash string
	optionsHash  string
}

// writePendingHashes computes the new digests, filters unchanged rows and
// applies the remainder with a single UPDATE per batch.
func (s *Store) writePendingHashes(q queryer, items []pendingHashRow, questionStale, optionsStale bool) (int, error) {
	values := make([]hashRow, 0, len(items))
	for _, it := range items {
		qh := it.questionHash
		if questionStale {
			qh = match.QuestionHash(it.question)
		}
		oh := it.optionsHash
		if optionsStale {
			opts := it.options
			oh = match.OptionsHash(&opts)
		}
		if qh == it.questionHash && oh == it.optionsHash {
			continue
		}
		values = append(values, hashRow{id: it.id, questionHash: qh, optionsHash: oh})
	}
	if len(values) == 0 {
		return 0, nil
	}
	if err := s.writeHashRowBatch(q, values); err != nil {
		return 0, err
	}
	return len(values), nil
}

// writeHashRowBatch updates both digest columns in one statement. PostgreSQL
// unnests three arrays; MySQL uses the CASE form bounded by a primary key IN
// list, which stays within the driver placeholder limit for the batch sizes
// produced by hashBatchSize.
func (s *Store) writeHashRowBatch(q queryer, values []hashRow) error {
	if len(values) == 0 {
		return nil
	}
	var b strings.Builder
	if s.dialect.isMySQL() {
		args := make([]any, 0, len(values)*5)
		add := func(v any) string {
			args = append(args, v)
			return fmt.Sprintf("$%d", len(args))
		}
		b.WriteString("UPDATE AIResponses SET QuestionHash = CASE Id")
		for _, v := range values {
			b.WriteString(" WHEN ")
			b.WriteString(add(v.id))
			b.WriteString(" THEN ")
			b.WriteString(add(v.questionHash))
		}
		b.WriteString(" END, OptionsHash = CASE Id")
		for _, v := range values {
			b.WriteString(" WHEN ")
			b.WriteString(add(v.id))
			b.WriteString(" THEN ")
			b.WriteString(add(v.optionsHash))
		}
		b.WriteString(" END WHERE Id IN (")
		for i, v := range values {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(add(v.id))
		}
		b.WriteString(")")
		if _, err := execOn(s.dialect, q, b.String(), args...); err != nil {
			return fmt.Errorf("批量重算哈希失败: %w", err)
		}
		return nil
	}
	ids := make([]int64, len(values))
	qhs := make([]string, len(values))
	ohs := make([]string, len(values))
	for i, v := range values {
		ids[i] = v.id
		qhs[i] = v.questionHash
		ohs[i] = v.optionsHash
	}
	b.WriteString(`UPDATE AIResponses a SET QuestionHash = v.qh, OptionsHash = v.oh
		FROM unnest($1::bigint[], $2::text[], $3::text[]) AS v(i, qh, oh)
		WHERE a.Id = v.i`)
	if _, err := execOn(s.dialect, q, b.String(), ids, qhs, ohs); err != nil {
		return fmt.Errorf("批量重算哈希失败: %w", err)
	}
	return nil
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

// hashPair is one (row id, digest) assignment produced by a rehash batch.
type hashPair struct {
	id   int64
	hash string
}

// writeQuestionHashBatch stores recomputed digests for one batch with a single
// UPDATE. A per-row statement would cost one database round trip per row, which
// turns a multi-million row rehash into hours of network latency; the CASE form
// keeps the whole batch atomic in one statement on both engines.
func (s *Store) writeQuestionHashBatch(q queryer, items []pendingHash) error {
	values := make([]hashPair, 0, len(items))
	for _, it := range items {
		values = append(values, hashPair{id: it.id, hash: match.QuestionHash(it.question)})
	}
	return s.writeHashBatchColumn(q, "QuestionHash", values)
}

// writeHashBatchColumn applies (id, digest) pairs to one hash column with a
// single UPDATE. PostgreSQL joins an unnest of two array parameters, which
// keeps parse/bind cost flat as the batch grows; a CASE expression or a large
// VALUES parameter list was measurably slower. MySQL keeps the CASE form
// bounded by a primary key IN list, within the 65535 driver parameter limit
// for the batch sizes used by callers.
func (s *Store) writeHashBatchColumn(q queryer, column string, values []hashPair) error {
	if len(values) == 0 {
		return nil
	}
	args := make([]any, 0, len(values)*2)
	var b strings.Builder
	if s.dialect.isMySQL() {
		// MySQL 5.7 has no VALUES table constructor, and a derived-table join
		// leaves the row access path to the optimizer. The CASE form with a
		// primary key IN list guarantees only the batch rows are touched.
		b.WriteString("UPDATE AIResponses SET ")
		b.WriteString(column)
		b.WriteString(" = CASE Id")
		for i, v := range values {
			fmt.Fprintf(&b, " WHEN $%d THEN $%d", i*2+1, i*2+2)
			args = append(args, v.id, v.hash)
		}
		b.WriteString(" END WHERE Id IN (")
		for i := range values {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "$%d", i*2+1)
		}
		b.WriteString(")")
	} else {
		// Two array parameters keep the statement tiny and the bind cost flat
		// regardless of batch size; a per-row VALUES list would bind thousands
		// of parameters and dominate the runtime on large banks.
		ids := make([]int64, len(values))
		hashes := make([]string, len(values))
		for i, v := range values {
			ids[i] = v.id
			hashes[i] = v.hash
		}
		b.WriteString("UPDATE AIResponses a SET ")
		b.WriteString(column)
		b.WriteString(" = v.h FROM unnest($1::bigint[], $2::text[]) AS v(i, h) WHERE a.Id = v.i")
		args = append(args, ids, hashes)
	}
	if _, err := execOn(s.dialect, q, b.String(), args...); err != nil {
		return fmt.Errorf("批量回填 %s 失败: %w", column, err)
	}
	return nil
}

// optionsHashVersionKey stores the NormalizeQuestion version used to build the
// OptionsHash column. OptionsHash is part of the uniqueness key and the cache
// key, so a stale digest would make formula options collide or miss.
const optionsHashVersionKey = "options_hash_version"

// pendingOptions is one row awaiting an options digest.
type pendingOptions struct {
	id      int64
	options string
}

func scanPendingOptions(rows *sql.Rows) ([]pendingOptions, error) {
	defer rows.Close()
	var items []pendingOptions
	for rows.Next() {
		var it pendingOptions
		if err := rows.Scan(&it.id, &it.options); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

func (s *Store) writeOptionsHashBatch(q queryer, items []pendingOptions) error {
	values := make([]hashPair, 0, len(items))
	for _, it := range items {
		opts := it.options
		values = append(values, hashPair{id: it.id, hash: match.OptionsHash(&opts)})
	}
	return s.writeHashBatchColumn(q, "OptionsHash", values)
}

// dedupeBankRows merges rows sharing a (QuestionHash, OptionsHash) key, keeping
// the most authoritative one: not pending correction, not AI, then oldest. It
// must run before the unique index can be built. A failure is logged and left
// for the next startup so a data problem never blocks the service; the writer
// then falls back to the in-process lock.
func (s *Store) dedupeBankRows() {
	// Only true (question, options) duplicates matter. Same question text with
	// different options is a distinct entry and must be left alone.
	var anyDuplicate int
	if err := s.queryRow(
		`SELECT 1 FROM AIResponses
			GROUP BY QuestionHash, OptionsHash HAVING COUNT(*) > 1 LIMIT 1`,
	).Scan(&anyDuplicate); err == sql.ErrNoRows {
		return
	} else if err != nil {
		log.Printf("警告: 检查重复题目失败: %v", err)
		return
	}

	log.Printf("检测到重复题目，开始合并（保留非待纠正、非 AI、最早的一行）")
	res, err := s.exec(`DELETE FROM AIResponses WHERE Id IN (
		SELECT Id FROM (
			SELECT Id, ROW_NUMBER() OVER (
				PARTITION BY QuestionHash, OptionsHash
				ORDER BY COALESCE(IsPendingCorrection, FALSE) ASC, IsAi ASC, Id ASC
			) AS rn
			FROM AIResponses
		) ranked WHERE rn > 1
	)`)
	if err != nil {
		log.Printf("警告: 合并重复题目失败: %v", err)
		return
	}
	if affected, _ := res.RowsAffected(); affected > 0 {
		log.Printf("已合并 %d 行重复题目", affected)
	}
}

// ensureOptionsUniqueIndex creates the (QuestionHash, OptionsHash) unique index
// that backs ON CONFLICT inserts. PostgreSQL builds it CONCURRENTLY; MySQL uses
// a plain unique index. On failure it is logged and uniqueQuestion stays false,
// so inserts fall back to the serialized NOT EXISTS path.
func (s *Store) ensureOptionsUniqueIndex() error {
	exists, err := s.indexExists("idx_airesponses_question_options")
	if err != nil {
		return err
	}
	if exists {
		s.uniqueQuestion.Store(true)
		return nil
	}
	if s.dialect.isMySQL() {
		if _, err := s.exec(
			`CREATE UNIQUE INDEX idx_airesponses_question_options ON AIResponses (QuestionHash, OptionsHash)`,
		); err != nil {
			log.Printf("警告: 创建题目唯一索引失败，退化为进程内写锁: %v", err)
			return nil
		}
		s.uniqueQuestion.Store(true)
		return nil
	}
	log.Printf("创建题目唯一索引（CONCURRENTLY）")
	if _, err := s.exec(
		`CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_airesponses_question_options
			ON AIResponses (QuestionHash, OptionsHash)`,
	); err != nil {
		log.Printf("警告: 创建题目唯一索引失败，退化为进程内写锁: %v", err)
		return nil
	}
	s.uniqueQuestion.Store(true)
	return nil
}

// ensureQuestionHashIndex creates the lookup index if it is missing. Unique is
// intentionally avoided here: this narrow index backs the by-question probes,
// while uniqueness is enforced separately on (QuestionHash, OptionsHash).
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
