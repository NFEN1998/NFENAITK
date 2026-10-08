// Package config separates bootstrapping from runtime configuration.
//
// Only the database and Redis connection strings live in a local file
// (config/config.json next to the executable). Everything else - application
// settings and AI model configuration - is stored in the database so a single
// deployment never loses configuration when the process restarts elsewhere.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Network holds the OCS server binding settings.
type Network struct {
	ServerPort    int    `json:"serverPort"`
	EnableLan     bool   `json:"enableLanAccess"`
	BindAddress   string `json:"bindAddress"`
	AutoStart     bool   `json:"autoStart"`
	DefaultFolder int64  `json:"defaultFolderId"`
}

// User is a multi-user token entry.
type User struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Token     string `json:"token"`
	CreatedAt string `json:"createdAt"`
}

// MultiUser enables token based access for shared deployments.
type MultiUser struct {
	Enabled bool   `json:"enabled"`
	Users   []User `json:"users"`
}

// Algorithm is a user supplied code snippet kept for compatibility.
type Algorithm struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	ApplicableType string `json:"applicableType"`
	Code           string `json:"code"`
}

// Database holds the PostgreSQL connection settings. DATABASE_URL always wins
// when it is set in the environment.
type Database struct {
	URL string `json:"url"`
}

// Redis holds the cache connection settings. REDIS_URL always wins when it is
// set in the environment.
type Redis struct {
	URL            string `json:"url"`
	Enabled        bool   `json:"enabled"`
	Prefix         string `json:"prefix"`
	QueryCacheTTL  int    `json:"queryCacheTTL"`
	HistoryEnabled bool   `json:"historyEnabled"`
}

// Settings mirrors the frontend AppSettings structure.
type Settings struct {
	SiteName               string      `json:"siteName"`
	SiteSubtitle           string      `json:"siteSubtitle"`
	Theme                  string      `json:"theme"`
	Language               string      `json:"language"`
	AutoSave               bool        `json:"autoSave"`
	AutoAddToQuestionBank  bool        `json:"autoAddToQuestionBank"`
	ModelResponseTimeout   int         `json:"modelResponseTimeout"`
	ModelHeartbeatTimeout  int         `json:"modelHeartbeatTimeout"`
	DefaultDifficulty      string      `json:"defaultDifficulty"`
	ItemsPerPage           int         `json:"itemsPerPage"`
	ShowExplanation        bool        `json:"showExplanation"`
	AutoUpdate             bool        `json:"autoUpdate"`
	EnableNotifications    bool        `json:"enableNotifications"`
	SuppressNoModelWarning bool        `json:"suppressNoModelWarning"`
	QuestionSaveDir        string      `json:"questionSaveDir"`
	QuestionSaveFolderID   *int64      `json:"questionSaveFolderId"`
	Algorithms             []Algorithm `json:"algorithms"`
	AdminToken             string      `json:"adminToken"`
	MultiUser              MultiUser   `json:"multiUser"`
	Network                Network     `json:"network"`
	Database               Database    `json:"database"`
	Redis                  Redis       `json:"redis"`

	// User token and plan system.
	UsersEnabled           bool   `json:"usersEnabled"`
	RequireTokenForQuery   bool   `json:"requireTokenForQuery"`
	UserLogRetentionDays   int    `json:"userLogRetentionDays"`
	UserLogPerUserLimit    int    `json:"userLogPerUserLimit"`
	UserLogGlobalLimit     int    `json:"userLogGlobalLimit"`
	UserAuditRetentionDays int    `json:"userAuditRetentionDays"`
	DefaultPlanCode        string `json:"defaultPlanCode"`
}

// Default site branding shown on the landing page, console and user portal.
const (
	DefaultSiteName     = "nfentik"
	DefaultSiteSubtitle = "智能题库查询服务"
)

