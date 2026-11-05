package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/nfentik/nfentik-go/internal/plan"
)

// Plan store errors.
var (
	ErrPlanNotFound = errors.New("套餐不存在")
	ErrPlanExists   = errors.New("套餐代码已存在")
	ErrPlanInUse    = errors.New("套餐正在被用户或卡密使用，请改为停用")
)

// PlanView is a catalogue row together with its references. The embedded Plan
// keeps the legacy Code/Label/Kind JSON keys used by the console.
type PlanView struct {
	plan.Plan
	UserCount  int64 `json:"UserCount"`
	BatchCount int64 `json:"BatchCount"`
}

const planColumns = `Code, Label, Kind, Days, QuotaCount, Unlimited, Custom, ExpiryDays,
	Enabled, Sort, PriceCents, Currency, Description, Badge`

func scanPlan(scan func(dest ...any) error) (plan.Plan, error) {
	var p plan.Plan
	err := scan(&p.Code, &p.Label, &p.Kind, &p.Days, &p.QuotaCount, &p.Unlimited,
		&p.Custom, &p.ExpiryDays, &p.Enabled, &p.Sort, &p.PriceCents,
		&p.Currency, &p.Description, &p.Badge)
	if err != nil {
		return plan.Plan{}, err
	}
	if strings.TrimSpace(p.Currency) == "" {
		p.Currency = "CNY"
	}
	return p, nil
}

