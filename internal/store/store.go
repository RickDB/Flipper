// Package store provides SQLite-backed persistence for Flipper, kept
// deliberately small: a handful of users, one settings row, and a 10-item
// history list. Uses the pure-Go modernc.org/sqlite driver, so no CGO / C
// toolchain is required to build or run.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const maxHistory = 100

type User struct {
	ID            int
	Username      string
	PasswordHash  string
	IsAdmin       bool
	CreatedAt     time.Time
	SpotwebAPIKey string // optional personal Spotweb API key/token, set by the user themselves
}

// Settings holds all admin-configurable connection settings.
type Settings struct {
	// Sabnzbd
	SabnzbdURL        string
	SabnzbdAPIKey     string
	SabnzbdSkipVerify bool
	AllowedCategories []string // subset of live categories users may pick from
	DefaultCategory   string

	// Spotweb
	SpotwebURL         string
	SpotwebUsername    string // optional HTTP basic auth in front of a reverse proxy — NOT Spotweb's own login
	SpotwebPassword    string
	SpotwebSkipVerify  bool
	SpotwebAPIKey      string // admin-level fallback Spotweb API key, used when a user hasn't set their own
	SpotwebNZBTemplate string // e.g. {base}/api?t=get&id={messageid}&apikey={apikey}
}

func DefaultSettings() Settings {
	return Settings{
		SpotwebNZBTemplate: "{base}/api?t=get&id={messageid}&apikey={apikey}",
	}
}

type HistoryItem struct {
	ID        string
	Timestamp time.Time
	Username  string
	SpotURL   string
	MessageID string
	Title     string // release title fetched from Spotweb's API; falls back to MessageID when unavailable
	Category  string
	Success   bool
	Message   string
}

// Share is an admin-defined named local folder that specific users may
// browse and download from on the dashboard. Name is chosen freely by the
// admin and is independent of Path (the real filesystem location, which is
// never shown to non-admin users).
type Share struct {
	ID             int
	Name           string
	Path           string
	AllowedUserIDs []int
	DeleteUserIDs  []int
	CreatedAt      time.Time
}

