package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

// dialectKind identifies the SQL engine backing the store.
type dialectKind string

const (
	dialectPostgres dialectKind = "postgres"
	dialectMySQL    dialectKind = "mysql"
)

// dialect carries the small set of behaviours that differ between PostgreSQL
// and MySQL. Queries are written once with $n placeholders; rebind translates
// them for the active driver so the bulk of the store stays dialect agnostic.
type dialect struct {
	kind dialectKind
}

func (d dialect) isPostgres() bool { return d.kind == dialectPostgres }
func (d dialect) isMySQL() bool    { return d.kind == dialectMySQL }

// detectDialect infers the engine from a DSN. PostgreSQL URLs start with
// postgres:// (or postgresql://); MySQL accepts a mysql:// URL or the native
// go-sql-driver form (user:pass@tcp(host:port)/dbname).
func detectDialect(dsn string) (dialect, error) {
	trimmed := strings.TrimSpace(dsn)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "postgres://"), strings.HasPrefix(lower, "postgresql://"):
		return dialect{kind: dialectPostgres}, nil
	case strings.HasPrefix(lower, "mysql://"),
		strings.Contains(lower, "@tcp("),
		strings.Contains(lower, "@unix("):
		return dialect{kind: dialectMySQL}, nil
	}
	return dialect{}, errors.New("无法识别的数据库连接串（需以 postgres:// 或 mysql:// 开头）")
}

