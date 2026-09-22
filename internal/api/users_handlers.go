package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/files"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/qrsvg"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/users"
)

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// vaultFolders lists the Vault's top-level folders (for granting access).
func (s *Server) vaultFolders() []string {
	out := []string{}
	cfg := s.config()
	if cfg.Pool.Mode == "none" || len(cfg.Sources) == 0 {
		return out
	}
	entries, err := os.ReadDir(cfg.Sources[0].DataDir())
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && users.ValidateFolders([]users.Folder{{Name: e.Name(), Access: users.ReadOnly}}) == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// SyncFiles mirrors accounts into SFTPGo; the file service calls it each
// time SFTPGo starts.
func (s *Server) SyncFiles(ctx context.Context) error { return s.syncFiles(ctx) }

// syncFiles mirrors accounts into SFTPGo when it runs.
func (s *Server) syncFiles(ctx context.Context) error {
	if s.Files == nil || s.Users == nil {
		return nil
	}
	c := s.Files.Client()
	if c == nil {
		return nil
	}
	return files.Sync(ctx, c, s.Users.List(), s.DataLink, s.Files.Paths.HomesDir)
}

func (s *Server) syncFilesAsync() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.syncFiles(ctx); err != nil {
			s.Log.Error("files: user sync failed", "err", err)
		}
	}()
}

// UsersResponse is returned by GET /api/users.
type UsersResponse struct {
	Users   []users.View `json:"users"`
	Folders []string     `json:"folders"`
}

func (s *Server) handleListUsers(w http.ResponseWriter, _ *http.Request) {
	resp := UsersResponse{Users: []users.View{}, Folders: s.vaultFolders()}
	if s.Users != nil {
		for _, u := range s.Users.List() {
			resp.Users = append(resp.Users, u.View())
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreateUserRequest is the body of POST /api/users.
type CreateUserRequest struct {
	Username string          `json:"username"`
	Password string          `json:"password"`
	Role     users.Role      `json:"role"`
	Folders  *[]users.Folder `json:"folders"`
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req CreateUserRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "The request could not be read.")
		return
	}
	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	bootstrap := !s.accountsExist()
	if bootstrap {
		// The very first account is always an admin.
		req.Role = users.Admin
	}
	if err := users.ValidateUsername(req.Username); err != nil {
		writeError(w, http.StatusBadRequest, "bad_username", err.Error())
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", "Password: "+err.Error()+".")
		return
	}
	folders := users.DefaultFolders(req.Role, s.vaultFolders())
	if req.Folders != nil {
		folders = *req.Folders
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
		return
	}
	u := users.User{Username: req.Username, Role: req.Role, PasswordHash: hash, Folders: folders}
	if err := s.Users.Create(u); err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, users.ErrExists) {
			code = http.StatusConflict
		}
		writeError(w, code, "invalid", err.Error())
		return
	}
	s.Log.Info("user created", "user", u.Username, "role", u.Role, "by", identityFrom(r).Username)
	created, _ := s.Users.Get(u.Username)
	if err := s.syncFiles(r.Context()); err != nil {
		s.Log.Error("files: user sync failed", "err", err)
	}
	if bootstrap {
		// First account: sign its creator in as that account.
		s.signIn(w, r, created, req.Password)
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": created.View()})
}

// UpdateUserRequest is the body of PUT /api/users/{name}.
type UpdateUserRequest struct {
	Role     *users.Role     `json:"role"`
	Disabled *bool           `json:"disabled"`
	Folders  *[]users.Folder `json:"folders"`
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req UpdateUserRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "The request could not be read.")
		return
	}
	u, err := s.Users.Update(name, func(u *users.User) error {
		if req.Role != nil {
			u.Role = *req.Role
		}
		if req.Disabled != nil {
			u.Disabled = *req.Disabled
		}
		if req.Folders != nil {
			u.Folders = *req.Folders
		}
		return nil
	})
	if s.userError(w, err) {
		return
	}
	if req.Role != nil || (req.Disabled != nil && *req.Disabled) {
		s.Auth.EndSessionsFor(name)
	}
	s.Log.Info("user updated", "user", name, "by", identityFrom(r).Username)
	if err := s.syncFiles(r.Context()); err != nil {
		s.Log.Error("files: user sync failed", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u.View()})
}

