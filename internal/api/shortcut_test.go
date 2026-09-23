package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/shortcuts"
)

func TestShortcutEditor(t *testing.T) {
	e := newEnv(t)
	admin := bootstrap(t, e)
	home := t.TempDir()
	conf := filepath.Join(home, ".config", "hypr", "hyprland.conf")
	os.MkdirAll(filepath.Dir(conf), 0o755)
	orig := "bind = SUPER SHIFT, D, exec, lazydocker\n"
	os.WriteFile(conf, []byte(orig), 0o644)
	e.srv.Shortcuts = shortcuts.Inspector{ConfigPath: conf, Home: home}

	type res struct {
		Results []shortcuts.Result
		Current []shortcuts.Planned
		Keys    []string `json:"key_choices"`
	}
	var got res
	w := e.req("POST", "/api/shortcuts/check", map[string]any{"bindings": []map[string]any{
		{"id": "download", "mods": []string{"SUPER", "SHIFT"}, "key": "D"},
		{"id": "upload", "mods": []string{"SUPER", "ALT"}, "key": "U"},
	}}, admin)
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.Results[0].Status != "conflict" || got.Results[1].Status != "available" {
		t.Fatalf("check = %d %s", w.Code, w.Body)
	}

	// Saving a taken key changes nothing.
	w = e.req("POST", "/api/shortcuts", map[string]any{"bindings": []map[string]any{
		{"id": "download", "mods": []string{"SUPER", "SHIFT"}, "key": "D"},
	}}, admin)
	if w.Code != http.StatusConflict {
		t.Fatalf("taken key saved: %d", w.Code)
	}
	if _, err := os.Stat(shortcuts.IncludePath(home)); err == nil {
		t.Fatal("file written despite conflict")
	}

	// Free keys are saved and read back.
	w = e.req("POST", "/api/shortcuts", map[string]any{"bindings": []map[string]any{
		{"id": "download", "mods": []string{"SUPER", "ALT"}, "key": "D"},
		{"id": "upload", "mods": []string{"SUPER", "ALT"}, "key": "U"},
	}}, admin)
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || len(got.Current) != 2 || len(got.Keys) < 40 {
		t.Fatalf("save = %d %s", w.Code, w.Body)
	}
	b, _ := os.ReadFile(conf)
	if !strings.HasPrefix(string(b), orig) || !strings.Contains(string(b), "source = ~/.config/hypr/omarchy-vault.conf") {
		t.Errorf("hyprland.conf = %q", b)
	}
	// Changing them again replaces only Vault's own lines.
	w = e.req("POST", "/api/shortcuts", map[string]any{"bindings": []map[string]any{
		{"id": "upload", "mods": []string{"SUPER", "CTRL"}, "key": "F9"},
	}}, admin)
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || len(got.Current) != 1 || got.Current[0].Combo() != "Super + Ctrl + F9" {
		t.Fatalf("change = %d %s", w.Code, w.Body)
	}
	// Injection attempts are refused.
	w = e.req("POST", "/api/shortcuts", map[string]any{"bindings": []map[string]any{
		{"id": "upload", "mods": []string{"SUPER"}, "key": "U, exec, curl evil"},
	}}, admin)
	if w.Code == 200 {
		t.Fatal("bad key accepted")
	}
	// Remove restores hyprland.conf exactly.
	if w := e.req("DELETE", "/api/shortcuts", nil, admin); w.Code != 200 {
		t.Fatalf("remove = %d", w.Code)
	}
	if b, _ := os.ReadFile(conf); string(b) != orig {
		t.Errorf("after remove = %q", b)
	}
}
