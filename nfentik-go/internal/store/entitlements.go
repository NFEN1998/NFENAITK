package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nfentik/nfentik-go/internal/plan"
)

// Entitlement is one of a user's independent plan balances. A user can hold a
// duration entitlement and a count entitlement at the same time.
type Entitlement struct {
	UserID      int64   `json:"user_id"`
	Kind        string  `json:"kind"`
	PlanCode    string  `json:"plan_code"`
	PlanLabel   string  `json:"plan_label"`
	StartAt     string  `json:"start_at"`
	ExpireAt    *string `json:"expire_at,omitempty"`
	Unlimited   bool    `json:"unlimited"`
	TotalCount  *int64  `json:"total_count,omitempty"`
	UsedCount   int64   `json:"used_count"`
	RemainCount *int64  `json:"remain_count,omitempty"`
	Status      string  `json:"status,omitempty"`
	UpdatedAt   string  `json:"updated_at,omitempty"`
}

const entitlementColumns = `UserId, Kind, PlanCode, PlanLabel, StartAt, ExpireAt, Unlimited,
	TotalCount, UsedCount, RemainCount, UpdatedAt`

func scanEntitlement(scan func(dest ...any) error) (Entitlement, error) {
	var (
		e         Entitlement
		expireAt  sql.NullString
		total     sql.NullInt64
		remain    sql.NullInt64
		updatedAt sql.NullString
	)
	if err := scan(&e.UserID, &e.Kind, &e.PlanCode, &e.PlanLabel, &e.StartAt,
		&expireAt, &e.Unlimited, &total, &e.UsedCount, &remain, &updatedAt); err != nil {
		return Entitlement{}, err
	}
	if expireAt.Valid {
		e.ExpireAt = &expireAt.String
	}
	if total.Valid {
		e.TotalCount = &total.Int64
	}
	if remain.Valid {
		e.RemainCount = &remain.Int64
	}
	if updatedAt.Valid {
		e.UpdatedAt = updatedAt.String
	}
	return e, nil
}

func (s *Store) entitlementSelect() string {
	return `SELECT ` + entitlementColumns + ` FROM UserEntitlements`
}

func (s *Store) loadEntitlements(userID int64) ([]Entitlement, error) {
	rows, err := s.query(s.entitlementSelect()+` WHERE UserId = $1 ORDER BY CASE Kind WHEN 'duration' THEN 0 ELSE 1 END`, userID)
	if err != nil {
		return nil, err
	}
	return scanEntitlements(rows)
}

func (s *Store) loadEntitlementsTx(tx *sql.Tx, userID int64) ([]Entitlement, error) {
	rows, err := queryOn(s.dialect, tx, s.entitlementSelect()+` WHERE UserId = $1 ORDER BY CASE Kind WHEN 'duration' THEN 0 ELSE 1 END`, userID)
	if err != nil {
		return nil, err
	}
	return scanEntitlements(rows)
}

