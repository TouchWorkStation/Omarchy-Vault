package shortcuts

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/sysexec"
)

func checkByID(t *testing.T, r Report, id string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("check %s missing", id)
	return Check{}
}

func TestParseMods(t *testing.T) {
	cases := map[string]string{
		"SUPER SHIFT":  "SUPER SHIFT",
		"SUPER_SHIFT":  "SUPER SHIFT",
		"shift+super":  "SUPER SHIFT",
		"WIN SHIFT":    "SUPER SHIFT",
		"MOD4 CONTROL": "SUPER CTRL",
		"SUPER CAPS":   "SUPER",
		"":             "",
	}
	for in, want := range cases {
		if got := strings.Join(parseMods(in), " "); got != want {
			t.Errorf("parseMods(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConfigFileAnalysis(t *testing.T) {
	in := Inspector{ConfigPath: "testdata/hypr/hyprland.conf"}
	r := in.Analyze(context.Background())
	if len(r.Sources) != 3 {
		t.Errorf("sources = %v (expected root, omarchy/tui.conf, bindings.conf; cycles ignored)", r.Sources)
	}

	v := checkByID(t, r, "open")
	if v.Status != "available" {
		t.Errorf("V: unbind should free it, got %s %+v", v.Status, v.Conflicts)
	}

	u := checkByID(t, r, "upload")
	if u.Status != "conflict" || len(u.Conflicts) != 1 || u.Conflicts[0].Arg != "beam send" {
		t.Fatalf("U: %+v", u)
	}
	if u.Suggestion == nil || u.Suggestion.Combo() != "Super + Ctrl + Shift + U" {
		t.Errorf("U suggestion = %+v (SUPER ALT U is taken)", u.Suggestion)
	}
	if strings.Join(u.Options, ",") != "choose_another,copy_binding,skip" {
		t.Errorf("options = %v", u.Options)
	}

	d := checkByID(t, r, "download")
	if d.Status != "conflict" || d.Conflicts[0].Description != "Lazydocker" {
		t.Fatalf("D: %+v", d)
	}
	if d.Suggestion == nil || d.Suggestion.Combo() != "Super + Alt + D" {
		t.Errorf("D suggestion = %+v", d.Suggestion)
	}
	if !strings.HasPrefix(d.Line, "bindd = SUPER SHIFT, D, Download from Vault, exec, vaultctl download") {
		t.Errorf("line = %q", d.Line)
	}
}

func TestHyprctlAnalysis(t *testing.T) {
	data, err := os.ReadFile("testdata/hyprctl_binds.json")
	if err != nil {
		t.Fatal(err)
	}
	fake := &sysexec.Fake{
		Installed: map[string]bool{"hyprctl": true},
		Outputs:   map[string][]byte{"hyprctl binds -j": data},
	}
	r := Inspector{Run: fake}.Analyze(context.Background())
	if got := checkByID(t, r, "open").Status; got != "installed" {
		t.Errorf("V = %s, want installed", got)
	}
	if got := checkByID(t, r, "upload").Status; got != "conflict" {
		t.Errorf("U = %s (lowercase key must match; submap bind ignored)", got)
	}
	if got := checkByID(t, r, "download").Status; got != "conflict" {
		t.Errorf("D = %s (NumLock bit must be ignored)", got)
	}
}

func TestNoSourcesIsUnknownNotAvailable(t *testing.T) {
	r := Inspector{ConfigPath: "testdata/does-not-exist.conf"}.Analyze(context.Background())
	for _, c := range r.Checks {
		if c.Status != "unknown" {
			t.Errorf("%s = %s; without any config Vault must not claim a shortcut is free", c.ID, c.Status)
		}
	}
}

func TestStripComment(t *testing.T) {
	if got := stripComment("bind = A, B, exec, echo ##1 # comment"); got != "bind = A, B, exec, echo #1 " {
		t.Errorf("got %q", got)
	}
}
