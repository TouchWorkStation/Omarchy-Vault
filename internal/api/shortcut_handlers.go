package api

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/shortcuts"
)

// ShortcutsResponse is GET /api/shortcuts: the conflict report for the
// default keys, what is installed now, and the choices the editor offers.
type ShortcutsResponse struct {
	shortcuts.Report
	Current []shortcuts.Planned `json:"current"`
	Mods    []string            `json:"mod_choices"`
	Keys    []string            `json:"key_choices"`
	File    string              `json:"file"`
}

func (s *Server) handleShortcuts(w http.ResponseWriter, r *http.Request) {
	cur := shortcuts.Installed(s.Shortcuts.Home)
	if cur == nil {
		cur = []shortcuts.Planned{}
	}
	writeJSON(w, http.StatusOK, ShortcutsResponse{
		Report:  s.Shortcuts.Analyze(r.Context()),
		Current: cur,
		Mods:    shortcuts.ModChoices,
		Keys:    shortcuts.KeyChoices(),
		File:    shortcuts.FileFor(s.Shortcuts.Home, s.Shortcuts.ConfigPath),
	})
}

type shortcutsRequest struct {
	Bindings []shortcuts.Request `json:"bindings"`
}

func (s *Server) readShortcutRequest(w http.ResponseWriter, r *http.Request) ([]shortcuts.Request, bool) {
	var req shortcutsRequest
	if err := decode(r, &req); err != nil || len(req.Bindings) > len(shortcuts.Plan()) {
		writeError(w, http.StatusBadRequest, "bad_request", "The request could not be read.")
		return nil, false
	}
	seen := map[string]bool{}
	for _, b := range req.Bindings {
		if seen[b.ID] {
			writeError(w, http.StatusBadRequest, "bad_request", "Each shortcut can be set once.")
			return nil, false
		}
		seen[b.ID] = true
	}
	return req.Bindings, true
}

// handleCheckShortcuts checks keys the user is trying, without changing
// anything.
func (s *Server) handleCheckShortcuts(w http.ResponseWriter, r *http.Request) {
	reqs, ok := s.readShortcutRequest(w, r)
	if !ok {
		return
	}
	existing, known := s.Shortcuts.Bindings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"results": shortcuts.CheckAll(existing, reqs), "checked": known})
}

// handleSaveShortcuts installs the chosen keys. Every one must be free:
// nothing the user already has is ever replaced. An empty list removes
// Vault's shortcuts.
func (s *Server) handleSaveShortcuts(w http.ResponseWriter, r *http.Request) {
	reqs, ok := s.readShortcutRequest(w, r)
	if !ok {
		return
	}
	if len(reqs) == 0 {
		s.removeShortcuts(w, r)
		return
	}
	existing, known := s.Shortcuts.Bindings(r.Context())
	if !known {
		writeError(w, http.StatusConflict, "unchecked", "No Hyprland configuration was found, so Vault can't check for conflicts and won't add shortcuts.")
		return
	}
	if _, err := os.Stat(s.Shortcuts.ConfigPath); err != nil {
		writeError(w, http.StatusConflict, "no_config", "Vault couldn't find your Hyprland config file ("+s.Shortcuts.ConfigPath+"), so nothing was changed. Run `vaultctl shortcuts install` in a terminal to see the lines to add yourself.")
		return
	}
	results := shortcuts.CheckAll(existing, reqs)
	var bs []shortcuts.Planned
	for i, res := range results {
		if res.Status != "available" {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "shortcut_taken", "message": "Pick free keys for every shortcut first; nothing was changed.", "results": results,
			})
			return
		}
		p, _ := shortcuts.Build(reqs[i])
		bs = append(bs, p)
	}
	bs = shortcuts.WithCommandPath(bs, vaultctlPath())
	if _, err := shortcuts.Install(s.Shortcuts.Home, s.Shortcuts.ConfigPath, bs); err != nil {
		s.Log.Warn("installing shortcuts failed", "err", err)
		writeError(w, http.StatusInternalServerError, "install_failed", "The shortcuts could not be saved: "+err.Error())
		return
	}
	shortcuts.Reload(r.Context(), s.Run)
	s.Log.Info("shortcuts saved", "count", len(bs))
	s.handleShortcuts(w, r)
}

func (s *Server) handleRemoveShortcuts(w http.ResponseWriter, r *http.Request) {
	s.removeShortcuts(w, r)
}

func (s *Server) removeShortcuts(w http.ResponseWriter, r *http.Request) {
	if err := shortcuts.Remove(s.Shortcuts.Home, s.Shortcuts.ConfigPath); err != nil {
		writeError(w, http.StatusInternalServerError, "remove_failed", "The shortcuts could not be removed: "+err.Error())
		return
	}
	shortcuts.Reload(r.Context(), s.Run)
	s.Log.Info("shortcuts removed")
	s.handleShortcuts(w, r)
}

// vaultctlPath finds vaultctl next to this vaultd (both are installed to
// ~/.local/bin), then on PATH.
func vaultctlPath() string {
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "vaultctl")
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath("vaultctl"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
	}
	return ""
}
