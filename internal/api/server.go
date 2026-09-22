// Package api serves Vault's local HTTP API and web UI.
//
// Read endpoints are open on loopback; writes go through requireAuth.
// Endpoints planned for later milestones are registered and answer 501 with
// the milestone that delivers them, so clients such as Beam can discover the
// API shape today.
package api

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/disks"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/files"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/services"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/shortcuts"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/storage"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/sysexec"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/users"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/version"
)

// Server holds the dependencies of the HTTP API.
type Server struct {
	// Config is the configuration at startup. Handlers read the live copy
	// through s.config(); storage changes update it and ConfigPath.
	Config      config.Config
	ConfigPath  string
	ConfigFound bool
	ConfigErr   error
	// DataLink is ~/.local/share/omarchy-vault/current (see storage.SetLink).
	DataLink string
	// Mounts reads the kernel mount table; storage.ReadMountTable by default.
	Mounts func() (storage.MountTable, error)
	// Auth guards state-changing endpoints. Nil disables all writes.
	Auth *auth.Local
	// Users is the account store; Limiter slows password guessing.
	Users   *users.Store
	Limiter *auth.LoginLimiter
	// Files supervises SFTPGo. Nil when not configured.
	Files     *files.Manager
	Disks     *disks.Scanner
	Run       sysexec.Runner
	Shortcuts shortcuts.Inspector
	// Static is the built web UI. When nil or empty a placeholder page is
	// served instead.
	Static fs.FS
	// PowerOff stops vaultd (and with it the file service). Set by vaultd.
	PowerOff func()
	// Demo marks responses as sample data (vaultd --demo).
	Demo    bool
	Log     *slog.Logger
	Started time.Time

	mu  sync.RWMutex
	cur config.Config
	// writeMu serialises storage changes.
	writeMu sync.Mutex
}

func (s *Server) config() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

func (s *Server) setConfig(c config.Config) {
	s.mu.Lock()
	s.cur = c
	s.ConfigFound = true
	s.ConfigErr = nil
	s.mu.Unlock()
}

func (s *Server) mounts() storage.MountTable {
	read := s.Mounts
	if read == nil {
		read = storage.ReadMountTable
	}
	m, err := read()
	if err != nil {
		s.Log.Warn("mount table unreadable", "err", err)
	}
	return m
}

// storageStatus combines config, the drive inventory and the mount table.
func (s *Server) storageStatus(ctx context.Context, inv *disks.Inventory) storage.Status {
	return storage.Inspect(s.config(), inv, s.mounts(), s.DataLink)
}

