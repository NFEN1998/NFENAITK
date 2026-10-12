package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RequestLog is a persisted record of one HTTP request handled by the OCS API.
type RequestLog struct {
	RowID        int64             `json:"row_id,omitempty"`
	ID           string            `json:"id"`
	Timestamp    string            `json:"timestamp"`
	Method       string            `json:"method"`
	Path         string            `json:"path"`
	Status       *int              `json:"status,omitempty"`
	ResponseTime *int64            `json:"response_time,omitempty"`
	RequestBody  *string           `json:"request_body,omitempty"`
	ResponseBody *string           `json:"response_body,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	IP           *string           `json:"ip,omitempty"`
	UserAgent    *string           `json:"user_agent,omitempty"`
	Stage        string            `json:"stage"`
}

// RequestLogFilter narrows and paginates a request-log query.
type RequestLogFilter struct {
	Keyword  string
	Method   string
	Path     string
	Status   string // exact code, or a class such as "2xx", "4xx", "5xx"
	Page     int
	PageSize int
}

// normalize clamps the pagination and returns the offset.
func (f *RequestLogFilter) normalize() int {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize <= 0 {
		f.PageSize = 50
	}
	if f.PageSize > 500 {
		f.PageSize = 500
	}
	return (f.Page - 1) * f.PageSize
}

// conditions builds the shared WHERE clause and argument list for the list and
// count queries.
func (f RequestLogFilter) conditions(d dialect) ([]string, []any) {
	where := []string{"1=1"}
	args := []any{}
	add := func(clause string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}

	if f.Method != "" {
		add("Method = $%d", f.Method)
	}
	if f.Path != "" {
		args = append(args, "%"+f.Path+"%")
		where = append(where, d.ilike("Path", fmt.Sprintf("$%d", len(args))))
	}
	if f.Status != "" {
		switch {
		case len(f.Status) == 3 && f.Status[1:] == "xx" && f.Status[0] >= '1' && f.Status[0] <= '5':
			low := int(f.Status[0]-'0') * 100
			args = append(args, low, low+99)
			where = append(where, fmt.Sprintf("Status BETWEEN $%d AND $%d", len(args)-1, len(args)))
		default:
			if code, err := parseInt(f.Status); err == nil {
				add("Status = $%d", code)
			}
		}
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := "%" + kw + "%"
		args = append(args, like, like, like, like, like, like)
		n := len(args)
		where = append(where, "("+
			d.ilike("RequestId", fmt.Sprintf("$%d", n-5))+" OR "+
			d.ilike("Path", fmt.Sprintf("$%d", n-4))+" OR "+
			d.ilike("RequestBody", fmt.Sprintf("$%d", n-3))+" OR "+
			d.ilike("ResponseBody", fmt.Sprintf("$%d", n-2))+" OR "+
			d.ilike("Ip", fmt.Sprintf("$%d", n-1))+" OR "+
			d.ilike("UserAgent", fmt.Sprintf("$%d", n))+")")
	}
	return where, args
}

// RequestLogsFiltered returns a page of logs (newest first) together with the
// total number of matching rows.
func (s *Store) RequestLogsFiltered(f RequestLogFilter) ([]RequestLog, int, error) {
	offset := f.normalize()
	where, args := f.conditions(s.dialect)
	clause := strings.Join(where, " AND ")

	var total int
	if err := s.queryRow(`SELECT COUNT(*) FROM RequestLogs WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, f.PageSize, offset)
	query := fmt.Sprintf(`SELECT LogId, RequestId, Timestamp, Method, Path, Status, ResponseTime,
		RequestBody, ResponseBody, Headers, Ip, UserAgent, Stage
		FROM RequestLogs WHERE %s ORDER BY LogId DESC LIMIT $%d OFFSET $%d`,
		clause, len(args)-1, len(args))

	rows, err := s.query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, err := scanRequestLogs(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// RequestLogByID loads one full log row.
func (s *Store) RequestLogByID(id int64) (*RequestLog, error) {
	rows, err := s.query(`SELECT LogId, RequestId, Timestamp, Method, Path, Status, ResponseTime,
		RequestBody, ResponseBody, Headers, Ip, UserAgent, Stage
		FROM RequestLogs WHERE LogId = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	logs, err := scanRequestLogs(rows)
	if err != nil {
		return nil, err
	}
	if len(logs) == 0 {
		return nil, nil
	}
	return &logs[0], nil
}

// InsertRequestLog stores a log row and trims the table to maxLogs rows.
func (s *Store) InsertRequestLog(log RequestLog, maxLogs int) error {
	headers := marshalHeaders(log.Headers)
	if _, err := s.exec(`INSERT INTO RequestLogs
		(RequestId, Timestamp, Method, Path, Status, ResponseTime, RequestBody, ResponseBody, Headers, Ip, UserAgent, Stage)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		log.ID, log.Timestamp, log.Method, log.Path, log.Status, log.ResponseTime,
		log.RequestBody, log.ResponseBody, headers, log.IP, log.UserAgent, log.Stage); err != nil {
		return err
	}
	if maxLogs > 0 {
		_ = s.deleteBeyondNewest("RequestLogs", "LogId", maxLogs)
	}
	return nil
}

// InsertRequestLogs stores a batch of log rows inside one transaction and then
// trims the table to maxLogs rows. Used by the Redis flusher.
func (s *Store) InsertRequestLogs(logs []RequestLog, maxLogs int) error {
	if len(logs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, log := range logs {
		headers := marshalHeaders(log.Headers)
		if _, err := execOn(s.dialect, tx, `INSERT INTO RequestLogs
			(RequestId, Timestamp, Method, Path, Status, ResponseTime, RequestBody, ResponseBody, Headers, Ip, UserAgent, Stage)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			log.ID, log.Timestamp, log.Method, log.Path, log.Status,
			log.ResponseTime, log.RequestBody, log.ResponseBody, headers, log.IP, log.UserAgent, log.Stage); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if maxLogs > 0 {
		_ = s.deleteBeyondNewest("RequestLogs", "LogId", maxLogs)
	}
	return nil
}

// deleteBeyondNewest removes every row except the newest keep rows of table.
// MySQL forbids selecting from the table being deleted inside a subquery, so the
// selection is wrapped in a derived table there.
func (s *Store) deleteBeyondNewest(table, keyColumn string, keep int) error {
	if keep <= 0 {
		return nil
	}
	inner := fmt.Sprintf("SELECT %s FROM %s ORDER BY %s DESC LIMIT $1", keyColumn, table, keyColumn)
	if s.dialect.isMySQL() {
		inner = fmt.Sprintf("SELECT %s FROM (%s) AS t", keyColumn, inner)
	}
	_, err := s.exec(fmt.Sprintf("DELETE FROM %s WHERE %s NOT IN (%s)", table, keyColumn, inner), keep)
	return err
}

func marshalHeaders(headers map[string]string) *string {
	if headers == nil {
		return nil
	}
	data, err := json.Marshal(headers)
	if err != nil {
		return nil
	}
	h := string(data)
	return &h
}

// scanRequestLogs reads rows produced by the shared log SELECT projection.
func scanRequestLogs(rows *sql.Rows) ([]RequestLog, error) {
	out := []RequestLog{}
	for rows.Next() {
		var (
			log          RequestLog
			status       *int
			responseTime *int64
			headersRaw   *string
		)
		if err := rows.Scan(&log.RowID, &log.ID, &log.Timestamp, &log.Method, &log.Path, &status, &responseTime,
			&log.RequestBody, &log.ResponseBody, &headersRaw, &log.IP, &log.UserAgent, &log.Stage); err != nil {
			return nil, err
		}
		log.Status = status
		log.ResponseTime = responseTime
		if headersRaw != nil {
			_ = json.Unmarshal([]byte(*headersRaw), &log.Headers)
		}
		out = append(out, log)
	}
	return out, rows.Err()
}

// parseInt parses a decimal integer, returning an error on failure.
func parseInt(v string) (int, error) {
	var n int
	_, err := fmt.Sscanf(v, "%d", &n)
	return n, err
}

// ClearRequestLogs deletes every persisted log entry.
func (s *Store) ClearRequestLogs() error {
	_, err := s.exec(`DELETE FROM RequestLogs`)
	return err
}

// IncrementDailyRequestCount bumps today's /query counter.
func (s *Store) IncrementDailyRequestCount() error {
	return s.AddDailyRequestCount(time.Now().Format("2006-01-02"), 1)
}

// AddDailyRequestCount adds delta to a specific day, used by the Redis flusher.
func (s *Store) AddDailyRequestCount(day string, delta int64) error {
	if delta == 0 {
		return nil
	}
	_, err := s.exec(`INSERT INTO DailyRequestCounts (Day, Count) VALUES ($1, $2)`+
		s.dialect.upsertDailyCountSuffix(), day, delta)
	return err
}

// DailyRequestCount describes one day of query volume.
type DailyRequestCount struct {
	Day   string `json:"day"`
	Count int64  `json:"count"`
}

// DailyRequestCounts returns the last 365 days of counts, oldest first.
func (s *Store) DailyRequestCounts() ([]DailyRequestCount, error) {
	rows, err := s.query(`SELECT Day, Count FROM DailyRequestCounts
		WHERE Day >= ` + s.dialect.dailyWindowStartExpr() + ` ORDER BY Day ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DailyRequestCount{}
	for rows.Next() {
		var d DailyRequestCount
		if err := rows.Scan(&d.Day, &d.Count); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DataPath normalises a filesystem path for display.
func DataPath(p string) string {
	return strings.TrimSpace(p)
}
