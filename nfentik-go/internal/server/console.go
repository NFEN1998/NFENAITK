package server

import (
	"net/http"
	"strings"
)

// consoleData is the payload rendered into the console template.
type consoleData struct {
	Title      string
	HasToken   bool
	MultiUser  bool
	Version    string
	DataPath   string
	ServerPort int
}

// handleConsole renders the embedded management console.
func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	settings := s.config.Settings()
	data := consoleData{
		Title:      "nfentik-go 管理控制台",
		HasToken:   settings.AdminToken != "",
		MultiUser:  settings.MultiUser.Enabled,
		Version:    "go",
		DataPath:   s.DataPath(),
		ServerPort: settings.Network.ServerPort,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "console.html", data); err != nil {
		http.Error(w, "render console: "+err.Error(), http.StatusInternalServerError)
	}
}

// handleUserPage renders the embedded end-user portal.
func (s *Server) handleUserPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/user" {
		http.NotFound(w, r)
		return
	}
	settings := s.config.Settings()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "user.html", map[string]any{
		"Title":      "nfentik 用户中心",
		"UsersOn":    settings.UsersEnabled,
		"ServerPort": settings.Network.ServerPort,
	}); err != nil {
		http.Error(w, "render user page: "+err.Error(), http.StatusInternalServerError)
	}
}

// wantsJSON reports whether the caller prefers a JSON response.
func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}