// DefaultSettings returns the baseline configuration used on first start.
func DefaultSettings() Settings {
	return Settings{
		SiteName:              DefaultSiteName,
		SiteSubtitle:          DefaultSiteSubtitle,
		Theme:                 "light",
		Language:              "zh-CN",
		AutoSave:              true,
		AutoAddToQuestionBank: false,
		ModelResponseTimeout:  60,
		ModelHeartbeatTimeout: 6,
		DefaultDifficulty:     "medium",
		ItemsPerPage:          20,
		ShowExplanation:       true,
		AutoUpdate:            false,
		EnableNotifications:   true,
		Algorithms:            []Algorithm{},
		AdminToken:            "",
		MultiUser:             MultiUser{Enabled: false, Users: []User{}},
		Network: Network{
			ServerPort:  3000,
			EnableLan:   false,
			BindAddress: "0.0.0.0",
			AutoStart:   true,
		},
		Database: Database{URL: ""},
		Redis: Redis{
			URL:            "",
			Enabled:        false,
			Prefix:         "nfentik",
			QueryCacheTTL:  600,
			HistoryEnabled: true,
		},
		UsersEnabled:           true,
		RequireTokenForQuery:   false,
		UserLogRetentionDays:   30,
		UserLogPerUserLimit:    200,
		UserLogGlobalLimit:     50000,
		UserAuditRetentionDays: 90,
		DefaultPlanCode:        "",
	}
}

// ---------------------------------------------------------------------------
// Bootstrap
// ---------------------------------------------------------------------------

// BootstrapFile is the local bootstrap document. It only carries the connection
// strings needed to reach the database and cache before settings are loaded.
type BootstrapFile struct {
	Database Database `json:"database"`
	Redis    Redis    `json:"redis"`
}

// ConfigDir returns the directory that holds the bootstrap config file. It is
// the "config" folder next to the running executable.
func ConfigDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "config"
	}
	return filepath.Join(filepath.Dir(exe), "config")
}

// BootstrapPath returns the full path of the bootstrap config file.
func BootstrapPath() string { return filepath.Join(ConfigDir(), "config.json") }

// DataDir returns the directory used for any residual local files. It is the
// parent of the config directory (the program folder).
func DataDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// LoadBootstrap reads the local bootstrap file and merges environment overrides.
// A missing file is not an error: the environment may provide everything.
func LoadBootstrap() (BootstrapFile, error) {
	var b BootstrapFile
	if data, err := os.ReadFile(BootstrapPath()); err == nil {
		if err := json.Unmarshal(data, &b); err != nil {
			return b, err
		}
	} else if !os.IsNotExist(err) {
		return b, err
	}

	// Environment variables always take precedence.
	if v := os.Getenv("DATABASE_URL"); v != "" {
		b.Database.URL = v
	}
	if v := os.Getenv("REDIS_URL"); v != "" {
		b.Redis.URL = v
		b.Redis.Enabled = true
	}
	if b.Redis.Prefix == "" {
		b.Redis.Prefix = "nfentik"
	}
	if b.Redis.QueryCacheTTL <= 0 {
		b.Redis.QueryCacheTTL = 600
	}
	return b, nil
}

