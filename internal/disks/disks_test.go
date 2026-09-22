package disks

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/health"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/sysexec"
)

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func byName(t *testing.T, inv *Inventory, name string) Disk {
	t.Helper()
	for _, d := range inv.Disks {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("disk %s not found", name)
	return Disk{}
}

func TestBuildOmarchyLayout(t *testing.T) {
	inv, err := Build(read(t, "omarchy_lsblk.json"), read(t, "omarchy_findmnt.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !inv.SystemDiskDetected {
		t.Fatal("system disk not detected")
	}
	for _, d := range inv.Disks {
		if d.Name == "zram0" || d.Name == "loop0" {
			t.Errorf("%s should be skipped", d.Name)
		}
	}
	if inv.Disks[0].Name != "nvme0n1" {
		t.Errorf("system disk should sort first, got %s", inv.Disks[0].Name)
	}

	cases := []struct {
		name      string
		status    DiskStatus
		adoptable bool
		system    bool
	}{
		{"nvme0n1", StatusSystem, false, true},
		{"sda", StatusAvailable, true, false},
		{"sdb", StatusUnmounted, false, false},
		{"sdc", StatusAvailable, true, false},
		{"sdd", StatusUnsupported, false, false},
		{"sde", StatusUnsupported, false, false},
		{"sdf", StatusUnsupported, false, false},
	}
	for _, c := range cases {
		d := byName(t, inv, c.name)
		if d.Status != c.status || d.Adoptable != c.adoptable || d.System != c.system {
			t.Errorf("%s: status=%s adoptable=%v system=%v, want %s %v %v",
				c.name, d.Status, d.Adoptable, d.System, c.status, c.adoptable, c.system)
		}
	}

	nvme := byName(t, inv, "nvme0n1")
	if !nvme.Protected || !strings.Contains(nvme.ProtectedReason, "/boot") {
		t.Errorf("system disk not protected with reason: %q", nvme.ProtectedReason)
	}
	for _, v := range nvme.Volumes {
		if v.Adoptable || v.Status != VolSystem {
			t.Errorf("system volume %s must be VolSystem and not adoptable (got %s %v)", v.Name, v.Status, v.Adoptable)
		}
	}

	sda := byName(t, inv, "sda")
	if sda.DisplayName != "WDC WD80EFZZ-68BTXN0" {
		t.Errorf("display name = %q (ATA vendor should be dropped)", sda.DisplayName)
	}
	if len(sda.Volumes) != 1 || sda.Volumes[0].FSAvail != 5276787884032 {
		t.Errorf("sda volumes = %+v", sda.Volumes)
	}

	sdc := byName(t, inv, "sdc")
	if !sdc.Removable || sdc.Kind != "usb" || len(sdc.Notes) == 0 {
		t.Errorf("usb stick flags: removable=%v kind=%s notes=%v", sdc.Removable, sdc.Kind, sdc.Notes)
	}

	sdd := byName(t, inv, "sdd")
	if len(sdd.Volumes) != 0 {
		t.Errorf("blank disk should have no volumes, got %+v", sdd.Volumes)
	}

	sdf := byName(t, inv, "sdf")
	if sdf.Volumes[0].Adoptable || !strings.Contains(strings.Join(sdf.Volumes[0].Notes, " "), "never uses") {
		t.Errorf("drive mounted under /var must not be adoptable: %+v", sdf.Volumes[0])
	}

	if inv.Summary.System != 1 || inv.Summary.Available != 2 || inv.Summary.Total != 7 {
		t.Errorf("summary = %+v", inv.Summary)
	}
}

func TestSystemDiskDetectedViaFindmntOnly(t *testing.T) {
	inv, err := Build(read(t, "nomounts_lsblk.json"), read(t, "nomounts_findmnt.json"))
	if err != nil {
		t.Fatal(err)
	}
	sda := byName(t, inv, "sda")
	if !sda.System || !sda.Protected {
		t.Fatal("sda should be detected as system via findmnt")
	}
	sdb := byName(t, inv, "sdb")
	if !sdb.Adoptable || sdb.Volumes[0].FSAvail != 990 {
		t.Errorf("legacy string-typed lsblk output mis-parsed: %+v", sdb)
	}
}

func TestFailsSafeWhenSystemDiskUnknown(t *testing.T) {
	inv, err := Build(read(t, "nomounts_lsblk.json"), read(t, "overlay_findmnt.json"))
	if err != nil {
		t.Fatal(err)
	}
	if inv.SystemDiskDetected {
		t.Fatal("no system disk should be detected")
	}
	if len(inv.Warnings) == 0 {
		t.Error("expected a warning")
	}
	for _, d := range inv.Disks {
		if d.Adoptable {
			t.Errorf("%s adoptable while system disk unknown", d.Name)
		}
		for _, v := range d.Volumes {
			if v.Adoptable {
				t.Errorf("%s adoptable while system disk unknown", v.Name)
			}
		}
	}
}

func TestSeparateHomeDiskIsSystem(t *testing.T) {
	lsblk := `{"blockdevices":[
	 {"name":"sda","type":"disk","size":100,"mountpoints":[null],"children":[{"name":"sda1","type":"part","size":100,"fstype":"ext4","mountpoints":["/"]}]},
	 {"name":"sdb","type":"disk","size":100,"mountpoints":[null],"children":[{"name":"sdb1","type":"part","size":100,"fstype":"ext4","mountpoints":["/home"]}]},
	 {"name":"sdc","type":"disk","size":100,"mountpoints":[null],"children":[{"name":"sdc1","type":"part","size":100,"fstype":"ext4","mountpoints":["/boot/efi"]}]}]}`
	inv, err := Build([]byte(lsblk), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range inv.Disks {
		if !d.System || d.Adoptable {
			t.Errorf("%s: system=%v adoptable=%v", d.Name, d.System, d.Adoptable)
		}
	}
}

func TestReservedTargets(t *testing.T) {
	reserved := []string{"/", "/home", "/usr/local", "/var/lib/x", "/boot", "/etc", "[SWAP]", "/tmp/x"}
	for _, r := range reserved {
		if !isReservedTarget(r) {
			t.Errorf("%s should be reserved", r)
		}
	}
	ok := []string{"/mnt/disk1", "/run/media/user/STICK", "/srv/data", "/home/user/disk", "/data", "/variable"}
	for _, o := range ok {
		if isReservedTarget(o) {
			t.Errorf("%s should not be reserved", o)
		}
	}
}

func TestValidDevicePath(t *testing.T) {
	good := []string{"/dev/sda", "/dev/nvme0n1"}
	bad := []string{"/dev/../etc/passwd", "/dev/mapper/root", "sda", "/dev/sda;rm", "/dev/", "/dev/sda -d"}
	for _, g := range good {
		if !validDevicePath(g) {
			t.Errorf("%s should be valid", g)
		}
	}
	for _, b := range bad {
		if validDevicePath(b) {
			t.Errorf("%s should be invalid", b)
		}
	}
}

func TestScannerCachesAndUsesReadOnlyCommands(t *testing.T) {
	fake := &sysexec.Fake{
		Installed: map[string]bool{"lsblk": true, "findmnt": true, "smartctl": true},
		Outputs: map[string][]byte{
			"lsblk -J -b -o " + lsblkColumns:           read(t, "omarchy_lsblk.json"),
			"findmnt -J -o TARGET,SOURCE,FSTYPE":       read(t, "omarchy_findmnt.json"),
			"smartctl -j -n standby -i -H -A /dev/sda": []byte(`{"smartctl":{"exit_status":0},"smart_status":{"passed":true},"temperature":{"current":33}}`),
		},
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := &Scanner{Run: fake, SMART: true, nowFn: func() time.Time { return now }}

	inv, err := s.Inventory(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if got := byName(t, inv, "sda").Health.Status; got != health.Healthy {
		t.Errorf("sda health = %s", got)
	}
	// Drives without registered output fall back to unknown, never an error.
	if got := byName(t, inv, "sdb").Health.Status; got != health.Unknown {
		t.Errorf("sdb health = %s", got)
	}
	calls := len(fake.Calls)

	now = now.Add(2 * time.Second)
	if _, err := s.Inventory(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != calls {
		t.Error("forced refresh inside MinRefresh should be served from cache")
	}

	now = now.Add(time.Minute)
	if _, err := s.Inventory(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	for _, c := range fake.Calls[calls:] {
		if strings.HasPrefix(c, "smartctl") {
			t.Errorf("SMART should be cached, but ran %q", c)
		}
	}
	for _, c := range fake.Calls {
		for _, bad := range []string{"mount", "mkfs", "wipefs", "parted", "-t ", "--test", "-s on"} {
			if strings.Contains(c, bad) {
				t.Errorf("unexpected non read-only invocation %q", c)
			}
		}
	}
}

func TestFSTypeFallbackAndHexVendor(t *testing.T) {
	lsblk := `{"blockdevices":[{"name":"vda","path":"/dev/vda","type":"disk","size":100,"vendor":"0x1af4","tran":"virtio","fstype":null,"mountpoints":["/"]},
	 {"name":"vdb","path":"/dev/vdb","type":"disk","size":100,"vendor":"0x1af4","tran":"virtio","fstype":null,"mountpoints":["/data"]}]}`
	findmnt := `{"filesystems":[{"target":"/","source":"/dev/vda","fstype":"ext4","children":[{"target":"/data","source":"/dev/vdb","fstype":"ext4"}]}]}`
	inv, err := Build([]byte(lsblk), []byte(findmnt))
	if err != nil {
		t.Fatal(err)
	}
	vdb := byName(t, inv, "vdb")
	if vdb.Volumes[0].FSType != "ext4" || !vdb.Adoptable {
		t.Errorf("fstype fallback failed: %+v", vdb.Volumes[0])
	}
	if vdb.DisplayName != "Virtual disk" {
		t.Errorf("display name = %q", vdb.DisplayName)
	}
}
