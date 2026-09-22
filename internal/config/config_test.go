package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingReturnsDefaults(t *testing.T) {
	cfg, exists, err := Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("exists = true for missing file")
	}
	if cfg.VaultRoot != DefaultVaultRoot || cfg.Listen != DefaultListen {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("defaults do not validate: %v", err)
	}
}

func TestLoadRejectsUnknownFieldsAndBadValues(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"unknown field": `{"version":1,"nope":true}`,
		"relative root": `{"version":1,"vault_root":"srv/vault"}`,
		"root is slash": `{"version":1,"vault_root":"/"}`,
		"dotdot root":   `{"version":1,"vault_root":"/srv/../etc"}`,
		"public listen": `{"version":1,"listen":"0.0.0.0:8788"}`,
		"bad upload":    `{"version":1,"preferences":{"upload_folder":"../x","upload_expiry_minutes":10,"download_expiry_minutes":10}}`,
		"bad version":   `{"version":99}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".json")
			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Load(p); err == nil {
				t.Fatalf("expected error for %s", body)
			}
		})
	}
}

func TestValidateListen(t *testing.T) {
	ok := []string{"127.0.0.1:8788", "[::1]:8788", "localhost:9000"}
	for _, a := range ok {
		if err := ValidateListen(a, false); err != nil {
			t.Errorf("%s: %v", a, err)
		}
	}
	bad := []string{"0.0.0.0:8788", ":8788", "192.168.1.5:8788", "nonsense"}
	for _, a := range bad {
		if err := ValidateListen(a, false); err == nil {
			t.Errorf("%s: expected error", a)
		}
	}
	if err := ValidateListen("0.0.0.0:8788", true); err != nil {
		t.Errorf("explicit override rejected: %v", err)
	}
}

func TestSaveAtomicPrivateRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "omarchy-vault")
	p := filepath.Join(dir, "config.json")
	cfg := Default()
	cfg.Pool.Mode = "single"
	cfg.Sources = []Source{{Path: "/mnt/wdred", Folder: "Vault", UUID: "u1", Label: "WD"}}
	if err := Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v %v", info.Mode().Perm(), err)
	}
	if d, _ := os.Stat(dir); d.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v", d.Mode().Perm())
	}
	got, found, err := Load(p)
	if err != nil || !found || got.Sources[0].DataDir() != "/mnt/wdred/Vault" {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}

	// Invalid configs are never written.
	bad := cfg
	bad.Sources = nil
	if err := Save(p, bad); err == nil {
		t.Fatal("saved an invalid config")
	}
	if again, _, _ := Load(p); len(again.Sources) != 1 {
		t.Fatal("failed save damaged the existing config")
	}
}

func TestValidateFolder(t *testing.T) {
	for _, ok := range []string{"", "Vault", "Media/Vault", "My Vault", "a/b/c/d"} {
		if err := ValidateFolder(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"..", "../x", "a/../b", "/abs", "a/", "a//b", ".", "a\\b", "x\x00y", "a/b/c/d/e", "tab\tname"} {
		if err := ValidateFolder(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
