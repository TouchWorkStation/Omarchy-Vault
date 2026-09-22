// Package storage reports on the logical Vault root (default /srv/vault).
//
// Milestone 1 only reads: it reports whether the root exists and how much
// space the filesystem under it has. Adopting drives arrives in Milestone 2.
package storage

import (
	"errors"
	"io/fs"
	"os"
	"syscall"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
)

// Status describes the Vault root.
type Status struct {
	Configured bool     `json:"configured"`
	Root       string   `json:"root"`
	RootExists bool     `json:"root_exists"`
	PoolMode   string   `json:"pool_mode"`
	Sources    []string `json:"sources"`
	TotalBytes uint64   `json:"total_bytes"`
	UsedBytes  uint64   `json:"used_bytes"`
	FreeBytes  uint64   `json:"free_bytes"`
	Message    string   `json:"message,omitempty"`
}

// Usage is filesystem capacity for a path.
type Usage struct {
	Total, Used, Free uint64
}

// StatFS returns capacity for the filesystem containing path. Free is the
// space available to unprivileged users, matching what df shows.
func StatFS(path string) (Usage, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Usage{}, err
	}
	bs := uint64(st.Bsize)
	total := st.Blocks * bs
	free := st.Bavail * bs
	used := total - st.Bfree*bs
	return Usage{Total: total, Used: used, Free: free}, nil
}

// Inspect reports on the Vault root described by cfg.
func Inspect(cfg config.Config) Status {
	s := Status{
		Configured: cfg.Pool.Mode != "none" && len(cfg.Sources) > 0,
		Root:       cfg.VaultRoot,
		PoolMode:   cfg.Pool.Mode,
		Sources:    []string{},
	}
	for _, src := range cfg.Sources {
		s.Sources = append(s.Sources, src.Path)
	}
	info, err := os.Stat(cfg.VaultRoot)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		s.Message = "Vault storage has not been set up yet."
		return s
	case err != nil:
		s.Message = "Vault root could not be read."
		return s
	case !info.IsDir():
		s.Message = "Vault root exists but is not a folder."
		return s
	}
	s.RootExists = true
	if !s.Configured {
		s.Message = "Vault storage has not been set up yet."
		return s
	}
	u, err := StatFS(cfg.VaultRoot)
	if err != nil {
		s.Message = "Capacity could not be read."
		return s
	}
	s.TotalBytes, s.UsedBytes, s.FreeBytes = u.Total, u.Used, u.Free
	return s
}
