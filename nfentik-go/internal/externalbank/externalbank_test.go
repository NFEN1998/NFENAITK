package externalbank

import (
	"encoding/json"
	"testing"
)

func TestParseEmptyDocument(t *testing.T) {
	s, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse(nil): %v", err)
	}
	if len(s.Sources) != 0 {
		t.Fatalf("sources = %d, want 0", len(s.Sources))
	}
}

func TestParseInvalidJSON(t *testing.T) {
	if _, err := Parse(json.RawMessage("{not json")); err == nil {
		t.Fatalf("expected an error for invalid JSON")
	}
}

func TestParseAppliesDefaultTimeout(t *testing.T) {
	raw := json.RawMessage(`{"sources":[{"id":"a","url":"https://x.example/q"}]}`)
	s, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := s.Sources[0].TimeoutSeconds; got != DefaultTimeoutSeconds {
		t.Fatalf("timeout = %d, want %d", got, DefaultTimeoutSeconds)
	}
}

func TestEnabledFiltersAndSorts(t *testing.T) {
	raw := json.RawMessage(`{"sources":[
		{"id":"low","name":"low","enabled":true,"priority":30,"url":"https://low.example/q"},
		{"id":"high","name":"high","enabled":true,"priority":10,"url":"https://high.example/q"},
		{"id":"off","name":"off","enabled":false,"priority":1,"url":"https://off.example/q"},
		{"id":"bad","name":"bad","enabled":true,"priority":2,"url":"ftp://bad.example/q"},
		{"id":"nourl","name":"nourl","enabled":true,"priority":3,"url":""}
	]}`)
	s, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := s.Enabled()
	if len(got) != 2 {
		t.Fatalf("enabled = %d, want 2 (%+v)", len(got), got)
	}
	if got[0].ID != "high" || got[1].ID != "low" {
		t.Fatalf("order = %s,%s; want high,low", got[0].ID, got[1].ID)
	}
}

func TestValidURL(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"https://api.example.com/search?q={question}", true},
		{"http://127.0.0.1:8080/query", true},
		{"ftp://api.example.com", false},
		{"api.example.com/query", false},
		{"", false},
	}
	for _, c := range cases {
		if got := ValidURL(c.raw); got != c.want {
			t.Errorf("ValidURL(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}
