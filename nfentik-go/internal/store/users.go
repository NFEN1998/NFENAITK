package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nfentik/nfentik-go/internal/plan"
)

// isUniqueViolation reports whether err is a PostgreSQL unique-constraint error.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// User is an end user that authenticates with a usertoken. The token is the
// only identity: there is no separate display name.
type User struct {
	ID          int64   `json:"id"`
	Token       string  `json:"token"`
	PlanType    string  `json:"plan_type"`  // duration | count
	PlanCode    string  `json:"plan_code"`  // monthly|quarterly|half_year|yearly|unlimited|duration_custom|count
	PlanLabel   string  `json:"plan_label"` // human readable plan name
	StartAt     string  `json:"start_at"`
	ExpireAt    *string `json:"expire_at"`    // nil = no expiry
	TotalCount  *int64  `json:"total_count"`  // count plans only
	UsedCount   int64   `json:"used_count"`   // count plans only
	RemainCount *int64  `json:"remain_count"` // count plans only
	Note        string  `json:"note"`
	Enabled     bool    `json:"enabled"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// UserRequestLog is one persisted query performed by a user.
type UserRequestLog struct {
	LogID        int64  `json:"log_id"`
	UserID       int64  `json:"user_id"`
	Timestamp    string `json:"timestamp"`
	Question     string `json:"question"`
	Options      string `json:"options"`
	Answer       string `json:"answer"`
	Source       string `json:"source"`
	Status       string `json:"status"`
	ResponseTime int64  `json:"response_time"`
}

// UserAuditLog records a token or plan change.
type UserAuditLog struct {
	AuditID   int64  `json:"audit_id"`
	Action    string `json:"action"`
	UserID    int64  `json:"user_id"`
	UserToken string `json:"user_token"`
	Detail    string `json:"detail"`
	Actor     string `json:"actor"`
	CreatedAt string `json:"created_at"`
}

// UserUsageRow is one entry of the per-user usage report.
type UserUsageRow struct {
	ID         int64  `json:"id"`
	Token      string `json:"token"`
	TotalCalls int64  `json:"total_calls"`
	Remain     *int64 `json:"remain"`
	Status     string `json:"status"`
}

// DailyCount is a day/count pair used in reports.
type DailyCount struct {
	Day   string `json:"day"`
	Count int64  `json:"count"`
}

// UsageReport is the aggregate usage report payload.
type UsageReport struct {
	Users      []UserUsageRow `json:"users"`
	Summary    UsageSummary   `json:"summary"`
	Daily      []DailyCount   `json:"daily"`
	WindowDays int            `json:"window_days"`
}

// UsageSummary is the top-level counters of a usage report.
type UsageSummary struct {
	TotalCalls     int64 `json:"total_calls"`
	ActiveUsers    int64 `json:"active_users"`
	ExpiredUsers   int64 `json:"expired_users"`
	ExhaustedUsers int64 `json:"exhausted_users"`
}

// ErrUserExists is returned when a user token is already taken.
var ErrUserExists = errors.New("用户令牌已存在")

// ErrUserNotFound is returned when no user matches the lookup.
var ErrUserNotFound = errors.New("用户不存在")

// NewUserToken generates a URL-safe random token.
func NewUserToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "nf_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

const userColumns = `Id, Token, PlanType, PlanCode, PlanLabel, StartAt, ExpireAt,
	TotalCount, UsedCount, RemainCount, Note, Enabled,
	to_char(CreatedAt, 'YYYY-MM-DD HH24:MI:SS'), to_char(UpdatedAt, 'YYYY-MM-DD HH24:MI:SS')`

func scanUser(scan func(dest ...any) error) (User, error) {
	var (
		u         User
		expireAt  sql.NullString
		total     sql.NullInt64
		remain    sql.NullInt64
		createdAt sql.NullString
		updatedAt sql.NullString
	)
	err := scan(&u.ID, &u.Token, &u.PlanType, &u.PlanCode, &u.PlanLabel,
		&u.StartAt, &expireAt, &total, &u.UsedCount, &remain, &u.Note, &u.Enabled,
		&createdAt, &updatedAt)
	if err != nil {
		return User{}, err
	}
	if expireAt.Valid {
		u.ExpireAt = &expireAt.String
	}
	if total.Valid {
		u.TotalCount = &total.Int64
	}
	if remain.Valid {
		u.RemainCount = &remain.Int64
	}
	if createdAt.Valid {
		u.CreatedAt = createdAt.String
	}
	if updatedAt.Valid {
		u.UpdatedAt = updatedAt.String
	}
	return u, nil
}

// CreateUser inserts a new user, generating a unique token when none is set.
func (s *Store) CreateUser(u User) (User, error) {
	if len([]rune(u.Note)) > 200 {
		return User{}, errors.New("备注过长")
	}
	id, token, err := s.insertUser(u)
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrUserExists
		}
		return User{}, err
	}
	u.ID = id
	u.Token = token
	return s.GetUserByID(id)
}

// insertUser writes a single user row and returns its id and token.
func (s *Store) insertUser(u User) (int64, string, error) {
	token := strings.TrimSpace(u.Token)
	if token == "" {
		var err error
		token, err = NewUserToken()
		if err != nil {
			return 0, "", err
		}
	}
	startAt := u.StartAt
	if startAt == "" {
		startAt = time.Now().Format("2006-01-02 15:04:05")
	}
	var id int64
	err := s.db.QueryRow(`INSERT INTO Users
		(Token, PlanType, PlanCode, PlanLabel, StartAt, ExpireAt, TotalCount, UsedCount, RemainCount, Note, Enabled, CreatedAt, UpdatedAt)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NOW(),NOW()) RETURNING Id`,
		token, u.PlanType, u.PlanCode, u.PlanLabel, startAt,
		u.ExpireAt, u.TotalCount, u.UsedCount, u.RemainCount, u.Note, u.Enabled).Scan(&id)
	if err != nil {
		return 0, "", err
	}
	return id, token, nil
}

// CreateUsers inserts up to n users with the same template in one transaction
// and returns the created rows. It is used by the batch-generation endpoint.
func (s *Store) CreateUsers(template User, n int) ([]User, error) {
	if n <= 0 {
		return nil, errors.New("生成数量必须大于 0")
	}
	if len([]rune(template.Note)) > 200 {
		return nil, errors.New("备注过长")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	created := make([]User, 0, n)
	for i := 0; i < n; i++ {
		token, err := NewUserToken()
		if err != nil {
			return nil, err
		}
		startAt := template.StartAt
		if startAt == "" {
			startAt = time.Now().Format("2006-01-02 15:04:05")
		}
		var id int64
		err = tx.QueryRow(`INSERT INTO Users
			(Token, PlanType, PlanCode, PlanLabel, StartAt, ExpireAt, TotalCount, UsedCount, RemainCount, Note, Enabled, CreatedAt, UpdatedAt)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NOW(),NOW()) RETURNING Id`,
			token, template.PlanType, template.PlanCode, template.PlanLabel, startAt,
			template.ExpireAt, template.TotalCount, template.UsedCount, template.RemainCount,
			template.Note, template.Enabled).Scan(&id)
		if err != nil {
			return nil, err
		}
		created = append(created, User{
			ID: id, Token: token, PlanType: template.PlanType, PlanCode: template.PlanCode,
			PlanLabel: template.PlanLabel, StartAt: startAt, ExpireAt: template.ExpireAt,
			TotalCount: template.TotalCount, UsedCount: template.UsedCount,
			RemainCount: template.RemainCount, Note: template.Note, Enabled: template.Enabled,
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return created, nil
}

// GetUserByToken loads a user by token.
func (s *Store) GetUserByToken(token string) (*User, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrUserNotFound
	}
	row := s.db.QueryRow(`SELECT `+userColumns+` FROM Users WHERE Token = $1`, token)
	u, err := scanUser(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUserByID loads a user by id.
func (s *Store) GetUserByID(id int64) (User, error) {
	row := s.db.QueryRow(`SELECT `+userColumns+` FROM Users WHERE Id = $1`, id)
	u, err := scanUser(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return u, err
}

// ListUsers returns all users ordered by id.
func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT ` + userColumns + ` FROM Users ORDER BY Id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateUser patches the mutable profile fields.
func (s *Store) UpdateUser(id int64, note *string, enabled *bool) error {
	sets := []string{}
	args := []any{}
	add := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if note != nil {
		if len([]rune(*note)) > 200 {
			return errors.New("备注过长")
		}
		add("Note", *note)
	}
	if enabled != nil {
		add("Enabled", *enabled)
	}
	if len(sets) == 0 {
		return nil
	}
	add("UpdatedAt", time.Now())
	args = append(args, id)
	_, err := s.db.Exec(`UPDATE Users SET `+strings.Join(sets, ", ")+fmt.Sprintf(" WHERE Id = $%d", len(args)), args...)
	return err
}

// DeleteUser removes a user and its logs.
func (s *Store) DeleteUser(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM UserRequestLogs WHERE UserId = $1`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM Users WHERE Id = $1`, id)
	return err
}

// ResetUserToken replaces a user's token and returns the new value.
func (s *Store) ResetUserToken(id int64) (string, error) {
	token, err := NewUserToken()
	if err != nil {
		return "", err
	}
	res, err := s.db.Exec(`UPDATE Users SET Token = $1, UpdatedAt = NOW() WHERE Id = $2`, token, id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrUserNotFound
	}
	return token, nil
}

// ConsumeQuota decrements the remaining count for count plans. It returns true
// when a decrement happened, false when the user has no count quota or is out.
func (s *Store) ConsumeQuota(id int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE Users
		SET RemainCount = RemainCount - 1, UsedCount = UsedCount + 1, UpdatedAt = NOW()
		WHERE Id = $1 AND RemainCount IS NOT NULL AND RemainCount > 0`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// IncrementUsedCount bumps the used count for duration and unlimited plans,
