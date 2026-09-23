package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/disks"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/storage"
)

// localStorageStatus inspects storage without the daemon.
func (a *app) localStorageStatus(inv *disks.Inventory) storage.Status {
	mounts, _ := storage.ReadMountTable()
	link, _ := storage.DefaultLinkPath()
	return storage.Inspect(a.cfg, inv, mounts, link)
}

func (a *app) token() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	tok, err := auth.ReadToken(auth.SecretsDir(dir))
	if err != nil {
		return "", errors.New("could not read Vault's local token; is the Vault service installed for this user? (" + err.Error() + ")")
	}
	return tok, nil
}

// call sends an authenticated request to the daemon and decodes the reply.
func (a *app) call(ctx context.Context, method, path string, body, out any) error {
	tok, err := a.token()
	if err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL()+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set(auth.HeaderToken, tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errDaemonDown
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct{ Message string }
		if json.Unmarshal(data, &e) == nil && e.Message != "" {
			return errors.New(e.Message)
		}
		return fmt.Errorf("%s %s returned %s", method, path, resp.Status)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

func (a *app) storageCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return a.storageShow(ctx)
	}
	switch args[0] {
	case "use":
		return a.storageUse(ctx, args[1:])
	case "forget":
		return a.storageForget(ctx, args[1:])
	default:
		return fmt.Errorf("unknown storage command %q (use: storage, storage use, storage forget)", args[0])
	}
}

func (a *app) storageShow(ctx context.Context) error {
	inv, invErr := a.scanner().Inventory(ctx, false)
	if invErr != nil {
		inv = nil
	}
	st := a.localStorageStatus(inv)
	if a.json {
		return a.emitJSON(map[string]any{"storage": st, "inventory": inv})
	}
	w := a.out
	fmt.Fprintln(w, "VAULT STORAGE")
	fmt.Fprintln(w)
	switch st.State {
	case storage.StateNotSetUp:
		row(w, "Status", "not set up yet — run: vaultctl setup")
	case storage.StateReady:
		src := st.Sources[0]
		row(w, "Status", "ready")
		row(w, "Drive", src.Label)
		row(w, "Folder", src.DataDir)
		row(w, "Total", humanBytes(st.TotalBytes))
		row(w, "Used", humanBytes(st.UsedBytes))
		row(w, "Available", humanBytes(st.FreeBytes))
	default:
		row(w, "Status", strings.ReplaceAll(string(st.State), "_", " "))
		fmt.Fprintf(w, "\n! %s\n", st.Message)
	}
	switch st.RootLink.State {
	case "ok":
		row(w, "Shortcut", st.Root+" → "+st.DataLink)
	case "missing":
		row(w, "Shortcut", st.Root+" not created yet (optional) — run: vaultctl link")
	case "elsewhere":
		row(w, "Shortcut", st.Root+" points somewhere else ("+st.RootLink.Target+"); Vault leaves it alone")
	case "not_link":
		row(w, "Shortcut", st.Root+" is an existing folder; Vault leaves it alone")
	}
	if inv == nil {
		return invErr
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Drives Vault can use:")
	n := 0
	for _, d := range inv.Disks {
		for _, v := range d.Volumes {
			if v.Adoptable {
				n++
				fmt.Fprintf(w, "  %-10s %-26s %-28s %s free of %s\n", v.Name, d.DisplayName, v.Mountpoints[0], humanBytes(uint64(v.FSAvail)), humanBytes(uint64(v.FSSize)))
			}
		}
	}
	if n == 0 {
		fmt.Fprintln(w, "  none — mount a drive (for example with your file manager) and check again")
	} else if !st.Configured {
		fmt.Fprintln(w, "\nUse one with: vaultctl storage use <name>   (e.g. vaultctl storage use sda1)")
	}
	return nil
}

func (a *app) storageUse(ctx context.Context, args []string) error {
	var target string
	folder := "Vault"
	createFolders, yes := true, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--folder":
			if i+1 >= len(args) {
				return errors.New("--folder needs a name")
			}
			folder = args[i+1]
			i++
		case "--whole-drive":
			folder = ""
		case "--no-default-folders":
			createFolders = false
		case "--yes", "-y":
			yes = true
		default:
			if strings.HasPrefix(args[i], "-") || target != "" {
				return fmt.Errorf("unexpected argument %q", args[i])
			}
			target = args[i]
		}
	}
	if target == "" {
		return errors.New("usage: vaultctl storage use <drive> (see: vaultctl storage)")
	}
	if err := config.ValidateFolder(folder); err != nil {
		return err
	}

	// Resolve a mount point or /dev path to a volume name locally; the
	// daemon re-checks everything against its own fresh scan.
	inv, err := a.scanner().Inventory(ctx, true)
	if err != nil {
		return err
	}
	var disk *disks.Disk
	var vol *disks.Volume
	for i := range inv.Disks {
		for j := range inv.Disks[i].Volumes {
			v := &inv.Disks[i].Volumes[j]
			if v.Name == target || v.Path == target || contains(v.Mountpoints, strings.TrimSuffix(target, "/")) {
				disk, vol = &inv.Disks[i], v
			}
		}
	}
	if vol == nil {
		return fmt.Errorf("no drive called %q; run `vaultctl storage` to see the ones Vault can use", target)
	}
	if disk.System {
		return fmt.Errorf("%s runs Omarchy; Vault will never use it for storage", disk.DisplayName)
	}
	if !vol.Adoptable {
		why := "it cannot be used as it is"
		if len(vol.Notes) > 0 {
			why = vol.Notes[0]
		}
		return fmt.Errorf("Vault cannot use %s: %s", vol.Name, why)
	}

	dest := vol.Mountpoints[0]
	if folder != "" {
		dest += "/" + folder
	}
	fmt.Fprintf(a.out, "Use %s (%s) as your Vault?\n", disk.DisplayName, humanBytes(uint64(vol.FSSize)))
	fmt.Fprintf(a.out, "  Vault folder:   %s\n", dest)
	if createFolders {
		fmt.Fprintln(a.out, "  Creates:        Photos, Documents, Backups, Projects, Phone Uploads, Shared (only if missing)")
	}
	fmt.Fprintln(a.out, "  Nothing is formatted, moved or deleted.")
	replace := a.cfg.Pool.Mode != "none"
	if replace {
		fmt.Fprintf(a.out, "  This replaces your current storage (%s). Its files stay where they are.\n", a.cfg.Sources[0].DataDir())
	}
	if !yes && !confirm("Continue?") {
		return errors.New("cancelled; nothing was changed")
	}

	var resp struct {
		Storage storage.Status       `json:"storage"`
		Result  *storage.AdoptResult `json:"result"`
		Notes   []string             `json:"notes"`
	}
	body := map[string]any{"volume": vol.Name, "folder": folder, "create_folders": createFolders, "replace": replace}
	if err := a.call(ctx, http.MethodPost, "/api/pool", body, &resp); err != nil {
		return err
	}
	if a.json {
		return a.emitJSON(resp)
	}
	fmt.Fprintln(a.out, "\nYOUR VAULT IS READY")
	fmt.Fprintf(a.out, "  %s available · %s\n", humanBytes(resp.Storage.FreeBytes), resp.Result.DataDir)
	if len(resp.Result.Created) > 0 {
		fmt.Fprintf(a.out, "  Created: %s\n", strings.Join(resp.Result.Created, ", "))
	}
	if len(resp.Result.Skipped) > 0 {
		fmt.Fprintf(a.out, "  Left alone (not a plain folder): %s\n", strings.Join(resp.Result.Skipped, ", "))
	}
	for _, n := range resp.Notes {
		fmt.Fprintf(a.out, "  %s\n", n)
	}
	if resp.Storage.RootLink.State == "missing" {
		fmt.Fprintln(a.out, "\nOptional: run `vaultctl link` to also reach it at /srv/vault.")
	}
	return nil
}

