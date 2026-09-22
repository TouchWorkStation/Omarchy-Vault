package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHumanBytes(t *testing.T) {
	cases := map[uint64]string{
		0:              "0 B",
		999:            "999 B",
		1000:           "1.0 KB",
		8001563222016:  "8.0 TB",
		512110190592:   "512 GB",
		20000000000000: "20.0 TB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestBadTransferArgsChangeNothing(t *testing.T) {
	// Rejected before Vault is contacted or turned on.
	for _, args := range [][]string{{"share"}, {"share", "x", "--expires", "90d"}, {"download", "--minutes", "99"}, {"upload", "--minutes", "0"}} {
		if code := realMain(args); code != 1 {
			t.Errorf("%v exit = %d, want 1", args, code)
		}
	}
	if code := realMain([]string{"bogus"}); code != 2 {
		t.Errorf("unknown command exit = %d", code)
	}
}

func TestParseFolders(t *testing.T) {
	got, err := parseFolders("Photos, Documents:ro,Shared:rw", "family")
	if err != nil || len(got) != 3 || got[1].Access != "ro" || got[2].Access != "rw" {
		t.Fatalf("%+v %v", got, err)
	}
	g, _ := parseFolders("Shared:rw", "guest")
	if g[0].Access != "ro" {
		t.Error("guests are always read-only")
	}
	if _, err := parseFolders("../etc", "family"); err == nil {
		t.Error("bad folder accepted")
	}
	if _, err := parseFolders("Photos:all", "family"); err == nil {
		t.Error("bad access accepted")
	}
}

func TestParseExpiry(t *testing.T) {
	for in, want := range map[string]int{"10m": 10, "1h": 60, "24h": 1440, "7d": 10080, "30d": 43200} {
		if got, err := parseExpiry(in); err != nil || got != want {
			t.Errorf("parseExpiry(%q) = %d %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "m", "0h", "31d", "5x", "-1h"} {
		if _, err := parseExpiry(bad); err == nil {
			t.Errorf("parseExpiry(%q) accepted", bad)
		}
	}
}

func TestVaultRel(t *testing.T) {
	drive := t.TempDir()
	os.MkdirAll(filepath.Join(drive, "Photos", "2024"), 0o755)
	os.WriteFile(filepath.Join(drive, "Photos", "2024", "a.jpg"), []byte("x"), 0o644)
	link := filepath.Join(t.TempDir(), "vault")
	os.Symlink(drive, link)
	a := &app{}
	a.cfg.VaultRoot = link
	cases := map[string]string{
		"Photos/2024/a.jpg":                            "Photos/2024/a.jpg",
		filepath.Join(link, "Photos", "2024", "a.jpg"): "Photos/2024/a.jpg",
		filepath.Join(drive, "Photos", "2024"):         "Photos/2024",
	}
	for in, want := range cases {
		if got, err := a.vaultRel(in); err != nil || got != want {
			t.Errorf("vaultRel(%q) = %q %v", in, got, err)
		}
	}
	if _, err := a.vaultRel(t.TempDir()); err == nil {
		t.Error("path outside the Vault accepted")
	}
}
