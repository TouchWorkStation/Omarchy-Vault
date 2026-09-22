// Package config loads and validates Vault's persistent configuration.
//
// Configuration lives at ~/.config/omarchy-vault/config.json. In Milestone 1
// Vault only reads it; adoption of storage (Milestone 2) is the first feature
// that writes it.
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
}

// Source is a storage location adopted into the Vault.
type Source struct {
	// Path is the existing mount point or directory being used.
	Path string `json:"path"`
	// UUID is the filesystem UUID, used to detect a drive being swapped.
	UUID string `json:"uuid,omitempty"`
	// Label is a human friendly name, e.g. "WD Red 8 TB".
	Label string `json:"label,omitempty"`
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
	}
	switch c.Pool.Mode {
	case "none", "single", "combined":
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