func (a *app) storageForget(ctx context.Context, args []string) error {
	yes := len(args) > 0 && (args[0] == "--yes" || args[0] == "-y")
	if a.cfg.Pool.Mode == "none" {
		fmt.Fprintln(a.out, "Vault storage is not set up; nothing to forget.")
		return nil
	}
	fmt.Fprintf(a.out, "Stop using %s as Vault storage?\nEvery file stays where it is.\n", a.cfg.Sources[0].DataDir())
	if !yes && !confirm("Continue?") {
		return errors.New("cancelled; nothing was changed")
	}
	var resp struct{ Notes []string }
	if err := a.call(ctx, http.MethodDelete, "/api/pool", nil, &resp); err != nil {
		return err
	}
	for _, n := range resp.Notes {
		fmt.Fprintln(a.out, n)
	}
	return nil
}

// openPage signs the browser in with a single-use code and opens path.
func (a *app) openPage(ctx context.Context, path string) error {
	base := a.baseURL()
	var lr struct {
		Path string `json:"path"`
	}
	url := base + path
	if err := a.call(ctx, http.MethodPost, "/api/local-login", nil, &lr); err == nil && lr.Path != "" {
		url = base + lr.Path + "&next=" + path
	} else if errors.Is(err, errDaemonDown) {
		return err
	} else if err != nil {
		fmt.Fprintf(os.Stderr, "vaultctl: opening read-only (%v)\n", err)
	}
	if err := launch(url); err != nil {
		fmt.Fprintf(a.out, "Couldn't open a browser window (%v).\n", err)
	} else {
		fmt.Fprintln(a.out, "Opening Vault in a browser window…")
	}
	fmt.Fprintf(a.out, "If nothing appears, open %s%s in your browser (or run: vaultctl open).\n", base, path)
	return nil
}

// linkRoot creates /srv/vault -> ~/.local/share/omarchy-vault/current, the
// only step in Milestone 2 that needs root. It runs one visible sudo
// command after asking, and never replaces anything that already exists.
func (a *app) linkRoot(args []string) error {
	yes := len(args) > 0 && (args[0] == "--yes" || args[0] == "-y")
	link, err := storage.DefaultLinkPath()
	if err != nil {
		return err
	}
	root := a.cfg.VaultRoot
	st := storage.CheckRootLink(root, link)
	switch st.State {
	case "ok":
		fmt.Fprintf(a.out, "%s already points to your Vault.\n", root)
		return nil
	case "elsewhere":
		return fmt.Errorf("%s already points to %s; Vault will not change it. Remove it yourself if you want Vault to use that name", root, st.Target)
	case "not_link":
		return fmt.Errorf("%s already exists as a real folder; Vault will not touch it", root)
	}
	cmdline := []string{"sudo", "ln", "-sn", "--", link, root}
	fmt.Fprintf(a.out, "This creates the shortcut %s → %s so your Vault is always at %s.\n", root, link, root)
	fmt.Fprintf(a.out, "It runs one command as root:\n\n    %s\n\n", strings.Join(cmdline, " "))
	if !yes && !confirm("Run it?") {
		return errors.New("cancelled; nothing was changed")
	}
	cmd := exec.Command(cmdline[0], cmdline[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("could not create %s: %w", root, err)
	}
	fmt.Fprintf(a.out, "Done. %s now leads to your Vault.\n", root)
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
