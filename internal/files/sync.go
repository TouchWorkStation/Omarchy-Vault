package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/users"
)

// managedTag marks SFTPGo users that Vault owns (and may delete).
const managedTag = "managed by omarchy-vault"

// Permissions are explicit lists, never "*": no symlink creation, chmod,
// chown or chtimes, so nothing a user does in Files can point outside the
// Vault or change file ownership.
var (
	permReadWrite = []string{"list", "download", "upload", "overwrite", "delete", "rename", "create_dirs"}
	permReadOnly  = []string{"list", "download"}
	permListOnly  = []string{"list"}
)

// Web client features Vault handles itself or does not offer yet.
var webClientLimits = []string{
	"password-change-disabled",     // passwords are changed in Vault
	"password-reset-disabled",      // no email reset
	"mfa-disabled",                 // 2FA is Vault's
	"api-key-auth-change-disabled", // no API keys
	"info-change-disabled",
	"publickey-change-disabled",
	"tls-cert-change-disabled",
	"shares-disabled", // share links come from Vault (Milestone 5)
}

// Only the web client (through Vault's proxy) is allowed for now.
var deniedProtocols = []string{"SSH", "FTP", "DAV"}

// FolderObjectName is SFTPGo's name for a Vault top-level folder. SFTPGo
// names allow only [a-zA-Z0-9-_.~], so it is a slug plus a short hash.
func FolderObjectName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	sum := sha256.Sum256([]byte(name))
	return "vault-" + strings.Trim(b.String(), "-") + "-" + hex.EncodeToString(sum[:3])
}

// Desired builds the SFTPGo user for a Vault user. root is the Vault's
// data link (~/.local/share/omarchy-vault/current): if the drive goes away
// the link dangles, so SFTPGo cannot write to the system disk.
func Desired(u users.User, root, homesDir string) (sftpUser, []folder) {
	su := sftpUser{
		Username:       u.Username,
		Password:       u.PasswordHash,
		Status:         1,
		AdditionalInfo: managedTag,
		Description:    "Omarchy Vault " + string(u.Role),
		Filters: userFilters{
			WebClient:       append([]string(nil), webClientLimits...),
			DeniedProtocols: append([]string(nil), deniedProtocols...),
		},
		Permissions:    map[string][]string{},
		VirtualFolders: []virtualFolder{},
	}
	if u.Disabled {
		su.Status = 0
	}
	if u.Role == users.Admin {
		su.HomeDir = root
		su.Permissions["/"] = permReadWrite
		return su, nil
	}
	su.HomeDir = filepath.Join(homesDir, u.Username)
	su.Permissions["/"] = permListOnly
	if u.Role == users.Guest {
		su.Filters.WebClient = append(su.Filters.WebClient, "write-disabled")
	}
	var folders []folder
	for _, f := range u.Folders {
		obj := folder{Name: FolderObjectName(f.Name), MappedPath: filepath.Join(root, f.Name)}
		folders = append(folders, obj)
		su.VirtualFolders = append(su.VirtualFolders, virtualFolder{Name: obj.Name, VirtualPath: "/" + f.Name})
		perm := permReadOnly
		if f.Access == users.ReadWrite && u.Role != users.Guest {
			perm = permReadWrite
		}
		su.Permissions["/"+f.Name] = perm
	}
	return su, folders
}

// Sync makes SFTPGo's users match Vault's. Users Vault did not create are
// never touched.
func Sync(ctx context.Context, c *Client, list []users.User, root, homesDir string) error {
	if c == nil {
		return errors.New("files: not running")
	}
	existing, err := c.listUsers(ctx)
	if err != nil {
		return fmt.Errorf("files: list users: %w", err)
	}
	have := map[string]sftpUser{}
	for _, u := range existing {
		have[u.Username] = u
	}
	var errs []error
	want := map[string]bool{}
	for _, u := range list {
		want[u.Username] = true
		su, folders := Desired(u, root, homesDir)
		if u.Role != users.Admin {
			if err := os.MkdirAll(su.HomeDir, 0o700); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		for _, f := range folders {
			if err := c.ensureFolder(ctx, f); err != nil {
				errs = append(errs, fmt.Errorf("folder %s: %w", f.MappedPath, err))
			}
		}
		prev, exists := have[u.Username]
		if exists && prev.AdditionalInfo != managedTag {
			errs = append(errs, fmt.Errorf("an SFTPGo user %q exists that Vault did not create; leaving it alone", u.Username))
			continue
		}
		if err := c.putUser(ctx, su, exists); err != nil {
			errs = append(errs, fmt.Errorf("user %s: %w", u.Username, err))
		}
	}
	for name, u := range have {
		if !want[name] && u.AdditionalInfo == managedTag {
			if err := c.deleteUser(ctx, name); err != nil {
				errs = append(errs, fmt.Errorf("remove %s: %w", name, err))
			}
		}
	}
	return errors.Join(errs...)
}
