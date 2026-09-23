package transfer

import (
	"archive/zip"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Sending files from anywhere on this computer (not only the Vault), for
// "copy a file in the file manager, press Super+Alt+D". Only the owner,
// through vaultctl and the local token, can create such a link; a
// download session's Path then holds absolute paths, one per line.

// ErrPrivate refuses paths holding secrets.
var ErrPrivate = errors.New("transfer: that location holds private keys or settings and can't be sent")

// IsLocal reports whether a session sends files from this computer rather
// than from the Vault.
func IsLocal(s Session) bool { return strings.HasPrefix(s.Path, "/") }

// LocalPaths are the absolute paths a local session sends.
func LocalPaths(s Session) []string {
	if !IsLocal(s) {
		return nil
	}
	return strings.Split(s.Path, "\n")
}

// private lists locations never sent, and folders containing them.
func private(home string) []string {
	return []string{
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".gnupg"),
		filepath.Join(home, ".password-store"),
		filepath.Join(home, ".config", "omarchy-vault"),
		filepath.Join(home, ".local", "share", "keyrings"),
		"/etc", "/proc", "/sys", "/dev", "/run", "/boot", "/root",
	}
}

func within(p, dir string) bool { return p == dir || strings.HasPrefix(p, dir+"/") }

// openRun reports whether p is inside /run but somewhere the file manager
// puts ordinary files: drives it mounts (/run/media/<user>/...) and network
// folders (/run/user/<uid>/gvfs/...). The rest of /run holds runtime state
// and stays private.
func openRun(p string) bool {
	if strings.HasPrefix(p, "/run/media/") {
		return true
	}
	parts := strings.Split(p, "/") // "", "run", "user", uid, "gvfs", ...
	return len(parts) > 5 && parts[1] == "run" && parts[2] == "user" && parts[4] == "gvfs"
}

// CheckLocal validates an absolute path the owner wants to send: it must
// be a plain file or folder (not a symlink), outside private locations,
// and not a folder that contains one (such as the whole home folder).
func CheckLocal(p, home string) (string, fs.FileInfo, error) {
	if !filepath.IsAbs(p) || strings.ContainsAny(p, "\n\x00") {
		return "", nil, ErrBadPath
	}
	p = filepath.Clean(p)
	if p == "/" {
		return "", nil, ErrPrivate
	}
	for _, d := range private(home) {
		if (within(p, d) && !openRun(p)) || within(d, p) {
			return "", nil, fmt.Errorf("%w (%s)", ErrPrivate, d)
		}
	}
	root, err := os.OpenRoot(filepath.Dir(p))
	if err != nil {
		return "", nil, ErrNoSuch
	}
	defer root.Close()
	info, err := Stat(root, filepath.Base(p))
	if err != nil {
		return "", nil, err
	}
	return p, info, nil
}

type localItem struct {
	root *os.Root
	name string
	info fs.FileInfo
}

func openLocal(paths []string) ([]localItem, func(), error) {
	home, _ := os.UserHomeDir()
	var items []localItem
	closeAll := func() {
		for _, it := range items {
			it.root.Close()
		}
	}
	for _, p := range paths {
		clean, info, err := CheckLocal(p, home)
		if err != nil {
			closeAll()
			return nil, func() {}, err
		}
		root, err := os.OpenRoot(filepath.Dir(clean))
		if err != nil {
			closeAll()
			return nil, func() {}, ErrNoSuch
		}
		items = append(items, localItem{root: root, name: filepath.Base(clean), info: info})
	}
	return items, closeAll, nil
}

// localSummary describes what a local session sends.
func localSummary(items []localItem) (name string, zipped bool, files int, total int64, err error) {
	for _, it := range items {
		if it.info.IsDir() {
			fs, t, err := Walk(it.root, it.name)
			if err != nil {
				return "", false, 0, 0, err
			}
			files += len(fs)
			total += t
		} else {
			files++
			total += it.info.Size()
		}
	}
	switch {
	case len(items) == 1 && !items[0].info.IsDir():
		return items[0].name, false, 1, total, nil
	case len(items) == 1:
		return items[0].name + ".zip", true, files, total, nil
	default:
		return fmt.Sprintf("%s and %d more.zip", strings.TrimSuffix(items[0].name, filepath.Ext(items[0].name)), len(items)-1), true, files, total, nil
	}
}

// serveLocal streams a local session: one file directly, anything else as
// one zip named after it.
func serveLocal(w http.ResponseWriter, r *http.Request, items []localItem, name string) error {
	if len(items) == 1 && !items[0].info.IsDir() {
		return ServeFile(w, r, items[0].root, items[0].name)
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", attachment(name))
	zw := zip.NewWriter(w)
	for _, it := range items {
		if it.info.IsDir() {
			files, _, err := Walk(it.root, it.name)
			if err != nil {
				return err
			}
			if err := zipFiles(zw, it.root, it.name, it.name, files); err != nil {
				return err
			}
			continue
		}
		if err := zipOne(zw, it.root, it.name, it.name, it.info.ModTime()); err != nil {
			return err
		}
	}
	return zw.Close()
}
