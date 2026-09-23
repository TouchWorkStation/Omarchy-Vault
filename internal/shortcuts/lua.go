package shortcuts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/sysexec"
)

// Hyprland's Lua config (hyprland.lua), used by current Omarchy. Vault
// writes its shortcuts to its own Lua file and loads it with one line at
// the end of hyprland.lua, wrapped in pcall so Hyprland's config still
// loads if that file is ever missing or broken.

const (
	luaName   = "omarchy_vault.lua"
	luaHeader = "-- Omarchy Vault shortcuts. Managed by `vaultctl shortcuts`; remove with `vaultctl shortcuts remove`."
	luaNote   = "-- Omarchy Vault shortcuts (remove with: vaultctl shortcuts remove)"
)

// IsLua reports whether a Hyprland config path is a Lua config.
func IsLua(conf string) bool { return strings.HasSuffix(conf, ".lua") }

// LuaIncludePath is ~/.config/hypr/omarchy_vault.lua.
func LuaIncludePath(home string) string { return filepath.Join(home, ".config", "hypr", luaName) }

// LuaLoadLine is the line added to hyprland.lua.
func LuaLoadLine() string {
	return `pcall(dofile, (os.getenv("HOME") or "") .. "/.config/hypr/` + luaName + `")`
}

// luaKeys renders mods and key the way Hyprland's Lua config writes them:
// "SUPER + ALT + U".
func luaKeys(p Planned) string {
	return strings.Join(append(append([]string{}, p.Mods...), p.Key), " + ")
}

func luaQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return `"` + s + `"`
}

// RenderLua returns Vault's Lua shortcut file. It uses Omarchy's o.bind
// (which adds the description to Omarchy's keybinding menu) when present,
// otherwise Hyprland's hl.bind. Each binding is guarded so one failure
// can't stop the rest or the user's config.
func RenderLua(bs []Planned) string {
	var b strings.Builder
	b.WriteString(luaHeader + "\n")
	b.WriteString(`local function vault_bind(keys, description, command)
  if type(o) == "table" and type(o.bind) == "function" then
    o.bind(keys, description, command)
  else
    hl.bind(keys, hl.dsp.exec_cmd(command))
  end
end
`)
	for _, p := range bs {
		fmt.Fprintf(&b, "pcall(vault_bind, %s, %s, %s)\n", luaQuote(luaKeys(p)), luaQuote(p.Description), luaQuote(p.Command))
	}
	return b.String()
}

func hasLuaLoadLine(conf string) bool {
	for _, l := range strings.Split(conf, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "--") {
			continue
		}
		if strings.Contains(t, luaName) {
			return true
		}
	}
	return false
}

// installLua writes Vault's Lua file and, if missing, appends the load
// line to hyprland.lua.
func installLua(home, conf string, bs []Planned) (bool, error) {
	main, err := os.ReadFile(conf)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", conf, err)
	}
	inc := LuaIncludePath(home)
	if existing, err := os.ReadFile(inc); err == nil && !strings.HasPrefix(string(existing), luaHeader) {
		return false, fmt.Errorf("%s exists and was not written by Vault; leaving it alone", inc)
	}
	if err := os.MkdirAll(filepath.Dir(inc), 0o755); err != nil {
		return false, err
	}
	if err := writeAtomic(inc, RenderLua(bs)); err != nil {
		return false, err
	}
	if hasLuaLoadLine(string(main)) {
		return false, nil
	}
	c := string(main)
	if !strings.HasSuffix(c, "\n") {
		c += "\n"
	}
	c += "\n" + luaNote + "\n" + LuaLoadLine() + "\n"
	return true, writeAtomic(conf, c)
}

// removeLua deletes Vault's Lua file and the lines Vault added.
func removeLua(home, conf string) error {
	inc := LuaIncludePath(home)
	if existing, err := os.ReadFile(inc); err == nil {
		if !strings.HasPrefix(string(existing), luaHeader) {
			return fmt.Errorf("%s was not written by Vault; leaving it alone", inc)
		}
		if err := os.Remove(inc); err != nil {
			return err
		}
	}
	main, err := os.ReadFile(conf)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return writeWithout(conf, string(main), luaNote, LuaLoadLine())
}

