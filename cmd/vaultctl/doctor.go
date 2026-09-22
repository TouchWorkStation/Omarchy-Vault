package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/storage"
)

type level string

const (
	lvOK   level = "ok"
	lvInfo level = "info"
	lvWarn level = "warn"
	lvFail level = "fail"
)

type finding struct {
	Check   string `json:"check"`
	Level   level  `json:"level"`
	Message string `json:"message"`
}

func (a *app) doctor(ctx context.Context) int {
	var fs []finding
	add := func(check string, lv level, format string, args ...any) {
		fs = append(fs, finding{Check: check, Level: lv, Message: fmt.Sprintf(format, args...)})
	}

	// Config validity and permissions.
	path, _ := config.DefaultPath()
	switch {
	case a.cfgErr != nil:
		add("config", lvFail, "%v", a.cfgErr)
	case !a.found:
		add("config", lvInfo, "no config yet at %s (created during setup in Milestone 2)", path)
	default:
		add("config", lvOK, "%s is valid", path)
		if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o022 != 0 {
			add("permissions", lvWarn, "%s is writable by other users; run: chmod 600 %s", path, path)
		}
	}
	if dir := filepath.Dir(path); dirExists(dir) {
		if info, err := os.Stat(dir); err == nil && info.Mode().Perm()&0o077 != 0 {
			add("permissions", lvWarn, "%s should be private; run: chmod 700 %s", dir, dir)
		} else {
			add("permissions", lvOK, "config folder is private")
		}
	}
	if os.Geteuid() == 0 {
		add("permissions", lvWarn, "vaultctl is running as root; Vault is designed to run as your user")
	}

	// Daemon and port.
	var st map[string]any
	if err := a.getJSON(ctx, "/api/status", &st); err == nil {
		add("daemon", lvOK, "Vault service is answering on http://%s", a.cfg.Listen)
	} else {
		conn, dErr := net.DialTimeout("tcp", a.cfg.Listen, time.Second)
		if dErr == nil {
			conn.Close()
			add("port", lvFail, "something other than Vault is using %s", a.cfg.Listen)
		} else {
			add("daemon", lvWarn, "Vault service is not running; start it with: systemctl --user enable --now omarchy-vault")
			add("port", lvOK, "%s is free", a.cfg.Listen)
		}
	}
	if err := config.ValidateListen(a.cfg.Listen, false); err != nil {
		add("port", lvWarn, "Vault listens on a non-loopback address (%s); make sure that is intended", a.cfg.Listen)
	}

	// Storage path.
	s := storage.Inspect(a.cfg)
	switch {
	case s.Configured && s.RootExists:
		add("storage", lvOK, "%s: %s free of %s", s.Root, humanBytes(s.FreeBytes), humanBytes(s.TotalBytes))
	case s.Configured:
		add("storage", lvFail, "%s is configured but missing", s.Root)
	default:
		add("storage", lvInfo, "Vault storage not set up yet (Milestone 2)")
	}

	// Required and optional tools.
	tools := []struct {
		bin, pkg, why string
		required      bool
		milestone     int
	}{
		{"lsblk", "util-linux", "drive discovery", true, 1},
		{"findmnt", "util-linux", "system drive detection", true, 1},
		{"smartctl", "smartmontools", "drive health", false, 1},
		{"sftpgo", "sftpgo", "file browser and users", false, 3},
		{"mergerfs", "mergerfs", "combining drives", false, 7},
		{"cloudflared", "cloudflared", "remote access", false, 6},
	}
	for _, t := range tools {
		if _, err := exec.LookPath(t.bin); err == nil {
			add(t.bin, lvOK, "installed (%s)", t.why)
			continue
		}
		switch {
		case t.required:
			add(t.bin, lvFail, "missing; install with: sudo pacman -S %s", t.pkg)
		case t.milestone <= 1:
			add(t.bin, lvWarn, "not installed; %s will show as unknown (sudo pacman -S %s)", t.why, t.pkg)
		default:
			add(t.bin, lvInfo, "not installed; needed for %s from Milestone %d", t.why, t.milestone)
		}
	}

	// Disk mounts and system disk protection.
	if inv, err := a.scanner().Inventory(ctx, true); err != nil {
		add("disks", lvFail, "drives could not be listed: %v", err)
	} else {
		if inv.SystemDiskDetected {
			add("system disk", lvOK, "identified and protected")
		} else {
			add("system disk", lvFail, "could not be identified; Vault will not offer any drive until it can")
		}
		add("disks", lvOK, "%d drives, %d available for Vault", inv.Summary.Total, inv.Summary.Available)
		for _, w := range inv.Warnings {
			add("disks", lvWarn, "%s", w)
		}
		if inv.Summary.Critical > 0 {
			add("health", lvFail, "%d drive(s) report CRITICAL health; run: vaultctl disks", inv.Summary.Critical)
		} else if inv.Summary.Warning > 0 {
			add("health", lvWarn, "%d drive(s) report warnings; run: vaultctl disks", inv.Summary.Warning)
		}
	}

	// Shortcuts.
	rep := a.shortcutInspector().Analyze(ctx)
	conflicts := 0
	for _, c := range rep.Checks {
		if c.Status == "conflict" {
			conflicts++
		}
	}
	switch {
	case len(rep.Sources) == 0:
		add("shortcuts", lvInfo, "no Hyprland config found; shortcuts cannot be checked here")
	case conflicts > 0:
		add("shortcuts", lvWarn, "%d Vault shortcut(s) conflict with existing bindings; run: vaultctl shortcuts", conflicts)
	default:
		add("shortcuts", lvOK, "no conflicts with existing bindings")
	}

	if a.json {
		_ = a.emitJSON(fs)
	} else {
		fmt.Fprintln(a.out, "VAULT DOCTOR")
		fmt.Fprintln(a.out)
		marks := map[level]string{lvOK: "✓", lvInfo: "·", lvWarn: "!", lvFail: "✗"}
		for _, f := range fs {
			fmt.Fprintf(a.out, "  %s %-12s %s\n", marks[f.Level], f.Check, f.Message)
		}
	}
	for _, f := range fs {
		if f.Level == lvFail {
			return 1
		}
	}
	return 0
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
