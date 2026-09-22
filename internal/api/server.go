// Package api serves Vault's local HTTP API and web UI.
//
// Milestone 1 exposes read-only endpoints only. Endpoints planned for later
// milestones are registered and answer 501 with the milestone that delivers
// them, so clients such as Beam can discover the API shape today.
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
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/disks"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/services"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/shortcuts"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/storage"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/sysexec"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/version"
)

// Server holds the dependencies of the HTTP API.
type Server struct {
	Config      config.Config
	ConfigFound bool
	ConfigErr   error
	Disks       *disks.Scanner
	Run         sysexec.Runner
	Shortcuts   shortcuts.Inspector
	// Static is the built web UI. When nil or empty a placeholder page is
	// served instead.
	Static fs.FS
	// Demo marks responses as sample data (vaultd --demo).
	Demo    bool
	Log     *slog.Logger
	Started time.Time
}

// Handler returns the full middleware-wrapped handler.
func (s *Server) Handler() http.Handler {
	if s.Log == nil {
		s.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if s.Started.IsZero() {
		s.Started = time.Now()
	}
	mux := http.NewServeMux()

	known := map[string]bool{}
	get := func(p string, h http.HandlerFunc) {
		mux.HandleFunc("GET /api/"+p, h)
		mux.HandleFunc("GET /api/v1/"+p, h)
		known["/api/"+p], known["/api/v1/"+p] = true, true
	}
	get("status", s.handleStatus)
	get("disks", s.handleDisks)
	get("storage", s.handleStorage)
	get("settings", s.handleSettings)
	get("shortcuts", s.handleShortcuts)
	get("services", s.handleServices)
	get("remote", s.handleRemote)

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
	mux.Handle("/", s.staticHandler())

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
	{"POST", "pool", "Using drives as Vault storage", 2},
	{"GET", "users", "User management", 3},
	{"POST", "users", "User management", 3},
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
	Count     *int `json:"count"`
	Milestone int  `json:"milestone"`
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
	st := storage.Inspect(s.Config)
	resp := StatusResponse{
		Name:          "Omarchy Vault",
		Demo:          s.Demo,
		Version:       version.Version,
		Milestone:     version.Milestone,
		Hostname:      host,
		UptimeSeconds: int64(time.Since(s.Started).Seconds()),
		Listen:        s.Config.Listen,
		SetupComplete: st.Configured,
		Storage:       st,
		Remote:        s.remoteStatus(),
		Users:         UsersStatus{Milestone: 3},
		Services:      services.MarkSelfRunning(services.Check(ctx, s.Run)),
	}
	if s.ConfigErr != nil {
		resp.Warnings = append(resp.Warnings, "Your Vault settings file has a problem, so defaults are in use. Run `vaultctl doctor` for details.")
	}
	if inv, err := s.Disks.Inventory(ctx, false); err != nil {
		resp.DrivesError = "Drives could not be listed."
		s.Log.Warn("disk inventory failed", "err", err)
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
	resp := StorageResponse{Status: storage.Inspect(s.Config), Candidates: []Candidate{}}
	if inv, err := s.Disks.Inventory(r.Context(), false); err == nil {
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
	resp := SettingsResponse{Config: s.Config, ConfigFound: s.ConfigFound, ReadOnly: true}
	if s.ConfigErr != nil {
		resp.ConfigError = s.ConfigErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleShortcuts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Shortcuts.Analyze(r.Context()))
}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"services": services.MarkSelfRunning(services.Check(r.Context(), s.Run))})
}

func (s *Server) remoteStatus() RemoteStatus {
	rs := RemoteStatus{Enabled: s.Config.Remote.Enabled, Provider: s.Config.Remote.Provider, Domain: s.Config.Remote.Domain, Milestone: 6}
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
