package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/disks"
)

// fixture builds an inventory with a system disk and one data drive whose
// "mount point" is a temp dir, plus a mount table that agrees.
func fixture(t *testing.T) (inv *disks.Inventory, mounts MountTable, mount string) {
	t.Helper()
	mount = t.TempDir()
	inv = &disks.Inventory{
		SystemDiskDetected: true,
		Disks: []disks.Disk{
			{Name: "nvme0n1", DisplayName: "System SSD", System: true, Protected: true, Status: disks.StatusSystem,
				Volumes: []disks.Volume{{Name: "nvme0n1p2", UUID: "sys-uuid", Mountpoints: []string{"/"}, Status: disks.VolSystem}}},
			{Name: "sda", DisplayName: "WD Red", Status: disks.StatusAvailable, Adoptable: true,
				Volumes: []disks.Volume{{Name: "sda1", UUID: "data-uuid", FSType: "ext4", Mountpoints: []string{mount}, Status: disks.VolMounted, Adoptable: true}}},
			{Name: "sdb", DisplayName: "Seagate", Status: disks.StatusUnmounted,
				Volumes: []disks.Volume{{Name: "sdb1", UUID: "other", FSType: "xfs", Mountpoints: []string{}, Status: disks.VolUnmounted, Notes: []string{"Not mounted."}}}},
		},
	}
	mounts = MountTable{"/", mount}
	return inv, mounts, mount
}

var now = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

func code(err error) string {
	var ae *AdoptError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func TestAdoptCreatesVaultAndDefaultFolders(t *testing.T) {
	inv, mounts, mount := fixture(t)
	res, err := Adopt(inv, mounts, AdoptRequest{Volume: "sda1", Folder: "Vault", CreateFolders: true}, "Phone Uploads", now)
	if err != nil {
		t.Fatal(err)
	}
	if res.DataDir != filepath.Join(mount, "Vault") || res.Source.UUID != "data-uuid" || res.Source.Path != mount {
		t.Fatalf("result = %+v", res)
	}
	for _, f := range []string{"Photos", "Documents", "Backups", "Projects", "Phone Uploads", "Shared"} {
		if info, err := os.Stat(filepath.Join(mount, "Vault", f)); err != nil || !info.IsDir() {
			t.Errorf("%s not created: %v", f, err)
		}
	}
	if len(res.Created) != 7 {
		t.Errorf("created = %v", res.Created)
	}

	// Running again changes nothing and reports folders as existing.
	res2, err := Adopt(inv, mounts, AdoptRequest{Volume: "sda1", Folder: "Vault", CreateFolders: true}, "Phone Uploads", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Created) != 0 || len(res2.Existing) != 6 {
		t.Errorf("second run created=%v existing=%v", res2.Created, res2.Existing)
	}
}

func TestAdoptNeverOverwritesExistingFiles(t *testing.T) {
	inv, mounts, mount := fixture(t)
	os.MkdirAll(filepath.Join(mount, "Vault", "Photos"), 0o755)
	os.WriteFile(filepath.Join(mount, "Vault", "Photos", "cat.jpg"), []byte("meow"), 0o644)
	os.WriteFile(filepath.Join(mount, "Vault", "Shared"), []byte("i am a file"), 0o644)

	res, err := Adopt(inv, mounts, AdoptRequest{Volume: "sda1", Folder: "Vault", CreateFolders: true}, "Phone Uploads", now)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(mount, "Vault", "Photos", "cat.jpg")); string(b) != "meow" {
		t.Error("existing photo changed")
	}
	if b, _ := os.ReadFile(filepath.Join(mount, "Vault", "Shared")); string(b) != "i am a file" {
		t.Error("existing file replaced")
	}
	if strings.Join(res.Skipped, ",") != "Shared" || !contains(res.Existing, "Photos") {
		t.Errorf("skipped=%v existing=%v", res.Skipped, res.Existing)
	}
}