// writeWithout removes exact lines from a config and tidies blank lines,
// leaving everything else byte for byte.
func writeWithout(path, content string, lines ...string) error {
	drop := map[string]bool{}
	for _, l := range lines {
		drop[l] = true
	}
	var out []string
	for _, l := range strings.Split(content, "\n") {
		if drop[strings.TrimSpace(l)] {
			continue
		}
		out = append(out, l)
	}
	cleaned := strings.Join(out, "\n")
	for strings.Contains(cleaned, "\n\n\n") {
		cleaned = strings.ReplaceAll(cleaned, "\n\n\n", "\n\n")
	}
	cleaned = strings.TrimRight(cleaned, "\n") + "\n"
	if cleaned == content {
		return nil
	}
	return writeAtomic(path, cleaned)
}

var luaBindRe = regexp.MustCompile(`(?m)^[^-\n]*?\b(?:hl\.bind|o\.bind|o\.rebind|vault_bind)\s*\(?\s*(?:pcall\s*\(\s*vault_bind\s*,\s*)?"([^"]+)"(?:\s*,\s*("(?:[^"\\]|\\.)*"|nil))?`)
var luaUnbindRe = regexp.MustCompile(`(?m)^[^-\n]*?\bhl\.unbind\s*\(\s*"([^"]+)"`)

// parseLuaKeys splits "SUPER + ALT + U" into modifiers and key.
func parseLuaKeys(s string) ([]string, string, bool) {
	parts := strings.Split(s, "+")
	if len(parts) < 1 {
		return nil, "", false
	}
	key := strings.TrimSpace(parts[len(parts)-1])
	if key == "" {
		return nil, "", false
	}
	return parseMods(strings.Join(parts[:len(parts)-1], " ")), key, true
}

// parseLuaTree scans Lua config files for bindings written with literal
// key strings: the user's ~/.config/hypr/*.lua and Omarchy's defaults.
// Bindings built from variables can't be read this way; the running
// Hyprland (hyprctl) covers those.
func parseLuaTree(conf, home string) ([]Binding, []string) {
	var dirs []string
	dirs = append(dirs, filepath.Dir(conf))
	for _, d := range []string{os.Getenv("OMARCHY_PATH"), filepath.Join(home, ".local", "share", "omarchy"), "/usr/share/omarchy"} {
		if d != "" {
			dirs = append(dirs, filepath.Join(d, "default", "hypr"))
		}
	}
	var binds []Binding
	var files []string
	seen := map[string]bool{}
	for _, dir := range dirs {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.lua"))
		more, _ := filepath.Glob(filepath.Join(dir, "*", "*.lua"))
		for _, f := range append(matches, more...) {
			if seen[f] || filepath.Base(f) == luaName {
				continue
			}
			seen[f] = true
			data, err := os.ReadFile(f)
			if err != nil || len(data) > 1<<20 {
				continue
			}
			files = append(files, f)
			text := string(data)
			for _, m := range luaBindRe.FindAllStringSubmatch(text, -1) {
				mods, key, ok := parseLuaKeys(m[1])
				if !ok {
					continue
				}
				desc := strings.Trim(m[2], `"`)
				if desc == "nil" {
					desc = ""
				}
				binds = append(binds, Binding{Mods: mods, Key: key, Dispatcher: "exec", Description: desc, Source: f})
			}
			for _, m := range luaUnbindRe.FindAllStringSubmatch(text, -1) {
				mods, key, ok := parseLuaKeys(m[1])
				if !ok {
					continue
				}
				kept := binds[:0]
				for _, b := range binds {
					if !sameCombo(b.Mods, b.Key, mods, key) {
						kept = append(kept, b)
					}
				}
				binds = kept
			}
		}
	}
	return binds, files
}

var luaInstalledRe = regexp.MustCompile(`pcall\(vault_bind,\s*"([^"]+)",\s*"(?:[^"\\]|\\.)*",\s*"((?:[^"\\]|\\.)*)"\)`)

// installedLua reads the shortcuts in Vault's Lua file.
func installedLua(home string) []Planned {
	data, err := os.ReadFile(LuaIncludePath(home))
	if err != nil {
		return nil
	}
	var out []Planned
	for _, m := range luaInstalledRe.FindAllStringSubmatch(string(data), -1) {
		mods, key, ok := parseLuaKeys(m[1])
		if !ok {
			continue
		}
		for _, p := range Plan() {
			if strings.HasSuffix(m[2], "vaultctl "+p.ID) {
				p.Mods, p.Key = mods, key
				out = append(out, p)
			}
		}
	}
	return out
}

// Reload asks the running Hyprland to reload its config so changed
// shortcuts work at once. Harmless when Hyprland isn't running.
func Reload(ctx context.Context, run sysexec.Runner) {
	if run != nil && run.Available("hyprctl") {
		_, _ = run.Output(ctx, "hyprctl", "reload")
	}
}
