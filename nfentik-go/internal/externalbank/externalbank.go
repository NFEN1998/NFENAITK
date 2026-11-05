// Package externalbank adapts configurable third-party question bank HTTP
// endpoints into a uniform lookup used by the query chain. A Source describes
// how to turn a question into a request and how to read the answer back, so any
// JSON shaped endpoint can be plugged in without code changes.
package externalbank

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

// DefaultTimeoutSeconds bounds a single source lookup when the source does not
// specify its own timeout.
const DefaultTimeoutSeconds = 10

// Source is one third-party question bank endpoint configuration.
type Source struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Enabled        bool              `json:"enabled"`
	Priority       int               `json:"priority"`
	URL            string            `json:"url"`
	Method         string            `json:"method"` // GET | POST
	Headers        map[string]string `json:"headers"`
	QueryParams    map[string]string `json:"queryParams"`  // GET parameters, templated
	BodyTemplate   string            `json:"bodyTemplate"` // POST body, templated
	ContentType    string            `json:"contentType"`  // application/json | form
	AnswerPath     string            `json:"answerPath"`   // dot path, e.g. data.answer
	QuestionPath   string            `json:"questionPath"` // optional, for verification
	TimeoutSeconds int               `json:"timeoutSeconds"`
	Trust          bool              `json:"trust"` // skip similarity verification
	WriteBack      bool              `json:"writeBack"`
	CacheTTL       int               `json:"cacheTTL"` // seconds; 0 uses the global TTL
}

// Settings is the persisted external_banks configuration document.
type Settings struct {
	Sources []Source `json:"sources"`
}

// Query is the uniform input to a lookup.
type Query struct {
	Title   string
	Options *string
	Type    *string
}

// Result is the uniform output of a lookup.
type Result struct {
	Answer   string
	Question string
}

// Parse decodes the stored external_banks document and normalises defaults. A
// missing document is not an error; it yields an empty Settings.
func Parse(raw json.RawMessage) (Settings, error) {
	var s Settings
	if len(raw) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, err
	}
	if s.Sources == nil {
		s.Sources = []Source{}
	}
	for i := range s.Sources {
		if s.Sources[i].TimeoutSeconds <= 0 {
			s.Sources[i].TimeoutSeconds = DefaultTimeoutSeconds
		}
	}
	return s, nil
}

// Enabled returns the enabled sources that carry a valid http(s) URL, ordered by
// ascending Priority so callers invoke the highest priority source first.
func (s Settings) Enabled() []Source {
	out := make([]Source, 0, len(s.Sources))
	for _, src := range s.Sources {
		if !src.Enabled || !ValidURL(src.URL) {
			continue
		}
		out = append(out, src)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out
}

// ValidURL reports whether raw is an http or https URL with a host. Templated
// placeholders (for example {question}) are tolerated because they are resolved
// before the request is sent.
func ValidURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}
