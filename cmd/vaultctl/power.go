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

	"github.com/TouchWorkStation/Omarchy-Vault/internal/version"
)

const unit = "omarchy-vault.service"

// running reports whether the Vault service answers.
func (a *app) running(ctx context.Context) bool {
	_, ok := a.runningVersion(ctx)
	return ok
}

// runningVersion reports the version of the Vault that answers, if any.
func (a *app) runningVersion(ctx context.Context) (string, bool) {
	var s struct {
		Version string `json:"version"`
	}
	if err := a.getJSON(ctx, "/api/session", &s); err != nil {
		return "", false
	}
	return s.Version, true
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
	if v, ok := a.runningVersion(ctx); ok {
		// After an update the old Vault keeps running until restarted;
		// restart it so the new features work straight away.
		if v != "" && !strings.HasSuffix(v, "-dev") && !strings.HasSuffix(version.Version, "-dev") && v != version.Version {
			if out, err := systemctlUser("is-active", unit); err == nil && strings.TrimSpace(string(out)) == "active" {
				fmt.Fprintf(os.Stderr, "Vault was updated (%s -> %s); restarting it…\n", v, version.Version)
				if out, err := systemctlUser("restart", unit); err != nil {
					return fmt.Errorf("could not restart Vault: %s", strings.TrimSpace(string(out)))
				}
				return a.waitUp(ctx)
			}
		}
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
	if err := a.waitUp(ctx); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "Vault is on: http://%s\n", a.cfg.Listen)
	return nil
}

// waitUp waits for Vault to answer after starting it.
func (a *app) waitUp(ctx context.Context) error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if a.running(ctx) {
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