// Handler returns the full middleware-wrapped handler.
func (s *Server) Handler() http.Handler {
	if s.Log == nil {
		s.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if s.Started.IsZero() {
		s.Started = time.Now()
	}
	s.cur = s.Config
	if s.Limiter == nil {
		s.Limiter = auth.NewLoginLimiter()
	}
	mux := http.NewServeMux()

	known := map[string]bool{}
	// route registers an endpoint under /api/ and /api/v1/.
	route := func(method, p string, h http.HandlerFunc) {
		mux.HandleFunc(method+" /api/"+p, h)
		mux.HandleFunc(method+" /api/v1/"+p, h)
		known["/api/"+p], known["/api/v1/"+p] = true, true
	}
	const user, admin = false, true

	// Any signed-in user (open on loopback until the first account exists).
	route("GET", "status", s.gate(user, s.handleStatus))
	route("GET", "storage", s.gate(user, s.handleStorage))
	route("GET", "remote", s.gate(user, s.handleRemote))
	route("GET", "files", s.gate(user, s.handleFilesStatus))
	route("POST", "account/password", s.gate(user, s.handleChangePassword))
	route("POST", "account/totp/setup", s.gate(user, s.handleTOTPSetup))
	route("POST", "account/totp/enable", s.gate(user, s.handleTOTPEnable))
	route("POST", "account/totp/disable", s.gate(user, s.handleTOTPDisable))

	// Admins only.
	route("GET", "disks", s.gate(admin, s.handleDisks))
	route("GET", "settings", s.gate(admin, s.handleSettings))
	route("GET", "shortcuts", s.gate(admin, s.handleShortcuts))
	route("GET", "services", s.gate(admin, s.handleServices))
	route("POST", "pool", s.gate(admin, s.handleAdopt))
	route("DELETE", "pool", s.gate(admin, s.handleForget))
	route("POST", "power/off", s.gate(admin, s.handlePowerOff))
	route("GET", "users", s.gate(admin, s.handleListUsers))
	route("POST", "users", s.gate(admin, s.handleCreateUser))
	route("PUT", "users/{name}", s.gate(admin, s.handleUpdateUser))
	route("DELETE", "users/{name}", s.gate(admin, s.handleDeleteUser))
	route("POST", "users/{name}/password", s.gate(admin, s.handleResetPassword))

	// Signing in and out.
	route("GET", "session", s.handleSession)
	route("POST", "auth/login", s.handlePasswordLogin)
	route("POST", "logout", s.handleLogout)
	route("POST", "local-login", s.handleLocalLogin)
	mux.HandleFunc("GET /login", s.handleLogin)

	// Files (SFTPGo web client) behind Vault's sign-in. The dashboard's
	// own /files page is registered exactly so it is not redirected into
	// the proxy.
	static := s.staticHandler()
	mux.Handle(files.WebRoot+"/", s.filesProxy())
	mux.Handle("GET "+files.WebRoot, static)

	for _, p := range planned {
		p := p
		h := func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusNotImplemented, map[string]any{
				"error":     "not_implemented",
				"message":   p.feature + " arrives in Milestone " + strconv.Itoa(p.milestone) + ".",
				"milestone": p.milestone,
			})
		}
		mux.HandleFunc(p.method+" /api/"+p.path, h)
		if !strings.HasPrefix(p.path, "v1/") {
			mux.HandleFunc(p.method+" /api/v1/"+p.path, h)
		}
	}

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		if known[r.URL.Path] {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed.")
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "Unknown API endpoint.")
	})
	mux.Handle("/", static)

	extraHosts := append([]string{}, s.Config.Security.AllowedHosts...)
	if s.Config.Remote.Domain != "" {
		extraHosts = append(extraHosts, s.Config.Remote.Domain)
	}

	var h http.Handler = mux
	h = limitBody(h)
	h = newRateLimiter(20, 60).wrap(h)
	h = newHostGuard(extraHosts).wrap(h)
	h = securityHeaders(h)
	h = logRequests(s.Log, h)
	h = recoverPanics(s.Log, h)
	return h
}

type plannedEndpoint struct {
	method, path, feature string
	milestone             int
}

// planned lists API endpoints that exist in the design but are delivered by
// later milestones.
var planned = []plannedEndpoint{
	{"POST", "upload-session", "Upload to Vault", 4},
	{"POST", "download-session", "Download from Vault", 5},
	{"POST", "share", "Share links", 5},
	{"DELETE", "share/{id}", "Share links", 5},
	{"POST", "remote", "Remote access", 6},
	{"POST", "v1/beam/upload-session", "Beam upload sessions", 4},
	{"POST", "v1/beam/download-session", "Beam download sessions", 5},
	{"POST", "v1/beam/share", "Beam shares", 5},
}