func TestAdoptWholeDrive(t *testing.T) {
	inv, mounts, mount := fixture(t)
	os.WriteFile(filepath.Join(mount, "keep.txt"), []byte("x"), 0o644)
	res, err := Adopt(inv, mounts, AdoptRequest{Volume: "sda1", Folder: ""}, "Phone Uploads", now)
	if err != nil {
		t.Fatal(err)
	}
	if res.DataDir != mount || len(res.Created) != 0 {
		t.Errorf("whole drive: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(mount, "keep.txt")); err != nil {
		t.Error("existing file disturbed")
	}
}

func TestAdoptRefusals(t *testing.T) {
	cases := []struct {
		name string
		mod  func(inv *disks.Inventory, mounts *MountTable, mount string)
		req  AdoptRequest
		want string
	}{
		{"system disk", nil, AdoptRequest{Volume: "nvme0n1p2"}, "system_disk"},
		{"unmounted", nil, AdoptRequest{Volume: "sdb1"}, "not_adoptable"},
		{"unknown volume", nil, AdoptRequest{Volume: "sdz9"}, "not_found"},
		{"dotdot", nil, AdoptRequest{Volume: "sda1", Folder: "../etc"}, "unsafe_path"},
		{"absolute", nil, AdoptRequest{Volume: "sda1", Folder: "/etc"}, "unsafe_path"},
		{"system disk unknown", func(inv *disks.Inventory, _ *MountTable, _ string) { inv.SystemDiskDetected = false },
			AdoptRequest{Volume: "sda1", Folder: "Vault"}, "unknown_system_disk"},
		{"not actually mounted", func(_ *disks.Inventory, m *MountTable, _ string) { *m = MountTable{"/"} },
			AdoptRequest{Volume: "sda1", Folder: "Vault"}, "not_adoptable"},
		{"symlink escape", func(_ *disks.Inventory, _ *MountTable, mount string) {
			os.Symlink(os.TempDir(), filepath.Join(mount, "Vault"))
		}, AdoptRequest{Volume: "sda1", Folder: "Vault"}, "unsafe_path"},
		{"nested mount", func(_ *disks.Inventory, m *MountTable, mount string) {
			os.Mkdir(filepath.Join(mount, "Vault"), 0o755)
			*m = append(*m, filepath.Join(mount, "Vault"))
		}, AdoptRequest{Volume: "sda1", Folder: "Vault"}, "unsafe_path"},
		{"folder is a file", func(_ *disks.Inventory, _ *MountTable, mount string) {
			os.WriteFile(filepath.Join(mount, "Vault"), []byte("x"), 0o644)
		}, AdoptRequest{Volume: "sda1", Folder: "Vault"}, "unsafe_path"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inv, mounts, mount := fixture(t)
			if c.mod != nil {
				c.mod(inv, &mounts, mount)
			}
			_, err := Adopt(inv, mounts, c.req, "Phone Uploads", now)
			if got := code(err); got != c.want {
				t.Fatalf("code = %q (%v), want %q", got, err, c.want)
			}
		})
	}
}

func TestAdoptSymlinkedDefaultFolderIsSkipped(t *testing.T) {
	inv, mounts, mount := fixture(t)
	os.MkdirAll(filepath.Join(mount, "Vault"), 0o755)
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(mount, "Vault", "Photos"))
	res, err := Adopt(inv, mounts, AdoptRequest{Volume: "sda1", Folder: "Vault", CreateFolders: true}, "Phone Uploads", now)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(res.Skipped, "Photos") {
		t.Errorf("symlinked Photos should be skipped: %+v", res)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Error("wrote outside the drive")
	}
}

func adopted(t *testing.T) (config.Config, *disks.Inventory, MountTable, string) {
	t.Helper()
	inv, mounts, mount := fixture(t)
	res, err := Adopt(inv, mounts, AdoptRequest{Volume: "sda1", Folder: "Vault"}, "Phone Uploads", now)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Pool.Mode = "single"
	cfg.Sources = []config.Source{res.Source}
	return cfg, inv, mounts, mount
}

