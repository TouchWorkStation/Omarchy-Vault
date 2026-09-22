// Package storage adopts drives into the Vault and reports on them.
//
// Adoption only creates folders on an already-mounted, non-system
// filesystem. Nothing here formats, partitions, mounts or deletes.
package storage

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/disks"
)

// State is the overall Vault storage state.
type State string

const (
	StateNotSetUp     State = "not_set_up"
	StateReady        State = "ready"
	StateDriveMissing State = "drive_missing"
	StateDriveMoved   State = "drive_moved"
	StateProblem      State = "problem"
)

// SourceStatus is the live state of one adopted source.
type SourceStatus struct {
	Label        string `json:"label"`
	Volume       string `json:"volume,omitempty"`
	Path         string `json:"path"`
	Folder       string `json:"folder,omitempty"`
	DataDir      string `json:"data_dir"`
	State        State  `json:"state"`
	CurrentMount string `json:"current_mount,omitempty"`
	Message      string `json:"message,omitempty"`
	TotalBytes   uint64 `json:"total_bytes"`
	UsedBytes    uint64 `json:"used_bytes"`
	FreeBytes    uint64 `json:"free_bytes"`
}

// Status describes the Vault's storage.
type Status struct {
	State      State          `json:"state"`
	Configured bool           `json:"configured"`
	Root       string         `json:"root"`
	RootLink   LinkStatus     `json:"root_link"`
	DataLink   string         `json:"data_link"`
	PoolMode   string         `json:"pool_mode"`
	Sources    []SourceStatus `json:"sources"`
	TotalBytes uint64         `json:"total_bytes"`
	UsedBytes  uint64         `json:"used_bytes"`
	FreeBytes  uint64         `json:"free_bytes"`
	Message    string         `json:"message,omitempty"`
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

// Inspect reports the live state of the configured storage. inv may be nil
// if drive discovery failed; mounts is the kernel mount table.
func Inspect(cfg config.Config, inv *disks.Inventory, mounts MountTable, dataLink string) Status {
	s := Status{
		State:    StateNotSetUp,
		Root:     cfg.VaultRoot,
		RootLink: CheckRootLink(cfg.VaultRoot, dataLink),
		DataLink: dataLink,
		PoolMode: cfg.Pool.Mode,
		Sources:  []SourceStatus{},
	}
	if cfg.Pool.Mode == "none" || len(cfg.Sources) == 0 {
		s.Message = "Vault storage has not been set up yet."
		return s
	}
	s.Configured = true
	s.State = StateReady
	for _, src := range cfg.Sources {
		ss := inspectSource(src, inv, mounts)
		if ss.State != StateReady && s.State == StateReady {
			s.State = ss.State
			s.Message = ss.Message
		}
		s.TotalBytes += ss.TotalBytes
		s.UsedBytes += ss.UsedBytes
		s.FreeBytes += ss.FreeBytes
		s.Sources = append(s.Sources, ss)
	}
	return s
}

func inspectSource(src config.Source, inv *disks.Inventory, mounts MountTable) SourceStatus {
	ss := SourceStatus{
		Label:   src.Label,
		Volume:  src.Volume,
		Path:    src.Path,
		Folder:  src.Folder,
		DataDir: src.DataDir(),
		State:   StateReady,
	}
	if ss.Label == "" {
		ss.Label = src.Path
	}

	// 1. Is the same filesystem still attached, and where?
	if inv != nil && src.UUID != "" {
		var found *disks.Volume
		var onSystem bool
		for _, d := range inv.Disks {
			for i := range d.Volumes {
				if d.Volumes[i].UUID == src.UUID {
					v := d.Volumes[i]
					found, onSystem = &v, d.System
				}
			}
		}
		switch {
		case found == nil:
			ss.State = StateDriveMissing
			ss.Message = ss.Label + " is not connected. Plug it in to use your Vault."
			return ss
		case onSystem:
			ss.State = StateProblem
			ss.Message = ss.Label + " now appears to hold the operating system. Vault has stopped using it."
			return ss
		case !contains(found.Mountpoints, src.Path):
			if len(found.Mountpoints) == 0 {
				ss.State = StateDriveMissing
				ss.Message = ss.Label + " is connected but not mounted."
			} else {
				ss.State = StateDriveMoved
				ss.CurrentMount = found.Mountpoints[0]
				ss.Message = ss.Label + " is now mounted at " + found.Mountpoints[0] + ". Choose it again in Storage to keep using it."
			}
			return ss
		}
	}

	// 2. The kernel must see the mount; an empty folder on the system disk
	// must never be mistaken for the drive.
	if !mounts.IsMountPoint(src.Path) {
		ss.State = StateDriveMissing
		ss.Message = ss.Label + " is not mounted at " + src.Path + "."
		return ss
	}

	// 3. The Vault folder must exist on that filesystem.
	info, err := os.Lstat(ss.DataDir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		ss.State = StateProblem
		ss.Message = "The Vault folder " + ss.DataDir + " is missing. Choose the drive again to recreate it."
		return ss
	case err != nil:
		ss.State = StateProblem
		ss.Message = "The Vault folder could not be read."
		return ss
	case !info.IsDir():
		ss.State = StateProblem
		ss.Message = ss.DataDir + " is not a folder."
		return ss
	}
	if c := mounts.Containing(ss.DataDir); c != src.Path {
		ss.State = StateProblem
		ss.Message = ss.DataDir + " is not on " + ss.Label + "."
		return ss
	}
	if resolved, err := filepath.EvalSymlinks(ss.DataDir); err != nil || mounts.Containing(resolved) != mounts.Containing(ss.DataDir) {
		ss.State = StateProblem
		ss.Message = ss.DataDir + " leads outside the drive."
		return ss
	}

	u, err := StatFS(ss.DataDir)
	if err != nil {
		ss.State = StateProblem
		ss.Message = "Capacity could not be read."
		return ss
	}
	ss.TotalBytes, ss.UsedBytes, ss.FreeBytes = u.Total, u.Used, u.Free
	return ss
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
