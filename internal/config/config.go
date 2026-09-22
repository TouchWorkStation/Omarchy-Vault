// Package config loads and validates Vault's persistent configuration.
//
// Configuration lives at ~/.config/omarchy-vault/config.json (mode 0600, in
// a 0700 directory). It never holds secrets; those live in secrets/.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// CurrentVersion is the config schema version written by this build.
const CurrentVersion = 1

// DefaultListen is the address vaultd binds to unless configured otherwise.
const DefaultListen = "127.0.0.1:8788"

// DefaultVaultRoot is the logical Vault mount point.
const DefaultVaultRoot = "/srv/vault"

// Config is the on-disk configuration.
type Config struct {
	Version     int         `json:"version"`
	VaultRoot   string      `json:"vault_root"`
	Listen      string      `json:"listen"`
	Sources     []Source    `json:"sources"`
	Pool        Pool        `json:"pool"`
	Preferences Preferences `json:"preferences"`
	Remote      Remote      `json:"remote"`
	Services    Services    `json:"services"`
	Security    Security    `json:"security"`
	Transfer    Transfer    `json:"transfer"`
}

// Transfer configures how phones reach Vault for QR transfers. The
// listener only exists while a transfer link is active.
type Transfer struct {
	// Host is the address phones use; empty = this computer's LAN address.
	Host string `json:"host,omitempty"`
	// Port defaults to 8790.
	Port int `json:"port,omitempty"`
}

// Source is a storage location adopted into the Vault.
type Source struct {
	// Path is the mount point of the drive's filesystem.
	Path string `json:"path"`
	// Folder is the Vault's folder on that drive, relative to Path.
	// Empty means the whole drive.
	Folder string `json:"folder,omitempty"`
	// UUID is the filesystem UUID, used to detect a missing or swapped drive.
	UUID string `json:"uuid,omitempty"`
	// Volume is the device name at adoption time (e.g. "sda1"), for display.
	Volume string `json:"volume,omitempty"`
	// Label is a human friendly name, e.g. "WDC WD80EFZZ".
	Label string `json:"label,omitempty"`
	// AddedAt records when the source was adopted (RFC 3339).
	AddedAt string `json:"added_at,omitempty"`
}

// DataDir is the absolute folder holding the Vault's files on this source.
func (s Source) DataDir() string {
	if s.Folder == "" {
		return s.Path
	}
	return filepath.Join(s.Path, s.Folder)
}

// Pool describes how Sources are combined.
type Pool struct {
	// Mode is "none" (not set up), "single" or "combined".
	Mode string `json:"mode"`
}

// Preferences holds UI and transfer defaults.
type Preferences struct {
	UploadFolder          string `json:"upload_folder"`
	UploadExpiryMinutes   int    `json:"upload_expiry_minutes"`
	DownloadExpiryMinutes int    `json:"download_expiry_minutes"`
	DownloadMaxCount      int    `json:"download_max_count"`
}

// Remote holds remote access settings. Secrets are never stored here; the
// tunnel token lives in a separate 0600 file (Milestone 6).
type Remote struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider,omitempty"`
	Domain   string `json:"domain,omitempty"`
}

// Services lists optional integrations the user has turned on.
type Services struct {
	FileServer bool `json:"file_server"`
	LANSharing bool `json:"lan_sharing"`
	Tunnel     bool `json:"tunnel"`
}

// Security holds listener safety switches.
type Security struct {
	// AllowNonLoopbackListen must be true for Listen to be anything other
	// than a loopback address. Off by default.
	AllowNonLoopbackListen bool `json:"allow_non_loopback_listen"`
	// AllowedHosts are extra Host header values accepted by the web server
	// (for example the remote access domain). Loopback names are always
	// accepted.
	AllowedHosts []string `json:"allowed_hosts,omitempty"`
}

// Default returns the configuration used when no file exists.
func Default() Config {
	return Config{
		Version:   CurrentVersion,
		VaultRoot: DefaultVaultRoot,
		Listen:    DefaultListen,
		Sources:   []Source{},
		Pool:      Pool{Mode: "none"},
		Preferences: Preferences{
			UploadFolder:          "Phone Uploads",
			UploadExpiryMinutes:   10,
			DownloadExpiryMinutes: 10,
			DownloadMaxCount:      1,
		},
	}
}

// Dir returns the configuration directory, honouring XDG_CONFIG_HOME.
func Dir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "omarchy-vault"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: cannot find home directory: %w", err)
	}
	return filepath.Join(home, ".config", "omarchy-vault"), nil
}

// DefaultPath returns the default config.json path.
func DefaultPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads path. A missing file yields Default() and exists=false.
func Load(path string) (cfg Config, exists bool, err error) {
	cfg = Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, false, nil
	}
	if err != nil {
		return cfg, false, fmt.Errorf("config: read %s: %w", path, err)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Default(), true, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return Default(), true, err
	}
	return cfg, true, nil
}