func TestInspectStates(t *testing.T) {
	link := filepath.Join(t.TempDir(), "current")

	cfg, inv, mounts, _ := adopted(t)
	s := Inspect(cfg, inv, mounts, link)
	if s.State != StateReady || s.TotalBytes == 0 || !s.Configured {
		t.Fatalf("ready: %+v", s)
	}

	// Drive unplugged: UUID gone from the inventory.
	inv2 := *inv
	inv2.Disks = inv.Disks[:1]
	if s := Inspect(cfg, &inv2, mounts, link); s.State != StateDriveMissing {
		t.Errorf("unplugged = %s", s.State)
	}

	// Mount point exists but nothing is mounted there (empty folder on /).
	if s := Inspect(cfg, inv, MountTable{"/"}, link); s.State != StateDriveMissing {
		t.Errorf("not mounted = %s", s.State)
	}

	// Remounted elsewhere.
	inv3 := *inv
	inv3.Disks = append([]disks.Disk{}, inv.Disks...)
	d := inv3.Disks[1]
	d.Volumes = []disks.Volume{{Name: "sda1", UUID: "data-uuid", Mountpoints: []string{"/run/media/x/WD"}}}
	inv3.Disks[1] = d
	if s := Inspect(cfg, &inv3, mounts, link); s.State != StateDriveMoved || s.Sources[0].CurrentMount != "/run/media/x/WD" {
		t.Errorf("moved = %+v", s.Sources[0])
	}

	// Vault folder deleted.
	os.RemoveAll(cfg.Sources[0].DataDir())
	if s := Inspect(cfg, inv, mounts, link); s.State != StateProblem {
		t.Errorf("folder missing = %s", s.State)
	}

	// Not set up.
	if s := Inspect(config.Default(), inv, mounts, link); s.State != StateNotSetUp || s.Configured {
		t.Errorf("default = %+v", s)
	}
}

func TestInspectDiskBecameSystem(t *testing.T) {
	cfg, inv, mounts, _ := adopted(t)
	inv.Disks[1].System = true
	if s := Inspect(cfg, inv, mounts, "/nonexistent"); s.State != StateProblem {
		t.Errorf("state = %s", s.State)
	}
}

func TestLinks(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "share", "current")
	a, b := t.TempDir(), t.TempDir()
	if err := SetLink(link, a); err != nil {
		t.Fatal(err)
	}
	if err := SetLink(link, b); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(link); got != b {
		t.Errorf("link -> %s", got)
	}

	root := filepath.Join(dir, "srv-vault")
	if st := CheckRootLink(root, link); st.State != "missing" || st.Fix == "" {
		t.Errorf("missing: %+v", st)
	}
	os.Symlink(link, root)
	if st := CheckRootLink(root, link); st.State != "ok" {
		t.Errorf("ok: %+v", st)
	}
	os.Remove(root)
	os.Symlink("/elsewhere", root)
	if st := CheckRootLink(root, link); st.State != "elsewhere" {
		t.Errorf("elsewhere: %+v", st)
	}
	os.Remove(root)
	os.Mkdir(root, 0o755)
	if st := CheckRootLink(root, link); st.State != "not_link" {
		t.Errorf("not_link: %+v", st)
	}

	// Never replace or remove a real folder.
	real := filepath.Join(dir, "real")
	os.Mkdir(real, 0o755)
	if err := SetLink(real, a); !errors.Is(err, ErrNotSymlink) {
		t.Errorf("SetLink over dir: %v", err)
	}
	if err := RemoveLink(real); !errors.Is(err, ErrNotSymlink) {
		t.Errorf("RemoveLink dir: %v", err)
	}
	if err := RemoveLink(link); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(b); err != nil {
		t.Error("removing the link removed its target")
	}
	if err := RemoveLink(link); err != nil {
		t.Error("removing a missing link should be fine")
	}
}

func TestMountTable(t *testing.T) {
	if got := unescapeMount(`/run/media/me/My\040Drive`); got != "/run/media/me/My Drive" {
		t.Errorf("unescape = %q", got)
	}
	m := MountTable{"/", "/mnt/disk", "/mnt/disk/inner", "/mnt/diskette"}
	cases := map[string]string{
		"/mnt/disk/Vault":   "/mnt/disk",
		"/mnt/disk":         "/mnt/disk",
		"/mnt/disk/inner/x": "/mnt/disk/inner",
		"/mnt/diskette/x":   "/mnt/diskette",
		"/mnt/disk2/x":      "/",
		"/home/user":        "/",
	}
	for p, want := range cases {
		if got := m.Containing(p); got != want {
			t.Errorf("Containing(%s) = %s, want %s", p, got, want)
		}
	}
	if !m.IsMountPoint("/mnt/disk") || m.IsMountPoint("/mnt/disk/Vault") {
		t.Error("IsMountPoint wrong")
	}
	if real, err := ReadMountTable(); err != nil || real.Containing("/proc/self") == "" {
		t.Errorf("ReadMountTable: %v", err)
	}
}
