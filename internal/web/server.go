// Package web implements Flipper's HTTP server: authentication, the
// regular-user dashboard (submit a Spotweb link, pick a category, see
// history), and the admin settings/users panel.
package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RickDB/Flipper/internal/auth"
	"github.com/RickDB/Flipper/internal/sabnzbd"
	"github.com/RickDB/Flipper/internal/spotweb"
	"github.com/RickDB/Flipper/internal/store"
)

const (
	sessionCookieName = "flipper_session"
	sessionTTL        = 30 * 24 * time.Hour
)

type Server struct {
	store    *store.Store
	sessions *auth.Manager
	tmpl     *template.Template
	logger   *slog.Logger
	version  string
}

func NewServer(st *store.Store, sessions *auth.Manager, logger *slog.Logger, version string) (*Server, error) {
	tmpl, err := loadTemplates()
	if err != nil {
		return nil, fmt.Errorf("loading templates: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{store: st, sessions: sessions, tmpl: tmpl, logger: logger, version: version}, nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("/static/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLoginSubmit)
	mux.HandleFunc("GET /logout", s.handleLogout)
	mux.HandleFunc("GET /setup", s.handleSetupForm)
	mux.HandleFunc("POST /setup", s.handleSetupSubmit)

	mux.HandleFunc("GET /{$}", s.requireAuth(s.handleDashboard))
	mux.HandleFunc("POST /submit", s.requireAuth(s.handleSubmit))
	mux.HandleFunc("GET /account", s.requireAuth(s.handleAccountForm))
	mux.HandleFunc("POST /account", s.requireAuth(s.handleAccountSubmit))
	mux.HandleFunc("POST /account/spotweb-key", s.requireAuth(s.handleAccountSpotwebKeySubmit))

	mux.HandleFunc("GET /admin", s.requireAdmin(s.handleAdmin))
	mux.HandleFunc("POST /admin/sabnzbd/save", s.requireAdmin(s.handleAdminSabnzbdSave))
	mux.HandleFunc("POST /admin/sabnzbd/test", s.requireAdmin(s.handleAdminSabnzbdTest))
	mux.HandleFunc("POST /admin/sabnzbd/categories", s.requireAdmin(s.handleAdminSabnzbdCategories))
	mux.HandleFunc("POST /admin/spotweb/save", s.requireAdmin(s.handleAdminSpotwebSave))
	mux.HandleFunc("POST /admin/spotweb/test", s.requireAdmin(s.handleAdminSpotwebTest))
	mux.HandleFunc("POST /admin/users", s.requireAdmin(s.handleAdminUserCreate))
	mux.HandleFunc("POST /admin/users/{id}/delete", s.requireAdmin(s.handleAdminUserDelete))

	mux.HandleFunc("POST /admin/shares", s.requireAdmin(s.handleAdminShareCreate))
	mux.HandleFunc("POST /admin/shares/{id}/delete", s.requireAdmin(s.handleAdminShareDelete))
	mux.HandleFunc("POST /admin/shares/{id}/users", s.requireAdmin(s.handleAdminShareUsers))

	mux.HandleFunc("GET /shares/{id}/browse", s.requireAuth(s.handleShareBrowse))
	mux.HandleFunc("GET /shares/{id}/download", s.requireAuth(s.handleShareDownload))
	mux.HandleFunc("GET /shares/{id}/zip", s.requireAuth(s.handleShareZip))
	mux.HandleFunc("POST /shares/{id}/delete", s.requireAuth(s.handleShareItemDelete))

	return s.withLogging(mux)
}

// --- shared page data ------------------------------------------------

type Flash struct {
	OK      bool
	Message string
}

type Base struct {
	Version    string
	User       *auth.Session
	Flash      *Flash
	WideLayout bool // widens the main content column; set by pages with a side-by-side layout
}

func (s *Server) base(sess *auth.Session, r *http.Request) Base {
	return Base{Version: s.version, User: sess, Flash: flashFromQuery(r)}
}

func flashFromQuery(r *http.Request) *Flash {
	if m := r.URL.Query().Get("ok"); m != "" {
		return &Flash{OK: true, Message: m}
	}
	if m := r.URL.Query().Get("err"); m != "" {
		return &Flash{OK: false, Message: m}
	}
	return nil
}

func redirectFlash(w http.ResponseWriter, r *http.Request, path string, ok bool, message string) {
	v := url.Values{}
	if ok {
		v.Set("ok", message)
	} else {
		v.Set("err", message)
	}
	http.Redirect(w, r, path+"?"+v.Encode(), http.StatusSeeOther)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.logger.Error("template render failed", "template", name, "error", err)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// --- middleware --------------------------------------------------------

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.logger.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "duration", time.Since(start))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) currentSession(r *http.Request) *auth.Session {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil
	}
	sess, err := s.sessions.Get(cookie.Value)
	if err != nil {
		return nil
	}
	return &sess
}