// Validate checks the configuration for unsafe or inconsistent values.
func (c Config) Validate() error {
	var errs []error
	if c.Version != CurrentVersion {
		errs = append(errs, fmt.Errorf("unsupported config version %d (expected %d)", c.Version, CurrentVersion))
	}
	if err := validateAbsClean("vault_root", c.VaultRoot); err != nil {
		errs = append(errs, err)
	} else if c.VaultRoot == "/" {
		errs = append(errs, errors.New("vault_root must not be /"))
	}
	if err := ValidateListen(c.Listen, c.Security.AllowNonLoopbackListen); err != nil {
		errs = append(errs, err)
	}
	for i, s := range c.Sources {
		if err := validateAbsClean(fmt.Sprintf("sources[%d].path", i), s.Path); err != nil {
			errs = append(errs, err)
		}
		if err := ValidateFolder(s.Folder); err != nil {
			errs = append(errs, fmt.Errorf("sources[%d].folder: %w", i, err))
		}
	}
	switch c.Pool.Mode {
	case "none":
		if len(c.Sources) != 0 {
			errs = append(errs, errors.New("pool.mode none must have no sources"))
		}
	case "single":
		if len(c.Sources) != 1 {
			errs = append(errs, errors.New("pool.mode single needs exactly one source"))
		}
	case "combined":
		if len(c.Sources) < 2 {
			errs = append(errs, errors.New("pool.mode combined needs at least two sources"))
		}
	default:
		errs = append(errs, fmt.Errorf("pool.mode %q is not one of none, single, combined", c.Pool.Mode))
	}
	p := c.Preferences
	if p.UploadFolder == "" || strings.ContainsAny(p.UploadFolder, "/\\\x00") || p.UploadFolder == "." || p.UploadFolder == ".." {
		errs = append(errs, fmt.Errorf("preferences.upload_folder %q must be a single folder name", p.UploadFolder))
	}
	if p.UploadExpiryMinutes < 1 || p.UploadExpiryMinutes > 24*60 {
		errs = append(errs, errors.New("preferences.upload_expiry_minutes must be between 1 and 1440"))
	}
	if p.DownloadExpiryMinutes < 1 || p.DownloadExpiryMinutes > 24*60 {
		errs = append(errs, errors.New("preferences.download_expiry_minutes must be between 1 and 1440"))
	}
	if c.Transfer.Port < 0 || c.Transfer.Port > 65535 {
		errs = append(errs, errors.New("transfer.port must be between 1 and 65535"))
	}
	if c.Transfer.Host != "" {
		if ip := net.ParseIP(c.Transfer.Host); ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			errs = append(errs, errors.New("transfer.host must be this computer's LAN IP address"))
		}
	}
	if p.DownloadMaxCount < 0 {
		errs = append(errs, errors.New("preferences.download_max_count must not be negative"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("config: invalid: %w", errors.Join(errs...))
	}
	return nil
}

// ValidateListen ensures addr is host:port and, unless explicitly allowed,
// that the host is a loopback address.
func ValidateListen(addr string, allowNonLoopback bool) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen %q: %w", addr, err)
	}
	if port == "" {
		return fmt.Errorf("listen %q: missing port", addr)
	}
	if allowNonLoopback {
		return nil
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen %q is not a loopback address; set security.allow_non_loopback_listen to override", addr)
	}
	return nil
}

// ValidateFolder checks a Vault folder name relative to a drive: empty (the
// whole drive) or up to four plain path components, no "..", no hidden
// tricks.
func ValidateFolder(f string) error {
	if f == "" {
		return nil
	}
	if strings.ContainsAny(f, "\x00\\") || strings.HasPrefix(f, "/") || strings.HasSuffix(f, "/") {
		return fmt.Errorf("folder %q must be a relative name like \"Vault\"", f)
	}
	parts := strings.Split(f, "/")
	if len(parts) > 4 {
		return fmt.Errorf("folder %q is nested too deeply", f)
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || len(p) > 255 {
			return fmt.Errorf("folder %q is not a valid folder name", f)
		}
		for _, r := range p {
			if r < 0x20 || r == 0x7f {
				return fmt.Errorf("folder %q contains control characters", f)
			}
		}
	}
	return nil
}

// Save writes cfg to path atomically: a 0600 temporary file in the same
// 0700 directory, fsynced, then renamed over the old file.
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: create %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("config: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: replace %s: %w", path, err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

func validateAbsClean(field, p string) error {
	if p == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("%s %q must be an absolute path", field, p)
	}
	if filepath.Clean(p) != p {
		return fmt.Errorf("%s %q must be a clean path (no .., //, or trailing /)", field, p)
	}
	if strings.ContainsRune(p, 0) {
		return fmt.Errorf("%s contains a NUL byte", field)
	}
	return nil
}