// StatusResponse is returned by GET /api/status.
type StatusResponse struct {
	Name          string                `json:"name"`
	Demo          bool                  `json:"demo,omitempty"`
	Version       string                `json:"version"`
	Milestone     int                   `json:"milestone"`
	Hostname      string                `json:"hostname"`
	UptimeSeconds int64                 `json:"uptime_seconds"`
	Listen        string                `json:"listen"`
	SetupComplete bool                  `json:"setup_complete"`
	Storage       storage.Status        `json:"storage"`
	Drives        *disks.Summary        `json:"drives"`
	DrivesError   string                `json:"drives_error,omitempty"`
	SystemDisk    bool                  `json:"system_disk_detected"`
	Remote        RemoteStatus          `json:"remote"`
	Users         UsersStatus           `json:"users"`
	Services      []services.Component  `json:"services"`
	Files         files.Status          `json:"files"`
	Shortcuts     []ShortcutStatusBrief `json:"shortcuts"`
	Warnings      []string              `json:"warnings,omitempty"`
}

// RemoteStatus summarises remote access.
type RemoteStatus struct {
	Enabled   bool   `json:"enabled"`
	Provider  string `json:"provider,omitempty"`
	Domain    string `json:"domain,omitempty"`
	State     string `json:"state"`
	Milestone int    `json:"milestone"`
}

// UsersStatus summarises user accounts.
type UsersStatus struct {
	Count int `json:"count"`
}

func (s *Server) usersStatus() UsersStatus {
	if s.Users == nil {
		return UsersStatus{}
	}
	return UsersStatus{Count: s.Users.Count()}
}

