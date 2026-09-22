// Package shortcuts plans Vault's Hyprland keybindings and detects
// conflicts with bindings the user already has.
//
// Installing (install.go) is an explicit, user-approved step that writes
// only Vault's own file plus one source line; an existing binding (Omarchy
// default, Beam, or the user's own) is never overwritten. See
// docs/shortcuts.md.
package shortcuts

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/sysexec"
)

// Planned is a binding Vault would like to install.
type Planned struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Direction   string   `json:"direction"`
	Mods        []string `json:"mods"`
	Key         string   `json:"key"`
	Command     string   `json:"command"`
	Description string   `json:"description"`
	// Since is the milestone whose command makes this shortcut useful;
	// shortcuts are only installed once their command exists.
	Since int `json:"since"`
}

// Combo renders the binding for humans, e.g. "Super + Shift + U".
func (p Planned) Combo() string { return comboString(p.Mods, p.Key) }

// Line renders the Hyprland config line Vault would install.
func (p Planned) Line() string {
	return fmt.Sprintf("bindd = %s, %s, %s, exec, %s", strings.Join(p.Mods, " "), p.Key, p.Description, p.Command)
}

// Plan is the default set of Vault shortcuts.
func Plan() []Planned {
	return []Planned{
		{ID: "open", Label: "Open Vault", Direction: "", Mods: []string{"SUPER", "SHIFT"}, Key: "V", Command: "vaultctl open", Description: "Open Vault", Since: 1},
		{ID: "upload", Label: "Upload to Vault", Direction: "Phone → Vault", Mods: []string{"SUPER", "SHIFT"}, Key: "U", Command: "vaultctl upload", Description: "Upload to Vault", Since: 4},
		{ID: "download", Label: "Download from Vault", Direction: "Vault → Phone", Mods: []string{"SUPER", "SHIFT"}, Key: "D", Command: "vaultctl download", Description: "Download from Vault", Since: 5},
	}
}

// Binding is an existing keybinding found on the system.
type Binding struct {
	Mods        []string `json:"mods"`
	Key         string   `json:"key"`
	Dispatcher  string   `json:"dispatcher"`
	Arg         string   `json:"arg,omitempty"`
	Description string   `json:"description,omitempty"`
	Source      string   `json:"source"`
}

// Combo renders the binding for humans.
func (b Binding) Combo() string { return comboString(b.Mods, b.Key) }

// Check is the conflict analysis for one planned shortcut.
type Check struct {
	Planned
	Combo      string    `json:"combo"`
	Line       string    `json:"binding_line"`
	Status     string    `json:"status"` // available | conflict | installed | unknown
	Conflicts  []Binding `json:"conflicts,omitempty"`
	Suggestion *Planned  `json:"suggestion,omitempty"`
	Options    []string  `json:"options,omitempty"`
}

// Report is the full analysis.
type Report struct {
	Sources  []string `json:"sources"`
	Checks   []Check  `json:"checks"`
	Warnings []string `json:"warnings,omitempty"`
	// Installed is always false in Milestone 1: Vault does not write
	// Hyprland config yet.
	Installed bool `json:"installed"`
}

// Hyprland modmask bits (see hyprland's KeybindManager).
const (
	modShift = 1 << 0
	modCaps  = 1 << 1
	modCtrl  = 1 << 2
	modAlt   = 1 << 3
	modMod2  = 1 << 4 // NumLock
	modMod3  = 1 << 5
	modSuper = 1 << 6
	modMod5  = 1 << 7
)

var modOrder = []string{"SUPER", "CTRL", "ALT", "SHIFT", "MOD3", "MOD5"}

func maskToMods(mask int) []string {
	var mods []string
	if mask&modSuper != 0 {
		mods = append(mods, "SUPER")
	}
	if mask&modCtrl != 0 {
		mods = append(mods, "CTRL")
	}
	if mask&modAlt != 0 {
		mods = append(mods, "ALT")
	}
	if mask&modShift != 0 {
		mods = append(mods, "SHIFT")
	}
	if mask&modMod3 != 0 {
		mods = append(mods, "MOD3")
	}
	if mask&modMod5 != 0 {
		mods = append(mods, "MOD5")
	}
	return mods
}

// normalizeMod maps Hyprland's modifier aliases to canonical names.
func normalizeMod(m string) string {
	switch strings.ToUpper(strings.TrimSpace(m)) {
	case "SUPER", "WIN", "LOGO", "MOD4", "META":
		return "SUPER"
	case "CTRL", "CONTROL":
		return "CTRL"
	case "ALT", "MOD1":
		return "ALT"
	case "SHIFT":
		return "SHIFT"
	case "CAPS", "MOD2", "":
		return "" // lock modifiers are ignored by Hyprland matching
	default:
		return strings.ToUpper(strings.TrimSpace(m))
	}
}

