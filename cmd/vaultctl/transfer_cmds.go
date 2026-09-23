package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

// upload: Super+Alt+U. Turns Vault on, creates an upload link and shows
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

// download: Super+Alt+D. With a path it sends that file or folder (in the
// Vault or anywhere on this computer). Without one it sends the files you
// copied in the file manager, or opens Vault's picker if none are copied
// (--picker always opens the picker).
func (a *app) download(ctx context.Context, args []string) error {
	target, minutes, count, terminal, picker := "", 0, 0, false, false
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
		case "--picker":
			picker = true
		default:
			if strings.HasPrefix(args[i], "-") || target != "" {
				return fmt.Errorf("unexpected argument %q", args[i])
			}
			target = args[i]
		}
	}
	body := map[string]any{"client": "cli", "minutes": minutes, "max_downloads": count}
	switch {
	case target != "":
		rel, local, err := a.resolveTarget(target)
		if err != nil {
			return err
		}
		if local != "" {
			body["local_paths"] = []string{local}
		} else {
			body["path"] = rel
		}
	case !picker:
		if copied := clipboardFiles(); len(copied) > 0 {
			body["local_paths"] = copied
			names := make([]string, 0, len(copied))
			for _, p := range copied {
				names = append(names, filepath.Base(p))
			}
			fmt.Fprintf(a.out, "Sending what you copied: %s\n", strings.Join(names, ", "))
		}
	}
	if err := a.ensureOn(ctx); err != nil {
		return err
	}
	if body["path"] == nil && body["local_paths"] == nil {
		if terminal || !hasDisplay() {
			return errors.New("choose what to send: vaultctl download <file or folder>, or copy files in the file manager first")
		}
		return a.openApp(ctx, "/download")
	}
	var link linkView
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
	target, minutes, count, askPassword := "", 60, 0, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--expires":
			if i+1 >= len(args) {
				return errors.New("--expires needs a duration such as 10m, 1h or 24h")
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
		return errors.New("usage: vaultctl share <file or folder> [--expires 1h] [--downloads N] [--password]")
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
	fmt.Fprintln(a.out, "Works on this Wi-Fi while Vault is on. Stop it any time in Vault › Download › Share links.")
	return nil
}

// parseExpiry reads 10m, 1h, 24h (at most 24 hours) into minutes.
func parseExpiry(s string) (int, error) {
	bad := errors.New("--expires must look like 10m, 1h or 24h (at most 24h)")
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
	if n > 24*60 {
		return 0, bad
	}
	return n, nil
}

// resolveTarget turns a typed path into a Vault path, or, for a file
// elsewhere on this computer, its absolute path.
func (a *app) resolveTarget(arg string) (rel, local string, err error) {
	rel, err = a.vaultRel(arg)
	if err == nil {
		return rel, "", nil
	}
	if abs, aerr := filepath.Abs(arg); aerr == nil {
		if _, serr := os.Lstat(abs); serr == nil {
			return "", abs, nil
		}
	}
	return "", "", err
}

// clipboardFiles returns the files copied in the file manager (Nautilus
// and others put file:// URIs on the clipboard), or nothing when the
// clipboard holds anything else. It only reads the clipboard.
func clipboardFiles() []string {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return nil
	}
	wlPaste, err := exec.LookPath("wl-paste")
	if err != nil {
		return nil
	}
	read := func(args ...string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, wlPaste, args...).Output()
		if err != nil || len(out) > 1<<20 {
			return ""
		}
		return string(out)
	}
	types := "\n" + read("--list-types") + "\n"
	for _, t := range []string{"x-special/gnome-copied-files", "text/uri-list", "text/plain;charset=utf-8", "text/plain", "UTF8_STRING"} {
		if !strings.Contains(types, "\n"+t+"\n") {
			continue
		}
		if files := parseCopiedFiles(read("--no-newline", "--type", t)); len(files) > 0 {
			return files
		}
	}
	return nil
}

// parseCopiedFiles reads file:// URIs or absolute paths, one per line.
// Anything that isn't entirely existing files (ordinary copied text) gives
// nothing.
func parseCopiedFiles(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		switch line {
		case "", "copy", "cut":
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		p := line
		if strings.HasPrefix(line, "file://") {
			u, err := url.Parse(line)
			if err != nil || (u.Host != "" && u.Host != "localhost") {
				return nil
			}
			p = u.Path
		}
		if !filepath.IsAbs(p) {
			return nil
		}
		if _, err := os.Lstat(p); err != nil {
			return nil
		}
		out = append(out, filepath.Clean(p))
	}
	return out
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
	return launch(a.baseURL() + lr.Path + "&next=" + path)
}

// launch opens url in a window: Omarchy's web-app window if available,
// otherwise the default browser. A launcher that fails within a moment
// (no browser configured, no display) falls through to the next one.
func launch(url string) error {
	if !hasDisplay() {
		return errors.New("no graphical session")
	}
	var last error = errors.New("no browser launcher found (install xdg-utils)")
	for _, name := range []string{"omarchy-launch-webapp", "xdg-open"} {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		cmd := exec.Command(p, url)
		var stderr strings.Builder
		cmd.Stdout, cmd.Stderr = nil, &stderr
		if err := cmd.Start(); err != nil {
			last = err
			continue
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err == nil {
				return nil // handed off to a running browser
			}
			msg := firstLine(stderr.String(), err.Error())
			if !strings.HasPrefix(msg, name) {
				msg = name + ": " + msg
			}
			last = errors.New(msg)
		case <-time.After(1500 * time.Millisecond):
			return nil // still running: the window is open
		}
	}
	return last
}

func firstLine(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	line, _, _ := strings.Cut(s, "\n")
	return line
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
		what := "Vault › " + link.Path
		if strings.HasPrefix(link.Path, "/") { // copied files from this computer
			var names []string
			for _, p := range strings.Split(link.Path, "\n") {
				names = append(names, filepath.Base(p))
			}
			what = strings.Join(names, ", ")
		}
		fmt.Fprintf(w, "Sending: %s   ·   valid until %s   ·   Ctrl+C to stop\n\n", what, link.ExpiresAt.Local().Format("15:04"))
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