// rebind rewrites a query for the target driver.
//
// PostgreSQL keeps $n placeholders untouched. MySQL expects ? placeholders and,
// because the same $n may occur more than once (for example a value reused in a
// guard clause), the argument slice is rebuilt in the order placeholders appear.
func (d dialect) rebind(query string, args []any) (string, []any) {
	if d.isPostgres() {
		return query, args
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	out := make([]any, 0, len(args))
	for i := 0; i < len(query); i++ {
		c := query[i]
		if c == '$' && i+1 < len(query) && query[i+1] >= '0' && query[i+1] <= '9' {
			j := i + 1
			for j < len(query) && query[j] >= '0' && query[j] <= '9' {
				j++
			}
			if n, err := strconv.Atoi(query[i+1 : j]); err == nil && n >= 1 && n <= len(args) {
				b.WriteByte('?')
				out = append(out, args[n-1])
				i = j - 1
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String(), out
}

// queryer is the subset of *sql.DB and *sql.Tx the helpers need.
type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

func queryOn(d dialect, q queryer, query string, args ...any) (*sql.Rows, error) {
	text, a := d.rebind(query, args)
	return q.Query(text, a...)
}

func queryRowOn(d dialect, q queryer, query string, args ...any) *sql.Row {
	text, a := d.rebind(query, args)
	return q.QueryRow(text, a...)
}

func execOn(d dialect, q queryer, query string, args ...any) (sql.Result, error) {
	text, a := d.rebind(query, args)
	return q.Exec(text, a...)
}

// ---------------------------------------------------------------------------
// SQL fragments
// ---------------------------------------------------------------------------

// nullSafeEqual renders a NULL-safe equality predicate between two expressions.
func (d dialect) nullSafeEqual(lhs, rhs string) string {
	if d.isMySQL() {
		return lhs + " <=> " + rhs
	}
	return lhs + " IS NOT DISTINCT FROM " + rhs
}

// ilike renders a case-insensitive LIKE. MySQL is made collation independent by
// comparing lowercased values.
func (d dialect) ilike(column, placeholder string) string {
	if d.isMySQL() {
		return "LOWER(" + column + ") LIKE LOWER(" + placeholder + ")"
	}
	return column + " ILIKE " + placeholder
}

// currentSchemaExpr names the active schema (PostgreSQL) or database (MySQL).
func (d dialect) currentSchemaExpr() string {
	if d.isMySQL() {
		return "DATABASE()"
	}
	return "current_schema()"
}

// settingKeyColumn quotes the AppSettings key column, which is a reserved word
// in MySQL.
func (d dialect) settingKeyColumn() string {
	if d.isMySQL() {
		return "`Key`"
	}
	return "Key"
}

// settingValueColumn quotes the AppSettings value column.
func (d dialect) settingValueColumn() string {
	if d.isMySQL() {
		return "`Value`"
	}
	return "Value"
}

// formatTimestamp renders a timestamp column as 'YYYY-MM-DD HH:MM:SS'.
func (d dialect) formatTimestamp(column string) string {
	if d.isMySQL() {
		return "DATE_FORMAT(" + column + ", '%Y-%m-%d %H:%i:%s')"
	}
	return "to_char(" + column + ", 'YYYY-MM-DD HH24:MI:SS')"
}

// dailyWindowStartExpr is the day-string lower bound for the 365 day chart.
func (d dialect) dailyWindowStartExpr() string {
	if d.isMySQL() {
		return "DATE_FORMAT(DATE_SUB(NOW(), INTERVAL 364 DAY), '%Y-%m-%d')"
	}
	return "to_char(NOW() - INTERVAL '364 days', 'YYYY-MM-DD')"
}

// upsertSettingSuffix appends the conflict resolution to an AppSettings insert.
func (d dialect) upsertSettingSuffix() string {
	key := d.settingKeyColumn()
	value := d.settingValueColumn()
	if d.isMySQL() {
		return " ON DUPLICATE KEY UPDATE " + value + " = VALUES(" + value + "), UpdateTime = NOW()"
	}
	return " ON CONFLICT (" + key + ") DO UPDATE SET " + value + " = EXCLUDED." + value + ", UpdateTime = NOW()"
}

// upsertDailyCountSuffix appends the conflict resolution to a daily counter
// insert, accumulating the incoming delta.
func (d dialect) upsertDailyCountSuffix() string {
	if d.isMySQL() {
		return " ON DUPLICATE KEY UPDATE Count = Count + VALUES(Count)"
	}
	return " ON CONFLICT (Day) DO UPDATE SET Count = DailyRequestCounts.Count + EXCLUDED.Count"
}

// recordVersionSuffix appends a no-op conflict resolution to a migration
// bookkeeping insert.
func (d dialect) recordVersionSuffix() string {
	if d.isMySQL() {
		return " ON DUPLICATE KEY UPDATE Version = Version"
	}
	return " ON CONFLICT (Version) DO NOTHING"
}

// isUniqueViolation reports whether err is a duplicate-key error, on either
// engine.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		return myErr.Number == 1062
	}
	return false
}

// ---------------------------------------------------------------------------
// MySQL connection
// ---------------------------------------------------------------------------

// mysqlInitSQL keeps an explicit 0 as a stored AUTO_INCREMENT value, so the
// implicit root folder (id 0) survives, while preserving whatever sql_mode the
// server ships with.
const mysqlInitSQL = "SET SESSION sql_mode = CONCAT(@@SESSION.sql_mode, ',NO_AUTO_VALUE_ON_ZERO')"

// initConnector runs a session initialisation statement on every pooled
// connection and then hands the connection back to database/sql.
type initConnector struct {
	base    driver.Connector
	initSQL string
}

func (c initConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	if c.initSQL != "" {
		ex, ok := conn.(driver.ExecerContext)
		if !ok {
			conn.Close()
			return nil, errors.New("mysql driver connection does not support ExecerContext")
		}
		if _, err := ex.ExecContext(ctx, c.initSQL, nil); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

func (c initConnector) Driver() driver.Driver { return c.base.Driver() }

// openDB opens a raw database handle for the given dialect.
func openDB(d dialect, dsn string) (*sql.DB, error) {
	if d.isPostgres() {
		return sql.Open("pgx", dsn)
	}
	return openMySQL(dsn)
}

// openMySQL builds a MySQL pool from a mysql:// URL or a native DSN.
func openMySQL(dsn string) (*sql.DB, error) {
	cfg, err := mysqlConfig(dsn)
	if err != nil {
		return nil, err
	}
	base, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(initConnector{base: base, initSQL: mysqlInitSQL}), nil
}

// mysqlConfig parses a mysql:// URL into a go-sql-driver config, or passes a
// native DSN through unchanged.
func mysqlConfig(dsn string) (*mysql.Config, error) {
	raw := strings.TrimSpace(dsn)
	if !strings.HasPrefix(strings.ToLower(raw), "mysql://") {
		return mysql.ParseDSN(raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	cfg := mysql.NewConfig()
	if u.User != nil {
		cfg.User = u.User.Username()
		if pass, ok := u.User.Password(); ok {
			cfg.Passwd = pass
		}
	}
	host := u.Hostname()
	if host == "" {
		host = "127.0.0.1"
	}
	port := u.Port()
	if port == "" {
		port = "3306"
	}
	cfg.Net = "tcp"
	cfg.Addr = host + ":" + port
	cfg.DBName = strings.TrimPrefix(u.Path, "/")
	cfg.ParseTime = false
	cfg.Collation = "utf8mb4_general_ci"
	cfg.Params = map[string]string{}
	for key, values := range u.Query() {
		if len(values) > 0 {
			cfg.Params[key] = values[0]
		}
	}
	return cfg, nil
}

// mysqlTableOptions is the engine/collation shared by every MySQL table.
const mysqlTableOptions = " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci"
