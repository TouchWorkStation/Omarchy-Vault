package shortcuts

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Choosing your own keys. The dashboard sends only an action id, modifiers
// and a key; the command for each action is fixed here, so nothing a
// browser sends can end up as a command in Hyprland's config.

// ModChoices are the modifiers offered in the editor, in display order.
var ModChoices = []string{"SUPER", "SHIFT", "CTRL", "ALT"}

// KeyChoices are the keys offered: letters, digits and F-keys. Anything
// else is refused, which also keeps config lines free of separators.
func KeyChoices() []string {
	var keys []string
	for c := 'A'; c <= 'Z'; c++ {
		keys = append(keys, string(c))
	}
	for c := '0'; c <= '9'; c++ {
		keys = append(keys, string(c))
	}
	for i := 1; i <= 12; i++ {
		keys = append(keys, fmt.Sprintf("F%d", i))
	}
	return keys
}

// Request is one shortcut the user wants.
type Request struct {
	ID   string   `json:"id"`
	Mods []string `json:"mods"`
	Key  string   `json:"key"`
}

// Result is the verdict for one Request.
type Result struct {
	ID        string    `json:"id"`
	Combo     string    `json:"combo"`
	Status    string    `json:"status"` // available | conflict | duplicate | invalid
	Message   string    `json:"message,omitempty"`
	Conflicts []Binding `json:"conflicts,omitempty"`
	// Suggestion is a free alternative for a conflict.
	Suggestion *Planned `json:"suggestion,omitempty"`
}

// Build validates a request and returns the binding it describes.
func Build(r Request) (Planned, error) {
	var base *Planned
	for _, p := range Plan() {
		if p.ID == r.ID {
			p := p
			base = &p
		}
	}
	if base == nil {
		return Planned{}, fmt.Errorf("unknown shortcut %q", r.ID)
	}
	seen := map[string]bool{}
	for _, m := range r.Mods {
		m = strings.ToUpper(strings.TrimSpace(m))
		ok := false
		for _, c := range ModChoices {
			ok = ok || m == c
		}
		if !ok {
			return Planned{}, fmt.Errorf("%q is not a modifier Vault offers", m)
		}
		seen[m] = true
	}
	if !seen["SUPER"] && !seen["CTRL"] && !seen["ALT"] {
		return Planned{}, errors.New("use Super, Ctrl or Alt, so the shortcut doesn't fire while you type")
	}
	key := strings.ToUpper(strings.TrimSpace(r.Key))
	valid := false
	for _, k := range KeyChoices() {
		valid = valid || key == k
	}
	if !valid {
		return Planned{}, fmt.Errorf("%q is not a key Vault offers (A-Z, 0-9, F1-F12)", r.Key)
	}
	p := *base
	p.Mods = nil
	for _, m := range ModChoices {
		if seen[m] {
			p.Mods = append(p.Mods, m)
		}
	}
	p.Key = key
	return p, nil
}

// isVault reports whether a binding is one of Vault's own (in its file,
// or running vaultctl), which a new choice may replace.
func isVault(b Binding) bool {
	return strings.Contains(b.Arg, "vaultctl ") || strings.HasSuffix(b.Source, "/"+includeName)
}

// Bindings collects the user's existing bindings from Hyprland and its
// config files, and whether that could be done at all.
func (in Inspector) Bindings(ctx context.Context) ([]Binding, bool) {
	all, rep := in.collect(ctx)
	return all, len(rep.Sources) > 0
}

// CheckAll checks requested shortcuts against existing bindings (Vault's
// own excluded, since they are being replaced) and against each other.
func CheckAll(existing []Binding, reqs []Request) []Result {
	var others []Binding
	for _, b := range existing {
		if !isVault(b) {
			others = append(others, b)
		}
	}
	out := make([]Result, len(reqs))
	built := make([]*Planned, len(reqs))
	for i, r := range reqs {
		out[i] = Result{ID: r.ID}
		p, err := Build(r)
		if err != nil {
			out[i].Status, out[i].Message = "invalid", err.Error()
			continue
		}
		built[i] = &p
		out[i].Combo = p.Combo()
		for _, b := range others {
			if sameCombo(p.Mods, p.Key, b.Mods, b.Key) {
				out[i].Conflicts = appendUnique(out[i].Conflicts, b)
			}
		}
		if len(out[i].Conflicts) > 0 {
			out[i].Status = "conflict"
			if s, ok := suggest(p, others); ok {
				out[i].Suggestion = &s
			}
			continue
		}
		out[i].Status = "available"
	}
	for i := range reqs {
		for j := 0; j < i; j++ {
			if built[i] != nil && built[j] != nil && out[i].Status == "available" &&
				sameCombo(built[i].Mods, built[i].Key, built[j].Mods, built[j].Key) {
				out[i].Status = "duplicate"
				out[i].Message = "Another Vault shortcut already uses " + out[i].Combo + "."
			}
		}
	}
	return out
}

// Installed reads Vault's own shortcut file: what is set up now.
func Installed(home string) []Planned {
	f, err := os.Open(IncludePath(home))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Planned
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "bindd") {
			continue
		}
		_, rest, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		parts := strings.SplitN(rest, ",", 5)
		if len(parts) != 5 {
			continue
		}
		cmd := strings.TrimSpace(parts[4])
		for _, p := range Plan() {
			if strings.HasSuffix(cmd, "vaultctl "+p.ID) {
				p.Mods = parseMods(parts[0])
				p.Key = strings.TrimSpace(parts[1])
				out = append(out, p)
			}
		}
	}
	return out
}

// WithCommandPath points each shortcut's command at vaultctl by full path,
// because Hyprland runs them with the session's PATH, which may not
// include ~/.local/bin. An empty or unusual path keeps the plain command.
func WithCommandPath(bs []Planned, vaultctl string) []Planned {
	if vaultctl == "" || strings.ContainsAny(vaultctl, " '\"\\$`;&|<>(){}\n") {
		return bs
	}
	out := make([]Planned, len(bs))
	for i, b := range bs {
		if rest, ok := strings.CutPrefix(b.Command, "vaultctl "); ok {
			b.Command = vaultctl + " " + rest
		}
		out[i] = b
	}
	return out
}
