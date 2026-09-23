package shortcuts

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// FindConfig locates the Hyprland config actually in use: the file the
// running Hyprland was started with (-c/--config), then
// $XDG_CONFIG_HOME/hypr, then ~/.config/hypr; in each folder hyprland.lua
// (Hyprland's Lua config, used by current Omarchy) before hyprland.conf.
// It returns the first that exists, or the default path and false.
func FindConfig(home string) (string, bool) {
	return findConfig("/proc", home, os.Getenv("XDG_CONFIG_HOME"), os.Getuid())
}

// ConfigCandidates lists where FindConfig looks, for messages.
func ConfigCandidates(home string) []string {
	return candidates("/proc", home, os.Getenv("XDG_CONFIG_HOME"), os.Getuid())
}

func findConfig(proc, home, xdg string, uid int) (string, bool) {
	for _, p := range candidates(proc, home, xdg, uid) {
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			return p, true
		}
	}
	return filepath.Join(home, ".config", "hypr", "hyprland.conf"), false
}

func candidates(proc, home, xdg string, uid int) []string {
	var out []string
	if p := runningConfig(proc, home, uid); p != "" {
		out = append(out, p)
	}
	var dirs []string
	if xdg != "" && filepath.IsAbs(xdg) {
		dirs = append(dirs, filepath.Join(xdg, "hypr"))
	}
	dirs = append(dirs, filepath.Join(home, ".config", "hypr"))
	seen := map[string]bool{}
	for _, d := range dirs {
		for _, name := range []string{"hyprland.lua", "hyprland.conf"} {
			if p := filepath.Join(d, name); !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// runningConfig reads the --config/-c argument of this user's Hyprland
// process, if it was started with one. Read-only; nothing is executed.
func runningConfig(proc, home string, uid int) string {
	entries, err := os.ReadDir(proc)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		dir := filepath.Join(proc, e.Name())
		if info, err := os.Stat(dir); err != nil || !ownedBy(info, uid) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		if !strings.Contains(strings.ToLower(filepath.Base(args[0])), "hyprland") {
			continue
		}
		for i, a := range args {
			var v string
			switch {
			case (a == "-c" || a == "--config") && i+1 < len(args):
				v = args[i+1]
			case strings.HasPrefix(a, "--config="):
				v = strings.TrimPrefix(a, "--config=")
			default:
				continue
			}
			if strings.HasPrefix(v, "~/") {
				v = filepath.Join(home, v[2:])
			}
			if filepath.IsAbs(v) {
				return filepath.Clean(v)
			}
		}
	}
	return ""
}

func ownedBy(info os.FileInfo, uid int) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == uid
}