// which have no remaining quota to decrement. It is a no-op for count plans so
// the two paths never double count.
func (s *Store) IncrementUsedCount(id int64) error {
	_, err := s.db.Exec(`UPDATE Users
		SET UsedCount = UsedCount + 1, UpdatedAt = NOW()
		WHERE Id = $1 AND RemainCount IS NULL`, id)
	return err
}

// SetPlan updates a user's plan fields in one statement.
func (s *Store) SetPlan(id int64, planType, planCode, planLabel, startAt string, expireAt *string, total, remain *int64, used int64) error {
	_, err := s.db.Exec(`UPDATE Users
		SET PlanType=$1, PlanCode=$2, PlanLabel=$3, StartAt=$4, ExpireAt=$5,
		    TotalCount=$6, RemainCount=$7, UsedCount=$8, UpdatedAt=NOW()
		WHERE Id=$9`, planType, planCode, planLabel, startAt, expireAt, total, remain, used, id)
	return err
}

// AddCountTopUp adds count to a count plan, extending TotalCount as well.
func (s *Store) AddCountTopUp(id int64, delta int64) error {
	_, err := s.db.Exec(`UPDATE Users
		SET RemainCount = COALESCE(RemainCount,0) + $1,
		    TotalCount = COALESCE(TotalCount,0) + $1,
		    UpdatedAt = NOW()
		WHERE Id = $2`, delta, id)
	return err
}