// ListPlans returns the catalogue ordered by Sort then Code.
func (s *Store) ListPlans() ([]plan.Plan, error) {
	rows, err := s.query(`SELECT ` + planColumns + ` FROM Plans ORDER BY Sort, Code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []plan.Plan{}
	for rows.Next() {
		p, err := scanPlan(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListPlansWithUsage returns the catalogue with per-plan reference counts.
func (s *Store) ListPlansWithUsage() ([]PlanView, error) {
	query := `SELECT ` + planColumns + `,
		(SELECT COUNT(*) FROM UserEntitlements e WHERE e.PlanCode = Plans.Code),
		(SELECT COUNT(*) FROM RedemptionBatches b WHERE b.PlanCode = Plans.Code)
		FROM Plans ORDER BY Sort, Code`
	rows, err := s.query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlanView{}
	for rows.Next() {
		var view PlanView
		var p plan.Plan
		err := rows.Scan(&p.Code, &p.Label, &p.Kind, &p.Days, &p.QuotaCount, &p.Unlimited,
			&p.Custom, &p.ExpiryDays, &p.Enabled, &p.Sort, &p.PriceCents,
			&p.Currency, &p.Description, &p.Badge, &view.UserCount, &view.BatchCount)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(p.Currency) == "" {
			p.Currency = "CNY"
		}
		view.Plan = p
		out = append(out, view)
	}
	return out, rows.Err()
}

// GetPlan loads one plan by code. Disabled plans are returned as well so
// already-issued cards can still be redeemed.
func (s *Store) GetPlan(code string) (plan.Plan, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return plan.Plan{}, ErrPlanNotFound
	}
	row := s.queryRow(`SELECT `+planColumns+` FROM Plans WHERE Code = $1`, code)
	p, err := scanPlan(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return plan.Plan{}, ErrPlanNotFound
	}
	return p, err
}

// CreatePlan inserts one catalogue row.
func (s *Store) CreatePlan(p plan.Plan) (plan.Plan, error) {
	p.Code = strings.TrimSpace(p.Code)
	p.Label = strings.TrimSpace(p.Label)
	p.Kind = plan.Kind(strings.ToLower(strings.TrimSpace(string(p.Kind))))
	if strings.TrimSpace(p.Currency) == "" {
		p.Currency = "CNY"
	}
	if err := plan.Validate(p); err != nil {
		return plan.Plan{}, err
	}
	if _, err := s.GetPlan(p.Code); err == nil {
		return plan.Plan{}, ErrPlanExists
	} else if !errors.Is(err, ErrPlanNotFound) {
		return plan.Plan{}, err
	}
	_, err := s.exec(`INSERT INTO Plans
		(Code, Label, Kind, Days, QuotaCount, Unlimited, Custom, ExpiryDays, Enabled, Sort,
		 PriceCents, Currency, Description, Badge, CreatedAt, UpdatedAt)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,NOW(),NOW())`,
		p.Code, p.Label, p.Kind, p.Days, p.QuotaCount, p.Unlimited, p.Custom, p.ExpiryDays,
		p.Enabled, p.Sort, p.PriceCents, p.Currency, p.Description, p.Badge)
	if err != nil {
		if isUniqueViolation(err) {
			return plan.Plan{}, ErrPlanExists
		}
		return plan.Plan{}, err
	}
	return s.GetPlan(p.Code)
}

// UpdatePlan changes the editable fields. Code and Kind are immutable.
func (s *Store) UpdatePlan(p plan.Plan) (plan.Plan, error) {
	existing, err := s.GetPlan(p.Code)
	if err != nil {
		return plan.Plan{}, err
	}
	if existing.Kind != p.Kind {
		return plan.Plan{}, errors.New("套餐类型创建后不可修改")
	}
	p.Label = strings.TrimSpace(p.Label)
	if strings.TrimSpace(p.Currency) == "" {
		p.Currency = "CNY"
	}
	if err := plan.Validate(p); err != nil {
		return plan.Plan{}, err
	}
	_, err = s.exec(`UPDATE Plans SET
		Label=$1, Days=$2, QuotaCount=$3, Unlimited=$4, Custom=$5, ExpiryDays=$6,
		Enabled=$7, Sort=$8, PriceCents=$9, Currency=$10, Description=$11, Badge=$12, UpdatedAt=NOW()
		WHERE Code=$13`,
		p.Label, p.Days, p.QuotaCount, p.Unlimited, p.Custom, p.ExpiryDays,
		p.Enabled, p.Sort, p.PriceCents, p.Currency, p.Description, p.Badge, p.Code)
	if err != nil {
		return plan.Plan{}, err
	}
	return s.GetPlan(p.Code)
}

// PlanUsage reports how many entitlements and redemptions reference a plan.
func (s *Store) PlanUsage(code string) (users, batches int64, err error) {
	err = s.queryRow(`SELECT
		(SELECT COUNT(*) FROM UserEntitlements WHERE PlanCode = $1),
		(SELECT COUNT(*) FROM RedemptionBatches WHERE PlanCode = $1)`, code).Scan(&users, &batches)
	return users, batches, err
}

// DeletePlan removes a plan only when nothing references it.
func (s *Store) DeletePlan(code string) error {
	users, batches, err := s.PlanUsage(code)
	if err != nil {
		return err
	}
	if users > 0 || batches > 0 {
		return ErrPlanInUse
	}
	res, err := s.exec(`DELETE FROM Plans WHERE Code = $1`, code)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrPlanNotFound
	}
	return nil
}

// SeedDefaultPlans inserts the built-in catalogue into an empty Plans table.
func (s *Store) SeedDefaultPlans() error {
	var count int64
	if err := s.queryRow(`SELECT COUNT(*) FROM Plans`).Scan(&count); err != nil {
		return fmt.Errorf("count plans: %w", err)
	}
	if count > 0 {
		return nil
	}
	for _, p := range plan.Plans {
		if strings.TrimSpace(p.Currency) == "" {
			p.Currency = "CNY"
		}
		if _, err := s.exec(`INSERT INTO Plans
			(Code, Label, Kind, Days, QuotaCount, Unlimited, Custom, ExpiryDays, Enabled, Sort,
			 PriceCents, Currency, Description, Badge, CreatedAt, UpdatedAt)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,NOW(),NOW())`,
			p.Code, p.Label, p.Kind, p.Days, p.QuotaCount, p.Unlimited, p.Custom, p.ExpiryDays,
			p.Enabled, p.Sort, p.PriceCents, p.Currency, p.Description, p.Badge); err != nil {
			return fmt.Errorf("seed plan %s: %w", p.Code, err)
		}
	}
	return nil
}
