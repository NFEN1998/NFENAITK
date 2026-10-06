package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/nfentik/nfentik-go/internal/config"
	"github.com/nfentik/nfentik-go/internal/store"
)

// SetupServer serves the first-run installation wizard. It works with or
// without a live database: when the store is reachable the submitted settings
// are persisted there as well, otherwise only the local bootstrap file is
// written and the operator restarts the program.
type SetupServer struct {
	tmpl      *template.Template
	webFS     fs.FS
	bootstrap config.BootstrapFile
	store     *store.Store  // optional, nil when the database is not configured yet
	cfg       *config.Store // optional, paired with store
	reason    string        // why setup is required, shown in logs
}

// NewSetupServer builds the wizard. store and cfg may both be nil on a first
// run where no database connection is available yet.
func NewSetupServer(webFS fs.FS, bootstrap config.BootstrapFile, st *store.Store, cfg *config.Store, reason string) (*SetupServer, error) {
	tmpl, err := template.ParseFS(webFS, "templates/setup.html")
	if err != nil {
		return nil, fmt.Errorf("parse setup template: %w", err)
	}
	return &SetupServer{tmpl: tmpl, webFS: webFS, bootstrap: bootstrap, store: st, cfg: cfg, reason: reason}, nil
}

// Reason explains why the wizard was entered.
func (s *SetupServer) Reason() string { return s.reason }

// Handler returns the setup-only HTTP handler. Every path outside the wizard is
// redirected to /setup so an unconfigured instance never exposes the console.
func (s *SetupServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/setup", s.handleSetup)
	mux.HandleFunc("/setup/state", s.handleState)
	if staticFS, err := fs.Sub(s.webFS, "static"); err == nil {
		mux.Handle("/setup/static/", http.StripPrefix("/setup/static/", http.FileServer(http.FS(staticFS))))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Static wizard assets and the wizard itself are handled above; send
		// everything else to the wizard.
		http.Redirect(w, r, "/setup", http.StatusFound)
	})
	return mux
}

// setupState is the prefilled payload returned to the wizard.
type setupState struct {
	DatabaseURL  string `json:"database_url"`
	RedisURL     string `json:"redis_url"`
	RedisEnabled bool   `json:"redis_enabled"`
	AdminToken   string `json:"admin_token"`
	SiteName     string `json:"site_name"`
	SiteSubtitle string `json:"site_subtitle"`
}

func (s *SetupServer) currentState() setupState {
	st := setupState{
		DatabaseURL:  s.bootstrap.Database.URL,
		RedisURL:     s.bootstrap.Redis.URL,
		RedisEnabled: s.bootstrap.Redis.Enabled,
		SiteName:     config.DefaultSiteName,
		SiteSubtitle: config.DefaultSiteSubtitle,
	}
	if s.cfg != nil {
		settings := s.cfg.Settings()
		st.AdminToken = settings.AdminToken
		if strings.TrimSpace(settings.SiteName) != "" {
			st.SiteName = settings.SiteName
		}
		if strings.TrimSpace(settings.SiteSubtitle) != "" {
			st.SiteSubtitle = settings.SiteSubtitle
		}
	}
	return st
}

func (s *SetupServer) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "state": s.currentState()})
}

// setupRequest is the wizard submission.
type setupRequest struct {
	DatabaseURL  string `json:"database_url"`
	RedisEnabled bool   `json:"redis_enabled"`
	RedisURL     string `json:"redis_url"`
	AdminToken   string `json:"admin_token"`
	SiteName     string `json:"site_name"`
	SiteSubtitle string `json:"site_subtitle"`
}

func (s *SetupServer) handleSetup(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := s.tmpl.ExecuteTemplate(w, "setup.html", map[string]any{
			"SiteName": config.DefaultSiteName,
		}); err != nil {
			http.Error(w, "render setup: "+err.Error(), http.StatusInternalServerError)
		}
	case http.MethodPost:
		s.handleSubmit(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *SetupServer) handleSubmit(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请求体解析失败"})
		return
	}
	req.DatabaseURL = strings.TrimSpace(req.DatabaseURL)
	req.RedisURL = strings.TrimSpace(req.RedisURL)
	req.AdminToken = strings.TrimSpace(req.AdminToken)
	req.SiteName = strings.TrimSpace(req.SiteName)
	req.SiteSubtitle = strings.TrimSpace(req.SiteSubtitle)

	if req.DatabaseURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请填写 PostgreSQL 连接串"})
		return
	}
	if req.AdminToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请设置管理员令牌"})
		return
	}
	if req.SiteName == "" {
		req.SiteName = config.DefaultSiteName
	}
	if req.SiteSubtitle == "" {
		req.SiteSubtitle = config.DefaultSiteSubtitle
	}

	// Verify the database is reachable before persisting, so the operator gets
	// immediate feedback instead of a broken restart.
	opened := s.store
	closeAfter := false
	if opened == nil {
		st, err := store.Open(req.DatabaseURL)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "连接数据库失败: " + err.Error()})
			return
		}
		opened = st
		closeAfter = true
	}
	if closeAfter {
		defer opened.Close()
	}

	// Persist application settings to the database when available.
	if s.cfg != nil {
		if _, err := s.cfg.Patch(encodeRawJSON(map[string]any{
			"adminToken":   req.AdminToken,
			"siteName":     req.SiteName,
			"siteSubtitle": req.SiteSubtitle,
		})); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "保存设置失败: " + err.Error()})
			return
		}
	} else {
		// Fresh database: seed settings so the restart picks up the new values.
		cfg, err := config.New(opened, config.BootstrapFile{})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "初始化设置失败: " + err.Error()})
			return
		}
		if _, err := cfg.Patch(encodeRawJSON(map[string]any{
			"adminToken":   req.AdminToken,
			"siteName":     req.SiteName,
			"siteSubtitle": req.SiteSubtitle,
		})); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "保存设置失败: " + err.Error()})
			return
		}
	}

	// Write the bootstrap file last: once it exists the next start connects to
	// the database directly.
	bootstrap := config.BootstrapFile{}
	bootstrap.Database.URL = req.DatabaseURL
	bootstrap.Redis = config.Redis{
		URL:            req.RedisURL,
		Enabled:        req.RedisEnabled && req.RedisURL != "",
		Prefix:         "nfentik",
		QueryCacheTTL:  600,
		HistoryEnabled: true,
	}
	if err := config.WriteBootstrap(bootstrap); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "写入配置文件失败: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"bootstrap_path": config.BootstrapPath(),
	})
}

// encodeRawJSON marshals a value that is known to be JSON-serialisable.
func encodeRawJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return data
}
