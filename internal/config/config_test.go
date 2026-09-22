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
