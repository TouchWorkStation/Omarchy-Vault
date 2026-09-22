// Command vaultctl is the Omarchy Vault command-line tool.
//
// Read-only commands (disks, storage, doctor, shortcuts) work without the
// daemon. Vault never formats, mounts or partitions drives, and never writes
// Hyprland config.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/disks"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/shortcuts"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/storage"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/sysexec"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/version"
)

const usage = `vaultctl — Omarchy Vault

Usage:
  vaultctl <command> [flags]

Commands:
  status          Vault daemon status
  disks           List drives (SYSTEM drives are protected)
  setup           Open the setup screens in your browser
  storage         Vault storage and drives that could be used
  storage use <drive> [--folder NAME | --whole-drive] [--no-default-folders] [--yes]
                  Use a mounted drive (e.g. sda1 or /mnt/wdred) as Vault storage
  storage forget [--yes]
                  Stop using the drive (files stay where they are)
  link            Create the /srv/vault shortcut (asks for sudo once)
  pool status     How drives are combined
  users           Vault users                          (Milestone 3)
  remote status   Remote access status
  upload          Upload to Vault: Phone -> Vault       (Milestone 4)
  download        Download from Vault: Vault -> Phone   (Milestone 5)
  share <file>    Create a share link                   (Milestone 5)
  shortcuts       Check Vault shortcuts for conflicts (never installs)
  open            Open the Vault dashboard
  doctor          Check that everything Vault needs is in place
  logs [-f]       Show Vault service logs
  version         Print version

Global flags:
  --json          Machine-readable output (status, disks, storage, shortcuts, doctor)
`

type app struct {
	json   bool
	cfg    config.Config
	cfgErr error
	found  bool
	run    sysexec.Runner
	out    io.Writer
}

func main() {
	os.Exit(realMain(os.Args[1:]))
}