// parseMods splits a Hyprland modifier string such as "SUPER SHIFT",
// "SUPER_SHIFT" or "SUPER+SHIFT" into canonical, sorted modifiers.
func parseMods(s string) []string {
	f := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '_' || r == '+' || r == '\t' })
	seen := map[string]bool{}
	for _, m := range f {
		if n := normalizeMod(m); n != "" {
			seen[n] = true
		}
	}
	var out []string
	for _, m := range modOrder {
		if seen[m] {
			out = append(out, m)
			delete(seen, m)
		}
	}
	var rest []string
	for m := range seen {
		rest = append(rest, m)
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func comboString(mods []string, key string) string {
	parts := make([]string, 0, len(mods)+1)
	for _, m := range mods {
		parts = append(parts, strings.ToUpper(m[:1])+strings.ToLower(m[1:]))
	}
	return strings.Join(append(parts, strings.ToUpper(key)), " + ")
}

func sameCombo(aMods []string, aKey string, bMods []string, bKey string) bool {
	if !strings.EqualFold(strings.TrimSpace(aKey), strings.TrimSpace(bKey)) {
		return false
	}
	a, b := parseMods(strings.Join(aMods, " ")), parseMods(strings.Join(bMods, " "))
	return strings.Join(a, " ") == strings.Join(b, " ")
}

type hyprBind struct {
	Modmask     int    `json:"modmask"`
	Submap      string `json:"submap"`
	Key         string `json:"key"`
	Keycode     int    `json:"keycode"`
	Description string `json:"description"`
	Dispatcher  string `json:"dispatcher"`
	Arg         string `json:"arg"`
	Mouse       bool   `json:"mouse"`
}

// parseHyprctl parses `hyprctl binds -j`. Bindings in submaps other than the
// default one are ignored because they cannot collide with global shortcuts.
func parseHyprctl(data []byte) ([]Binding, error) {
	var raw []hyprBind
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("shortcuts: parse hyprctl binds: %w", err)
	}
	var out []Binding
	for _, b := range raw {
		if b.Mouse || b.Key == "" || (b.Submap != "" && b.Submap != "global") {
			continue
		}
		mask := b.Modmask &^ (modCaps | modMod2)
		out = append(out, Binding{
			Mods:        maskToMods(mask),
			Key:         b.Key,
			Dispatcher:  b.Dispatcher,
			Arg:         b.Arg,
			Description: b.Description,
			Source:      "hyprctl (running Hyprland)",
		})
	}
	return out, nil
}

// Inspector gathers existing bindings.
type Inspector struct {
	Run sysexec.Runner
	// ConfigPath is the root Hyprland config. Defaults to
	// ~/.config/hypr/hyprland.conf.
	ConfigPath string
	// Home is used to expand ~ in source= lines.
	Home string
}

// Analyze collects existing bindings from the running compositor and from
// config files, then checks each planned shortcut against them.
func (in Inspector) Analyze(ctx context.Context) Report {
	rep := Report{Sources: []string{}}
	var all []Binding

	if in.Run != nil && in.Run.Available("hyprctl") {
		out, err := in.Run.Output(ctx, "hyprctl", "binds", "-j")
		if err == nil {
			bs, perr := parseHyprctl(out)
			if perr == nil {
				all = append(all, bs...)
				rep.Sources = append(rep.Sources, "hyprctl")
			} else {
				rep.Warnings = append(rep.Warnings, "Could not read bindings from Hyprland.")
			}
		} else {
			rep.Warnings = append(rep.Warnings, "Hyprland is not running in this session; checked config files only.")
		}
	}

	if in.ConfigPath != "" {
		bs, files, warns := parseConfigTree(in.ConfigPath, in.Home)
		rep.Warnings = append(rep.Warnings, warns...)
		if len(files) > 0 {
			all = append(all, bs...)
			rep.Sources = append(rep.Sources, files...)
		}
	}

	known := len(rep.Sources) > 0
	if !known {
		rep.Warnings = append(rep.Warnings, "No Hyprland configuration was found, so conflicts cannot be ruled out. Vault will not install shortcuts until it can check.")
	}

	for _, p := range Plan() {
		c := Check{Planned: p, Combo: p.Combo(), Line: p.Line()}
		for _, b := range all {
			if !sameCombo(p.Mods, p.Key, b.Mods, b.Key) {
				continue
			}
			if isOurs(b, p) {
				c.Status = "installed"
				continue
			}
			c.Conflicts = appendUnique(c.Conflicts, b)
		}
		switch {
		case len(c.Conflicts) > 0:
			c.Status = "conflict"
			c.Options = []string{"choose_another", "copy_binding", "skip"}
			if s, ok := suggest(p, all); ok {
				c.Suggestion = &s
			}
		case c.Status == "installed":
		case !known:
			c.Status = "unknown"
			c.Options = []string{"copy_binding", "skip"}
		default:
			c.Status = "available"
		}
		rep.Checks = append(rep.Checks, c)
	}
	return rep
}

func isOurs(b Binding, p Planned) bool {
	return strings.Contains(b.Arg, p.Command)
}

func appendUnique(list []Binding, b Binding) []Binding {
	for _, x := range list {
		if x.Dispatcher == b.Dispatcher && x.Arg == b.Arg && sameCombo(x.Mods, x.Key, b.Mods, b.Key) {
			return list
		}
	}
	return append(list, b)
}

// suggest proposes a free alternative for a conflicting shortcut.
func suggest(p Planned, existing []Binding) (Planned, bool) {
	alts := [][]string{
		{"SUPER", "ALT"},
		{"SUPER", "CTRL", "SHIFT"},
		{"SUPER", "CTRL", "ALT"},
	}
	for _, mods := range alts {
		free := true
		for _, b := range existing {
			if sameCombo(mods, p.Key, b.Mods, b.Key) {
				free = false
				break
			}
		}
		if free {
			s := p
			s.Mods = mods
			return s, true
		}
	}
	return Planned{}, false
}
