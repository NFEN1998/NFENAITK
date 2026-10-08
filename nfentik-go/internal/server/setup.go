package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
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
	mux.HandleFunc("/setup/test-db", s.handleTestDB)
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

// dbFields holds the individual PostgreSQL connection fields entered in the
// wizard. They are assembled into a DSN before being persisted.
type dbFields struct {
	Host     string `json:"db_host"`
	Port     string `json:"db_port"`
	User     string `json:"db_user"`
	Password string `json:"db_password"`
	Name     string `json:"db_name"`
	SSLMode  string `json:"db_sslmode"`
}

// Default DB field values point at a local PostgreSQL instance.
const (
	defaultDBHost    = "127.0.0.1"
	defaultDBPort    = "5432"
	defaultDBUser    = "postgres"
	defaultDBName    = "nfentik"
	defaultDBSSLMode = "disable"
)

// dsn assembles a PostgreSQL connection URL from the fields. Empty optional
// values fall back to sensible defaults so a partial form still produces a
// usable string.
func (f dbFields) dsn() string {
	host := strings.TrimSpace(f.Host)
	if host == "" {
		host = defaultDBHost
	}
	port := strings.TrimSpace(f.Port)
	if port == "" {
		port = defaultDBPort
	}
	user := url.UserPassword(strings.TrimSpace(f.User), f.Password)

	u := &url.URL{
		Scheme: "postgres",
		User:   user,
		Host:   host + ":" + port,
	}
	if name := strings.TrimSpace(f.Name); name != "" {
		u.Path = "/" + name
	}
	ssl := strings.TrimSpace(f.SSLMode)
	if ssl == "" {
		ssl = defaultDBSSLMode
	}
	q := url.Values{}
	q.Set("sslmode", ssl)
	u.RawQuery = q.Encode()
	return u.String()
}

// parseDSN splits an existing DSN back into fields so the wizard can prefill
// them. Unknown or unparseable values return defaults.
func parseDSN(dsn string) dbFields {
	f := dbFields{
		Host: defaultDBHost, Port: defaultDBPort, User: defaultDBUser,
		Name: defaultDBName, SSLMode: defaultDBSSLMode,
	}
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return f
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return f
	}
	if u.Hostname() != "" {
		f.Host = u.Hostname()
	}
	if u.Port() != "" {
		f.Port = u.Port()
	}
	if u.User != nil {
		if name := u.User.Username(); name != "" {
			f.User = name
		}
		if pass, ok := u.User.Password(); ok {
			f.Password = pass
		}
	}
	if p := strings.TrimPrefix(u.Path, "/"); p != "" {
		f.Name = p
	}
	if ssl := u.Query().Get("sslmode"); ssl != "" {
		f.SSLMode = ssl
	}
	return f
}

// setupState is the prefilled payload returned to the wizard.
type setupState struct {
	DBHost       string `json:"db_host"`
	DBPort       string `json:"db_port"`
	DBUser       string `json:"db_user"`
	DBPassword   string `json:"db_password"`
	DBName       string `json:"db_name"`
	DBSSLMode    string `json:"db_sslmode"`
	RedisURL     string `json:"redis_url"`
	RedisEnabled bool   `json:"redis_enabled"`
	AdminToken   string `json:"admin_token"`
	SiteName     string `json:"site_name"`
	SiteSubtitle string `json:"site_subtitle"`
}

func (s *SetupServer) currentState() setupState {
	db := parseDSN(s.bootstrap.Database.URL)
	st := setupState{
		DBHost:       db.Host,
		DBPort:       db.Port,
		DBUser:       db.User,
		DBPassword:   db.Password,
		DBName:       db.Name,
		DBSSLMode:    db.SSLMode,
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

// setupRequest is the wizard submission. DB connection fields are carried
// separately and assembled into a DSN on save.
type setupRequest struct {
	dbFields
	RedisEnabled bool   `json:"redis_enabled"`
	RedisURL     string `json:"redis_url"`
	AdminToken   string `json:"admin_token"`
	SiteName     string `json:"site_name"`
	SiteSubtitle string `json:"site_subtitle"`
}

// handleTestDB verifies a candidate database connection without persisting
// anything, so the operator gets feedback before saving.
func (s *SetupServer) handleTestDB(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var f dbFields
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请求体解析失败"})
		return
	}
	if strings.TrimSpace(f.Host) == "" || strings.TrimSpace(f.User) == "" || strings.TrimSpace(f.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请完整填写主机、用户名和数据库名"})
		return
	}
	dsn := f.dsn()

	// Reuse an already-open store when the form points at the same database,
	// otherwise open a short-lived connection to probe it.
	if s.store != nil && s.bootstrap.Database.URL == dsn {
		if err := s.store.Ping(); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"success": false, "message": "连接失败: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "连接成功"})
		return
	}

	st, err := store.Open(dsn)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "message": "连接失败: " + err.Error()})
		return
	}
	defer st.Close()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "连接成功"})
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
	req.RedisURL = strings.TrimSpace(req.RedisURL)
	req.AdminToken = strings.TrimSpace(req.AdminToken)
	req.SiteName = strings.TrimSpace(req.SiteName)
	req.SiteSubtitle = strings.TrimSpace(req.SiteSubtitle)

	if strings.TrimSpace(req.Host) == "" || strings.TrimSpace(req.User) == "" || strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "请完整填写数据库主机、用户名和数据库名"})
		return
	}
	if req.Port != "" {
		if p, err := strconv.Atoi(strings.TrimSpace(req.Port)); err != nil || p <= 0 || p > 65535 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "数据库端口无效"})
			return
		}
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

	dsn := req.dsn()

	// Verify the database is reachable before persisting, so the operator gets
	// immediate feedback instead of a broken restart. When the form still
	// points at the already-open store we reuse its settings repository,
	// otherwise we open a fresh connection and seed it from scratch.
	sameDB := s.store != nil && s.bootstrap.Database.URL == dsn
	var cfg *config.Store
	if sameDB {
		cfg = s.cfg
	} else {
		st, err := store.Open(dsn)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "连接数据库失败: " + err.Error()})
			return
		}
		defer st.Close()
		seeded, err := config.New(st, config.BootstrapFile{})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "初始化设置失败: " + err.Error()})
			return
		}
		cfg = seeded
	}

	// Persist application settings to the database.
	if _, err := cfg.Patch(encodeRawJSON(map[string]any{
		"adminToken":   req.AdminToken,
		"siteName":     req.SiteName,
		"siteSubtitle": req.SiteSubtitle,
	})); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "保存设置失败: " + err.Error()})
		return
	}

	// Write the bootstrap file last: once it exists the next start connects to
	// the database directly.
	bootstrap := config.BootstrapFile{}
	bootstrap.Database.URL = dsn
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
