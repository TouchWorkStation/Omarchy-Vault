package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/qrsvg"
)

type linkView struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Folder    string    `json:"folder"`
	Path      string    `json:"path"`
	MaxFiles  int       `json:"max_files"`
	Files     int       `json:"files"`
	ExpiresAt time.Time `json:"expires_at"`
	State     string    `json:"state"`
	URL       string    `json:"url"`
	Received  []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"received"`
}

// upload: Super+Shift+U. Turns Vault on, creates an upload link and shows
// its QR code in a small window (or the terminal with --terminal).
func (a *app) upload(ctx context.Context, args []string) error {
	folder, minutes, terminal := "", 0, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--folder":
			if i+1 >= len(args) {
				return errors.New("--folder needs a Vault folder name")
			}
			folder = args[i+1]
			i++
		case "--minutes":
			if i+1 >= len(args) {
				return errors.New("--minutes needs a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 1 || n > 60 {
				return errors.New("--minutes must be between 1 and 60")
			}
			minutes = n
			i++
		case "--terminal", "-t":
			terminal = true
		default:
			return fmt.Errorf("unexpected argument %q", args[i])
		}
	}
	if err := a.ensureOn(ctx); err != nil {
		return err
	}
	var link linkView
	body := map[string]any{"client": "cli", "folder": folder, "minutes": minutes}
	if err := a.call(ctx, http.MethodPost, "/api/upload-session", body, &link); err != nil {
		return err
	}
	if a.json {
		return a.emitJSON(link)
	}
	if terminal || !hasDisplay() {
		return a.watchInTerminal(ctx, link)
	}
	if err := a.openApp(ctx, "/transfer/"+link.ID); err != nil {
		return a.watchInTerminal(ctx, link)
	}
	fmt.Fprintf(a.out, "Upload to Vault: scan the QR code with your phone (valid until %s).\n", link.ExpiresAt.Local().Format("15:04"))
	return nil
}

// download: Super+Shift+D. Without a path it opens Vault's file picker;
// with one it creates a download link for that file or folder right away.
func (a *app) download(ctx context.Context, args []string) error {
	target, minutes, count, terminal := "", 0, 0, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--minutes", "--downloads":
			if i+1 >= len(args) {
				return fmt.Errorf("%s needs a number", args[i])
			}
			n, err := strconv.Atoi(args[i+1])
			if args[i] == "--minutes" {
				if err != nil || n < 1 || n > 60 {
					return errors.New("--minutes must be between 1 and 60")
				}
				minutes = n
			} else {
				if err != nil || n < 1 || n > 10 {
					return errors.New("--downloads must be between 1 and 10")
				}
				count = n
			}
			i++
		case "--terminal", "-t":
			terminal = true
		default:
			if strings.HasPrefix(args[i], "-") || target != "" {
				return fmt.Errorf("unexpected argument %q", args[i])
			}
			target = args[i]
		}
	}
	if err := a.ensureOn(ctx); err != nil {
		return err
	}
	if target == "" {
		if terminal || !hasDisplay() {
			return errors.New("choose what to send: vaultctl download <file or folder in the Vault>")
		}
		return a.openApp(ctx, "/download")
	}
	rel, err := a.vaultRel(target)
	if err != nil {
		return err
	}
	var link linkView
	body := map[string]any{"client": "cli", "path": rel, "minutes": minutes, "max_downloads": count}
	if err := a.call(ctx, http.MethodPost, "/api/download-session", body, &link); err != nil {
		return err
	}
	if a.json {
		return a.emitJSON(link)
	}
	if terminal || !hasDisplay() {
		return a.watchInTerminal(ctx, link)
	}
	if err := a.openApp(ctx, "/transfer/"+link.ID); err != nil {
		return a.watchInTerminal(ctx, link)
	}
	fmt.Fprintf(a.out, "Download from Vault: scan the QR code with your phone (valid until %s).\n", link.ExpiresAt.Local().Format("15:04"))
	return nil
}

// share creates a read-only share link and prints it with its QR code.
func (a *app) share(ctx context.Context, args []string) error {
	target, minutes, count, askPassword := "", 24*60, 0, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--expires":
			if i+1 >= len(args) {
				return errors.New("--expires needs a duration such as 10m, 1h, 24h or 7d")
			}
			m, err := parseExpiry(args[i+1])
			if err != nil {
				return err
			}
			minutes = m
			i++
		case "--downloads":
			if i+1 >= len(args) {
				return errors.New("--downloads needs a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 1 || n > 1000 {
				return errors.New("--downloads must be between 1 and 1000 (leave it out for unlimited)")
			}
			count = n
			i++
		case "--password":
			askPassword = true
		default:
			if strings.HasPrefix(args[i], "-") || target != "" {
				return fmt.Errorf("unexpected argument %q", args[i])
			}
			target = args[i]
		}
	}
	if target == "" {
		return errors.New("usage: vaultctl share <file or folder> [--expires 24h] [--downloads N] [--password]")
	}
	rel, err := a.vaultRel(target)
	if err != nil {
		return err
	}
	password := ""
	if askPassword {
		if password, err = readPassword("Password for this link: "); err != nil {
			return err
		}
	}
	if err := a.ensureOn(ctx); err != nil {
		return err
	}
	var link linkView
	body := map[string]any{"client": "cli", "path": rel, "minutes": minutes, "max_downloads": count, "password": password}
	if err := a.call(ctx, http.MethodPost, "/api/share", body, &link); err != nil {
		return err
	}
	if a.json {
		return a.emitJSON(link)
	}
	qr, err := qrsvg.Terminal(link.URL)
	if err != nil {
		return err
	}
	limit := "unlimited downloads"
	if link.MaxFiles < 1<<30 {
		limit = fmt.Sprintf("%d download(s)", link.MaxFiles)
	}
	fmt.Fprintf(a.out, "SHARE LINK  ·  read only\n\n%s\n%s\n\n", qr, link.URL)
	fmt.Fprintf(a.out, "%s  ·  %s  ·  valid until %s\n", link.Path, limit, link.ExpiresAt.Local().Format("Mon 2 Jan 15:04"))
	fmt.Fprintln(a.out, "Works on this network while Vault is on. Stop it any time in Vault › Download › Share links.")
	return nil
}

// parseExpiry reads 10m, 1h, 24h, 7d (at most 30 days) into minutes.
func parseExpiry(s string) (int, error) {
	bad := errors.New("--expires must look like 10m, 1h, 24h or 7d (at most 30d)")
	if len(s) < 2 {
		return 0, bad
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 1 {
		return 0, bad
	}
	switch s[len(s)-1] {
	case 'm':
	case 'h':
		n *= 60
	case 'd':
		n *= 24 * 60
	default:
		return 0, bad
	}
	if n > 30*24*60 {
		return 0, bad
	}
	return n, nil
}

// vaultRel turns a path the user typed into a Vault path such as
// "Photos/2024/beach.jpg". Paths on disk must be inside the Vault
// (/srv/vault/...); anything else is taken as already Vault-relative.
func (a *app) vaultRel(arg string) (string, error) {
	onDisk := filepath.IsAbs(arg) || strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../")
	if !onDisk {
		if _, err := os.Lstat(arg); err == nil {
			onDisk = true
		}
	}
	if !onDisk {
		return strings.Trim(arg, "/"), nil
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("%s: %w", arg, err)
	}
	vaultRoot := a.cfg.VaultRoot
	if vaultRoot == "" {
		vaultRoot = config.DefaultVaultRoot
	}
	// /srv/vault is a chain of links to the drive folder; compare real paths.
	if rb, err := filepath.EvalSymlinks(vaultRoot); err == nil {
		if rel, err := filepath.Rel(rb, real); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, "../") {
			return filepath.ToSlash(rel), nil
		}
	}
	return "", fmt.Errorf("%s is not in the Vault (%s)", arg, vaultRoot)
}

func hasDisplay() bool { return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != "" }

// openApp opens path signed in, preferring Omarchy's web-app window.
func (a *app) openApp(ctx context.Context, path string) error {
	var lr struct {
		Path string `json:"path"`
	}
	if err := a.call(ctx, http.MethodPost, "/api/local-login", nil, &lr); err != nil {
		return err
	}
	url := a.baseURL() + lr.Path + "&next=" + path
	for _, launcher := range [][]string{{"omarchy-launch-webapp", url}, {"xdg-open", url}} {
		if p, err := exec.LookPath(launcher[0]); err == nil {
			cmd := exec.Command(p, launcher[1:]...)
			cmd.Stdout, cmd.Stderr = nil, nil
			return cmd.Start()
		}
	}
	return errors.New("no browser launcher found")
}

func (a *app) watchInTerminal(ctx context.Context, link linkView) error {
	qr, err := qrsvg.Terminal(link.URL)
	if err != nil {
		return err
	}
	w := a.out
	endpoint, done := "/api/upload-session/", "received"
	if link.Kind == "download" {
		endpoint, done = "/api/download-session/", "downloaded"
		fmt.Fprintln(w, "DOWNLOAD FROM VAULT  ·  Vault → Phone")
	} else {
		fmt.Fprintln(w, "UPLOAD TO VAULT  ·  Phone → Vault")
	}
	fmt.Fprintln(w)
	fmt.Fprint(w, qr)
	fmt.Fprintf(w, "\nScan with your phone (same Wi-Fi as this computer).\n%s\n", link.URL)
	if link.Kind == "download" {
		fmt.Fprintf(w, "Sending: Vault › %s   ·   valid until %s   ·   Ctrl+C to stop\n\n", link.Path, link.ExpiresAt.Local().Format("15:04"))
	} else {
		fmt.Fprintf(w, "Files go to: Vault › %s   ·   valid until %s   ·   Ctrl+C to stop\n\n", link.Folder, link.ExpiresAt.Local().Format("15:04"))
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	seen := 0
	for {
		select {
		case <-ctx.Done():
			// Stop the link when the user quits.
			_ = a.call(context.Background(), http.MethodDelete, endpoint+link.ID, nil, nil)
			fmt.Fprintf(w, "\nStopped. %d file(s) %s.\n", seen, done)
			return nil
		case <-time.After(2 * time.Second):
		}
		var cur linkView
		if err := a.call(ctx, http.MethodGet, endpoint+link.ID, nil, &cur); err != nil {
			continue
		}
		for _, f := range cur.Received[min(seen, len(cur.Received)):] {
			fmt.Fprintf(w, "  ✓ %s  (%s)\n", f.Name, humanBytes(uint64(f.Size)))
		}
		seen = len(cur.Received)
		if cur.State != "active" {
			if link.Kind == "download" {
				fmt.Fprintf(w, "\nDone: sent to your phone.\n")
				if cur.State != "full" {
					fmt.Fprintf(w, "Link %s.\n", cur.State)
				}
				return nil
			}
			fmt.Fprintf(w, "\nLink %s. %d file(s) received into Vault › %s.\n", strings.ReplaceAll(cur.State, "_", " "), seen, cur.Folder)
			return nil
		}
	}
}
