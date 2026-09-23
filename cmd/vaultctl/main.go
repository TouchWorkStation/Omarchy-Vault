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

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
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
  on              Turn Vault on (it never starts by itself)
  off             Turn Vault off (stops Files too)
  status          Vault status
  disks           List drives (SYSTEM drives are protected)
  setup           Open the setup screens in your browser
  storage         Vault storage and drives that could be used
  storage use <drive> [--folder NAME | --whole-drive] [--no-default-folders] [--yes]
                  Use a mounted drive (e.g. sda1 or /mnt/wdred) as Vault storage
  storage forget [--yes]
                  Stop using the drive (files stay where they are)
  link            Create the /srv/vault shortcut (asks for sudo once)
  pool status     How drives are combined
  users                        List Vault users
  users add <name> [--role admin|family|guest] [--folders "Photos,Documents:ro"]
                               Create a user (asks for the password)
  users disable|enable <name>  Stop or allow someone signing in
  users reset-password <name>  Set a new password (signs them out)
  users folders <name> "A,B:ro"  Change which folders they can open
  users remove <name> [--yes]  Delete the account (never their files)
  files           File service status and address
  upload [--folder NAME] [--minutes N] [--terminal]
                  Upload to Vault (Phone -> Vault): shows a QR code for your phone
  download [<file or folder>] [--minutes N] [--downloads N] [--terminal]
                  Download from Vault (Vault -> Phone): pick a file, get a QR code
  share <file or folder> [--expires 1h] [--downloads N] [--password]
                  Read-only share link on this Wi-Fi, up to 24h (unlimited downloads unless --downloads)
  shortcuts       Check Vault's shortcuts for conflicts
  shortcuts install [--use-suggestions] [--yes]
                  Add the free ones (never replaces an existing binding)
  shortcuts remove [--yes]
                  Remove Vault's shortcuts
  open            Open the Vault dashboard (turns Vault on if needed)
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
	case "on":
		err = a.powerOn(ctx)
	case "off":
		err = a.powerOff(ctx)
	case "status":
		err = a.status(ctx)
	case "disks":
		err = a.disks(ctx)
	case "storage":
		err = a.storageCmd(ctx, cmdArgs)
	case "setup":
		if err = a.ensureOn(ctx); err == nil {
			err = a.openPage(ctx, "/setup")
		}
	case "link":
		err = a.linkRoot(cmdArgs)
	case "pool":
		if len(cmdArgs) == 0 || cmdArgs[0] != "status" {
			return fail("usage: vaultctl pool status")
		}
		err = a.poolStatus()
	case "users":
		err = a.usersCmd(ctx, cmdArgs)
	case "files":
		err = a.filesCmd(ctx)
	case "upload":
		err = a.upload(ctx, cmdArgs)
	case "download":
		err = a.download(ctx, cmdArgs)
	case "share":
		err = a.share(ctx, cmdArgs)
	case "shortcuts":
		err = a.shortcutsCmd(ctx, cmdArgs)
	case "open":
		if err = a.ensureOn(ctx); err == nil {
			err = a.openPage(ctx, "/")
		}
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

func (a *app) baseURL() string {
	if v := os.Getenv("VAULT_ADDR"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://" + a.cfg.Listen
}

var errDaemonDown = errors.New("Vault is off. Turn it on with: vaultctl on")

func (a *app) getJSON(ctx context.Context, path string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL()+path, nil)
	if err != nil {
		return err
	}
	if tok, err := a.token(); err == nil {
		req.Header.Set(auth.HeaderToken, tok)
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
		Phone     struct {
			ActiveLinks int  `json:"active_links"`
			Listening   bool `json:"listening"`
		} `json:"phone"`
		AutoOff  int      `json:"auto_off_minutes"`
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
	phone := "closed (no active code)"
	if typed.Phone.Listening {
		phone = fmt.Sprintf("open on Wi-Fi for %d active code(s)", typed.Phone.ActiveLinks)
	}
	row(w, "Phone port", phone)
	if typed.AutoOff > 0 {
		row(w, "Auto-off", fmt.Sprintf("after %d minutes with nothing to do", typed.AutoOff))
	} else {
		row(w, "Auto-off", "never (auto_off_minutes is 0)")
	}
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

func (a *app) shortcutsCmd(ctx context.Context, args []string) error {
	insp := a.shortcutInspector()
	yes, suggest := false, false
	for _, x := range args[min(1, len(args)):] {
		switch x {
		case "--yes", "-y":
			yes = true
		case "--use-suggestions":
			suggest = true
		default:
			return fmt.Errorf("unexpected argument %q", x)
		}
	}
	if len(args) == 0 {
		rep := insp.Analyze(ctx)
		if a.json {
			return a.emitJSON(rep)
		}
		printShortcuts(a.out, rep)
		return nil
	}
	switch args[0] {
	case "install":
		bs, skipped, err := shortcuts.Choose(insp.Analyze(ctx), version.Milestone, suggest)
		if err != nil {
			return err
		}
		for _, s := range skipped {
			fmt.Fprintf(a.out, "  skip  %s\n", s)
		}
		if len(bs) == 0 {
			fmt.Fprintln(a.out, "Nothing to install. Try --use-suggestions to use a free alternative.")
			return nil
		}
		bs = absoluteCommands(bs)
		fmt.Fprintf(a.out, "Vault will write %s:\n\n%s\n", shortcuts.IncludePath(insp.Home), shortcuts.Render(bs))
		fmt.Fprintf(a.out, "and, if it is not there yet, add this line to %s:\n\n    %s\n\n", insp.ConfigPath, shortcuts.SourceLine())
		if !yes && !confirm("Install these shortcuts?") {
			return errors.New("cancelled; nothing was changed")
		}
		if _, err := shortcuts.Install(insp.Home, insp.ConfigPath, bs); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "Done. Hyprland picks them up right away:")
		for _, b := range bs {
			fmt.Fprintf(a.out, "  %-22s %s\n", b.Combo(), b.Label)
		}
		return nil
	case "remove":
		if !yes && !confirm("Remove Vault's shortcuts?") {
			return errors.New("cancelled; nothing was changed")
		}
		if err := shortcuts.Remove(insp.Home, insp.ConfigPath); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "Vault's shortcuts were removed. Your other bindings were not touched.")
		return nil
	default:
		return fmt.Errorf("unknown shortcuts command %q (install, remove)", args[0])
	}
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

// absoluteCommands points shortcuts at this vaultctl by full path:
// Hyprland runs them with the session's PATH, which may not include
// ~/.local/bin.
func absoluteCommands(bs []shortcuts.Planned) []shortcuts.Planned {
	exe, err := os.Executable()
	if err != nil || filepath.Base(exe) != "vaultctl" {
		return bs
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil && filepath.Base(real) == "vaultctl" {
		exe = real
	}
	return shortcuts.WithCommandPath(bs, exe)
}