// ShortcutStatusBrief is the dashboard view of one shortcut.
type ShortcutStatusBrief struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Combo  string `json:"combo"`
	Status string `json:"status"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	host, _ := os.Hostname()
	inv, invErr := s.Disks.Inventory(ctx, false)
	if invErr != nil {
		inv = nil
	}
	st := s.storageStatus(ctx, inv)
	resp := StatusResponse{
		Name:          "Omarchy Vault",
		Demo:          s.Demo,
		Version:       version.Version,
		Milestone:     version.Milestone,
		Hostname:      host,
		UptimeSeconds: int64(time.Since(s.Started).Seconds()),
		Listen:        s.config().Listen,
		SetupComplete: st.Configured,
		Storage:       st,
		Remote:        s.remoteStatus(),
		Users:         s.usersStatus(),
		Services:      s.servicesStatus(ctx),
	}
	if s.Files != nil {
		resp.Files = s.Files.Status()
	} else {
		resp.Files = files.Status{State: files.StateNotInstalled, URL: files.ClientPath}
	}
	s.mu.RLock()
	cfgErr := s.ConfigErr
	s.mu.RUnlock()
	if cfgErr != nil {
		resp.Warnings = append(resp.Warnings, "Your Vault settings file has a problem, so defaults are in use. Run `vaultctl doctor` for details.")
	}
	if st.Configured && st.State != storage.StateReady && st.Message != "" {
		resp.Warnings = append(resp.Warnings, st.Message)
	}
	if invErr != nil {
		resp.DrivesError = "Drives could not be listed."
		s.Log.Warn("disk inventory failed", "err", invErr)
	} else {
		sum := inv.Summary
		resp.Drives = &sum
		resp.SystemDisk = inv.SystemDiskDetected
		resp.Warnings = append(resp.Warnings, inv.Warnings...)
	}
	rep := s.Shortcuts.Analyze(ctx)
	for _, c := range rep.Checks {
		resp.Shortcuts = append(resp.Shortcuts, ShortcutStatusBrief{ID: c.ID, Label: c.Label, Combo: c.Combo, Status: c.Status})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDisks(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("refresh") == "1"
	inv, err := s.Disks.Inventory(r.Context(), force)
	if err != nil {
		s.Log.Warn("disk inventory failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "disks_unavailable", "Drives could not be listed. Is util-linux installed?")
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

// StorageResponse is returned by GET /api/storage.
type StorageResponse struct {
	storage.Status
	Candidates []Candidate `json:"candidates"`
}

// Candidate is a mounted filesystem that could become Vault storage.
type Candidate struct {
	Disk        string `json:"disk"`
	DisplayName string `json:"display_name"`
	Volume      string `json:"volume"`
	Mountpoint  string `json:"mountpoint"`
	FSType      string `json:"fstype"`
	SizeBytes   int64  `json:"size_bytes"`
	FreeBytes   int64  `json:"free_bytes"`
	Health      string `json:"health"`
}

func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	inv, err := s.Disks.Inventory(r.Context(), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		inv = nil
	}
	resp := StorageResponse{Status: s.storageStatus(r.Context(), inv), Candidates: []Candidate{}}
	if inv != nil {
		for _, d := range inv.Disks {
			for _, v := range d.Volumes {
				if !v.Adoptable || len(v.Mountpoints) == 0 {
					continue
				}
				resp.Candidates = append(resp.Candidates, Candidate{
					Disk: d.Name, DisplayName: d.DisplayName, Volume: v.Name,
					Mountpoint: v.Mountpoints[0], FSType: v.FSType,
					SizeBytes: v.FSSize, FreeBytes: v.FSAvail, Health: string(d.Health.Status),
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// SettingsResponse is returned by GET /api/settings. Vault stores no secrets
// in config.json, and future secret-bearing fields must be redacted here.
type SettingsResponse struct {
	Config      config.Config `json:"config"`
	ConfigFound bool          `json:"config_found"`
	ConfigError string        `json:"config_error,omitempty"`
	ReadOnly    bool          `json:"read_only"`
}

func (s *Server) handleSettings(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	resp := SettingsResponse{Config: s.cur, ConfigFound: s.ConfigFound, ReadOnly: true}
	if s.ConfigErr != nil {
		resp.ConfigError = s.ConfigErr.Error()
	}
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleShortcuts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Shortcuts.Analyze(r.Context()))
}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"services": s.servicesStatus(r.Context())})
}

func (s *Server) servicesStatus(ctx context.Context) []services.Component {
	comps := services.MarkSelfRunning(services.Check(ctx, s.Run))
	if s.Files != nil {
		st := s.Files.Status()
		comps = services.MarkFiles(comps, st.Installed, st.Running, s.Files.Paths.Binary)
	}
	return comps
}

func (s *Server) remoteStatus() RemoteStatus {
	c := s.config()
	rs := RemoteStatus{Enabled: c.Remote.Enabled, Provider: c.Remote.Provider, Domain: c.Remote.Domain, Milestone: 6}
	if rs.Enabled {
		rs.State = "unknown"
	} else {
		rs.State = "not_configured"
	}
	return rs
}

func (s *Server) handleRemote(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.remoteStatus())
}

func (s *Server) staticHandler() http.Handler {
	var index []byte
	if s.Static != nil {
		index, _ = fs.ReadFile(s.Static, "index.html")
	}
	if index == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(placeholderPage))
		})
	}
	files := http.FileServerFS(s.Static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed.")
			return
		}
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if info, err := fs.Stat(s.Static, p); err == nil && !info.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback: every other path renders the app shell.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

const placeholderPage = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Omarchy Vault</title>
<meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body>
<h1>VAULT</h1><p>The web UI has not been built into this binary.</p>
<p>Run <code>make web</code> then <code>make build</code>. The API is available at <code>/api/status</code>.</p>
</body></html>`

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": code, "message": msg})
}

// handlePowerOff turns Vault off: the reply is sent first, then vaultd
// shuts down cleanly (systemd does not restart a clean exit).
func (s *Server) handlePowerOff(w http.ResponseWriter, r *http.Request) {
	if s.PowerOff == nil {
		writeError(w, http.StatusNotImplemented, "unavailable", "Vault cannot turn itself off in this mode.")
		return
	}
	s.Log.Info("turning off", "by", identityFrom(r).Username)
	writeJSON(w, http.StatusOK, map[string]string{"message": "Vault is turning off. Turn it on again with Super+Shift+V or `vaultctl on`."})
	go func() {
		time.Sleep(300 * time.Millisecond)
		s.PowerOff()
	}()
}