func realMain(args []string) int {
	a := &app{run: sysexec.System{Timeout: 15 * time.Second}, out: os.Stdout}

	// Accept --json anywhere.
	var rest []string
	for _, arg := range args {
		switch arg {
		case "--json", "-json":
			a.json = true
		case "-h", "--help", "help":
			fmt.Print(usage)
			return 0
		default:
			rest = append(rest, arg)
		}
	}
	if len(rest) == 0 {
		fmt.Print(usage)
		return 2
	}

	path, _ := config.DefaultPath()
	a.cfg, a.found, a.cfgErr = config.Load(path)

	ctx := context.Background()
	cmd, cmdArgs := rest[0], rest[1:]
	var err error
	switch cmd {
	case "version":
		fmt.Fprintf(a.out, "vaultctl %s (commit %s, milestone %d)\n", version.Version, version.Commit, version.Milestone)
	case "status":
		err = a.status(ctx)
	case "disks":
		err = a.disks(ctx)
	case "storage":
		err = a.storageCmd(ctx, cmdArgs)
	case "setup":
		err = a.openPage(ctx, "/setup")
	case "link":
		err = a.linkRoot(cmdArgs)
	case "pool":
		if len(cmdArgs) == 0 || cmdArgs[0] != "status" {
			return fail("usage: vaultctl pool status")
		}
		err = a.poolStatus()
	case "remote":
		if len(cmdArgs) == 0 || cmdArgs[0] != "status" {
			return fail("usage: vaultctl remote status")
		}
		err = a.remoteStatus()
	case "users":
		return notYet("User management", 3)
	case "upload":
		return notYet("Upload to Vault (Phone -> Vault)", 4)
	case "download":
		return notYet("Download from Vault (Vault -> Phone)", 5)
	case "share":
		if len(cmdArgs) == 0 {
			return fail("usage: vaultctl share <file>")
		}
		return notYet("Share links", 5)
	case "shortcuts":
		err = a.shortcuts(ctx)
	case "open":
		err = a.openPage(ctx, "/")
	case "doctor":
		return a.doctor(ctx)
	case "logs":
		err = logs(cmdArgs)
	default:
		fmt.Fprintf(os.Stderr, "vaultctl: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "vaultctl:", err)
		return 1
	}
	return 0
}

func fail(msg string) int {
	fmt.Fprintln(os.Stderr, msg)
	return 2
}

func notYet(feature string, milestone int) int {
	fmt.Fprintf(os.Stderr, "%s arrives in Milestone %d. Nothing was changed.\n", feature, milestone)
	return 3
}

func (a *app) baseURL() string {
	if v := os.Getenv("VAULT_ADDR"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://" + a.cfg.Listen
}

var errDaemonDown = errors.New("the Vault service is not running (start it with: systemctl --user start omarchy-vault)")

func (a *app) getJSON(ctx context.Context, path string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL()+path, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errDaemonDown
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", path, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}

func (a *app) emitJSON(v any) error {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (a *app) scanner() *disks.Scanner {
	return &disks.Scanner{Run: a.run, SMART: true}
}

func (a *app) shortcutInspector() shortcuts.Inspector {
	home, _ := os.UserHomeDir()
	return shortcuts.Inspector{Run: a.run, ConfigPath: filepath.Join(home, ".config", "hypr", "hyprland.conf"), Home: home}
}

func (a *app) status(ctx context.Context) error {
	var st map[string]any
	if err := a.getJSON(ctx, "/api/status", &st); err != nil {
		return err
	}
	if a.json {
		return a.emitJSON(st)
	}
	var typed struct {
		Version   string         `json:"version"`
		Milestone int            `json:"milestone"`
		Hostname  string         `json:"hostname"`
		Listen    string         `json:"listen"`
		Uptime    int64          `json:"uptime_seconds"`
		Setup     bool           `json:"setup_complete"`
		Storage   storage.Status `json:"storage"`
		Drives    *disks.Summary `json:"drives"`
		Remote    struct {
			State  string `json:"state"`
			Domain string `json:"domain"`
		} `json:"remote"`
		Warnings []string `json:"warnings"`
	}
	raw, _ := json.Marshal(st)
	_ = json.Unmarshal(raw, &typed)

	w := a.out
	fmt.Fprintln(w, "VAULT")
	fmt.Fprintln(w)
	row(w, "Service", fmt.Sprintf("running on http://%s (up %s)", typed.Listen, (time.Duration(typed.Uptime)*time.Second).String()))
	row(w, "Version", fmt.Sprintf("%s · milestone %d", typed.Version, typed.Milestone))
	row(w, "Host", typed.Hostname)
	if typed.Setup {
		row(w, "Storage", fmt.Sprintf("%s / %s used", humanBytes(typed.Storage.UsedBytes), humanBytes(typed.Storage.TotalBytes)))
	} else {
		row(w, "Storage", "not set up yet")
	}
	if d := typed.Drives; d != nil {
		row(w, "Drives", fmt.Sprintf("%d found · %d system (protected) · %d available", d.Total, d.System, d.Available))
	}
	remote := "not configured"
	if typed.Remote.State != "not_configured" {
		remote = typed.Remote.State + " " + typed.Remote.Domain
	}
	row(w, "Remote", remote)
	for _, warn := range typed.Warnings {
		fmt.Fprintln(w, "\n! "+warn)
	}
	return nil
}

func (a *app) disks(ctx context.Context) error {
	inv, err := a.scanner().Inventory(ctx, true)
	if err != nil {
		return err
	}
	if a.json {
		return a.emitJSON(inv)
	}
	printInventory(a.out, inv)
	return nil
}

func (a *app) poolStatus() error {
	if a.json {
		return a.emitJSON(map[string]any{"mode": a.cfg.Pool.Mode, "sources": a.cfg.Sources})
	}
	switch a.cfg.Pool.Mode {
	case "none":
		fmt.Fprintln(a.out, "No drives are in the Vault yet. Run: vaultctl setup")
	case "single":
		src := a.cfg.Sources[0]
		fmt.Fprintf(a.out, "One drive: %s (%s)\n", src.Label, src.DataDir())
	case "combined":
		fmt.Fprintf(a.out, "%d drives combined into one Vault:\n", len(a.cfg.Sources))
		for _, s := range a.cfg.Sources {
			fmt.Fprintf(a.out, "  %s\n", s.Path)
		}
	}
	return nil
}

func (a *app) remoteStatus() error {
	r := a.cfg.Remote
	if a.json {
		return a.emitJSON(r)
	}
	if !r.Enabled {
		fmt.Fprintln(a.out, "Remote access is off. Vault is only reachable from this computer.")
		fmt.Fprintln(a.out, "Connecting your domain through Cloudflare arrives in Milestone 6.")
		return nil
	}
	fmt.Fprintf(a.out, "Remote access: %s via %s\n", r.Domain, r.Provider)
	return nil
}

func (a *app) shortcuts(ctx context.Context) error {
	rep := a.shortcutInspector().Analyze(ctx)
	if a.json {
		return a.emitJSON(rep)
	}
	printShortcuts(a.out, rep)
	return nil
}

func logs(args []string) error {
	jargs := []string{"--user", "-u", "omarchy-vault.service", "-n", "200", "--no-pager"}
	for _, a := range args {
		if a == "-f" || a == "--follow" {
			jargs = append(jargs, "-f")
		}
	}
	path, err := exec.LookPath("journalctl")
	if err != nil {
		return errors.New("journalctl not found")
	}
	cmd := exec.Command(path, jargs...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
