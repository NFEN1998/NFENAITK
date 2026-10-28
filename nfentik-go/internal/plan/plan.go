// Package plan defines the subscription plans available to users and the
// helpers used to compute expiry and current status.
package plan

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

// Kind distinguishes the two families of plans.
type Kind string

const (
	KindDuration Kind = "duration"
	KindCount    Kind = "count"
)

// Plan is one selectable subscription option. Plans are stored in the Plans
// table after the first migration; the slice below is the seed catalogue.
type Plan struct {
	Code        string `json:"Code"`
	Label       string `json:"Label"`
	Kind        Kind   `json:"Kind"`
	Days        int    `json:"Days"`       // fixed duration days
	QuotaCount  int64  `json:"QuotaCount"` // fixed count quota
	Unlimited   bool   `json:"Unlimited"`  // duration plan without expiry
	Custom      bool   `json:"Custom"`     // amount supplied at assignment time
	ExpiryDays  int    `json:"ExpiryDays"` // default validity for count plans; 0 = no expiry
	Enabled     bool   `json:"Enabled"`
	Sort        int    `json:"Sort"`
	PriceCents  int64  `json:"PriceCents"`
	Currency    string `json:"Currency"`
	Description string `json:"Description"`
	Badge       string `json:"Badge"`
}

// Plans is the ordered catalogue used to seed a fresh database.
var Plans = []Plan{
	{Code: "monthly", Label: "包月", Kind: KindDuration, Days: 30, Enabled: true, Sort: 10, Currency: "CNY"},
	{Code: "quarterly", Label: "包季", Kind: KindDuration, Days: 90, Enabled: true, Sort: 20, Currency: "CNY"},
	{Code: "half_year", Label: "半年", Kind: KindDuration, Days: 180, Enabled: true, Sort: 30, Currency: "CNY"},
	{Code: "yearly", Label: "包年", Kind: KindDuration, Days: 365, Enabled: true, Sort: 40, Currency: "CNY"},
	{Code: "unlimited", Label: "无限", Kind: KindDuration, Unlimited: true, Enabled: true, Sort: 50, Currency: "CNY"},
	{Code: "duration_custom", Label: "自定义时长", Kind: KindDuration, Custom: true, Enabled: true, Sort: 60, Currency: "CNY"},
	{Code: "count", Label: "按次", Kind: KindCount, Custom: true, Enabled: true, Sort: 70, Currency: "CNY"},
}

var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

// ErrInvalidPlan reports a validation failure.
var ErrInvalidPlan = errors.New("套餐参数无效")

// Validate checks the catalogue fields shared by create and update.
func Validate(p Plan) error {
	if !codePattern.MatchString(strings.TrimSpace(p.Code)) {
		return errors.New("套餐代码需为 2-32 位小写字母、数字或下划线，并以字母开头")
	}
	if strings.TrimSpace(p.Label) == "" || len([]rune(p.Label)) > 64 {
		return errors.New("套餐名称需为 1-64 个字符")
	}
	if p.Kind != KindDuration && p.Kind != KindCount {
		return errors.New("套餐类型无效")
	}
	if p.PriceCents < 0 {
		return errors.New("套餐价格不能为负数")
	}
	if p.Sort < 0 {
		return errors.New("排序值不能为负数")
	}
	if p.ExpiryDays < 0 || p.ExpiryDays > 3650 {
		return errors.New("有效期天数需在 0 到 3650 之间")
	}
	switch p.Kind {
	case KindDuration:
		if p.Unlimited && p.Custom {
			return errors.New("无限时长套餐不能设置为自定义时长")
		}
		if !p.Unlimited && !p.Custom && (p.Days <= 0 || p.Days > 3650) {
			return errors.New("固定时长需在 1 到 3650 天之间")
		}
		if p.QuotaCount != 0 {
			return errors.New("时长套餐不能设置次数额度")
		}
	case KindCount:
		if p.Unlimited {
			return errors.New("次数套餐不能设置为无限")
		}
		if !p.Custom && (p.QuotaCount <= 0 || p.QuotaCount > 1000000000) {
			return errors.New("固定次数需在 1 到 1000000000 之间")
		}
		if p.Custom && p.QuotaCount != 0 {
			return errors.New("自定义次数套餐不能预设次数额度")
		}
		if p.Days != 0 {
			return errors.New("次数套餐不能设置固定时长")
		}
	}
	if strings.TrimSpace(p.Currency) == "" {
		p.Currency = "CNY"
	}
	if len([]rune(p.Currency)) > 8 || len([]rune(p.Description)) > 255 || len([]rune(p.Badge)) > 32 {
		return errors.New("套餐扩展字段过长")
	}
	return nil
}

// Lookup returns the seed plan with the given code. It is used for legacy
// labels and tests; live lookups go through the store.
func Lookup(code string) (Plan, bool) {
	for _, p := range Plans {
		if p.Code == code {
			return p, true
		}
	}
	return Plan{}, false
}

// ExpireAt computes the expiry time for a duration plan started at start.
// It returns nil for unlimited plans.
func ExpireAt(start time.Time, p Plan, customDays int) *time.Time {
	if p.Unlimited {
		return nil
	}
	days := p.Days
	if p.Custom {
		days = customDays
	}
	if days <= 0 {
		return nil
	}
	t := start.AddDate(0, 0, days)
	return &t
}

// Status values.
const (
	StatusActive    = "active"
	StatusExpiring  = "expiring"
	StatusExpired   = "expired"
	StatusExhausted = "exhausted"
	StatusDisabled  = "disabled"
)

// StatusInput carries the fields needed to classify a user.
type StatusInput struct {
	Enabled     bool
	Unlimited   bool
	ExpireAt    *time.Time
	RemainCount *int64 // nil for duration plans
	TotalCount  *int64 // nil for duration plans
}

// ExpiringDays is the threshold at which a duration plan is flagged expiring.
const ExpiringDays = 7

// Status classifies a user's plan at the given time.
func Status(in StatusInput, now time.Time) string {
	if !in.Enabled {
		return StatusDisabled
	}
	if in.ExpireAt != nil && now.After(*in.ExpireAt) {
		return StatusExpired
	}
	if in.RemainCount != nil {
		if *in.RemainCount <= 0 {
			return StatusExhausted
		}
		// Count plans with an optional expiry can still be expiring.
		if in.ExpireAt != nil && in.ExpireAt.Sub(now) <= ExpiringDays*24*time.Hour {
			return StatusExpiring
		}
		return StatusActive
	}
	if in.ExpireAt != nil && in.ExpireAt.Sub(now) <= ExpiringDays*24*time.Hour {
		return StatusExpiring
	}
	return StatusActive
}
