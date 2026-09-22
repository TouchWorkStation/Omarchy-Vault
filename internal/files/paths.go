// Package files runs and manages SFTPGo, the engine behind Vault's file
// browser, users' folders, and (optional, off by default) SFTP and WebDAV.
//
// Vault owns SFTPGo completely: it starts it as a child process bound to
// 127.0.0.1:8789 with web admin, SFTP and WebDAV switched off, creates the
// SFTPGo admin with a random password, and mirrors every Vault account into
// SFTPGo. Users only ever see "Files".
package files

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// PinnedVersion is the SFTPGo release the installer builds from source.
const PinnedVersion = "v2.7.6"

// Paths locates SFTPGo and Vault's SFTPGo state.
type Paths struct {
	Binary     string // sftpgo executable
	Assets     string // directory containing templates/ and static/
	ConfigDir  string // SFTPGo --config-dir (Vault keeps it empty; config is env)
	DataDir    string // SFTPGo database lives here
	HomesDir   string // empty private home folders for non-admin users
	SecretsDir string // admin password and signing key (0600)
}

// DataHome returns ~/.local/share (honouring XDG_DATA_HOME).
func DataHome() (string, error) {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" && filepath.IsAbs(x) {
		return x, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

// ErrNotInstalled means no usable SFTPGo was found.
var ErrNotInstalled = errors.New("files: SFTPGo is not installed")

func hasAssets(dir string) bool {
	for _, sub := range []string{"templates", "static"} {
		if info, err := os.Stat(filepath.Join(dir, sub)); err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// Locate finds SFTPGo, in order: $VAULT_SFTPGO (+ $VAULT_SFTPGO_ASSETS),
// the copy the installer builds into ~/.local/share/omarchy-vault/sftpgo,
// then a system install (sftpgo on PATH with /usr/share/sftpgo assets).
func Locate(dataHome string) (binary, assets string, err error) {
	if b := os.Getenv("VAULT_SFTPGO"); b != "" {
		a := os.Getenv("VAULT_SFTPGO_ASSETS")
		if a == "" {
			a = filepath.Dir(b)
		}
		if isExec(b) && hasAssets(a) {
			return b, a, nil
		}
	}
	own := filepath.Join(dataHome, "omarchy-vault", "sftpgo")
	if b := filepath.Join(own, "bin", "sftpgo"); isExec(b) && hasAssets(own) {
		return b, own, nil
	}
	if b, err := exec.LookPath("sftpgo"); err == nil {
		for _, a := range []string{"/usr/share/sftpgo", "/usr/local/share/sftpgo", "/etc/sftpgo"} {
			if hasAssets(a) {
				return b, a, nil
			}
		}
	}
	return "", "", ErrNotInstalled
}

func isExec(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
