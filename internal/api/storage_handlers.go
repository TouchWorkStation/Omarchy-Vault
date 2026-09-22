package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/storage"
)

// PoolRequest is the body of POST /api/pool.
type PoolRequest struct {
	// Mode is "single" (default). "combined" arrives in Milestone 7.
	Mode string `json:"mode"`
	// Volume is a device name from GET /api/disks, e.g. "sda1".
	Volume string `json:"volume"`
	// Folder on the drive; omitted means "Vault", "" means the whole drive.
	Folder *string `json:"folder"`
	// CreateFolders creates Photos, Documents, … when missing. Default true.
	CreateFolders *bool `json:"create_folders"`
	// Replace must be true to switch away from already configured storage.
	Replace bool `json:"replace"`
}

// PoolResponse is returned by POST and DELETE /api/pool.
type PoolResponse struct {
	Storage storage.Status       `json:"storage"`
	Result  *storage.AdoptResult `json:"result,omitempty"`
	Notes   []string             `json:"notes,omitempty"`
}

var adoptStatus = map[string]int{
	"not_found":           http.StatusNotFound,
	"system_disk":         http.StatusForbidden,
	"not_adoptable":       http.StatusConflict,
	"unknown_system_disk": http.StatusConflict,
	"unsafe_path":         http.StatusBadRequest,
	"io":                  http.StatusInternalServerError,
}

func (s *Server) handleAdopt(w http.ResponseWriter, r *http.Request) {
	var req PoolRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "The request could not be read.")
		return
	}
	switch req.Mode {
	case "", "single":
	case "combined":
		writeJSON(w, http.StatusNotImplemented, map[string]any{
			"error": "not_implemented", "milestone": 7,
			"message": "Combining several drives into one Vault arrives in Milestone 7. Choose one drive for now.",
		})
		return
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "Unknown mode.")
		return
	}
	if req.Volume == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Choose a drive.")
		return
	}
	folder := "Vault"
	if req.Folder != nil {
		folder = *req.Folder
	}
	createFolders := req.CreateFolders == nil || *req.CreateFolders

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	cfg := s.config()
	if cfg.Pool.Mode != "none" && !req.Replace {
		writeError(w, http.StatusConflict, "already_set_up", "Vault storage is already set up. Confirm that you want to switch drives.")
		return
	}
	if s.ConfigPath == "" {
		writeError(w, http.StatusInternalServerError, "no_config_path", "Vault does not know where to save settings.")
		return
	}

	// Always decide on a fresh scan, never on what the browser saw.
	inv, err := s.Disks.Inventory(r.Context(), true)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "disks_unavailable", "Drives could not be listed.")
		return
	}
	res, err := storage.Adopt(inv, s.mounts(), storage.AdoptRequest{
		Volume: req.Volume, Folder: folder, CreateFolders: createFolders,
	}, cfg.Preferences.UploadFolder, time.Now())
	if err != nil {
		var ae *storage.AdoptError
		if errors.As(err, &ae) {
			writeError(w, adoptStatus[ae.Code], ae.Code, ae.Reason)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
		return
	}

	previous := cfg.Sources
	cfg.Pool.Mode = "single"
	cfg.Sources = []config.Source{res.Source}
	if err := config.Save(s.ConfigPath, cfg); err != nil {
		s.Log.Error("saving config failed", "err", err)
		writeError(w, http.StatusInternalServerError, "save_failed", "Settings could not be saved. Nothing else was changed.")
		return
	}
	s.setConfig(cfg)
	s.Log.Info("vault storage set", "volume", res.Source.Volume, "data_dir", res.DataDir)

	resp := PoolResponse{Result: res}
	if s.DataLink != "" {
		if err := storage.SetLink(s.DataLink, res.DataDir); err != nil {
			s.Log.Warn("could not update data link", "err", err)
			resp.Notes = append(resp.Notes, "Vault's shortcut folder could not be updated; your storage still works.")
		}
	}
	for _, p := range previous {
		if p.DataDir() != res.DataDir {
			resp.Notes = append(resp.Notes, "Files already in "+p.DataDir()+" stay there. Vault did not move or delete them.")
		}
	}
	resp.Storage = s.storageStatus(r.Context(), inv)
	s.checkStorage(r.Context())
	s.syncFilesAsync()
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleForget(w http.ResponseWriter, r *http.Request) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	cfg := s.config()
	if cfg.Pool.Mode == "none" {
		writeJSON(w, http.StatusOK, PoolResponse{Storage: s.storageStatus(r.Context(), nil)})
		return
	}
	previous := cfg.Sources
	cfg.Pool.Mode = "none"
	cfg.Sources = []config.Source{}
	if err := config.Save(s.ConfigPath, cfg); err != nil {
		s.Log.Error("saving config failed", "err", err)
		writeError(w, http.StatusInternalServerError, "save_failed", "Settings could not be saved. Nothing was changed.")
		return
	}
	s.setConfig(cfg)
	resp := PoolResponse{}
	if s.DataLink != "" {
		if err := storage.RemoveLink(s.DataLink); err != nil {
			s.Log.Warn("could not remove data link", "err", err)
		}
	}
	for _, p := range previous {
		resp.Notes = append(resp.Notes, "Vault no longer uses "+p.DataDir()+". Every file is still there.")
	}
	s.Log.Info("vault storage released")
	if s.Files != nil {
		s.Files.SetWanted(false)
	}
	inv, _ := s.Disks.Inventory(r.Context(), false)
	resp.Storage = s.storageStatus(r.Context(), inv)
	writeJSON(w, http.StatusOK, resp)
}
