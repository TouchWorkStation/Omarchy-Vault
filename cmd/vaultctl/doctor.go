package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/files"
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
		add("config", lvInfo, "no config yet at %s (created when you choose storage: vaultctl setup)", path)
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
	daemonUp := false
	if err := a.getJSON(ctx, "/api/session", &st); err == nil {
		daemonUp = true
		add("daemon", lvOK, "Vault is on at http://%s", a.cfg.Listen)
	} else {
		conn, dErr := net.DialTimeout("tcp", a.cfg.Listen, time.Second)
		if dErr == nil {
			conn.Close()
			add("port", lvFail, "something other than Vault is using %s", a.cfg.Listen)
		} else {
			add("daemon", lvInfo, "Vault is off (it only runs when you turn it on): vaultctl on")
			add("port", lvOK, "%s is free", a.cfg.Listen)
		}
	}
	if err := config.ValidateListen(a.cfg.Listen, false); err != nil {
		add("port", lvWarn, "Vault listens on a non-loopback address (%s); make sure that is intended", a.cfg.Listen)
	}

	// Storage.
	inv, invErr := a.scanner().Inventory(ctx, true)
	if invErr != nil {
		inv = nil
	}
	s := a.localStorageStatus(inv)
	switch s.State {
	case storage.StateReady:
		add("storage", lvOK, "%s: %s free of %s", s.Sources[0].DataDir, humanBytes(s.FreeBytes), humanBytes(s.TotalBytes))
	case storage.StateNotSetUp:
		add("storage", lvInfo, "Vault storage not set up yet; run: vaultctl setup")
	default:
		add("storage", lvFail, "%s", s.Message)
	}
	switch s.RootLink.State {
	case "ok":
		add("vault root", lvOK, "%s → %s", s.Root, s.DataLink)
	case "missing":
		add("vault root", lvInfo, "%s not created (optional); run: vaultctl link", s.Root)
	default:
		add("vault root", lvWarn, "%s exists but is not Vault's shortcut; Vault leaves it alone", s.Root)
	}
	if dir, err := config.Dir(); err == nil {
		if _, err := auth.ReadToken(auth.SecretsDir(dir)); err != nil {
			add("local token", lvWarn, "not found yet; it is created when the Vault service first starts")
		} else {
			add("local token", lvOK, "present and private (0600)")
		}
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
	if inv == nil {
		add("disks", lvFail, "drives could not be listed: %v", invErr)
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

	// Files (SFTPGo, run by Vault).
	if dh, err := files.DataHome(); err == nil {
		if bin, _, err := files.Locate(dh); err != nil {
			add("files", lvWarn, "file service not installed; run: ./scripts/build-sftpgo.sh")
		} else {
			var fst struct {
				State   string `json:"state"`
				Message string `json:"message"`
			}
			switch {
			case !daemonUp:
				add("files", lvInfo, "installed (%s); starts with the Vault service", bin)
			case a.call(ctx, "GET", "/api/files", nil, &fst) != nil:
				add("files", lvInfo, "installed (%s)", bin)
			case fst.State == "running":
				add("files", lvOK, "running")
			case fst.State == "error":
				add("files", lvFail, "%s", fst.Message)
			default:
				add("files", lvInfo, "%s", strings.TrimSpace(strings.ReplaceAll(fst.State, "_", " ")+". "+fst.Message))
			}
		}
	}

	// Accounts.
	var ul userList
	if daemonUp && a.call(ctx, "GET", "/api/users", nil, &ul) == nil {
		if len(ul.Users) == 0 {
			add("accounts", lvWarn, "no accounts yet; create yours with: vaultctl users add <name>")
		} else {
			add("accounts", lvOK, "%d account(s)", len(ul.Users))
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