// Store wraps a SQLite database connection. modernc.org/sqlite is pure Go
// (no CGO), and the connection pool is capped at one open connection so
// SQLite's single-writer model never has to deal with SQLITE_BUSY under
// this app's light, single-process load — simplest possible concurrency
// story for a "minimal fuss" deployment.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash TEXT NOT NULL,
	is_admin      INTEGER NOT NULL DEFAULT 0,
	created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS settings (
	id                    INTEGER PRIMARY KEY CHECK (id = 1),
	sabnzbd_url           TEXT NOT NULL DEFAULT '',
	sabnzbd_api_key       TEXT NOT NULL DEFAULT '',
	sabnzbd_skip_verify   INTEGER NOT NULL DEFAULT 0,
	allowed_categories    TEXT NOT NULL DEFAULT '[]',
	default_category      TEXT NOT NULL DEFAULT '',
	spotweb_url           TEXT NOT NULL DEFAULT '',
	spotweb_username      TEXT NOT NULL DEFAULT '',
	spotweb_password      TEXT NOT NULL DEFAULT '',
	spotweb_skip_verify   INTEGER NOT NULL DEFAULT 0,
	spotweb_api_key       TEXT NOT NULL DEFAULT '',
	spotweb_nzb_template  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS history (
	id         TEXT PRIMARY KEY,
	timestamp  TIMESTAMP NOT NULL,
	username   TEXT NOT NULL,
	spot_url   TEXT NOT NULL,
	message_id TEXT NOT NULL,
	title      TEXT NOT NULL DEFAULT '',
	category   TEXT NOT NULL,
	success    INTEGER NOT NULL,
	message    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS shares (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	name             TEXT NOT NULL,
	path             TEXT NOT NULL,
	allowed_user_ids TEXT NOT NULL DEFAULT '[]',
	delete_user_ids  TEXT NOT NULL DEFAULT '[]',
	created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

// Open opens (creating if necessary) the SQLite database at path,
// applies the schema, and seeds the singleton settings row.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("creating data directory: %w", err)
		}
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// A single connection avoids SQLite's "database is locked" errors under
	// concurrent handlers without needing an app-level mutex, and this app's
	// traffic is far too light for that to be a bottleneck.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO settings (id, spotweb_nzb_template) VALUES (1, ?)`,
		DefaultSettings().SpotwebNZBTemplate); err != nil {
		db.Close()
		return nil, fmt.Errorf("seeding settings: %w", err)
	}

	// Lightweight forward migration for installs created before per-user
	// Spotweb API keys existed. Safe to run on every boot: ignored once the
	// column is present.
	if _, err := db.Exec(`ALTER TABLE users ADD COLUMN spotweb_api_key TEXT NOT NULL DEFAULT ''`); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("migrating users table: %w", err)
		}
	}
	// Same idea for the admin-level fallback Spotweb API key.
	if _, err := db.Exec(`ALTER TABLE settings ADD COLUMN spotweb_api_key TEXT NOT NULL DEFAULT ''`); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("migrating settings table: %w", err)
		}
	}

	// Installs from before the real Spotweb Newznab-style API endpoint was
	// known got seeded with a guessed template that only ever returned an
	// HTML login page. Heal it in place: only overwrite when the stored
	// value is still exactly that known-broken guess, so any template an
	// admin has since customized is left untouched.
	const brokenGuessedTemplate = "{base}/index.php?page=getnzb&messageid={messageid}"
	if _, err := db.Exec(`UPDATE settings SET spotweb_nzb_template = ? WHERE id = 1 AND spotweb_nzb_template = ?`,
		DefaultSettings().SpotwebNZBTemplate, brokenGuessedTemplate); err != nil {
		db.Close()
		return nil, fmt.Errorf("healing spotweb_nzb_template: %w", err)
	}

	// Forward migration for installs created before history rows carried the
	// release title.
	if _, err := db.Exec(`ALTER TABLE history ADD COLUMN title TEXT NOT NULL DEFAULT ''`); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("migrating history table: %w", err)
		}
	}

	// Forward migration for per-user delete permissions on shared folders.
	if _, err := db.Exec(`ALTER TABLE shares ADD COLUMN delete_user_ids TEXT NOT NULL DEFAULT '[]'`); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("migrating shares table: %w", err)
		}
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// --- Users -----------------------------------------------------------

func (s *Store) HasAnyUser() bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n > 0
}

func (s *Store) HasAdmin() bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin = 1`).Scan(&n)
	return n > 0
}

const userColumns = `id, username, password_hash, is_admin, created_at, spotweb_api_key`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var isAdmin int
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &isAdmin, &u.CreatedAt, &u.SpotwebAPIKey); err != nil {
		return User{}, err
	}
	u.IsAdmin = isAdmin != 0
	return u, nil
}

func (s *Store) ListUsers() []User {
	rows, err := s.db.Query(`SELECT ` + userColumns + ` FROM users ORDER BY is_admin DESC, username ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			continue
		}
		out = append(out, u)
	}
	return out
}

func (s *Store) GetUserByUsername(username string) (User, bool) {
	row := s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE username = ? COLLATE NOCASE`, username)
	u, err := scanUser(row)
	if err != nil {
		return User{}, false
	}
	return u, true
}

func (s *Store) GetUserByID(id int) (User, bool) {
	row := s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE id = ?`, id)
	u, err := scanUser(row)
	if err != nil {
		return User{}, false
	}
	return u, true
}

var ErrUsernameTaken = fmt.Errorf("username already taken")

