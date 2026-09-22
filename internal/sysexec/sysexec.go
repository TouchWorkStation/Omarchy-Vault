// Package sysexec runs a small, fixed set of read-only system tools.
//
// Vault never executes commands supplied by a web request. Every command that
// Vault runs goes through this package, which only accepts binaries on an
// explicit allowlist, never invokes a shell, applies a timeout, and forces a
// stable C locale so output can be parsed reliably.
package sysexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// allowed lists every binary Vault may execute. Adding to this list is a
// security-relevant change and must be reviewed as such (see SECURITY.md).
var allowed = map[string]bool{
	"lsblk":     true, // read-only block device inventory
	"findmnt":   true, // read-only mount table
	"blkid":     true, // read-only filesystem identification
	"smartctl":  true, // read-only SMART health (-H/-A/-i only)
	"hyprctl":   true, // read-only keybinding listing (binds -j)
	"systemctl": true, // read-only unit state (is-active)
}

// ErrNotAllowed is returned when a caller asks for a binary that is not on
// the allowlist.
var ErrNotAllowed = errors.New("sysexec: command not allowed")

// ErrNotFound is returned when an allowed binary is not installed.
var ErrNotFound = errors.New("sysexec: command not installed")

// Runner executes allowlisted commands. It exists so callers can be tested
// with canned output.
type Runner interface {
	// Output runs name with args and returns stdout. When the process exits
	// non-zero, stdout is still returned together with an *ExitError so
	// tools such as smartctl, which encode status in exit bits, can be parsed.
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
	// Available reports whether name is allowlisted and installed.
	Available(name string) bool
}

// ExitError reports a non-zero exit status.
type ExitError struct {
	Name   string
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("%s exited with status %d", e.Name, e.Code)
}

// System is the real Runner.
type System struct {
	// Timeout bounds each command. Defaults to 10s.
	Timeout time.Duration
}

// Available implements Runner.
func (s System) Available(name string) bool {
	if !allowed[name] {
		return false
	}
	_, err := exec.LookPath(name)
	return err == nil
}

// Output implements Runner.
func (s System) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	if !allowed[name] {
		return nil, fmt.Errorf("%w: %q", ErrNotAllowed, name)
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return stdout.Bytes(), &ExitError{Name: name, Code: ee.ExitCode(), Stderr: trim(stderr.String())}
		}
		return nil, fmt.Errorf("sysexec: %s: %w", name, err)
	}
	return stdout.Bytes(), nil
}

func trim(s string) string {
	const max = 512
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// Fake is a Runner for tests. Keys are "name arg1 arg2 ...".
type Fake struct {
	Outputs   map[string][]byte
	Errors    map[string]error
	Installed map[string]bool
	Calls     []string
}

// Available implements Runner.
func (f *Fake) Available(name string) bool { return allowed[name] && f.Installed[name] }

// Output implements Runner.
func (f *Fake) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	if !allowed[name] {
		return nil, fmt.Errorf("%w: %q", ErrNotAllowed, name)
	}
	key := name
	for _, a := range args {
		key += " " + a
	}
	f.Calls = append(f.Calls, key)
	if err, ok := f.Errors[key]; ok {
		return f.Outputs[key], err
	}
	if out, ok := f.Outputs[key]; ok {
		return out, nil
	}
	if !f.Installed[name] {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return nil, fmt.Errorf("fake: no output registered for %q", key)
}
