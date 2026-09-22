package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const unit = "omarchy-vault.service"

// running reports whether the Vault service answers.
func (a *app) running(ctx context.Context) bool {
	var s map[string]any
	return a.getJSON(ctx, "/api/session", &s) == nil
}

func systemctlUser(args ...string) ([]byte, error) {
	path, err := exec.LookPath("systemctl")
	if err != nil {
		return nil, errors.New("systemctl not found")
	}
	return exec.Command(path, append([]string{"--user"}, args...)...).CombinedOutput()
}

// ensureOn turns Vault on if it is off. Used by explicit actions such as
// opening Vault; Vault never starts by itself.
func (a *app) ensureOn(ctx context.Context) error {
	if a.running(ctx) {
		return nil
	}
	fmt.Fprintln(os.Stderr, "Turning Vault on…")
	return a.powerOn(ctx)
}

func (a *app) powerOn(ctx context.Context) error {
	if a.running(ctx) {
		fmt.Fprintln(a.out, "Vault is already on.")
		return nil
	}
	if out, err := systemctlUser("start", unit); err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "not found") || strings.Contains(msg, "not be found") {
			return errors.New("the Vault service is not installed for this user; run ./scripts/install.sh")
		}
		return fmt.Errorf("could not turn Vault on: %s", msg)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if a.running(ctx) {
			fmt.Fprintf(a.out, "Vault is on: http://%s\n", a.cfg.Listen)
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("Vault did not come up; see: vaultctl logs")
}

func (a *app) powerOff(ctx context.Context) error {
	wasRunning := a.running(ctx)
	// Prefer systemd so the unit is cleanly inactive; fall back to asking a
	// manually started vaultd to exit.
	if out, err := systemctlUser("is-active", unit); err == nil && strings.TrimSpace(string(out)) == "active" {
		if out, err := systemctlUser("stop", unit); err != nil {
			return fmt.Errorf("could not turn Vault off: %s", strings.TrimSpace(string(out)))
		}
	} else if wasRunning {
		if err := a.call(ctx, http.MethodPost, "/api/power/off", nil, nil); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(a.out, "Vault is already off.")
		return nil
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && a.running(ctx) {
		time.Sleep(250 * time.Millisecond)
	}
	fmt.Fprintln(a.out, "Vault is off. Nothing runs in the background until you turn it on again.")
	return nil
}