// ExtendExpiry moves the expiry from base by the given number of days.
// expireAt is stored as text; callers pass an already formatted value.
func (s *Store) SetExpiry(id int64, expireAt *string) error {
	_, err := s.db.Exec(`UPDATE Users SET ExpireAt = $1, UpdatedAt = NOW() WHERE Id = $2`, expireAt, id)
	return err
}

// RenewUser applies a renewal to an existing user. Duration plans extend from
// the current expiry (or now when already expired); count plans add the new
// total, and an optional expiry wins when the plan carries one.
func (s *Store) RenewUser(id int64, p plan.Plan, customDays int, totalCount int64, enforceExpiry bool, expiryDays int) (User, error) {
	u, err := s.GetUserByID(id)
	if err != nil {
		return User{}, err
	}
	now := time.Now()
	switch p.Kind {
	case plan.KindCount:
		u.PlanType = string(plan.KindCount)
		u.PlanCode = p.Code
		u.PlanLabel = p.Label
		if u.TotalCount == nil {
			u.TotalCount = int64ptr(0)
		}
		if u.RemainCount == nil {
			u.RemainCount = int64ptr(0)
		}
		*u.TotalCount += totalCount
		*u.RemainCount += totalCount
		if enforceExpiry && expiryDays > 0 {
			base := now
			if u.ExpireAt != nil {
				if t, err := parseTime(*u.ExpireAt); err == nil && t.After(now) {
					base = t
				}
			}
			exp := base.AddDate(0, 0, expiryDays).Format("2006-01-02 15:04:05")
			u.ExpireAt = &exp
		}
	default:
		base := now
		if u.ExpireAt != nil {
			if t, err := parseTime(*u.ExpireAt); err == nil && t.After(now) {
				base = t
			}
		}
		exp := plan.ExpireAt(base, p, customDays)
		u.PlanType = string(plan.KindDuration)
		u.PlanCode = p.Code
		u.PlanLabel = p.Label
		u.TotalCount = nil
		u.RemainCount = nil
		if exp != nil {
			v := exp.Format("2006-01-02 15:04:05")
			u.ExpireAt = &v
		} else {
			u.ExpireAt = nil
		}
	}
	if err := s.SetPlan(u.ID, u.PlanType, u.PlanCode, u.PlanLabel, u.StartAt,
		u.ExpireAt, u.TotalCount, u.RemainCount, u.UsedCount); err != nil {
		return User{}, err
	}
	return s.GetUserByID(id)
}