// WriteBootstrap persists the bootstrap document so first-run connection
// settings survive the next start.
func WriteBootstrap(b BootstrapFile) error {
	if err := os.MkdirAll(ConfigDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(BootstrapPath(), data, 0o600)
}

// ---------------------------------------------------------------------------
// Store (database backed)
// ---------------------------------------------------------------------------

// SettingsRepo is the subset of the storage layer the config store needs.
type SettingsRepo interface {
	GetSetting(key string) (json.RawMessage, bool, error)
	SetSetting(key string, value json.RawMessage) error
	DeleteSetting(key string) error
}

// Store provides thread safe access to the settings and model configuration
// that live in the database.
type Store struct {
	repo     SettingsRepo
	mu       sync.RWMutex
	settings Settings
}

// New loads the configuration from the repository, seeding defaults on first
// run. bootstrap supplies the connection strings that must also be reflected in
// the settings document for display in the console.
func New(repo SettingsRepo, bootstrap BootstrapFile) (*Store, error) {
	s := &Store{repo: repo, settings: DefaultSettings()}
	if err := s.load(bootstrap); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load(bootstrap BootstrapFile) error {
	raw, found, err := s.repo.GetSetting("app_settings")
	if err != nil {
		return err
	}
	settings := DefaultSettings()
	if found && len(raw) > 0 {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return err
		}
	}
	// Connection strings are owned by the bootstrap file / environment.
	settings.Database = bootstrap.Database
	settings.Redis = bootstrap.Redis
	normalize(&settings)
	s.settings = settings
	if !found {
		return s.saveLocked()
	}
	return nil
}

func normalize(settings *Settings) {
	if strings.TrimSpace(settings.SiteName) == "" {
		settings.SiteName = DefaultSiteName
	}
	if strings.TrimSpace(settings.SiteSubtitle) == "" {
		settings.SiteSubtitle = DefaultSiteSubtitle
	}
	if settings.Network.ServerPort == 0 {
		settings.Network.ServerPort = 3000
	}
	if settings.ModelResponseTimeout <= 0 {
		settings.ModelResponseTimeout = 60
	}
	if settings.ModelHeartbeatTimeout <= 0 {
		settings.ModelHeartbeatTimeout = 6
	}
	if settings.Redis.Prefix == "" {
		settings.Redis.Prefix = "nfentik"
	}
	if settings.Redis.QueryCacheTTL <= 0 {
		settings.Redis.QueryCacheTTL = 600
	}
	if settings.UserLogRetentionDays <= 0 {
		settings.UserLogRetentionDays = 30
	}
	if settings.UserLogPerUserLimit <= 0 {
		settings.UserLogPerUserLimit = 200
	}
	if settings.UserLogGlobalLimit <= 0 {
		settings.UserLogGlobalLimit = 50000
	}
	if settings.UserAuditRetentionDays <= 0 {
		settings.UserAuditRetentionDays = 90
	}
}

func (s *Store) saveLocked() error {
	data, err := json.Marshal(s.settings)
	if err != nil {
		return err
	}
	return s.repo.SetSetting("app_settings", data)
}

// Settings returns a copy of the current settings.
func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// Replace overwrites the settings document.
func (s *Store) Replace(settings Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	normalize(&settings)
	// Connection strings remain owned by the bootstrap file.
	settings.Database = s.settings.Database
	settings.Redis = s.settings.Redis
	s.settings = settings
	return s.saveLocked()
}

// Patch merges the raw JSON object into the existing settings.
func (s *Store) Patch(raw json.RawMessage) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	merged := make(map[string]json.RawMessage)
	current, err := json.Marshal(s.settings)
	if err != nil {
		return s.settings, err
	}
	if err := json.Unmarshal(current, &merged); err != nil {
		return s.settings, err
	}
	var incoming map[string]json.RawMessage
	if err := json.Unmarshal(raw, &incoming); err != nil {
		return s.settings, err
	}
	for key, value := range incoming {
		merged[key] = value
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return s.settings, err
	}
	settings := DefaultSettings()
	if err := json.Unmarshal(out, &settings); err != nil {
		return s.settings, err
	}
	// Connection strings stay under bootstrap control even if the client sends
	// new values; they are applied from the local file / environment.
	settings.Database = s.settings.Database
	settings.Redis = s.settings.Redis
	normalize(&settings)
	s.settings = settings
	if err := s.saveLocked(); err != nil {
		return s.settings, err
	}
	return s.settings, nil
}

// AdminToken returns the configured administrator token.
func (s *Store) AdminToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings.AdminToken
}

// DatabaseURL returns the effective PostgreSQL DSN.
func (s *Store) DatabaseURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings.Database.URL
}

// RedisConfig returns the effective Redis settings.
func (s *Store) RedisConfig() Redis {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings.Redis
}

// ModelConfig returns the stored model configuration document.
func (s *Store) ModelConfig() (json.RawMessage, error) {
	raw, found, err := s.repo.GetSetting("model_config")
	if err != nil {
		return nil, err
	}
	if !found || len(raw) == 0 {
		return json.RawMessage("{}"), nil
	}
	if !json.Valid(raw) {
		return nil, errors.New("model config is not valid JSON")
	}
	return raw, nil
}

// SaveModelConfig persists the model configuration document.
func (s *Store) SaveModelConfig(raw json.RawMessage) error {
	if !json.Valid(raw) {
		return errors.New("invalid model config payload")
	}
	var pretty any
	if err := json.Unmarshal(raw, &pretty); err != nil {
		return err
	}
	data, err := json.MarshalIndent(pretty, "", "  ")
	if err != nil {
		return err
	}
	return s.repo.SetSetting("model_config", data)
}

// TrimSettings removes secrets from a settings copy before it is logged.
func TrimSettings(settings Settings) Settings {
	if settings.AdminToken != "" {
		settings.AdminToken = "***"
	}
	return settings
}

// IsEmptyOrPlaceholder reports whether a settings document looks untouched.
func IsEmptyOrPlaceholder(s Settings) bool {
	return strings.TrimSpace(s.AdminToken) == "" && len(s.Algorithms) == 0
}
