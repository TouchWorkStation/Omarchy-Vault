package shortcuts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildValidates(t *testing.T) {
	p, err := Build(Request{ID: "upload", Mods: []string{"shift", "super"}, Key: "u"})
	if err != nil || strings.Join(p.Mods, " ") != "SUPER SHIFT" || p.Key != "U" || p.Command != "vaultctl upload" {
		t.Fatalf("build = %+v %v", p, err)
	}
	for _, bad := range []Request{
		{ID: "rm -rf", Mods: []string{"SUPER"}, Key: "U"},
		{ID: "upload", Mods: []string{"SHIFT"}, Key: "U"},               // would fire while typing
		{ID: "upload", Mods: []string{"SUPER"}, Key: "U, exec, evil"},   // injection
		{ID: "upload", Mods: []string{"SUPER", "HYPER"}, Key: "U"},      // unknown modifier
		{ID: "upload", Mods: []string{"SUPER"}, Key: "RETURN\nbind = "}, // newline
	} {
		if _, err := Build(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestCheckAll(t *testing.T) {
	existing := []Binding{
		{Mods: []string{"SUPER", "SHIFT"}, Key: "D", Dispatcher: "exec", Arg: "lazydocker", Source: "/h/.config/hypr/bindings.conf"},
		// Vault's own current binding may be replaced.
		{Mods: []string{"SUPER", "SHIFT"}, Key: "U", Dispatcher: "exec", Arg: "/h/.local/bin/vaultctl upload", Source: "hyprctl"},
	}
	res := CheckAll(existing, []Request{
		{ID: "download", Mods: []string{"SUPER", "SHIFT"}, Key: "D"},
		{ID: "upload", Mods: []string{"SUPER", "SHIFT"}, Key: "U"},
		{ID: "open", Mods: []string{"SUPER", "SHIFT"}, Key: "U"},
		{ID: "open", Mods: []string{"SHIFT"}, Key: "V"},
	})
	want := []string{"conflict", "available", "duplicate", "invalid"}
	for i, r := range res {
		if r.Status != want[i] {
			t.Errorf("%d %s: %s (%s), want %s", i, r.ID, r.Status, r.Message, want[i])
		}
	}
	if res[0].Suggestion == nil || res[0].Suggestion.Combo() != "Super + Alt + D" {
		t.Errorf("suggestion = %+v", res[0].Suggestion)
	}
}

func TestInstalledReadsCustomKeys(t *testing.T) {
	home := t.TempDir()
	conf := filepath.Join(home, ".config", "hypr", "hyprland.conf")
	os.MkdirAll(filepath.Dir(conf), 0o755)
	os.WriteFile(conf, []byte("bind = SUPER, RETURN, exec, alacritty\n"), 0o644)
	up, _ := Build(Request{ID: "upload", Mods: []string{"SUPER", "ALT"}, Key: "F5"})
	bs := WithCommandPath([]Planned{up}, "/home/me/.local/bin/vaultctl")
	if _, err := Install(home, conf, bs); err != nil {
		t.Fatal(err)
	}
	got := Installed(home)
	if len(got) != 1 || got[0].ID != "upload" || got[0].Combo() != "Super + Alt + F5" {
		t.Fatalf("installed = %+v", got)
	}
	if b, _ := os.ReadFile(IncludePath(home)); !strings.Contains(string(b), "exec, /home/me/.local/bin/vaultctl upload") {
		t.Errorf("file = %s", b)
	}
	if WithCommandPath(bs, "/tmp/x; rm -rf ~")[0].Command != bs[0].Command {
		t.Error("unsafe path used")
	}
}
