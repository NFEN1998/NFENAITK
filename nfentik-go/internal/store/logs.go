package store

import (
	"encoding/json"
	"strings"
	"time"
)

// RequestLog is a persisted record of one HTTP request handled by the OCS API.
type RequestLog struct {
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

// InsertRequestLog stores a log row and trims the table to maxLogs rows.
func (s *Store) InsertRequestLog(log RequestLog, maxLogs int) error {
	headers := marshalHeaders(log.Headers)
	if _, err := s.db.Exec(`INSERT INTO RequestLogs
		(RequestId, Timestamp, Method, Path, Status, ResponseTime, RequestBody, ResponseBody, Headers, Ip, UserAgent, Stage)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		log.ID, log.Timestamp, log.Method, log.Path, log.Status, log.ResponseTime,
		log.RequestBody, log.ResponseBody, headers, log.IP, log.UserAgent, log.Stage); err != nil {
		return err
	}
	if maxLogs > 0 {
		_, _ = s.db.Exec(`DELETE FROM RequestLogs WHERE LogId NOT IN
			(SELECT LogId FROM RequestLogs ORDER BY LogId DESC LIMIT $1)`, maxLogs)
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
	stmt, err := tx.Prepare(`INSERT INTO RequestLogs
		(RequestId, Timestamp, Method, Path, Status, ResponseTime, RequestBody, ResponseBody, Headers, Ip, UserAgent, Stage)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, log := range logs {
		headers := marshalHeaders(log.Headers)
		if _, err := stmt.Exec(log.ID, log.Timestamp, log.Method, log.Path, log.Status,
			log.ResponseTime, log.RequestBody, log.ResponseBody, headers, log.IP, log.UserAgent, log.Stage); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if maxLogs > 0 {
		_, _ = s.db.Exec(`DELETE FROM RequestLogs WHERE LogId NOT IN
			(SELECT LogId FROM RequestLogs ORDER BY LogId DESC LIMIT $1)`, maxLogs)
	}
	return nil
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

// RequestLogs returns the newest logs first.
func (s *Store) RequestLogs(limit int) ([]RequestLog, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT RequestId, Timestamp, Method, Path, Status, ResponseTime,
		RequestBody, ResponseBody, Headers, Ip, UserAgent, Stage
		FROM RequestLogs ORDER BY LogId DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RequestLog{}
	for rows.Next() {
		var (
			log          RequestLog
			status       *int
			responseTime *int64
			headersRaw   *string
		)
		if err := rows.Scan(&log.ID, &log.Timestamp, &log.Method, &log.Path, &status, &responseTime,
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

// ClearRequestLogs deletes every persisted log entry.
func (s *Store) ClearRequestLogs() error {
	_, err := s.db.Exec(`DELETE FROM RequestLogs`)
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
	_, err := s.db.Exec(`INSERT INTO DailyRequestCounts (Day, Count) VALUES ($1, $2)
		ON CONFLICT (Day) DO UPDATE SET Count = DailyRequestCounts.Count + EXCLUDED.Count`, day, delta)
	return err
}

// DailyRequestCount describes one day of query volume.
type DailyRequestCount struct {
	Day   string `json:"day"`
	Count int64  `json:"count"`
}

// DailyRequestCounts returns the last 365 days of counts, oldest first.
func (s *Store) DailyRequestCounts() ([]DailyRequestCount, error) {
	rows, err := s.db.Query(`SELECT Day, Count FROM DailyRequestCounts
		WHERE Day >= to_char(NOW() - INTERVAL '364 days', 'YYYY-MM-DD') ORDER BY Day ASC`)
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