// SwitchPlan replaces a user's plan from now, discarding any old quota.
func (s *Store) SwitchPlan(id int64, p plan.Plan, customDays int, totalCount int64, enforceExpiry bool, expiryDays int) (User, error) {
	now := time.Now()
	u, err := s.GetUserByID(id)
	if err != nil {
		return User{}, err
	}
	u.StartAt = now.Format("2006-01-02 15:04:05")
	u.PlanCode = p.Code
	u.PlanLabel = p.Label
	u.UsedCount = 0
	switch p.Kind {
	case plan.KindCount:
		u.PlanType = string(plan.KindCount)
		u.TotalCount = int64ptr(totalCount)
		u.RemainCount = int64ptr(totalCount)
		if enforceExpiry && expiryDays > 0 {
			exp := now.AddDate(0, 0, expiryDays).Format("2006-01-02 15:04:05")
			u.ExpireAt = &exp
		} else {
			u.ExpireAt = nil
		}
	default:
		u.PlanType = string(plan.KindDuration)
		u.TotalCount = nil
		u.RemainCount = nil
		if exp := plan.ExpireAt(now, p, customDays); exp != nil {
			v := exp.Format("2006-01-02 15:04:05")
			u.ExpireAt = &v
		} else {
			u.ExpireAt = nil
		}
	}
	if err := s.SetPlan(u.ID, u.PlanType, u.PlanCode, u.PlanLabel, u.StartAt,
		u.ExpireAt, u.TotalCount, u.RemainCount, u.UsedCount); err != nil {
		return User{}, err
	}
	return s.GetUserByID(id)
}

// ---------------------------------------------------------------------------
// User request logs
// ---------------------------------------------------------------------------

