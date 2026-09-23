package shortcuts

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Vault's shortcuts live in their own file, sourced from hyprland.conf by a
// single line Vault adds (after asking) and can remove again. Vault never
// edits any other binding file.
const (
	fileHeader  = "# Omarchy Vault shortcuts. Managed by `vaultctl shortcuts`; remove with `vaultctl shortcuts remove`."
	sourceNote  = "# Omarchy Vault shortcuts (remove with: vaultctl shortcuts remove)"
	includeName = "omarchy-vault.conf"
)

// IncludePath is ~/.config/hypr/omarchy-vault.conf.
func IncludePath(home string) string { return filepath.Join(home, ".config", "hypr", includeName) }

// SourceLine is the line added to hyprland.conf.
func SourceLine() string { return "source = ~/.config/hypr/" + includeName }

// Render returns the include file for the chosen bindings.
func Render(bs []Planned) string {
	var b strings.Builder
	b.WriteString(fileHeader + "\n")
	for _, p := range bs {
		b.WriteString(p.Line() + "\n")
	}
	return b.String()
}

// Choose decides what to install: free shortcuts whose command exists by
// milestone, plus suggested alternatives for conflicts when useSuggestions
// is set. Everything else is reported, never overwritten.
func Choose(rep Report, milestone int, useSuggestions bool) (install []Planned, skipped []string, err error) {
	if len(rep.Sources) == 0 {
		return nil, nil, errors.New("no Hyprland configuration found, so conflicts can't be checked; nothing was installed")
	}
	for _, c := range rep.Checks {
		switch {
		case c.Since > milestone:
			skipped = append(skipped, fmt.Sprintf("%s (%s): available from Milestone %d", c.Combo, c.Label, c.Since))
		case c.Status == "installed":
			install = append(install, c.Planned) // keep it in our file
		case c.Status == "available":
			install = append(install, c.Planned)
		case c.Status == "conflict" && useSuggestions && c.Suggestion != nil:
			install = append(install, *c.Suggestion)
		case c.Status == "conflict":
			skipped = append(skipped, fmt.Sprintf("%s (%s): already used; skipped", c.Combo, c.Label))
		}
	}
	return install, skipped, nil
}

func hasSourceLine(conf string) bool {
	for _, l := range strings.Split(conf, "\n") {
		t := strings.Join(strings.Fields(stripComment(l)), " ")
		if t == SourceLine() || strings.HasSuffix(t, "/"+includeName) && strings.HasPrefix(t, "source =") {
			return true
		}
	}
	return false
}

// writeAtomic replaces path, keeping its permissions.
func writeAtomic(path, content string) error {
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vault-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Install writes Vault's include file and, if missing, appends the source
// line to hyprland.conf. It returns whether hyprland.conf was changed.
func Install(home, hyprlandConf string, bs []Planned) (bool, error) {
	conf, err := os.ReadFile(hyprlandConf)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", hyprlandConf, err)
	}
	inc := IncludePath(home)
	if existing, err := os.ReadFile(inc); err == nil && !strings.HasPrefix(string(existing), fileHeader) {
		return false, fmt.Errorf("%s exists and was not written by Vault; leaving it alone", inc)
	}
	if err := os.MkdirAll(filepath.Dir(inc), 0o755); err != nil {
		return false, err
	}
	if err := writeAtomic(inc, Render(bs)); err != nil {
		return false, err
	}
	if hasSourceLine(string(conf)) {
		return false, nil
	}
	c := string(conf)
	if !strings.HasSuffix(c, "\n") {
		c += "\n"
	}
	c += "\n" + sourceNote + "\n" + SourceLine() + "\n"
	return true, writeAtomic(hyprlandConf, c)
}

// Remove deletes Vault's include file and the source line Vault added.
func Remove(home, hyprlandConf string) error {
	inc := IncludePath(home)
	if existing, err := os.ReadFile(inc); err == nil {
		if !strings.HasPrefix(string(existing), fileHeader) {
			return fmt.Errorf("%s was not written by Vault; leaving it alone", inc)
		}
		if err := os.Remove(inc); err != nil {
			return err
		}
	}
	conf, err := os.ReadFile(hyprlandConf)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var out []string
	for _, l := range strings.Split(string(conf), "\n") {
		t := strings.TrimSpace(l)
		if t == sourceNote || t == SourceLine() {
			continue
		}
		out = append(out, l)
	}
	cleaned := strings.Join(out, "\n")
	for strings.Contains(cleaned, "\n\n\n") {
		cleaned = strings.ReplaceAll(cleaned, "\n\n\n", "\n\n")
	}
	cleaned = strings.TrimRight(cleaned, "\n") + "\n"
	if cleaned == string(conf) {
		return nil
	}
	return writeAtomic(hyprlandConf, cleaned)
}
