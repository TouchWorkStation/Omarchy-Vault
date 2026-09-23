package shortcuts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindConfig(t *testing.T) {
	home, proc, xdg := t.TempDir(), t.TempDir(), t.TempDir()
	uid := os.Getuid()
	write := func(p string) string {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("# conf\n"), 0o644)
		return p
	}
	// Nothing exists: default path, not found.
	if p, ok := findConfig(proc, home, "", uid); ok || p != filepath.Join(home, ".config/hypr/hyprland.conf") {
		t.Errorf("none = %s %v", p, ok)
	}
	std := write(filepath.Join(home, ".config/hypr/hyprland.conf"))
	if p, ok := findConfig(proc, home, "", uid); !ok || p != std {
		t.Errorf("standard = %s %v", p, ok)
	}
	// XDG_CONFIG_HOME wins over ~/.config.
	x := write(filepath.Join(xdg, "hypr/hyprland.conf"))
	if p, _ := findConfig(proc, home, xdg, uid); p != x {
		t.Errorf("xdg = %s", p)
	}
	// The running Hyprland's --config wins over everything.
	custom := write(filepath.Join(home, "dotfiles/hypr/main.conf"))
	os.MkdirAll(filepath.Join(proc, "4242"), 0o755)
	os.WriteFile(filepath.Join(proc, "4242", "cmdline"), []byte("/usr/bin/Hyprland\x00--config\x00~/dotfiles/hypr/main.conf\x00"), 0o644)
	os.MkdirAll(filepath.Join(proc, "self"), 0o755) // non-numeric: ignored
	if p, _ := findConfig(proc, home, xdg, uid); p != custom {
		t.Errorf("running = %s", p)
	}
	// Another program's -c is ignored.
	os.WriteFile(filepath.Join(proc, "4242", "cmdline"), []byte("/usr/bin/bash\x00-c\x00/etc/passwd\x00"), 0o644)
	if p, _ := findConfig(proc, home, xdg, uid); p != x {
		t.Errorf("non-hyprland = %s", p)
	}
}
