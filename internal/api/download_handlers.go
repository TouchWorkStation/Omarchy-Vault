package api

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/transfer"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/users"
)

// Download and share link limits.
const (
	maxDownloadCount = 10
	// Share links are short-lived: while one is active the phone port is
	// open and Vault stays on.
	maxShareMinutes  = 24 * 60
	defaultShareMins = 60
	maxBrowseEntries = 5000
)

// canRead reports whether the signed-in user may read the Vault folder
// top: admins everywhere, others in folders they were given (read only or
// read & write).
func (s *Server) canRead(r *http.Request, top string) bool {
	id := identityFrom(r)
	if id.Role == string(users.Admin) {
		return true
	}
	if s.Users == nil {
		return false
	}
	u, ok := s.Users.Get(id.Username)
	if !ok {
		return false
	}
	for _, f := range u.Folders {
		if f.Name == top {
			return true
		}
	}
	return false
}

// canShare: admins, and family members for folders they can read. Guests
// can download to their own phone but not hand out links.
func (s *Server) canShare(r *http.Request, top string) bool {
	id := identityFrom(r)
	if id.Role == string(users.Admin) {
		return true
	}
	return id.Role == string(users.Family) && s.canRead(r, top)
}

// BrowseEntry is one item in GET /api/browse.
type BrowseEntry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Dir      bool      `json:"dir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// handleBrowse lists one Vault folder for the download picker. Symlinks,
// hidden files and unfinished uploads are never shown.
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	dataDir, err := s.vaultDataDir(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, "storage_not_ready", "Vault storage is offline or not set up.")
		return
	}
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		writeError(w, http.StatusConflict, "storage_not_ready", "Vault storage is offline or not set up.")
		return
	}
	defer root.Close()

	rel := strings.Trim(r.URL.Query().Get("path"), "/")
	dir := "."
	if rel != "" {
		clean, err := transfer.CleanRel(rel)
		if err != nil || !s.canRead(r, transfer.TopFolder(clean)) {
			writeError(w, http.StatusNotFound, "not_found", "No such folder.")
			return
		}
		info, err := transfer.Stat(root, clean)
		if err != nil || !info.IsDir() {
			writeError(w, http.StatusNotFound, "not_found", "No such folder.")
			return
		}
		rel, dir = clean, clean
	}
	f, err := root.Open(dir)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No such folder.")
		return
	}
	list, err := f.ReadDir(-1)
	f.Close()
	if err != nil && len(list) == 0 {
		writeError(w, http.StatusInternalServerError, "unreadable", "That folder could not be read.")
		return
	}
	entries := []BrowseEntry{}
	truncated := false
	for _, d := range list {
		name := d.Name()
		if strings.HasPrefix(name, ".") || d.Type()&fs.ModeSymlink != 0 || !(d.IsDir() || d.Type().IsRegular()) {
			continue
		}
		p := name
		if rel != "" {
			p = rel + "/" + name
		} else if !d.IsDir() || !s.canRead(r, name) {
			continue // the top level shows only folders you can open
		}
		info, err := d.Info()
		if err != nil {
			continue
		}
		if len(entries) == maxBrowseEntries {
			truncated = true
			break
		}
		e := BrowseEntry{Name: name, Path: p, Dir: d.IsDir(), Modified: info.ModTime()}
		if !e.Dir {
			e.Size = info.Size()
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{"path": rel, "entries": entries, "truncated": truncated})
}

// resolveOffer checks that the user may offer path and that it is a plain
// file or folder in the Vault. It returns the clean path.
func (s *Server) resolveOffer(w http.ResponseWriter, r *http.Request, raw string, share bool) (string, bool) {
	rel, err := transfer.CleanRel(raw)
	allowed := err == nil && s.canRead(r, transfer.TopFolder(rel))
	if allowed && share {
		allowed = s.canShare(r, transfer.TopFolder(rel))
	}
	if err != nil || !allowed {
		if err == nil && s.canRead(r, transfer.TopFolder(rel)) {
			writeError(w, http.StatusForbidden, "not_allowed", "You can't share from that folder.")
			return "", false
		}
		writeError(w, http.StatusNotFound, "not_found", "No such file or folder.")
		return "", false
	}
	dataDir, err := s.vaultDataDir(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, "storage_not_ready", "Vault storage is offline or not set up.")
		return "", false
	}
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		writeError(w, http.StatusConflict, "storage_not_ready", "Vault storage is offline or not set up.")
		return "", false
	}
	defer root.Close()
	info, err := transfer.Stat(root, rel)
	switch {
	case errors.Is(err, transfer.ErrNotPlain):
		writeError(w, http.StatusBadRequest, "not_plain", "Links and special files can't be sent.")
		return "", false
	case err != nil:
		writeError(w, http.StatusNotFound, "not_found", "No such file or folder.")
		return "", false
	case info.IsDir():
		if _, _, err := transfer.Walk(root, rel); errors.Is(err, transfer.ErrTooMany) {
			writeError(w, http.StatusBadRequest, "too_many_files", "That folder has too many files. Choose a smaller folder.")
			return "", false
		}
	}
	return rel, true
}

// issue creates a link, starts the phone listener and returns its view.
func (s *Server) issue(w http.ResponseWriter, r *http.Request, n transfer.NewSession) {
	n.CreatedBy = identityFrom(r).Username
	sess, token, err := s.Transfers.Create(r.Context(), n)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
		return
	}
	base, err := s.Transfer.Ensure()
	if err != nil {
		_ = s.Transfers.Revoke(r.Context(), sess.ID)
		s.Log.Warn("transfer listener unavailable", "err", err)
		writeError(w, http.StatusServiceUnavailable, "no_network", "Your phone can't reach this computer: "+err.Error()+".")
		return
	}
	s.links.put(sess.ID, transfer.LinkFor(base, n.Kind, token))
	s.Log.Info("link created", "kind", sess.Kind, "id", sess.ID, "folder", sess.Folder, "by", sess.CreatedBy)
	writeJSON(w, http.StatusOK, s.linkView(r.Context(), sess, true))
}

// DownloadSessionRequest is the body of POST /api/download-session.
type DownloadSessionRequest struct {
	Path string `json:"path"`
	// LocalPaths sends files from anywhere on this computer (absolute
	// paths) instead of a Vault path. Only the owner's local token (vaultctl)
	// may use it; a browser session never can.
	LocalPaths   []string `json:"local_paths,omitempty"`
	Minutes      int      `json:"minutes"`
	MaxDownloads int      `json:"max_downloads"`
	Client       string   `json:"client"`
}

func (s *Server) handleCreateDownload(w http.ResponseWriter, r *http.Request) {
	if s.Transfers == nil || s.Transfer == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Transfers are not available.")
		return
	}
	var req DownloadSessionRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "The request could not be read.")
		return
	}
	var rel, folder string
	if len(req.LocalPaths) > 0 {
		var ok bool
		if rel, ok = s.resolveLocal(w, r, req.LocalPaths); !ok {
			return
		}
	} else {
		var ok bool
		if rel, ok = s.resolveOffer(w, r, req.Path, false); !ok {
			return
		}
		folder = transfer.TopFolder(rel)
	}
	cfg := s.config()
	minutes := req.Minutes
	if minutes <= 0 {
		minutes = cfg.Preferences.DownloadExpiryMinutes
	}
	minutes = min(max(minutes, 1), maxLinkMinutes)
	count := req.MaxDownloads
	if count <= 0 {
		count = cfg.Preferences.DownloadMaxCount
	}
	count = min(max(count, 1), maxDownloadCount)
	client := req.Client
	if client == "" {
		client = "dashboard"
	}
	s.issue(w, r, transfer.NewSession{
		Kind: transfer.KindDownload, Folder: folder, Path: rel, Client: client,
		TTL: time.Duration(minutes) * time.Minute, MaxFiles: count, MaxBytes: transfer.NoByteLimit,
	})
}

// ShareRequest is the body of POST /api/share.
type ShareRequest struct {
	Path string `json:"path"`
	// Minutes until the link expires (default 1 h, at most 24 h).
	Minutes int `json:"minutes"`
	// MaxDownloads limits downloads; 0 means unlimited until expiry.
	MaxDownloads int    `json:"max_downloads"`
	Password     string `json:"password"`
	Client       string `json:"client"`
}

func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request) {
	if s.Transfers == nil || s.Transfer == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Transfers are not available.")
		return
	}
	var req ShareRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "The request could not be read.")
		return
	}
	rel, ok := s.resolveOffer(w, r, req.Path, true)
	if !ok {
		return
	}
	minutes := req.Minutes
	if minutes <= 0 {
		minutes = defaultShareMins
	}
	minutes = min(minutes, maxShareMinutes)
	count := transfer.Unlimited
	if req.MaxDownloads > 0 {
		count = min(req.MaxDownloads, 1000)
	}
	var hash string
	if req.Password != "" {
		if err := auth.ValidatePassword(req.Password); err != nil {
			writeError(w, http.StatusBadRequest, "weak_password", "Share password: "+err.Error()+".")
			return
		}
		var err error
		if hash, err = auth.HashPassword(req.Password); err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
			return
		}
	}
	client := req.Client
	if client == "" {
		client = "dashboard"
	}
	s.issue(w, r, transfer.NewSession{
		Kind: transfer.KindShare, Folder: transfer.TopFolder(rel), Path: rel, Client: client,
		TTL: time.Duration(minutes) * time.Minute, MaxFiles: count, MaxBytes: transfer.NoByteLimit,
		PasswordHash: hash,
	})
}

// --- Listing, viewing and stopping links. The handlers take the kind the
// route is for; "" (GET/DELETE /api/link/{id}) accepts any kind. ---

func (s *Server) handleListLinks(kind transfer.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := []LinkView{}
		if s.Transfers != nil {
			active, err := s.Transfers.Active(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
				return
			}
			for _, sess := range active {
				if sess.Kind == kind && s.ownsLink(r, sess) {
					out = append(out, s.linkView(r.Context(), sess, true))
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"links": out, "listening": s.Transfer != nil && s.Transfer.Running() != ""})
	}
}

func (s *Server) findLink(w http.ResponseWriter, r *http.Request, kind transfer.Kind) (transfer.Session, bool) {
	if s.Transfers != nil {
		sess, err := s.Transfers.Get(r.Context(), r.PathValue("id"))
		if err == nil && (kind == "" || sess.Kind == kind) && s.ownsLink(r, sess) {
			return sess, true
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "No such link.")
	return transfer.Session{}, false
}

func (s *Server) handleGetLink(kind transfer.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if sess, ok := s.findLink(w, r, kind); ok {
			writeJSON(w, http.StatusOK, s.linkView(r.Context(), sess, true))
		}
	}
}

func (s *Server) handleStopLink(kind transfer.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.findLink(w, r, kind)
		if !ok {
			return
		}
		if err := s.Transfers.Revoke(r.Context(), sess.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
			return
		}
		s.links.drop(sess.ID)
		if s.Transfer != nil {
			s.Transfer.StopIfIdle(r.Context())
		}
		s.Log.Info("link stopped", "kind", kind, "id", sess.ID, "by", identityFrom(r).Username)
		sess, _ = s.Transfers.Get(r.Context(), sess.ID)
		writeJSON(w, http.StatusOK, s.linkView(r.Context(), sess, false))
	}
}

// maxLocalItems caps how many copied files one link sends.
const maxLocalItems = 200

// resolveLocal checks files the owner wants to send from this computer
// and returns them as a session path (one absolute path per line).
func (s *Server) resolveLocal(w http.ResponseWriter, r *http.Request, paths []string) (string, bool) {
	if s.Auth == nil || !s.Auth.CheckToken(r.Header.Get(auth.HeaderToken)) {
		writeError(w, http.StatusForbidden, "local_only", "Files outside the Vault can only be sent from this computer's keyboard shortcut or vaultctl.")
		return "", false
	}
	if len(paths) > maxLocalItems {
		writeError(w, http.StatusBadRequest, "too_many_files", "Copy fewer items at once, or copy their folder.")
		return "", false
	}
	home, _ := os.UserHomeDir()
	seen := map[string]bool{}
	var clean []string
	for _, p := range paths {
		c, info, err := transfer.CheckLocal(p, home)
		switch {
		case errors.Is(err, transfer.ErrPrivate):
			writeError(w, http.StatusForbidden, "private", p+" holds private keys or settings; Vault won't send it.")
			return "", false
		case errors.Is(err, transfer.ErrNotPlain):
			writeError(w, http.StatusBadRequest, "not_plain", p+" is a link or special file and can't be sent.")
			return "", false
		case err != nil:
			writeError(w, http.StatusNotFound, "not_found", p+" doesn't exist or can't be read.")
			return "", false
		}
		if info.IsDir() {
			root, err := os.OpenRoot(filepath.Dir(c))
			if err == nil {
				_, _, werr := transfer.Walk(root, filepath.Base(c))
				root.Close()
				if errors.Is(werr, transfer.ErrTooMany) {
					writeError(w, http.StatusBadRequest, "too_many_files", p+" has too many files. Choose a smaller folder.")
					return "", false
				}
			}
		}
		if !seen[c] {
			seen[c] = true
			clean = append(clean, c)
		}
	}
	return strings.Join(clean, "\n"), true
}
