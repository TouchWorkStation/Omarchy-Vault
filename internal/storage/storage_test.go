package storage

import (
	"path/filepath"
	"testing"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
)

func TestInspectUnconfigured(t *testing.T) {
	cfg := config.Default()
	cfg.VaultRoot = filepath.Join(t.TempDir(), "missing")
	s := Inspect(cfg)
	if s.Configured || s.RootExists || s.Message == "" {
		t.Fatalf("unexpected: %+v", s)
	}
}

func TestInspectConfigured(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.VaultRoot = dir
	cfg.Pool.Mode = "single"
	cfg.Sources = []config.Source{{Path: dir}}
	s := Inspect(cfg)
	if !s.Configured || !s.RootExists || s.TotalBytes == 0 {
		t.Fatalf("unexpected: %+v", s)
	}
	if s.UsedBytes+s.FreeBytes > s.TotalBytes {
		t.Fatalf("used+free exceeds total: %+v", s)
	}
}