// InsertUserLog appends one user query record.
func (s *Store) InsertUserLog(log UserRequestLog) error {
	_, err := s.db.Exec(`INSERT INTO UserRequestLogs (UserId, Token, Timestamp, Question, Options, Answer, Source, Status, ResponseTime)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		log.UserID, "", log.Timestamp, truncateRunes(log.Question, 500),
		truncateRunes(log.Options, 500), truncateRunes(log.Answer, 2000),
		log.Source, log.Status, log.ResponseTime)
	return err
}

// UserLogs returns a page of a user's request logs, newest first.
func (s *Store) UserLogs(userID int64, page, pageSize int) ([]UserRequestLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	var total int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM UserRequestLogs WHERE UserId = $1`, userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`SELECT LogId, UserId, Timestamp, COALESCE(Question,''), COALESCE(Options,''), COALESCE(Answer,''), COALESCE(Source,''), COALESCE(Status,''), COALESCE(ResponseTime,0)
		FROM UserRequestLogs WHERE UserId = $1 ORDER BY LogId DESC LIMIT $2 OFFSET $3`,
		userID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []UserRequestLog{}
	for rows.Next() {
		var l UserRequestLog
		if err := rows.Scan(&l.LogID, &l.UserID, &l.Timestamp, &l.Question, &l.Options, &l.Answer, &l.Source, &l.Status, &l.ResponseTime); err != nil {
			return nil, 0, err
		}
		out = append(out, l)
	}
	return out, total, rows.Err()
}

// PruneUserLogs enforces the three retention limits: per-user rows, age in
// days, and a global row cap.
func (s *Store) PruneUserLogs(perUserLimit, retentionDays, globalLimit int) error {
	if perUserLimit > 0 {
		if _, err := s.db.Exec(`DELETE FROM UserRequestLogs
			WHERE LogId IN (
				SELECT LogId FROM UserRequestLogs ul
				WHERE (SELECT COUNT(*) FROM UserRequestLogs x WHERE x.UserId = ul.UserId AND x.LogId >= ul.LogId) > $1
			)`, perUserLimit); err != nil {
			return err
		}
	}
	if retentionDays > 0 {
		if _, err := s.db.Exec(`DELETE FROM UserRequestLogs WHERE Timestamp < $1`,
			time.Now().AddDate(0, 0, -retentionDays).Format("2006-01-02 15:04:05")); err != nil {
			return err
		}
	}
	if globalLimit > 0 {
		if _, err := s.db.Exec(`DELETE FROM UserRequestLogs
			WHERE LogId IN (SELECT LogId FROM UserRequestLogs ORDER BY LogId DESC OFFSET $1)`, globalLimit); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Audit logs
// ---------------------------------------------------------------------------

// InsertAudit records a token/plan change.
func (s *Store) InsertAudit(action string, userID int64, userToken, detail, actor string) error {
	_, err := s.db.Exec(`INSERT INTO UserAuditLogs (Action, UserId, UserToken, Detail, Actor, CreatedAt)
		VALUES ($1,$2,$3,$4,$5,NOW())`, action, userID, userToken, detail, actor)
	return err
}

// ListAudits returns a page of audit entries, newest first.
func (s *Store) ListAudits(page, pageSize int) ([]UserAuditLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	var total int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM UserAuditLogs`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`SELECT AuditId, Action, COALESCE(UserId,0), UserToken, Detail, Actor,
		to_char(CreatedAt, 'YYYY-MM-DD HH24:MI:SS')
		FROM UserAuditLogs ORDER BY AuditId DESC LIMIT $1 OFFSET $2`, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []UserAuditLog{}
	for rows.Next() {
		var a UserAuditLog
		if err := rows.Scan(&a.AuditID, &a.Action, &a.UserID, &a.UserToken, &a.Detail, &a.Actor, &a.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// PruneAudits removes audit entries older than the retention window.
func (s *Store) PruneAudits(retentionDays int) error {
	if retentionDays <= 0 {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM UserAuditLogs WHERE CreatedAt < $1`,
		time.Now().AddDate(0, 0, -retentionDays))
	return err
}

// ---------------------------------------------------------------------------
// Usage report
// ---------------------------------------------------------------------------

// UsageReport aggregates calls per user and per day over the given window.
func (s *Store) UsageReport(windowDays int) (UsageReport, error) {
	if windowDays <= 0 {
		windowDays = 14
	}
	since := time.Now().AddDate(0, 0, -windowDays).Format("2006-01-02 15:04:05")
	report := UsageReport{WindowDays: windowDays, Users: []UserUsageRow{}, Daily: []DailyCount{}}

	users, err := s.ListUsers()
	if err != nil {
		return report, err
	}
	// Per-user call counts in the window.
	rows, err := s.db.Query(`SELECT UserId, COUNT(*) FROM UserRequestLogs WHERE Timestamp >= $1 GROUP BY UserId`, since)
	if err != nil {
		return report, err
	}
	counts := map[int64]int64{}
	for rows.Next() {
		var uid, c int64
		if err := rows.Scan(&uid, &c); err != nil {
			rows.Close()
			return report, err
		}
		counts[uid] = c
	}
	rows.Close()

	now := time.Now()
	for _, u := range users {
		row := UserUsageRow{ID: u.ID, Token: u.Token, TotalCalls: counts[u.ID], Remain: u.RemainCount}
		row.Status = userStatus(u, now)
		report.Users = append(report.Users, row)
		report.Summary.TotalCalls += row.TotalCalls
		switch row.Status {
		case plan.StatusActive, plan.StatusExpiring:
			report.Summary.ActiveUsers++
		case plan.StatusExpired:
			report.Summary.ExpiredUsers++
		case plan.StatusExhausted:
			report.Summary.ExhaustedUsers++
		}
	}

	daily, err := s.db.Query(`SELECT substring(Timestamp,1,10) AS day, COUNT(*) FROM UserRequestLogs
		WHERE Timestamp >= $1 GROUP BY day ORDER BY day`, since)
	if err != nil {
		return report, err
	}
	defer daily.Close()
	for daily.Next() {
		var d DailyCount
		if err := daily.Scan(&d.Day, &d.Count); err != nil {
			return report, err
		}
		report.Daily = append(report.Daily, d)
	}
	return report, daily.Err()
}

// userStatus classifies a user via the plan package.
func userStatus(u User, now time.Time) string {
	in := plan.StatusInput{Enabled: u.Enabled, RemainCount: u.RemainCount, TotalCount: u.TotalCount}
	if u.ExpireAt != nil {
		if t, err := parseTime(*u.ExpireAt); err == nil {
			in.ExpireAt = &t
		}
	}
	return plan.Status(in, now)
}

func parseTime(v string) (time.Time, error) {
	layouts := []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00", "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间: %s", v)
}

func int64ptr(v int64) *int64 { return &v }

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
