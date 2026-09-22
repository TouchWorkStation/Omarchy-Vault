// Package services reports which supporting tools and systemd units are
// present. It is read-only: it never installs, starts or stops anything.
package services

import (
	"context"
	"os/exec"
	"strings"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/sysexec"
)

// Component is a dependency Vault knows about.
type Component struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Purpose     string `json:"purpose"`
	Binary      string `json:"-"`
	Unit        string `json:"unit,omitempty"`
	UserUnit    bool   `json:"-"`
	Package     string `json:"package"`
	Milestone   int    `json:"milestone"`
	Required    bool   `json:"required"`
	Installed   bool   `json:"installed"`
	BinaryPath  string `json:"binary_path,omitempty"`
	UnitState   string `json:"unit_state,omitempty"`
	Description string `json:"description,omitempty"`
}

// Known is the fixed list of components. Unit names here are the only ones
// ever passed to systemctl.
func Known() []Component {
	return []Component{
		{ID: "lsblk", Name: "Drive discovery", Purpose: "Lists attached drives", Binary: "lsblk", Package: "util-linux", Milestone: 1, Required: true},
		{ID: "findmnt", Name: "Mount table", Purpose: "Identifies the system drive", Binary: "findmnt", Package: "util-linux", Milestone: 1, Required: true},
		{ID: "smartctl", Name: "Drive health", Purpose: "Reads SMART health data", Binary: "smartctl", Package: "smartmontools", Milestone: 1},
		{ID: "vault", Name: "Vault service", Purpose: "Runs the Vault dashboard and API", Binary: "vaultd", Unit: "omarchy-vault.service", UserUnit: true, Package: "omarchy-vault", Milestone: 1, Required: true},
		{ID: "mergerfs", Name: "Drive pooling", Purpose: "Combines several drives into one Vault", Binary: "mergerfs", Package: "mergerfs", Milestone: 7},
		{ID: "sftpgo", Name: "Files", Purpose: "Browser file access for every Vault user (SFTPGo, run by Vault)", Binary: "sftpgo", Package: "scripts/build-sftpgo.sh", Milestone: 3},
		{ID: "cloudflared", Name: "Remote access", Purpose: "Secure tunnel to your domain", Binary: "cloudflared", Unit: "cloudflared.service", Package: "cloudflared", Milestone: 6},
		{ID: "samba", Name: "LAN sharing", Purpose: "Optional Windows/macOS network share", Binary: "smbd", Unit: "smb.service", Package: "samba", Milestone: 7},
		{ID: "rsync", Name: "Computer backup", Purpose: "Copies folders into Vault", Binary: "rsync", Package: "rsync", Milestone: 8},
	}
}

// Check fills in install and unit state for every known component.
func Check(ctx context.Context, run sysexec.Runner) []Component {
	comps := Known()
	for i := range comps {
		c := &comps[i]
		if p, err := exec.LookPath(c.Binary); err == nil {
			c.Installed = true
			c.BinaryPath = p
		}
		if c.Unit == "" {
			continue
		}
		c.UnitState = unitState(ctx, run, c.Unit, c.UserUnit)
	}
	return comps
}

// unitState asks systemd whether unit is active. Only units from Known()
// reach this function.
func unitState(ctx context.Context, run sysexec.Runner, unit string, user bool) string {
	if !run.Available("systemctl") {
		return "unknown"
	}
	args := []string{"is-active", unit}
	if user {
		args = []string{"--user", "is-active", unit}
	}
	out, _ := run.Output(ctx, "systemctl", args...)
	state := strings.TrimSpace(string(out))
	switch state {
	case "active", "inactive", "failed", "activating", "deactivating", "reloading":
		return state
	case "":
		return "unknown"
	default:
		// systemctl prints "inactive" for units that do not exist on most
		// versions; anything else we do not recognise is reported as-is but
		// truncated so it cannot flood the UI.
		if len(state) > 32 {
			state = state[:32]
		}
		return state
	}
}

// MarkSelfRunning records that the Vault daemon answering the request is,
// by definition, running — even in development where vaultd is not on PATH
// or not started by systemd.
func MarkSelfRunning(comps []Component) []Component {
	for i := range comps {
		if comps[i].ID == "vault" {
			comps[i].Installed = true
			if comps[i].UnitState != "active" {
				comps[i].UnitState = "active"
				comps[i].Description = "running outside systemd"
			}
		}
	}
	return comps
}

// MarkFiles reports the file service as Vault sees it: SFTPGo may live in
// ~/.local/share/omarchy-vault (not on PATH) and runs as Vault's child.
func MarkFiles(comps []Component, installed, running bool, binary string) []Component {
	for i := range comps {
		if comps[i].ID == "sftpgo" {
			comps[i].Installed = installed
			if binary != "" {
				comps[i].BinaryPath = binary
			}
			switch {
			case running:
				comps[i].UnitState = "active"
			case installed:
				comps[i].UnitState = "inactive"
			}
		}
	}
	return comps
}
