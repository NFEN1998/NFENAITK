package server

import (
	"net/http"
	"strings"
)

// consoleData is the payload rendered into the console template.
type consoleData struct {
	Title      string
	SiteName   string
	HasToken   bool
	Version    string
	DataPath   string
	ServerPort int
}

// handleConsole renders the embedded management console.
func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	settings := s.config.Settings()
	data := consoleData{
		Title:      settings.SiteName + " 管理控制台",
		SiteName:   settings.SiteName,
		HasToken:   settings.AdminToken != "",
		Version:    "go",
		DataPath:   s.DataPath(),
		ServerPort: settings.Network.ServerPort,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "console.html", data); err != nil {
		http.Error(w, "render console: "+err.Error(), http.StatusInternalServerError)
	}
}

// userPageData is the payload rendered into the end-user portal shell.
type userPageData struct {
	Title      string
	SiteName   string
	UsersOn    bool
	ServerPort int
	Page       string
	PageTitle  string
}

// userPages maps each user-center route to its display name. Every feature has
// its own page, so the portal never turns into one very long scroll.
var userPages = map[string]string{
	"overview":  "概览",
	"plan":      "套餐与额度",
	"account":   "账号与安全",
	"redeem":    "卡密充值",
	"integrate": "接入配置",
	"logs":      "调用记录",
}

// handleUserPage renders one page of the multi-page end-user portal.
func (s *Server) handleUserPage(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/user"), "/")
	settings := s.config.Settings()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if path == "login" {
		data := userPageData{
			Title:      "登录 · " + settings.SiteName + " 用户中心",
			SiteName:   settings.SiteName,
			UsersOn:    settings.UsersEnabled,
			ServerPort: settings.Network.ServerPort,
			Page:       "login",
			PageTitle:  "登录",
		}
		if err := s.tmpl.ExecuteTemplate(w, "user_login.html", data); err != nil {
			http.Error(w, "render user login: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if path == "" {
		path = "overview"
	}
	pageTitle, ok := userPages[path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	data := userPageData{
		Title:      pageTitle + " · " + settings.SiteName + " 用户中心",
		SiteName:   settings.SiteName,
		UsersOn:    settings.UsersEnabled,
		ServerPort: settings.Network.ServerPort,
		Page:       path,
		PageTitle:  pageTitle,
	}
	if err := s.tmpl.ExecuteTemplate(w, "user.html", data); err != nil {
		http.Error(w, "render user page: "+err.Error(), http.StatusInternalServerError)
	}
}

// wantsJSON reports whether the caller prefers a JSON response.
func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}
