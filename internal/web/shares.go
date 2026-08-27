package web

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/RickDB/Flipper/internal/auth"
	"github.com/RickDB/Flipper/internal/browse"
	"github.com/RickDB/Flipper/internal/store"
)

// --- admin: manage shares -------------------------------------------------

func (s *Server) handleAdminShareCreate(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	p := strings.TrimSpace(r.FormValue("path"))
	if name == "" || p == "" {
		redirectFlash(w, r, "/admin", false, "Share name and folder path are required")
		return
	}
	info, err := os.Stat(p)
	if err != nil {
		redirectFlash(w, r, "/admin", false, "Flipper can't see that path: "+err.Error())
		return
	}
	if !info.IsDir() {
		redirectFlash(w, r, "/admin", false, "That path is not a folder: "+p)
		return
	}
	if _, err := s.store.CreateShare(name, p); err != nil {
		redirectFlash(w, r, "/admin", false, "Could not create share: "+err.Error())
		return
	}
	redirectFlash(w, r, "/admin", true, "Share \""+name+"\" created")
}

func (s *Server) handleAdminShareDelete(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		redirectFlash(w, r, "/admin", false, "Invalid share id")
		return
	}
	if err := s.store.DeleteShare(id); err != nil {
		redirectFlash(w, r, "/admin", false, "Could not remove share: "+err.Error())
		return
	}
	redirectFlash(w, r, "/admin", true, "Share removed (no files were touched)")
}

func (s *Server) handleAdminShareUsers(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		redirectFlash(w, r, "/admin", false, "Invalid share id")
		return
	}
	_ = r.ParseForm()
	raw := r.Form["user_ids"]
	ids := make([]int, 0, len(raw))
	for _, v := range raw {
		n, err := strconv.Atoi(v)
		if err == nil {
			ids = append(ids, n)
		}
	}
	rawDelete := r.Form["delete_user_ids"]
	deleteIDs := make([]int, 0, len(rawDelete))
	for _, v := range rawDelete {
		n, err := strconv.Atoi(v)
		if err == nil {
			deleteIDs = append(deleteIDs, n)
		}
	}
	if err := s.store.SetSharePermissions(id, ids, deleteIDs); err != nil {
		redirectFlash(w, r, "/admin", false, "Could not update share access: "+err.Error())
		return
	}
	redirectFlash(w, r, "/admin", true, "Share access updated")
}

func canDeleteFromShare(share store.Share, sess auth.Session) bool {
	if sess.IsAdmin {
		return true
	}
	for _, uid := range share.DeleteUserIDs {
		if uid == sess.UserID {
			return true
		}
	}
	return false
}

// --- user-facing: browse / download ---------------------------------------

// authorizedShare resolves the {id} path value and checks the current user
// (admin or explicitly allowed) may see it.
func (s *Server) authorizedShare(r *http.Request, sess auth.Session) (store.Share, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return store.Share{}, false
	}
	share, ok := s.store.GetShare(id)
	if !ok {
		return store.Share{}, false
	}
	if sess.IsAdmin {
		return share, true
	}
	for _, uid := range share.AllowedUserIDs {
		if uid == sess.UserID {
			return share, true
		}
	}
	return store.Share{}, false
}

// cleanRelPath normalizes a user-supplied, forward-slash relative path for
// display and for building child paths: "" (or anything that collapses to
// root) becomes "". Actual filesystem safety is enforced separately by
// browse.ResolvePath — this is only for tidy round-tripping in JSON.
func cleanRelPath(p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	c := path.Clean("/" + p)
	if c == "/" {
		return ""
	}
	return strings.TrimPrefix(c, "/")
}

func joinRelPath(base, name string) string {
	if base == "" {
		return name
	}
	return base + "/" + name
}

func breadcrumbsFor(rel string) []map[string]string {
	crumbs := []map[string]string{{"name": "Home", "path": ""}}
	if rel == "" {
		return crumbs
	}
	cum := ""
	for _, part := range strings.Split(rel, "/") {
		if part == "" {
			continue
		}
		cum = joinRelPath(cum, part)
		crumbs = append(crumbs, map[string]string{"name": part, "path": cum})
	}
	return crumbs
}

func (s *Server) handleShareBrowse(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	share, ok := s.authorizedShare(r, sess)
	if !ok {
		writeJSON(w, map[string]any{"ok": false, "message": "share not found or you don't have access to it"})
		return
	}
	rel := cleanRelPath(r.URL.Query().Get("path"))
	abs, err := browse.ResolvePath(share.Path, rel)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "message": "invalid path"})
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "message": "could not read that folder: " + err.Error()})
		return
	}
	if !info.IsDir() {
		writeJSON(w, map[string]any{"ok": false, "message": "that's a file, not a folder"})
		return
	}
	entries, err := browse.List(abs)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "message": "could not read that folder: " + err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"name":    e.Name,
			"isDir":   e.IsDir,
			"size":    e.Size,
			"modTime": e.ModTime.Format(time.RFC3339),
			"path":    joinRelPath(rel, e.Name),
		})
	}
	writeJSON(w, map[string]any{
		"ok":          true,
		"shareId":     share.ID,
		"shareName":   share.Name,
		"path":        rel,
		"breadcrumbs": breadcrumbsFor(rel),
		"entries":     out,
		"canDelete":   canDeleteFromShare(share, sess),
	})
}

func (s *Server) handleShareItemDelete(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	share, ok := s.authorizedShare(r, sess)
	if !ok || !canDeleteFromShare(share, sess) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rel := cleanRelPath(r.URL.Query().Get("path"))
	if rel == "" {
		http.Error(w, "the share root cannot be deleted", http.StatusBadRequest)
		return
	}
	abs, err := browse.ResolvePath(share.Path, rel)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if _, err := os.Lstat(abs); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := os.RemoveAll(abs); err != nil {
		http.Error(w, "could not delete item: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleShareDownload(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	share, ok := s.authorizedShare(r, sess)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rel := cleanRelPath(r.URL.Query().Get("path"))
	abs, err := browse.ResolvePath(share.Path, rel)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+sanitizeFilename(filepath.Base(abs))+"\"")
	http.ServeFile(w, r, abs)
}

func (s *Server) handleShareZip(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	share, ok := s.authorizedShare(r, sess)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rel := cleanRelPath(r.URL.Query().Get("path"))
	abs, err := browse.ResolvePath(share.Path, rel)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	name := share.Name
	if rel != "" {
		name = filepath.Base(abs)
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+sanitizeFilename(name)+".zip\"")
	if err := browse.WriteZip(w, abs); err != nil {
		s.logger.Error("share zip stream failed", "share", share.ID, "path", rel, "error", err)
	}
}
