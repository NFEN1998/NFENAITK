package store

import (
	"database/sql"
	"fmt"
	"log"
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
}

// migrate applies every pending migration in order.
func (s *Store) migrate() error {
	if err := s.ensureMigrationTable(); err != nil {
		return err
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

	// Guarantee the implicit root folder exists and the sequence is in sync.
	return s.ensureRootFolder()
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

// SchemaVersion returns the highest applied migration version. Exposed for the
// console and diagnostics.
func (s *Store) SchemaVersion() (int, error) {
	var v sql.NullInt64
	err := s.db.QueryRow(`SELECT MAX(Version) FROM SchemaMigrations`).Scan(&v)
	if err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}