// CreateUser adds a new user.
func (s *Store) CreateUser(username, passwordHash string, isAdmin bool) (User, error) {
	if _, ok := s.GetUserByUsername(username); ok {
		return User{}, ErrUsernameTaken
	}
	res, err := s.db.Exec(`INSERT INTO users (username, password_hash, is_admin, created_at) VALUES (?, ?, ?, ?)`,
		username, passwordHash, boolToInt(isAdmin), time.Now())
	if err != nil {
		return User{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	u, _ := s.GetUserByID(int(id))
	return u, nil
}

// DeleteUser removes a non-admin user by id.
func (s *Store) DeleteUser(id int) error {
	u, ok := s.GetUserByID(id)
	if !ok {
		return fmt.Errorf("user not found")
	}
	if u.IsAdmin {
		return fmt.Errorf("cannot delete the admin account")
	}
	_, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

// SetUserPassword updates a user's password hash.
func (s *Store) SetUserPassword(id int, passwordHash string) error {
	res, err := s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

// SetUserSpotwebAPIKey updates a user's personal Spotweb API key/token,
// used (when set) when fetching NZBs on that user's behalf.
func (s *Store) SetUserSpotwebAPIKey(id int, apiKey string) error {
	res, err := s.db.Exec(`UPDATE users SET spotweb_api_key = ? WHERE id = ?`, apiKey, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// --- Settings ----------------------------------------------------------

func (s *Store) GetSettings() Settings {
	var st Settings
	var sabSkip, spotSkip int
	var allowedJSON string
	err := s.db.QueryRow(`SELECT sabnzbd_url, sabnzbd_api_key, sabnzbd_skip_verify, allowed_categories,
		default_category, spotweb_url, spotweb_username, spotweb_password, spotweb_skip_verify, spotweb_api_key, spotweb_nzb_template
		FROM settings WHERE id = 1`).Scan(
		&st.SabnzbdURL, &st.SabnzbdAPIKey, &sabSkip, &allowedJSON,
		&st.DefaultCategory, &st.SpotwebURL, &st.SpotwebUsername, &st.SpotwebPassword, &spotSkip, &st.SpotwebAPIKey, &st.SpotwebNZBTemplate,
	)
	if err != nil {
		return DefaultSettings()
	}
	st.SabnzbdSkipVerify = sabSkip != 0
	st.SpotwebSkipVerify = spotSkip != 0
	_ = json.Unmarshal([]byte(allowedJSON), &st.AllowedCategories)
	return st
}

// UpdateSettings loads the current settings, applies fn, and persists the
// result.
func (s *Store) UpdateSettings(fn func(*Settings)) error {
	st := s.GetSettings()
	fn(&st)
	allowedJSON, err := json.Marshal(st.AllowedCategories)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE settings SET sabnzbd_url = ?, sabnzbd_api_key = ?, sabnzbd_skip_verify = ?,
		allowed_categories = ?, default_category = ?, spotweb_url = ?, spotweb_username = ?, spotweb_password = ?,
		spotweb_skip_verify = ?, spotweb_api_key = ?, spotweb_nzb_template = ? WHERE id = 1`,
		st.SabnzbdURL, st.SabnzbdAPIKey, boolToInt(st.SabnzbdSkipVerify),
		string(allowedJSON), st.DefaultCategory, st.SpotwebURL, st.SpotwebUsername, st.SpotwebPassword,
		boolToInt(st.SpotwebSkipVerify), st.SpotwebAPIKey, st.SpotwebNZBTemplate)
	return err
}

// --- History -------------------------------------------------------------

func (s *Store) AddHistory(item HistoryItem) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`INSERT INTO history (id, timestamp, username, spot_url, message_id, title, category, success, message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.Timestamp, item.Username, item.SpotURL, item.MessageID, item.Title, item.Category, boolToInt(item.Success), item.Message)
	if err != nil {
		return err
	}
	// Keep only the most recent maxHistory rows.
	_, err = tx.Exec(`DELETE FROM history WHERE id NOT IN (
		SELECT id FROM history ORDER BY timestamp DESC LIMIT ?
	)`, maxHistory)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// CountHistory returns the total number of kept history rows (up to
// maxHistory), for computing pagination.
func (s *Store) CountHistory() int {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM history`).Scan(&n); err != nil {
		return 0
	}
	return n
}

// ListHistoryPage returns up to limit history rows, most recent first,
// starting after the given offset.
func (s *Store) ListHistoryPage(offset, limit int) []HistoryItem {
	rows, err := s.db.Query(`SELECT id, timestamp, username, spot_url, message_id, title, category, success, message
		FROM history ORDER BY timestamp DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []HistoryItem
	for rows.Next() {
		var h HistoryItem
		var success int
		if err := rows.Scan(&h.ID, &h.Timestamp, &h.Username, &h.SpotURL, &h.MessageID, &h.Title, &h.Category, &success, &h.Message); err != nil {
			continue
		}
		h.Success = success != 0
		out = append(out, h)
	}
	return out
}

// --- Shares --------------------------------------------------------------

func scanShare(row interface{ Scan(...any) error }) (Share, error) {
	var sh Share
	var allowedJSON, deleteJSON string
	if err := row.Scan(&sh.ID, &sh.Name, &sh.Path, &allowedJSON, &deleteJSON, &sh.CreatedAt); err != nil {
		return Share{}, err
	}
	_ = json.Unmarshal([]byte(allowedJSON), &sh.AllowedUserIDs)
	_ = json.Unmarshal([]byte(deleteJSON), &sh.DeleteUserIDs)
	return sh, nil
}

const shareColumns = `id, name, path, allowed_user_ids, delete_user_ids, created_at`

// ListShares returns every share, admin's-eye view.
func (s *Store) ListShares() []Share {
	rows, err := s.db.Query(`SELECT ` + shareColumns + ` FROM shares ORDER BY name COLLATE NOCASE ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Share
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			continue
		}
		out = append(out, sh)
	}
	return out
}

// ListSharesForUser returns the shares a given user may browse: all of them
// if isAdmin, otherwise only those the user's id appears in.
func (s *Store) ListSharesForUser(userID int, isAdmin bool) []Share {
	all := s.ListShares()
	if isAdmin {
		return all
	}
	out := make([]Share, 0, len(all))
	for _, sh := range all {
		for _, id := range sh.AllowedUserIDs {
			if id == userID {
				out = append(out, sh)
				break
			}
		}
	}
	return out
}

func (s *Store) GetShare(id int) (Share, bool) {
	row := s.db.QueryRow(`SELECT `+shareColumns+` FROM shares WHERE id = ?`, id)
	sh, err := scanShare(row)
	if err != nil {
		return Share{}, false
	}
	return sh, true
}

// CreateShare adds a new named share.
func (s *Store) CreateShare(name, path string) (Share, error) {
	res, err := s.db.Exec(`INSERT INTO shares (name, path, allowed_user_ids, created_at) VALUES (?, ?, '[]', ?)`,
		name, path, time.Now())
	if err != nil {
		return Share{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Share{}, err
	}
	sh, _ := s.GetShare(int(id))
	return sh, nil
}

// DeleteShare removes a share (the underlying folder on disk is untouched).
func (s *Store) DeleteShare(id int) error {
	_, err := s.db.Exec(`DELETE FROM shares WHERE id = ?`, id)
	return err
}

// SetSharePermissions replaces the access and delete grants for a share.
// Delete permission is only retained for users who also have access.
func (s *Store) SetSharePermissions(id int, userIDs, deleteUserIDs []int) error {
	allowed := make(map[int]bool, len(userIDs))
	for _, userID := range userIDs {
		allowed[userID] = true
	}
	filteredDeleteIDs := make([]int, 0, len(deleteUserIDs))
	for _, userID := range deleteUserIDs {
		if allowed[userID] {
			filteredDeleteIDs = append(filteredDeleteIDs, userID)
		}
	}
	allowedJSON, err := json.Marshal(userIDs)
	if err != nil {
		return err
	}
	deleteJSON, err := json.Marshal(filteredDeleteIDs)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE shares SET allowed_user_ids = ?, delete_user_ids = ? WHERE id = ?`, string(allowedJSON), string(deleteJSON), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("share not found")
	}
	return nil
}
