package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
)

// Vault's logical root is reached through two symlinks:
//
//	/srv/vault  ->  ~/.local/share/omarchy-vault/current  ->  /mnt/drive/Vault
//
// The first is created once by the installer (with sudo, after asking).
// The second is owned by the user, so vaultd switches storage without any
// root privileges. If the drive is unplugged the chain dangles instead of
// silently landing on the system disk.

// DefaultLinkPath returns ~/.local/share/omarchy-vault/current.
func DefaultLinkPath() (string, error) {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "omarchy-vault", "current"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "omarchy-vault", "current"), nil
}

// ErrNotSymlink is returned when Vault would have to replace something that
// is not a symlink. Vault never removes real files or folders.
var ErrNotSymlink = errors.New("storage: refusing to replace a real file or folder")

// SetLink atomically points link at target.
func SetLink(link, target string) error {
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	if info, err := os.Lstat(link); err == nil && info.Mode()&fs.ModeSymlink == 0 {
		return fmt.Errorf("%w: %s", ErrNotSymlink, link)
	}
	suffix, err := auth.Random(6)
	if err != nil {
		return err
	}
	tmp := link + ".tmp-" + suffix
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	if err := os.Rename(tmp, link); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("storage: %w", err)
	}
	return nil
}

// RemoveLink removes link if it is a symlink. Missing is fine.
func RemoveLink(link string) error {
	info, err := os.Lstat(link)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		return fmt.Errorf("%w: %s", ErrNotSymlink, link)
	}
	return os.Remove(link)
}

// LinkStatus describes the /srv/vault shortcut.
type LinkStatus struct {
	Path   string `json:"path"`
	State  string `json:"state"` // ok | missing | elsewhere | not_link
	Target string `json:"target,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// CheckRootLink reports whether root (usually /srv/vault) points at link.
func CheckRootLink(root, link string) LinkStatus {
	st := LinkStatus{Path: root}
	info, err := os.Lstat(root)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		st.State = "missing"
		st.Fix = "vaultctl link"
		return st
	case err != nil:
		st.State = "missing"
		return st
	case info.Mode()&fs.ModeSymlink == 0:
		st.State = "not_link"
		return st
	}
	target, err := os.Readlink(root)
	if err != nil {
		st.State = "missing"
		return st
	}
	st.Target = target
	if filepath.Clean(target) == filepath.Clean(link) {
		st.State = "ok"
	} else {
		st.State = "elsewhere"
	}
	return st
}
