// Package server exposes the OCS query API, the admin JSON API and the
// embedded management console.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nfentik/nfentik-go/internal/ai"
	"github.com/nfentik/nfentik-go/internal/cache"
	"github.com/nfentik/nfentik-go/internal/config"
	"github.com/nfentik/nfentik-go/internal/match"
	"github.com/nfentik/nfentik-go/internal/store"
)

// Server wires the storage, configuration and AI client together.
type Server struct {
	store       *store.Store
	config      *config.Store
	cache       *cache.Client
	ai          *ai.Client
	bus         *Bus
	tmpl        *template.Template
	webFS       fs.FS
	storageInfo string

	pendingMu sync.Mutex
	pending   map[string]*pendingCall

	maxLogs int
}

type pendingCall struct {
	query  string
	chunks chan ai.Delta
	done   chan callResult
}

type callResult struct {
	content   string
	reasoning string
	err       error
}

// Deps configures a new server instance.
type Deps struct {
	Store  *store.Store
	Config *config.Store
	Cache  *cache.Client
	// StorageInfo is a human readable description of where data lives, shown
	// in the console (for example a redacted database DSN).
	StorageInfo string
	WebFS       fs.FS
	TmplDir     string
}

// New builds a server from its dependencies.
func New(deps Deps) (*Server, error) {
	tmpl, err := template.ParseFS(deps.WebFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	s := &Server{
		store:       deps.Store,
		config:      deps.Config,
		cache:       deps.Cache,
		ai:          ai.New(),
		bus:         NewBus(),
		tmpl:        tmpl,
		webFS:       deps.WebFS,
		storageInfo: deps.StorageInfo,
		pending:     make(map[string]*pendingCall),
		maxLogs:     1000,
	}
	if deps.Store != nil {
		deps.Store.OnChange(s.invalidateQueryCache)
	}
	return s, nil
}

// Handler returns the fully registered HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// ---- OCS integration endpoints (no auth, consumed by the browser userscript) ----
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/query", s.handleQuery)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/time", s.handleTime)
	mux.HandleFunc("/api/echo", s.handleEcho)
	mux.HandleFunc("/api/questions/", s.handleQuestionAction)
	mux.HandleFunc("/api/model/response", s.handleModelResponse)
	mux.HandleFunc("/api/model/progress", s.handleModelProgress)
	mux.HandleFunc("/api/logs/stream", s.handleLogStream)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/models", s.handleModels)
	mux.HandleFunc("/api/model/call", s.handleModelCall)
	mux.HandleFunc("/api/model/stream", s.handleModelStream)

	// ---- User endpoints (usertoken protected) ----
	mux.HandleFunc("/api/user/", s.handleUser)
	mux.HandleFunc("/user", s.handleUserPage)

	// ---- Admin API (token protected) ----
	mux.HandleFunc("/api/admin/", s.handleAdmin)

	// ---- Static console ----
	if staticFS, err := fs.Sub(s.webFS, "static"); err == nil {
		mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	}
	if userFS, err := fs.Sub(s.webFS, "static"); err == nil {
		mux.Handle("/user/static/", http.StripPrefix("/user/static/", http.FileServer(http.FS(userFS))))
	}
	mux.HandleFunc("/console", s.handleConsole)

	return withCORS(s.withLogging(mux))
}

// ConsoleHandler is used when embedding the server behind another mux.
func (s *Server) ConsoleHandler() http.HandlerFunc { return s.handleConsole }

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, HEAD")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func decodeJSON(r *http.Request, dst any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, dst)
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	if v := r.Header.Get("X-Real-Ip"); v != "" {
		return v
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) origin(r *http.Request) string {
	host := r.Host
	if host == "" {
		host = "127.0.0.1:3000"
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + host
}

func (s *Server) logRequest(entry store.RequestLog) {
	// Prefer the Redis queue so hot request paths avoid a database round trip.
	// A background worker flushes queued entries to PostgreSQL in batches.
	if s.cache != nil {
		s.cache.PushLog(entry)
		return
	}
	if err := s.store.InsertRequestLog(entry, s.maxLogs); err != nil {
		log.Printf("persist request log: %v", err)
	}
}

// PublishError surfaces internal problems during development.
func (s *Server) PublishError(format string, args ...any) {
	log.Printf(format, args...)
}

// StopAll terminates any workers. Reserved for graceful shutdown.
func (s *Server) StopAll() {}

// ModelSettings loads the current model configuration.
func (s *Server) ModelSettings() (ai.ModelSettings, error) {
	raw, err := s.config.ModelConfig()
	if err != nil {
		return ai.ModelSettings{}, err
	}
	return ai.ParseModelSettings(raw)
}

// CloseSegmenter releases the jieba dictionary. Wired in main.
func (s *Server) CloseSegmenter() { match.CloseSegmenter() }

// DataPath is exposed for the console to display where data is stored.
func (s *Server) DataPath() string { return s.storageInfo }

// ContextWithTimeout provides a helper used by callers.
func ContextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
