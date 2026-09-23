package shortcuts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAndRemove(t *testing.T) {
	home := t.TempDir()
	hypr := filepath.Join(home, ".config", "hypr")
	os.MkdirAll(hypr, 0o755)
	conf := filepath.Join(hypr, "hyprland.conf")
	original := "# my config\nsource = ~/.config/hypr/bindings.conf\n"
	os.WriteFile(conf, []byte(original), 0o600)
	os.WriteFile(filepath.Join(hypr, "bindings.conf"), []byte("bindd = SUPER ALT, D, Docker, exec, lazydocker\n"), 0o644)

	in := Inspector{ConfigPath: conf, Home: home}
	rep := in.Analyze(context.Background())
	bs, skipped, err := Choose(rep, 4, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 2 || bs[0].Key != "V" || bs[1].Key != "U" {
		t.Fatalf("install = %+v", bs)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "Milestone 5") {
		t.Errorf("skipped = %v", skipped)
	}
	changed, err := Install(home, conf, bs)
	if err != nil || !changed {
		t.Fatalf("install: %v %v", changed, err)
	}
	got, _ := os.ReadFile(conf)
	if !strings.HasPrefix(string(got), original) || !strings.Contains(string(got), SourceLine()) {
		t.Fatalf("hyprland.conf = %q", got)
	}
	if info, _ := os.Stat(conf); info.Mode().Perm() != 0o600 {
		t.Error("hyprland.conf permissions changed")
	}
	// Installing again changes nothing in hyprland.conf.
	if changed, _ := Install(home, conf, bs); changed {
		t.Error("source line added twice")
	}
	// Our bindings are now recognised as installed, the docker one untouched.
	rep = in.Analyze(context.Background())
	for _, c := range rep.Checks {
		if c.ID == "open" && c.Status != "installed" {
			t.Errorf("open = %s", c.Status)
		}
		if c.ID == "download" && c.Status != "conflict" {
			t.Errorf("download = %s", c.Status)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(hypr, "bindings.conf")); !strings.Contains(string(b), "lazydocker") {
		t.Fatal("user bindings touched")
	}

	if err := Remove(home, conf); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(conf); string(got) != original {
		t.Errorf("after remove = %q, want the original back", got)
	}
	if _, err := os.Stat(IncludePath(home)); err == nil {
		t.Error("include file not removed")
	}
}

func TestInstallRefusesForeignFileAndNoConfig(t *testing.T) {
	home := t.TempDir()
	hypr := filepath.Join(home, ".config", "hypr")
	os.MkdirAll(hypr, 0o755)
	conf := filepath.Join(hypr, "hyprland.conf")
	os.WriteFile(conf, []byte("\n"), 0o644)
	os.WriteFile(IncludePath(home), []byte("bind = SUPER, X, exec, mine\n"), 0o644)
	if _, err := Install(home, conf, Plan()[:1]); err == nil {
		t.Fatal("overwrote a file Vault did not write")
	}
	if err := Remove(home, conf); err == nil {
		t.Fatal("removed a file Vault did not write")
	}
	if _, _, err := Choose(Report{}, 4, false); err == nil {
		t.Fatal("installed without being able to check conflicts")
	}
}

func TestChooseSuggestions(t *testing.T) {
	sug := Planned{ID: "upload", Mods: []string{"SUPER", "SHIFT"}, Key: "U", Command: "vaultctl upload", Since: 4}
	rep := Report{Sources: []string{"x"}, Checks: []Check{{Planned: Plan()[1], Status: "conflict", Suggestion: &sug}}}
	if bs, _, _ := Choose(rep, 4, false); len(bs) != 0 {
		t.Error("conflict installed without --use-suggestions")
	}
	if bs, _, _ := Choose(rep, 4, true); len(bs) != 1 || bs[0].Combo() != "Super + Shift + U" {
		t.Errorf("suggestion = %+v", bs)
	}
}
