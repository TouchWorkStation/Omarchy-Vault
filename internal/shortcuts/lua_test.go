package shortcuts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// luaHome copies Omarchy's real hyprland.lua and bindings.lua into a
// temporary home.
func luaHome(t *testing.T) (home, conf string) {
	t.Helper()
	home = t.TempDir()
	dir := filepath.Join(home, ".config", "hypr")
	os.MkdirAll(dir, 0o755)
	for _, f := range []string{"hyprland.lua", "bindings.lua"} {
		b, err := os.ReadFile(filepath.Join("testdata", "lua", f))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, f), b, 0o644)
	}
	return home, filepath.Join(dir, "hyprland.lua")
}

func TestFindConfigPrefersLua(t *testing.T) {
	home, conf := luaHome(t)
	os.WriteFile(filepath.Join(home, ".config", "hypr", "hyprland.conf"), []byte("# old\n"), 0o644)
	if got, ok := findConfig(t.TempDir(), home, filepath.Join(home, ".config"), os.Getuid()); !ok || got != conf {
		t.Errorf("found %s %v, want %s", got, ok, conf)
	}
}

func TestLuaInstallRemove(t *testing.T) {
	home, conf := luaHome(t)
	orig, _ := os.ReadFile(conf)
	t.Setenv("OMARCHY_PATH", t.TempDir())
	in := Inspector{ConfigPath: conf, Home: home}

	// The user's literal Lua binding is seen as a conflict for Super+Alt+D;
	// commented examples are ignored.
	rep := in.Analyze(context.Background())
	if len(rep.Sources) == 0 {
		t.Fatal("no sources from Lua files")
	}
	for _, c := range rep.Checks {
		want := "available"
		if c.ID == "download" {
			want = "conflict"
		}
		if c.Status != want {
			t.Errorf("%s = %s (%+v), want %s", c.ID, c.Status, c.Conflicts, want)
		}
	}

	bs, _, err := Choose(rep, 5, true)
	if err != nil || len(bs) != 3 {
		t.Fatalf("choose = %+v %v", bs, err)
	}
	bs = WithCommandPath(bs, "/home/chris/.local/bin/vaultctl")
	if changed, err := Install(home, conf, bs); err != nil || !changed {
		t.Fatalf("install = %v %v", changed, err)
	}
	main, _ := os.ReadFile(conf)
	if !strings.HasPrefix(string(main), string(orig)) || !strings.HasSuffix(string(main), luaNote+"\n"+LuaLoadLine()+"\n") {
		t.Fatalf("hyprland.lua = %q", main)
	}
	inc, _ := os.ReadFile(LuaIncludePath(home))
	for _, want := range []string{
		`pcall(vault_bind, "SUPER + ALT + V", "Open Vault", "/home/chris/.local/bin/vaultctl open")`,
		`pcall(vault_bind, "SUPER + ALT + U", "Upload to Vault", "/home/chris/.local/bin/vaultctl upload")`,
		`pcall(vault_bind, "SUPER + SHIFT + D", "Download from Vault", "/home/chris/.local/bin/vaultctl download")`,
	} {
		if !strings.Contains(string(inc), want) {
			t.Errorf("missing %s in\n%s", want, inc)
		}
	}
	// Installing again doesn't add a second load line.
	if changed, _ := Install(home, conf, bs); changed {
		t.Error("load line added twice")
	}
	got := Installed(home)
	if len(got) != 3 || got[2].Combo() != "Super + Shift + D" {
		t.Errorf("installed = %+v", got)
	}
	// Vault's own bindings don't count as conflicts when choosing again.
	if res := CheckAll(mustBindings(t, in), []Request{{ID: "upload", Mods: []string{"SUPER", "ALT"}, Key: "U"}}); res[0].Status != "available" {
		t.Errorf("own binding conflicts: %+v", res[0])
	}
	if err := Remove(home, conf); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(conf); string(after) != string(orig) {
		t.Errorf("hyprland.lua not restored:\n%s", after)
	}
	if _, err := os.Stat(LuaIncludePath(home)); err == nil {
		t.Error("Vault's Lua file left behind")
	}
}

func mustBindings(t *testing.T, in Inspector) []Binding {
	t.Helper()
	b, ok := in.Bindings(context.Background())
	if !ok {
		t.Fatal("no sources")
	}
	return b
}

func TestLuaRefusesForeignFile(t *testing.T) {
	home, conf := luaHome(t)
	os.WriteFile(LuaIncludePath(home), []byte("-- someone else's file\n"), 0o644)
	up, _ := Build(Request{ID: "upload", Mods: []string{"SUPER", "ALT"}, Key: "U"})
	if _, err := Install(home, conf, []Planned{up}); err == nil {
		t.Fatal("overwrote a file Vault didn't write")
	}
}