func (s *Server) requireAuth(next func(w http.ResponseWriter, r *http.Request, sess auth.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.store.HasAdmin() {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		sess := s.currentSession(r)
		if sess == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r, *sess)
	}
}

func (s *Server) requireAdmin(next func(w http.ResponseWriter, r *http.Request, sess auth.Session)) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request, sess auth.Session) {
		if !sess.IsAdmin {
			redirectFlash(w, r, "/", false, "Admin access required")
			return
		}
		next(w, r, sess)
	})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		Expires:  time.Now().Add(sessionTTL),
	})
}

// --- health --------------------------------------------------------------

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

// --- auth: login / logout / setup ---------------------------------------

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if !s.store.HasAdmin() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if s.currentSession(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, "login_page", Base{Version: s.version, Flash: flashFromQuery(r)})
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	u, ok := s.store.GetUserByUsername(username)
	if !ok || !auth.VerifyPassword(password, u.PasswordHash) {
		redirectFlash(w, r, "/login", false, "Invalid username or password")
		return
	}
	token, err := s.sessions.Create(u.ID, u.Username, u.IsAdmin)
	if err != nil {
		s.logger.Error("session create failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.setSessionCookie(w, r, token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.Delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleSetupForm(w http.ResponseWriter, r *http.Request) {
	if s.store.HasAdmin() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, "setup_page", Base{Version: s.version, Flash: flashFromQuery(r)})
}