func scanEntitlements(rows *sql.Rows) ([]Entitlement, error) {
	defer rows.Close()
	out := []Entitlement{}
	for rows.Next() {
		e, err := scanEntitlement(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetEntitlements returns a user's duration and count balances.
func (s *Store) GetEntitlements(userID int64) ([]Entitlement, error) {
	return s.loadEntitlements(userID)
}

func findEntitlement(ents []Entitlement, kind string) *Entitlement {
	for i := range ents {
		if ents[i].Kind == kind {
			return &ents[i]
		}
	}
	return nil
}

func entitlementActive(e Entitlement, now time.Time) bool {
	if e.Kind == string(plan.KindDuration) {
		if e.Unlimited || e.ExpireAt == nil {
			return true
		}
		t, err := parseTime(*e.ExpireAt)
		return err == nil && t.After(now)
	}
	if e.RemainCount != nil && *e.RemainCount > 0 {
		if e.ExpireAt == nil {
			return true
		}
		t, err := parseTime(*e.ExpireAt)
		return err == nil && t.After(now)
	}
	return false
}

func primaryEntitlement(ents []Entitlement, now time.Time) *Entitlement {
	duration := findEntitlement(ents, string(plan.KindDuration))
	count := findEntitlement(ents, string(plan.KindCount))
	if duration != nil && entitlementActive(*duration, now) {
		return duration
	}
	if count != nil && entitlementActive(*count, now) {
		return count
	}
	if duration != nil {
		return duration
	}
	return count
}

// attachEntitlements loads balances and mirrors the primary one into the legacy
// User fields for older pages and API consumers.
func (s *Store) attachEntitlements(u *User) error {
	ents, err := s.loadEntitlements(u.ID)
	if err != nil {
		return err
	}
	now := time.Now()
	for i := range ents {
		ents[i].Status = entitlementStatus(ents[i], now)
	}
	u.Entitlements = ents
	applyPrimaryToUser(u, primaryEntitlement(ents, now))
	return nil
}

func entitlementStatus(e Entitlement, now time.Time) string {
	in := plan.StatusInput{Enabled: true, Unlimited: e.Unlimited}
	if e.ExpireAt != nil {
		if t, err := parseTime(*e.ExpireAt); err == nil {
			in.ExpireAt = &t
		}
	}
	if e.Kind == string(plan.KindCount) {
		remain := e.RemainCount
		if remain == nil {
			zero := int64(0)
			remain = &zero
		}
		in.RemainCount = remain
		in.TotalCount = e.TotalCount
	}
	return plan.Status(in, now)
}

func applyPrimaryToUser(u *User, e *Entitlement) {
	if e == nil {
		return
	}
	u.PlanType = e.Kind
	u.PlanCode = e.PlanCode
	u.PlanLabel = e.PlanLabel
	u.StartAt = e.StartAt
	u.ExpireAt = e.ExpireAt
	u.TotalCount = e.TotalCount
	u.UsedCount = e.UsedCount
	u.RemainCount = e.RemainCount
}

// insertEntitlementTx inserts or updates one balance inside a transaction.
func (s *Store) insertEntitlementTx(tx *sql.Tx, e Entitlement) error {
	res, err := execOn(s.dialect, tx, `UPDATE UserEntitlements SET
		PlanCode=$3, PlanLabel=$4, StartAt=$5, ExpireAt=$6, Unlimited=$7,
		TotalCount=$8, UsedCount=$9, RemainCount=$10, UpdatedAt=NOW()
		WHERE UserId=$1 AND Kind=$2`,
		e.UserID, e.Kind, e.PlanCode, e.PlanLabel, e.StartAt, e.ExpireAt, e.Unlimited,
		e.TotalCount, e.UsedCount, e.RemainCount)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	_, err = execOn(s.dialect, tx, `INSERT INTO UserEntitlements
		(UserId, Kind, PlanCode, PlanLabel, StartAt, ExpireAt, Unlimited, TotalCount, UsedCount, RemainCount, UpdatedAt)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NOW())`,
		e.UserID, e.Kind, e.PlanCode, e.PlanLabel, e.StartAt, e.ExpireAt, e.Unlimited,
		e.TotalCount, e.UsedCount, e.RemainCount)
	if isUniqueViolation(err) {
		_, err = execOn(s.dialect, tx, `UPDATE UserEntitlements SET
			PlanCode=$3, PlanLabel=$4, StartAt=$5, ExpireAt=$6, Unlimited=$7,
			TotalCount=$8, UsedCount=$9, RemainCount=$10, UpdatedAt=NOW()
			WHERE UserId=$1 AND Kind=$2`,
			e.UserID, e.Kind, e.PlanCode, e.PlanLabel, e.StartAt, e.ExpireAt, e.Unlimited,
			e.TotalCount, e.UsedCount, e.RemainCount)
	}
	return err
}

func (s *Store) syncPrimaryPlanTx(tx *sql.Tx, userID int64) error {
	ents, err := s.loadEntitlementsTx(tx, userID)
	if err != nil {
		return err
	}
	primary := primaryEntitlement(ents, time.Now())
	if primary == nil {
		return nil
	}
	_, err = execOn(s.dialect, tx, `UPDATE Users SET
		PlanType=$1, PlanCode=$2, PlanLabel=$3, StartAt=$4, ExpireAt=$5,
		TotalCount=$6, RemainCount=$7, UsedCount=$8, UpdatedAt=NOW()
		WHERE Id=$9`,
		primary.Kind, primary.PlanCode, primary.PlanLabel, primary.StartAt, primary.ExpireAt,
		primary.TotalCount, primary.RemainCount, primary.UsedCount, userID)
	return err
}

func (s *Store) syncPrimaryPlan(userID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.syncPrimaryPlanTx(tx, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// ConsumeCountEntitlement atomically takes one query from the count balance.
// The Users row lock serialises this decrement with redemption and renewal so
// a concurrent write cannot overwrite the new balance.
func (s *Store) ConsumeCountEntitlement(userID int64) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var locked int64
	if err := queryRowOn(s.dialect, tx, `SELECT Id FROM Users WHERE Id = $1 FOR UPDATE`, userID).Scan(&locked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrUserNotFound
		}
		return false, err
	}
	res, err := execOn(s.dialect, tx, `UPDATE UserEntitlements
		SET RemainCount = RemainCount - 1, UsedCount = UsedCount + 1, UpdatedAt = NOW()
		WHERE UserId = $1 AND Kind = 'count' AND RemainCount IS NOT NULL AND RemainCount > 0`, userID)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	if err := s.syncPrimaryPlanTx(tx, userID); err != nil {
		return true, err
	}
	if err := tx.Commit(); err != nil {
		return true, err
	}
	return true, nil
}

// RecordDurationUse increments the usage counter for an active duration plan.
func (s *Store) RecordDurationUse(userID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var locked int64
	if err := queryRowOn(s.dialect, tx, `SELECT Id FROM Users WHERE Id = $1 FOR UPDATE`, userID).Scan(&locked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		return err
	}
	if _, err := execOn(s.dialect, tx, `UPDATE UserEntitlements
		SET UsedCount = UsedCount + 1, UpdatedAt = NOW()
		WHERE UserId = $1 AND Kind = 'duration'`, userID); err != nil {
		return err
	}
	if err := s.syncPrimaryPlanTx(tx, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// insertEntitlementFromUserTx creates the initial balance for a new user.
func (s *Store) insertEntitlementFromUserTx(tx *sql.Tx, userID int64, u User) error {
	switch u.PlanType {
	case string(plan.KindDuration):
		e := Entitlement{
			UserID: userID, Kind: string(plan.KindDuration),
			PlanCode: u.PlanCode, PlanLabel: u.PlanLabel, StartAt: u.StartAt,
			ExpireAt: u.ExpireAt, Unlimited: u.ExpireAt == nil, UsedCount: u.UsedCount,
		}
		return s.insertEntitlementTx(tx, e)
	case string(plan.KindCount):
		e := Entitlement{
			UserID: userID, Kind: string(plan.KindCount),
			PlanCode: u.PlanCode, PlanLabel: u.PlanLabel, StartAt: u.StartAt,
			ExpireAt: u.ExpireAt, TotalCount: u.TotalCount,
			UsedCount: u.UsedCount, RemainCount: u.RemainCount,
		}
		return s.insertEntitlementTx(tx, e)
	}
	return nil
}

// BackfillEntitlements creates balances for users that predate the table. It is
// idempotent and runs on every startup.
func (s *Store) BackfillEntitlements() error {
	if s.dialect.isMySQL() {
		_, err := s.exec(`INSERT IGNORE INTO UserEntitlements
			(UserId, Kind, PlanCode, PlanLabel, StartAt, ExpireAt, Unlimited, TotalCount, UsedCount, RemainCount, UpdatedAt)
			SELECT Id, PlanType, PlanCode, PlanLabel, StartAt, ExpireAt,
				CASE WHEN PlanType='duration' AND ExpireAt IS NULL THEN 1 ELSE 0 END,
				CASE WHEN PlanType='count' THEN COALESCE(TotalCount,0) ELSE NULL END,
				COALESCE(UsedCount,0),
				CASE WHEN PlanType='count' THEN COALESCE(RemainCount,0) ELSE NULL END,
				NOW()
			FROM Users WHERE PlanType IN ('duration','count')`)
		if err != nil {
			return fmt.Errorf("backfill entitlements: %w", err)
		}
		return nil
	}
	_, err := s.exec(`INSERT INTO UserEntitlements
		(UserId, Kind, PlanCode, PlanLabel, StartAt, ExpireAt, Unlimited, TotalCount, UsedCount, RemainCount, UpdatedAt)
		SELECT Id, PlanType, PlanCode, PlanLabel, StartAt, ExpireAt,
			CASE WHEN PlanType='duration' AND ExpireAt IS NULL THEN TRUE ELSE FALSE END,
			CASE WHEN PlanType='count' THEN COALESCE(TotalCount,0) ELSE NULL END,
			COALESCE(UsedCount,0),
			CASE WHEN PlanType='count' THEN COALESCE(RemainCount,0) ELSE NULL END,
			NOW()
		FROM Users WHERE PlanType IN ('duration','count')
		ON CONFLICT (UserId, Kind) DO NOTHING`)
	if err != nil {
		return fmt.Errorf("backfill entitlements: %w", err)
	}
	return nil
}

// GetEntitlement returns one balance, or nil when absent.
func (s *Store) GetEntitlement(userID int64, kind string) (*Entitlement, error) {
	ents, err := s.loadEntitlements(userID)
	if err != nil {
		return nil, err
	}
	return findEntitlement(ents, kind), nil
}

// PlanLabel resolves a label from the catalogue with a fallback.
func (s *Store) PlanLabel(code, fallback string) string {
	if p, err := s.GetPlan(strings.TrimSpace(code)); err == nil && strings.TrimSpace(p.Label) != "" {
		return p.Label
	}
	return fallback
}

// ErrEntitlementMissing reports a missing balance.
var ErrEntitlementMissing = errors.New("用户权益不存在")

func (s *Store) attachEntitlementsToUsers(users []User) error {
	if len(users) == 0 {
		return nil
	}
	ids := make([]any, 0, len(users))
	placeholders := make([]string, 0, len(users))
	for i := range users {
		ids = append(ids, users[i].ID)
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
	}
	rows, err := s.query(s.entitlementSelect()+` WHERE UserId IN (`+strings.Join(placeholders, ",")+`)`, ids...)
	if err != nil {
		return err
	}
	byUser, err := scanEntitlements(rows)
	if err != nil {
		return err
	}
	grouped := make(map[int64][]Entitlement, len(users))
	for _, e := range byUser {
		grouped[e.UserID] = append(grouped[e.UserID], e)
	}
	now := time.Now()
	for i := range users {
		ents := grouped[users[i].ID]
		for j := range ents {
			ents[j].Status = entitlementStatus(ents[j], now)
		}
		users[i].Entitlements = ents
		applyPrimaryToUser(&users[i], primaryEntitlement(ents, now))
	}
	return nil
}
