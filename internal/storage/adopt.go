package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/disks"
)

// DefaultFolders are created on first setup when missing. "Phone Uploads"
// follows preferences.upload_folder.
func DefaultFolders(uploadFolder string) []string {
	return []string{"Photos", "Documents", "Backups", "Projects", uploadFolder, "Shared"}
}

// AdoptRequest asks Vault to use one mounted volume.
type AdoptRequest struct {
	// Volume is the device name from the inventory, e.g. "sda1". Clients
	// never pass raw paths: the mount point comes from Vault's own scan.
	Volume string `json:"volume"`
	// Folder is created (or reused) on the drive. Empty = whole drive.
	Folder string `json:"folder"`
	// CreateFolders creates the default folders inside the Vault.
	CreateFolders bool `json:"create_folders"`
}

// AdoptResult reports what adoption did.
type AdoptResult struct {
	Source   config.Source `json:"source"`
	DataDir  string        `json:"data_dir"`
	Created  []string      `json:"created"`
	Existing []string      `json:"existing"`
	Skipped  []string      `json:"skipped"`
}

// AdoptError is a refusal with a user-facing reason.
type AdoptError struct {
	Code   string // not_found | not_adoptable | system_disk | unsafe_path | unknown_system_disk | io
	Reason string
}

func (e *AdoptError) Error() string { return e.Reason }

func refuse(code, format string, args ...any) error {
	return &AdoptError{Code: code, Reason: fmt.Sprintf(format, args...)}
}

// Adopt validates req against a fresh inventory and prepares the Vault
// folder on the chosen drive. It creates folders only; it never formats,
// mounts, deletes or overwrites anything.
func Adopt(inv *disks.Inventory, mounts MountTable, req AdoptRequest, uploadFolder string, now time.Time) (*AdoptResult, error) {
	if inv == nil || !inv.SystemDiskDetected {
		return nil, refuse("unknown_system_disk", "Vault could not identify the system drive, so it will not use any drive right now.")
	}
	if err := config.ValidateFolder(req.Folder); err != nil {
		return nil, refuse("unsafe_path", "%v", err)
	}

	var disk *disks.Disk
	var vol *disks.Volume
	for i := range inv.Disks {
		for j := range inv.Disks[i].Volumes {
			if inv.Disks[i].Volumes[j].Name == req.Volume {
				disk, vol = &inv.Disks[i], &inv.Disks[i].Volumes[j]
			}
		}
	}
	if vol == nil {
		return nil, refuse("not_found", "That drive is no longer connected. Rescan and try again.")
	}
	if disk.System || disk.Protected || vol.Status == disks.VolSystem {
		return nil, refuse("system_disk", "%s runs Omarchy. Vault will never use it for storage.", disk.DisplayName)
	}
	if !vol.Adoptable || len(vol.Mountpoints) == 0 {
		why := "it cannot be used as it is"
		if len(vol.Notes) > 0 {
			why = strings.TrimSuffix(vol.Notes[0], ".")
		}
		return nil, refuse("not_adoptable", "Vault cannot use %s: %s.", vol.Name, why)
	}
	mount := vol.Mountpoints[0]

	// The kernel must agree that the drive is mounted there right now.
	if !mounts.IsMountPoint(mount) {
		return nil, refuse("not_adoptable", "%s is not mounted at %s any more.", vol.Name, mount)
	}

	dataDir := mount
	if req.Folder != "" {
		dataDir = filepath.Join(mount, req.Folder)
	}
	res := &AdoptResult{DataDir: dataDir, Created: []string{}, Existing: []string{}, Skipped: []string{}}

	root, err := os.OpenRoot(mount)
	if err != nil {
		return nil, refuse("io", "Could not open %s: %v", mount, err)
	}
	defer root.Close()

	// Create the Vault folder one component at a time. os.Root refuses to
	// follow symlinks out of the drive; Lstat refuses symlinks inside it.
	if req.Folder != "" {
		parts := strings.Split(req.Folder, "/")
		for i := range parts {
			rel := filepath.Join(parts[:i+1]...)
			created, err := ensureDir(root, rel)
			if err != nil {
				return nil, err
			}
			if created && i == len(parts)-1 {
				res.Created = append(res.Created, req.Folder)
			}
		}
	}

	// Belt and braces: the folder must resolve to itself and live on the
	// adopted filesystem, not on a different mount nested inside it.
	resolvedMount, err := filepath.EvalSymlinks(mount)
	if err != nil {
		return nil, refuse("io", "Could not resolve %s: %v", mount, err)
	}
	resolved, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		return nil, refuse("io", "Could not resolve %s: %v", dataDir, err)
	}
	want := resolvedMount
	if req.Folder != "" {
		want = filepath.Join(resolvedMount, req.Folder)
	}
	if resolved != want {
		return nil, refuse("unsafe_path", "%s leads somewhere else; Vault will not follow it.", dataDir)
	}
	if c := mounts.Containing(dataDir); c != mount {
		return nil, refuse("unsafe_path", "%s is on a different filesystem (%s).", dataDir, c)
	}

	if req.CreateFolders {
		sub := root
		if req.Folder != "" {
			sub, err = root.OpenRoot(req.Folder)
			if err != nil {
				return nil, refuse("io", "Could not open %s: %v", dataDir, err)
			}
			defer sub.Close()
		}
		for _, name := range DefaultFolders(uploadFolder) {
			created, err := ensureDir(sub, name)
			var ae *AdoptError
			switch {
			case errors.As(err, &ae):
				res.Skipped = append(res.Skipped, name)
			case err != nil:
				return nil, err
			case created:
				res.Created = append(res.Created, name)
			default:
				res.Existing = append(res.Existing, name)
			}
		}
	}

	res.Source = config.Source{
		Path:    mount,
		Folder:  req.Folder,
		UUID:    vol.UUID,
		Volume:  vol.Name,
		Label:   disk.DisplayName,
		AddedAt: now.UTC().Format(time.RFC3339),
	}
	return res, nil
}

// ensureDir creates rel inside root if missing. An existing folder is
// reused; an existing file or symlink is refused and left untouched.
func ensureDir(root *os.Root, rel string) (created bool, err error) {
	info, err := root.Lstat(rel)
	switch {
	case err == nil:
		if info.Mode()&fs.ModeSymlink != 0 {
			return false, refuse("unsafe_path", "%s is a link; Vault will not follow it.", rel)
		}
		if !info.IsDir() {
			return false, refuse("unsafe_path", "%s already exists and is not a folder; it was left alone.", rel)
		}
		return false, nil
	case errors.Is(err, fs.ErrNotExist):
		if err := root.Mkdir(rel, 0o750); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return false, nil
			}
			return false, refuse("io", "Could not create %s: %v", rel, err)
		}
		return true, nil
	default:
		return false, refuse("io", "Could not check %s: %v", rel, err)
	}
}
