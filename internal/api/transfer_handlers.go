package api

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/qrsvg"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/storage"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/transfer"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/users"
)

// Upload link defaults.
const (
	uploadMaxFiles = 1000
	uploadMaxBytes = int64(100) << 30 // 100 GB per link
	maxLinkMinutes = 60
)

// liveLinks keeps each active link's URL in memory only, so the QR can be
// shown again (e.g. by the window `vaultctl upload` opens). Tokens are
// never written to disk; after a restart old links still work from the
// phone, but their QR cannot be shown again.
type liveLinks struct {
	mu   sync.Mutex
	urls map[string]string
}

func (l *liveLinks) put(id, url string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.urls == nil {
		l.urls = map[string]string{}
	}
	l.urls[id] = url
}

func (l *liveLinks) get(id string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.urls[id]
}

func (l *liveLinks) drop(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.urls, id)
}

// vaultDataDir returns the Vault's data folder if storage is ready.
func (s *Server) vaultDataDir(ctx context.Context) (string, error) {
	inv, err := s.Disks.Inventory(ctx, false)
	if err != nil {
		inv = nil
	}
	st := s.storageStatus(ctx, inv)
	if st.State != storage.StateReady || len(st.Sources) == 0 {
		return "", errors.New("vault storage is not ready")
	}
	return st.Sources[0].DataDir, nil
}

// TransferDest is the transfer server's destination callback.
func (s *Server) TransferDest() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.vaultDataDir(ctx)
}

// canUploadTo reports whether the signed-in user may send files into
// folder: admins anywhere, family into folders they can write, guests
// nowhere.
func (s *Server) canUploadTo(r *http.Request, folder string) bool {
	id := identityFrom(r)
	if id.Role == string(users.Admin) {
		return true
	}
	if s.Users == nil {
		return false
	}
	u, ok := s.Users.Get(id.Username)
	if !ok || u.Role != users.Family {
		return false
	}
	for _, f := range u.Folders {
		if f.Name == folder && f.Access == users.ReadWrite {
			return true
		}
	}
	return false
}

// UploadSessionRequest is the body of POST /api/upload-session.
type UploadSessionRequest struct {
	Folder  string `json:"folder"`
	Minutes int    `json:"minutes"`
	Client  string `json:"client"`
}

// LinkView describes a transfer link to the dashboard.
type LinkView struct {
	transfer.Session
	State    string          `json:"state"`
	URL      string          `json:"url,omitempty"`
	QRSVG    string          `json:"qr_svg,omitempty"`
	Received []transfer.Item `json:"received"`
}

func (s *Server) linkView(ctx context.Context, sess transfer.Session, withQR bool) LinkView {
	v := LinkView{Session: sess, State: "active", Received: []transfer.Item{}}
	now := time.Now()
	switch {
	case sess.Revoked:
		v.State = "stopped"
	case !now.Before(sess.ExpiresAt):
		v.State = "expired"
	case sess.Files >= sess.MaxFiles || sess.Bytes >= sess.MaxBytes:
		v.State = "full"
	}
	if items, err := s.Transfers.Received(ctx, sess.ID); err == nil {
		v.Received = items
	}
	if v.State == "active" && withQR {
		if url := s.links.get(sess.ID); url != "" {
			v.URL = url
			v.QRSVG, _ = qrsvg.Encode(url)
		}
	}
	return v
}

func (s *Server) handleCreateUpload(w http.ResponseWriter, r *http.Request) {
	if s.Transfers == nil || s.Transfer == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Transfers are not available.")
		return
	}
	var req UploadSessionRequest
	if r.ContentLength != 0 {
		if err := decode(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "The request could not be read.")
			return
		}
	}
	cfg := s.config()
	if req.Folder == "" {
		req.Folder = cfg.Preferences.UploadFolder
	}
	if err := users.ValidateFolders([]users.Folder{{Name: req.Folder, Access: users.ReadWrite}}); err != nil {
		writeError(w, http.StatusBadRequest, "bad_folder", "Choose one of the Vault's folders.")
		return
	}
	minutes := req.Minutes
	if minutes <= 0 {
		minutes = cfg.Preferences.UploadExpiryMinutes
	}
	if minutes > maxLinkMinutes {
		minutes = maxLinkMinutes
	}
	if !s.canUploadTo(r, req.Folder) {
		writeError(w, http.StatusForbidden, "not_allowed", "You can't upload into that folder.")
		return
	}
	dataDir, err := s.vaultDataDir(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, "storage_not_ready", "Vault storage is offline or not set up.")
		return
	}
	// Create the destination if it is one of the default folders that was
	// removed; never through a link.
	if root, err := os.OpenRoot(dataDir); err == nil {
		if info, err := root.Lstat(req.Folder); errors.Is(err, fs.ErrNotExist) && req.Folder == cfg.Preferences.UploadFolder {
			_ = root.Mkdir(req.Folder, 0o750)
		} else if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			root.Close()
			writeError(w, http.StatusBadRequest, "bad_folder", "That folder does not exist in the Vault.")
			return
		}
		root.Close()
	}

	client := req.Client
	if client == "" {
		client = "dashboard"
	}
	s.issue(w, r, transfer.NewSession{
		Kind: transfer.KindUpload, Folder: req.Folder, Client: client,
		TTL: time.Duration(minutes) * time.Minute, MaxFiles: uploadMaxFiles, MaxBytes: uploadMaxBytes,
	})
}

// ownsLink: admins see every link, others only their own.
func (s *Server) ownsLink(r *http.Request, sess transfer.Session) bool {
	id := identityFrom(r)
	return id.Role == string(users.Admin) || sess.CreatedBy == id.Username
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	items := []transfer.Item{}
	if s.Transfers != nil {
		all, err := s.Transfers.Recent(r.Context(), 50)
		if err == nil {
			id := identityFrom(r)
			for _, it := range all {
				if id.Role == string(users.Admin) || it.Actor == id.Username {
					items = append(items, it)
				}
				if len(items) == 20 {
					break
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
