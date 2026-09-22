package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/qrsvg"
)

type linkView struct {
	ID        string    `json:"id"`
	Folder    string    `json:"folder"`
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
	fmt.Fprintln(w, "UPLOAD TO VAULT  ·  Phone → Vault")
	fmt.Fprintln(w)
	fmt.Fprint(w, qr)
	fmt.Fprintf(w, "\nScan with your phone (same Wi-Fi as this computer).\n%s\n", link.URL)
	fmt.Fprintf(w, "Files go to: Vault › %s   ·   valid until %s   ·   Ctrl+C to stop\n\n", link.Folder, link.ExpiresAt.Local().Format("15:04"))

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	seen := 0
	for {
		select {
		case <-ctx.Done():
			// Stop the link when the user quits.
			_ = a.call(context.Background(), http.MethodDelete, "/api/upload-session/"+link.ID, nil, nil)
			fmt.Fprintf(w, "\nStopped. %d file(s) received.\n", seen)
			return nil
		case <-time.After(2 * time.Second):
		}
		var cur linkView
		if err := a.call(ctx, http.MethodGet, "/api/upload-session/"+link.ID, nil, &cur); err != nil {
			continue
		}
		for _, f := range cur.Received[min(seen, len(cur.Received)):] {
			fmt.Fprintf(w, "  ✓ %s  (%s)\n", f.Name, humanBytes(uint64(f.Size)))
		}
		seen = len(cur.Received)
		if cur.State != "active" {
			fmt.Fprintf(w, "\nLink %s. %d file(s) received into Vault › %s.\n", strings.ReplaceAll(cur.State, "_", " "), seen, cur.Folder)
			return nil
		}
	}
}