func (s *Server) handleSetupSubmit(w http.ResponseWriter, r *http.Request) {
	if s.store.HasAdmin() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	confirm := r.FormValue("confirm")

	if username == "" {
		redirectFlash(w, r, "/setup", false, "Username is required")
		return
	}
	if len(password) < 8 {
		redirectFlash(w, r, "/setup", false, "Password must be at least 8 characters")
		return
	}
	if password != confirm {
		redirectFlash(w, r, "/setup", false, "Passwords do not match")
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.logger.Error("hash password failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	u, err := s.store.CreateUser(username, hash, true)
	if err != nil {
		redirectFlash(w, r, "/setup", false, "Could not create admin account: "+err.Error())
		return
	}
	token, err := s.sessions.Create(u.ID, u.Username, u.IsAdmin)
	if err != nil {
		s.logger.Error("session create failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.setSessionCookie(w, r, token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- dashboard / submit --------------------------------------------------

const historyPageSize = 10

type dashboardData struct {
	Base
	Categories        []string
	DefaultCategory   string
	History           []store.HistoryItem
	PrefillURL        string
	Shares            []store.Share
	HistoryTotal      int
	HistoryPage       int
	HistoryTotalPages int
	HistoryPages      []int
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	settings := s.store.GetSettings()

	total := s.store.CountHistory()
	totalPages := (total + historyPageSize - 1) / historyPageSize
	if totalPages < 1 {
		totalPages = 1
	}
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("hpage")); err == nil && p > 0 {
		page = p
	}
	if page > totalPages {
		page = totalPages
	}
	pages := make([]int, totalPages)
	for i := range pages {
		pages[i] = i + 1
	}

	base := s.base(&sess, r)
	base.WideLayout = true
	data := dashboardData{
		Base:              base,
		Categories:        settings.AllowedCategories,
		DefaultCategory:   settings.DefaultCategory,
		History:           s.store.ListHistoryPage((page-1)*historyPageSize, historyPageSize),
		Shares:            s.store.ListSharesForUser(sess.UserID, sess.IsAdmin),
		HistoryTotal:      total,
		HistoryPage:       page,
		HistoryTotalPages: totalPages,
		HistoryPages:      pages,
	}
	s.render(w, "dashboard_page", data)
}

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	_ = r.ParseForm()
	spotURL := strings.TrimSpace(r.FormValue("spot_url"))
	category := strings.TrimSpace(r.FormValue("category"))
	settings := s.store.GetSettings()

	if category == "" {
		category = settings.DefaultCategory
	}
	if len(settings.AllowedCategories) == 0 {
		redirectFlash(w, r, "/", false, "No SABnzbd categories are configured yet — ask an admin to set them up")
		return
	}
	if !isAllowed(category, settings.AllowedCategories) {
		redirectFlash(w, r, "/", false, "That category is not allowed")
		return
	}

	record := func(messageID, title string, success bool, message string) {
		_ = s.store.AddHistory(store.HistoryItem{
			ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
			Timestamp: time.Now(),
			Username:  sess.Username,
			SpotURL:   spotURL,
			MessageID: messageID,
			Title:     title,
			Category:  category,
			Success:   success,
			Message:   message,
		})
	}

	messageID, err := spotweb.ExtractMessageID(spotURL)
	if err != nil {
		record("", "", false, err.Error())
		redirectFlash(w, r, "/", false, err.Error())
		return
	}

	// Prefer the submitting user's own Spotweb API key; fall back to the
	// admin-configured shared key so Flipper works out of the box before
	// individual users have set up their own.
	apiKey := settings.SpotwebAPIKey
	if u, ok := s.store.GetUserByID(sess.UserID); ok && u.SpotwebAPIKey != "" {
		apiKey = u.SpotwebAPIKey
	}

	spotClient := spotweb.New(settings.SpotwebURL, settings.SpotwebUsername, settings.SpotwebPassword, settings.SpotwebSkipVerify, settings.SpotwebNZBTemplate)

	// Best-effort: the real release title makes history far more useful than
	// a bare messageid, but a failure here shouldn't block the actual send.
	title, err := spotClient.FetchTitle(messageID, apiKey)
	if err != nil {
		title = messageID
	}

	nzb, err := spotClient.FetchNZB(messageID, apiKey)
	if err != nil {
		record(messageID, title, false, "Spotweb: "+err.Error())
		redirectFlash(w, r, "/", false, "Could not fetch the NZB from Spotweb: "+err.Error())
		return
	}

	sabClient := sabnzbd.New(settings.SabnzbdURL, settings.SabnzbdAPIKey, settings.SabnzbdSkipVerify)
	_, err = sabClient.AddNZB(sanitizeFilename(title)+".nzb", nzb, category)
	if err != nil {
		record(messageID, title, false, "SABnzbd: "+err.Error())
		redirectFlash(w, r, "/", false, "SABnzbd rejected the release: "+err.Error())
		return
	}

	record(messageID, title, true, "Added to SABnzbd: "+title)
	redirectFlash(w, r, "/", true, "🐬 Sent \""+title+"\" to SABnzbd under category \""+category+"\"")
}

func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ' ', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		out = "release"
	}
	return out
}

// --- account -------------------------------------------------------------

type accountData struct {
	Base
	SpotwebAPIKey string
}

func (s *Server) handleAccountForm(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var apiKey string
	if u, ok := s.store.GetUserByID(sess.UserID); ok {
		apiKey = u.SpotwebAPIKey
	}
	data := accountData{
		Base:          Base{Version: s.version, User: &sess, Flash: flashFromQuery(r)},
		SpotwebAPIKey: apiKey,
	}
	s.render(w, "account_page", data)
}

func (s *Server) handleAccountSpotwebKeySubmit(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	_ = r.ParseForm()
	apiKey := strings.TrimSpace(r.FormValue("spotweb_api_key"))
	if err := s.store.SetUserSpotwebAPIKey(sess.UserID, apiKey); err != nil {
		redirectFlash(w, r, "/account", false, "Could not save your Spotweb API key: "+err.Error())
		return
	}
	redirectFlash(w, r, "/account", true, "Spotweb API key saved")
}

func (s *Server) handleAccountSubmit(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	_ = r.ParseForm()
	current := r.FormValue("current_password")
	newPass := r.FormValue("new_password")
	confirm := r.FormValue("confirm_password")

	u, ok := s.store.GetUserByID(sess.UserID)
	if !ok || !auth.VerifyPassword(current, u.PasswordHash) {
		redirectFlash(w, r, "/account", false, "Current password is incorrect")
		return
	}
	if len(newPass) < 8 {
		redirectFlash(w, r, "/account", false, "New password must be at least 8 characters")
		return
	}
	if newPass != confirm {
		redirectFlash(w, r, "/account", false, "New passwords do not match")
		return
	}
	hash, err := auth.HashPassword(newPass)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.store.SetUserPassword(u.ID, hash); err != nil {
		redirectFlash(w, r, "/account", false, "Could not update password: "+err.Error())
		return
	}
	redirectFlash(w, r, "/account", true, "Password updated")
}

// --- admin -----------------------------------------------------------

type adminData struct {
	Base
	Settings           store.Settings
	Users              []store.User
	NonAdminUsers      []store.User
	AdminUsername      string
	DisplayCategories  []string
	CategoryFetchError string
	Shares             []store.Share
}

func (s *Server) liveCategoriesOrFallback(settings store.Settings) ([]string, string) {
	if settings.SabnzbdURL == "" || settings.SabnzbdAPIKey == "" {
		return settings.AllowedCategories, ""
	}
	client := sabnzbd.New(settings.SabnzbdURL, settings.SabnzbdAPIKey, settings.SabnzbdSkipVerify)
	cats, err := client.GetCategories()
	if err != nil {
		return settings.AllowedCategories, "Could not refresh categories from SABnzbd: " + err.Error()
	}
	merged := append([]string{}, cats...)
	for _, a := range settings.AllowedCategories {
		if !isAllowed(a, merged) {
			merged = append(merged, a)
		}
	}
	return merged, ""
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	settings := s.store.GetSettings()
	users := s.store.ListUsers()
	adminUsername := ""
	nonAdminUsers := make([]store.User, 0, len(users))
	for _, u := range users {
		if u.IsAdmin {
			adminUsername = u.Username
		} else {
			nonAdminUsers = append(nonAdminUsers, u)
		}
	}
	displayCats, catErr := s.liveCategoriesOrFallback(settings)
	data := adminData{
		Base:               s.base(&sess, r),
		Settings:           settings,
		Users:              users,
		NonAdminUsers:      nonAdminUsers,
		AdminUsername:      adminUsername,
		DisplayCategories:  displayCats,
		CategoryFetchError: catErr,
		Shares:             s.store.ListShares(),
	}
	s.render(w, "admin_page", data)
}

func (s *Server) handleAdminSabnzbdSave(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	_ = r.ParseForm()
	sabURL := strings.TrimSpace(r.FormValue("url"))
	apikey := strings.TrimSpace(r.FormValue("apikey"))
	skip := r.FormValue("skip_verify") == "on"
	allowed := r.Form["allowed_categories"]
	def := strings.TrimSpace(r.FormValue("default_category"))

	err := s.store.UpdateSettings(func(st *store.Settings) {
		st.SabnzbdURL = sabURL
		st.SabnzbdAPIKey = apikey
		st.SabnzbdSkipVerify = skip
		st.AllowedCategories = allowed
		st.DefaultCategory = def
	})
	if err != nil {
		redirectFlash(w, r, "/admin", false, "Could not save SABnzbd settings: "+err.Error())
		return
	}
	redirectFlash(w, r, "/admin", true, "SABnzbd settings saved")
}

func (s *Server) handleAdminSabnzbdTest(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	_ = r.ParseForm()
	client := sabnzbd.New(r.FormValue("url"), r.FormValue("apikey"), r.FormValue("skip_verify") == "on")
	version, err := client.TestConnection()
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "message": "Connected — SABnzbd version " + version})
}

