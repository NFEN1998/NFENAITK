package ai

import (
	"encoding/json"
	"strings"
)

// Model describes one configured model.
type Model struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	DisplayName    string  `json:"displayName"`
	PlatformID     string  `json:"platformId"`
	MaxTokens      int     `json:"maxTokens"`
	Temperature    float64 `json:"temperature"`
	TopP           float64 `json:"topP"`
	Enabled        bool    `json:"enabled"`
	Category       string  `json:"category"`
	Description    string  `json:"description"`
	EnableThinking bool    `json:"enableThinking"`
}

// Platform is an AI provider plus its models.
type Platform struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	DisplayName   string            `json:"displayName"`
	BaseURL       string            `json:"baseUrl"`
	APIKey        string            `json:"apiKey"`
	Enabled       bool              `json:"enabled"`
	Models        []Model           `json:"models"`
	CustomHeaders map[string]string `json:"customHeaders"`
}

// ModelSettings mirrors the frontend persisted selection.
type ModelSettings struct {
	SelectedTextModel     *string    `json:"selectedTextModel"`
	SelectedTextModels    []string   `json:"selectedTextModels"`
	SelectedSummaryModel  *string    `json:"selectedSummaryModel"`
	SelectedSummaryModels []string   `json:"selectedSummaryModels"`
	SelectedVisionModel   *string    `json:"selectedVisionModel"`
	Platforms             []Platform `json:"platforms"`
}

// Resolve returns the model definition together with its owning platform.
func (m ModelSettings) Resolve(modelID string) (*Model, *Platform, bool) {
	for i := range m.Platforms {
		p := &m.Platforms[i]
		for j := range p.Models {
			if p.Models[j].ID == modelID {
				return &p.Models[j], p, true
			}
		}
	}
	return nil, nil, false
}

// TextModels returns the enabled selected text models in priority order.
func (m ModelSettings) TextModels() []Model {
	ids := m.SelectedTextModels
	if len(ids) == 0 && m.SelectedTextModel != nil {
		ids = []string{*m.SelectedTextModel}
	}
	return m.collect(ids, "text")
}

// SummaryModels returns the enabled selected summary models.
func (m ModelSettings) SummaryModels() []Model {
	ids := m.SelectedSummaryModels
	if len(ids) == 0 && m.SelectedSummaryModel != nil {
		ids = []string{*m.SelectedSummaryModel}
	}
	return m.collect(ids, "summary")
}

// VisionModel returns the enabled selected vision model.
func (m ModelSettings) VisionModel() (Model, *Platform, bool) {
	if m.SelectedVisionModel == nil {
		return Model{}, nil, false
	}
	model, platform, ok := m.Resolve(*m.SelectedVisionModel)
	if !ok || !model.Enabled {
		return Model{}, nil, false
	}
	return *model, platform, true
}

func (m ModelSettings) collect(ids []string, _ string) []Model {
	out := []Model{}
	for _, id := range ids {
		model, _, ok := m.Resolve(id)
		if ok && model.Enabled {
			out = append(out, *model)
		}
	}
	return out
}

// ParseModelSettings decodes the stored model_config.json document.
func ParseModelSettings(raw json.RawMessage) (ModelSettings, error) {
	var settings ModelSettings
	if len(raw) == 0 {
		return settings, nil
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return settings, err
	}
	if settings.SelectedTextModels == nil {
		settings.SelectedTextModels = []string{}
	}
	if settings.Platforms == nil {
		settings.Platforms = []Platform{}
	}
	for i := range settings.Platforms {
		for j := range settings.Platforms[i].Models {
			m := &settings.Platforms[i].Models[j]
			if m.MaxTokens == 0 {
				m.MaxTokens = 4096
			}
		}
	}
	return settings, nil
}

// ModelByCategory filters the configured models by category.
func (m ModelSettings) ModelByCategory(category string) []Model {
	out := []Model{}
	for _, p := range m.Platforms {
		for _, model := range p.Models {
			if !model.Enabled {
				continue
			}
			if category != "" && strings.EqualFold(model.Category, category) {
				out = append(out, model)
			}
		}
	}
	return out
}