func (s *Server) userError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, users.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such user.")
	case errors.Is(err, users.ErrLastAdmin):
		writeError(w, http.StatusConflict, "last_admin", err.Error()+".")
	default:
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
	}
	return true
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "The request could not be read.")
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", "Password: "+err.Error()+".")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
		return
	}
	_, err = s.Users.Update(name, func(u *users.User) error { u.PasswordHash = hash; return nil })
	if s.userError(w, err) {
		return
	}
	s.Auth.EndSessionsFor(name)
	s.Log.Info("password reset", "user", name, "by", identityFrom(r).Username)
	if err := s.syncFiles(r.Context()); err != nil {
		s.Log.Error("files: user sync failed", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.userError(w, s.Users.Delete(name)) {
		return
	}
	s.Auth.EndSessionsFor(name)
	s.Log.Info("user removed", "user", name, "by", identityFrom(r).Username)
	if err := s.syncFiles(r.Context()); err != nil {
		s.Log.Error("files: user sync failed", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "note": "The account was removed. No files were deleted."})
}

// --- the signed-in user's own account ---

func (s *Server) accountUser(w http.ResponseWriter, r *http.Request) (users.User, bool) {
	id := identityFrom(r)
	if id.Local {
		writeError(w, http.StatusBadRequest, "local_session", "Sign in with your own account to change these settings.")
		return users.User{}, false
	}
	u, ok := s.Users.Get(id.Username)
	if !ok {
		writeError(w, http.StatusUnauthorized, "login_required", "Please sign in.")
		return users.User{}, false
	}
	return u, true
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u, ok := s.accountUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "The request could not be read.")
		return
	}
	key := "user:" + u.Username
	if _, blocked := s.Limiter.Blocked(key); blocked {
		writeError(w, http.StatusTooManyRequests, "locked", "Too many attempts. Try again in a few minutes.")
		return
	}
	if !auth.VerifyPassword(req.Current, u.PasswordHash) {
		s.Limiter.Fail(key)
		writeError(w, http.StatusUnauthorized, "bad_credentials", "Your current password is not right.")
		return
	}
	if err := auth.ValidatePassword(req.New); err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", "Password: "+err.Error()+".")
		return
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
		return
	}
	u, err = s.Users.Update(u.Username, func(x *users.User) error { x.PasswordHash = hash; return nil })
	if s.userError(w, err) {
		return
	}
	s.Auth.EndSessionsFor(u.Username) // sign out everywhere else …
	if err := s.syncFiles(r.Context()); err != nil {
		s.Log.Error("files: user sync failed", "err", err)
	}
	s.signIn(w, r, u, req.New) // … and keep this browser signed in
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	u, ok := s.accountUser(w, r)
	if !ok {
		return
	}
	if u.TOTPEnabled {
		writeError(w, http.StatusConflict, "totp_enabled", "Two-factor sign-in is already on.")
		return
	}
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
		return
	}
	if _, err := s.Users.Update(u.Username, func(x *users.User) error { x.TOTPSecret = secret; x.TOTPLast = 0; return nil }); s.userError(w, err) {
		return
	}
	uri := auth.OTPAuthURI(secret, u.Username, "Omarchy Vault")
	svg, err := qrsvg.Encode(uri)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "uri": uri, "qr_svg": svg})
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	u, ok := s.accountUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := decode(r, &req); err != nil || u.TOTPSecret == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Start two-factor setup first.")
		return
	}
	counter, valid := auth.VerifyTOTP(u.TOTPSecret, req.Code, time.Now(), 0)
	if !valid {
		writeError(w, http.StatusBadRequest, "bad_totp", "That code is not right. Check the time on your phone and try the next code.")
		return
	}
	u, err := s.Users.Update(u.Username, func(x *users.User) error { x.TOTPEnabled = true; x.TOTPLast = counter; return nil })
	if s.userError(w, err) {
		return
	}
	s.Log.Info("two-factor enabled", "user", u.Username)
	writeJSON(w, http.StatusOK, map[string]any{"user": u.View()})
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	u, ok := s.accountUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "The request could not be read.")
		return
	}
	if !auth.VerifyPassword(req.Password, u.PasswordHash) {
		s.Limiter.Fail("user:" + u.Username)
		writeError(w, http.StatusUnauthorized, "bad_credentials", "Your password is not right.")
		return
	}
	u, err := s.Users.Update(u.Username, func(x *users.User) error {
		x.TOTPEnabled, x.TOTPSecret, x.TOTPLast = false, "", 0
		return nil
	})
	if s.userError(w, err) {
		return
	}
	s.Log.Info("two-factor disabled", "user", u.Username)
	writeJSON(w, http.StatusOK, map[string]any{"user": u.View()})
}
