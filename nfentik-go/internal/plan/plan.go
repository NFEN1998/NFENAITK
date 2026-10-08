// Package plan defines the subscription plans available to users and the
// helpers used to compute expiry and current status.
package plan

import "time"

// Kind distinguishes the two families of plans.
type Kind string

const (
	KindDuration Kind = "duration"
	KindCount    Kind = "count"
)

// Plan is one selectable subscription option.
type Plan struct {
	Code      string
	Label     string
	Kind      Kind
	Days      int  // duration plans: validity in days; 0 for unlimited/count
	Unlimited bool // duration plan without expiry or quota
	Custom    bool // amount supplied by the admin (days or total count)
}

// Plans is the ordered catalogue shown in the console.
var Plans = []Plan{
	{Code: "monthly", Label: "包月", Kind: KindDuration, Days: 30},
	{Code: "quarterly", Label: "包季", Kind: KindDuration, Days: 90},
	{Code: "half_year", Label: "半年", Kind: KindDuration, Days: 180},
	{Code: "yearly", Label: "包年", Kind: KindDuration, Days: 365},
	{Code: "unlimited", Label: "无限", Kind: KindDuration, Unlimited: true},
	{Code: "duration_custom", Label: "自定义时长", Kind: KindDuration, Custom: true},
	{Code: "count", Label: "按次", Kind: KindCount, Custom: true},
}

// Lookup returns the plan with the given code.
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