func (s *Server) handleAdminSabnzbdCategories(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	_ = r.ParseForm()
	client := sabnzbd.New(r.FormValue("url"), r.FormValue("apikey"), r.FormValue("skip_verify") == "on")
	cats, err := client.GetCategories()
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "categories": cats, "message": "ok"})
}

func (s *Server) handleAdminSpotwebSave(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	_ = r.ParseForm()
	spotURL := strings.TrimSpace(r.FormValue("url"))
	username := r.FormValue("username")
	password := r.FormValue("password")
	skip := r.FormValue("skip_verify") == "on"
	apiKey := strings.TrimSpace(r.FormValue("apikey"))
	tpl := strings.TrimSpace(r.FormValue("nzb_template"))
	if tpl == "" {
		tpl = store.DefaultSettings().SpotwebNZBTemplate
	}

	err := s.store.UpdateSettings(func(st *store.Settings) {
		st.SpotwebURL = spotURL
		st.SpotwebUsername = username
		st.SpotwebPassword = password
		st.SpotwebSkipVerify = skip
		st.SpotwebAPIKey = apiKey
		st.SpotwebNZBTemplate = tpl
	})
	if err != nil {
		redirectFlash(w, r, "/admin", false, "Could not save Spotweb settings: "+err.Error())
		return
	}
	redirectFlash(w, r, "/admin", true, "Spotweb settings saved")
}

func (s *Server) handleAdminSpotwebTest(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	_ = r.ParseForm()
	client := spotweb.New(r.FormValue("url"), r.FormValue("username"), r.FormValue("password"), r.FormValue("skip_verify") == "on", r.FormValue("nzb_template"))
	if err := client.TestConnection(); err != nil {
		writeJSON(w, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "message": "Spotweb is reachable"})
}

func (s *Server) handleAdminUserCreate(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	_ = r.ParseForm()
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	if username == "" || len(password) < 8 {
		redirectFlash(w, r, "/admin", false, "Username and an 8+ character password are required")
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if _, err := s.store.CreateUser(username, hash, false); err != nil {
		redirectFlash(w, r, "/admin", false, "Could not create user: "+err.Error())
		return
	}
	redirectFlash(w, r, "/admin", true, "User \""+username+"\" created")
}

func (s *Server) handleAdminUserDelete(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		redirectFlash(w, r, "/admin", false, "Invalid user id")
		return
	}
	if id == sess.UserID {
		redirectFlash(w, r, "/admin", false, "You cannot remove your own account")
		return
	}
	if err := s.store.DeleteUser(id); err != nil {
		redirectFlash(w, r, "/admin", false, "Could not remove user: "+err.Error())
		return
	}
	s.sessions.DeleteForUser(id)
	redirectFlash(w, r, "/admin", true, "User removed")
}
