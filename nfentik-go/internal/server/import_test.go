package server

import "testing"
import "time"

func TestScheduleDue(t *testing.T) {
	now := time.Date(2026, 10, 13, 10, 30, 0, 0, time.Local)
	cases := []struct {
		name    string
		sched   mogoSchedule
		lastRun string
		want    bool
	}{
		{"manual never due", mogoSchedule{Type: "manual"}, "", false},
		{"interval never ran due", mogoSchedule{Type: "interval", EveryMinutes: 30}, "", true},
		{"interval not yet", mogoSchedule{Type: "interval", EveryMinutes: 30}, now.Add(-10 * time.Minute).Format(time.RFC3339), false},
		{"interval elapsed", mogoSchedule{Type: "interval", EveryMinutes: 30}, now.Add(-40 * time.Minute).Format(time.RFC3339), true},
		{"interval zero minutes", mogoSchedule{Type: "interval", EveryMinutes: 0}, now.Add(-time.Hour).Format(time.RFC3339), false},
		{"daily before time", mogoSchedule{Type: "daily", AtTime: "23:00"}, now.Add(-24 * time.Hour).Format(time.RFC3339), false},
		{"daily after time same day ran", mogoSchedule{Type: "daily", AtTime: "09:00"}, now.Add(-30 * time.Minute).Format(time.RFC3339), false},
		{"daily after time ran yesterday", mogoSchedule{Type: "daily", AtTime: "09:00"}, now.Add(-30 * time.Hour).Format(time.RFC3339), true},
		{"daily never ran", mogoSchedule{Type: "daily", AtTime: "09:00"}, "", true},
		{"daily bad time", mogoSchedule{Type: "daily", AtTime: "oops"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scheduleDue(tc.sched, tc.lastRun, now); got != tc.want {
				t.Fatalf("scheduleDue = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNormalizeSourceDefaults(t *testing.T) {
	src := mogoSource{Name: "x", BaseURL: "http://h", AKey: "k", Mode: "weird"}
	normalizeSource(&src)
	if src.Item != 50 {
		t.Fatalf("item = %d, want 50", src.Item)
	}
	if src.Mode != "incremental" {
		t.Fatalf("mode = %q, want incremental", src.Mode)
	}
	if src.Schedule.Type != "manual" {
		t.Fatalf("schedule = %q, want manual", src.Schedule.Type)
	}
	if src.Throttle.PageDelayMs != 1500 {
		t.Fatalf("pageDelayMs = %d, want 1500", src.Throttle.PageDelayMs)
	}
}
