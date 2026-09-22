// Package disks discovers attached storage, strictly read-only.
//
// Discovery uses lsblk and findmnt and never modifies anything: it does not
// mount, unmount, format, partition or write to any device. Its most
// important job is to identify the operating system disk so Vault can mark
// it SYSTEM / PROTECTED and never offer it as Vault storage.
package disks

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/health"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/sysexec"
)

// DiskStatus summarises what Vault can do with a drive.
type DiskStatus string

const (
	// StatusSystem: the drive holds the running OS. Never usable by Vault.
	StatusSystem DiskStatus = "system"
	// StatusAvailable: the drive has a mounted, supported filesystem that
	// can be adopted as Vault storage (adoption arrives in Milestone 2).
	StatusAvailable DiskStatus = "available"
	// StatusUnmounted: the drive has a filesystem that is not mounted. Vault
	// shows it but does not mount it.
	StatusUnmounted DiskStatus = "unmounted"
	// StatusUnsupported: nothing on the drive can be used without changes
	// Vault refuses to make (no filesystem, encrypted and locked, etc).
	StatusUnsupported DiskStatus = "unsupported"
	// StatusReadOnly: the device is read-only.
	StatusReadOnly DiskStatus = "read-only"
)

// VolumeStatus describes one filesystem / partition.
type VolumeStatus string

const (
	VolSystem      VolumeStatus = "system"
	VolMounted     VolumeStatus = "mounted"
	VolUnmounted   VolumeStatus = "unmounted"
	VolContainer   VolumeStatus = "container"
	VolUnsupported VolumeStatus = "unsupported"
	VolSwap        VolumeStatus = "swap"
)

// Disk is one physical (or virtual) drive.
type Disk struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	Path            string        `json:"path"`
	DisplayName     string        `json:"display_name"`
	Model           string        `json:"model,omitempty"`
	Vendor          string        `json:"vendor,omitempty"`
	Serial          string        `json:"serial,omitempty"`
	Kind            string        `json:"kind"`
	Transport       string        `json:"transport,omitempty"`
	SizeBytes       int64         `json:"size_bytes"`
	Rotational      bool          `json:"rotational"`
	Removable       bool          `json:"removable"`
	ReadOnly        bool          `json:"read_only"`
	System          bool          `json:"system"`
	Protected       bool          `json:"protected"`
	ProtectedReason string        `json:"protected_reason,omitempty"`
	Status          DiskStatus    `json:"status"`
	Adoptable       bool          `json:"adoptable"`
	Volumes         []Volume      `json:"volumes"`
	Health          health.Report `json:"health"`
	Notes           []string      `json:"notes,omitempty"`
}

// Volume is a partition, mapped device or whole-disk filesystem.
type Volume struct {
	Name        string       `json:"name"`
	Path        string       `json:"path"`
	Type        string       `json:"type"`
	FSType      string       `json:"fstype,omitempty"`
	Label       string       `json:"label,omitempty"`
	UUID        string       `json:"uuid,omitempty"`
	SizeBytes   int64        `json:"size_bytes"`
	Mountpoints []string     `json:"mountpoints"`
	FSSize      int64        `json:"fs_size_bytes,omitempty"`
	FSUsed      int64        `json:"fs_used_bytes,omitempty"`
	FSAvail     int64        `json:"fs_avail_bytes,omitempty"`
	Status      VolumeStatus `json:"status"`
	Adoptable   bool         `json:"adoptable"`
	Notes       []string     `json:"notes,omitempty"`
}

// Summary gives dashboard-level counts.
type Summary struct {
	Total       int `json:"total"`
	System      int `json:"system"`
	Available   int `json:"available"`
	Unmounted   int `json:"unmounted"`
	Unsupported int `json:"unsupported"`
	Healthy     int `json:"healthy"`
	Warning     int `json:"warning"`
	Critical    int `json:"critical"`
	Unknown     int `json:"unknown"`
}

// Inventory is the result of a discovery pass.
type Inventory struct {
	Disks              []Disk    `json:"disks"`
	SystemDiskDetected bool      `json:"system_disk_detected"`
	Summary            Summary   `json:"summary"`
	Warnings           []string  `json:"warnings,omitempty"`
	ScannedAt          time.Time `json:"scanned_at"`
}

// systemMounts are mount targets that belong to the operating system. Any
// drive backing one of them is SYSTEM / PROTECTED.
var systemMounts = map[string]bool{
	"/":         true,
	"/boot":     true,
	"/boot/efi": true,
	"/efi":      true,
	"/usr":      true,
	"/var":      true,
	"/home":     true,
	"[SWAP]":    true,
}

// reservedPrefixes are locations Vault will never treat as adoptable
// storage even when a non-system drive is mounted there.
var reservedPrefixes = []string{"/boot", "/efi", "/usr", "/etc", "/var", "/proc", "/sys", "/dev", "/root", "/tmp", "/bin", "/sbin", "/lib", "/lib64"}

// fsSupport lists filesystems Vault can adopt, with caveats for limited ones.
var fsSupport = map[string]string{
	"ext4":    "",
	"ext3":    "",
	"xfs":     "",
	"btrfs":   "",
	"f2fs":    "",
	"exfat":   "exFAT does not store Linux permissions; fine for shared media, not for multi-user folders",
	"vfat":    "FAT32 cannot store files larger than 4 GB and has no Linux permissions",
	"ntfs":    "NTFS works but is slower on Linux and has limited permission support",
	"ntfs3":   "NTFS works but has limited permission support on Linux",
	"fuseblk": "FUSE filesystem (often NTFS); works with limited permission support",
}

var containerFS = map[string]string{
	"crypto_LUKS":       "Encrypted volume",
	"LVM2_member":       "LVM physical volume",
	"linux_raid_member": "Software RAID member",
	"zfs_member":        "ZFS pool member",
	"bcache":            "bcache backing device",
}

// isSystemMount reports whether target is an OS mount.
func isSystemMount(target string) bool {
	return systemMounts[target] || strings.HasPrefix(target, "/boot/")
}

// isReservedTarget reports whether target is somewhere Vault must not adopt.
func isReservedTarget(target string) bool {
	if target == "/" || target == "/home" || strings.HasPrefix(target, "[") {
		return true
	}
	for _, p := range reservedPrefixes {
		if target == p || strings.HasPrefix(target, p+"/") {
			return true
		}
	}
	return false
}

// Build turns lsblk and findmnt JSON into an Inventory. It is pure, which
// keeps the safety-critical logic fully testable.
func Build(lsblkJSON, findmntJSON []byte) (*Inventory, error) {
	nodes, err := parseLsblk(lsblkJSON)
	if err != nil {
		return nil, err
	}
	inv := &Inventory{Disks: []Disk{}}

	// Sources of system mounts according to the kernel mount table. This is
	// a second, independent signal on top of lsblk's own mountpoints.
	systemSources := map[string]bool{}
	var mounts map[string]mountInfo
	if len(findmntJSON) > 0 {
		mounts, err = parseFindmnt(findmntJSON)
		if err != nil {
			inv.Warnings = append(inv.Warnings, "Mount table could not be read; relying on lsblk only.")
		}
		for target, m := range mounts {
			if isSystemMount(target) && strings.HasPrefix(m.Source, "/dev/") {
				systemSources[m.Source] = true
			}
		}
	}

	for _, n := range nodes {
		if skipTopLevel(n) {
			continue
		}
		inv.Disks = append(inv.Disks, buildDisk(n, systemSources, mounts))
	}

	for _, d := range inv.Disks {
		if d.System {
			inv.SystemDiskDetected = true
		}
	}
	if !inv.SystemDiskDetected {
		inv.Warnings = append(inv.Warnings,
			"Vault could not identify the drive the operating system runs from. To stay safe, no drive is offered as Vault storage until it can.")
		for i := range inv.Disks {
			inv.Disks[i].Adoptable = false
			for j := range inv.Disks[i].Volumes {
				inv.Disks[i].Volumes[j].Adoptable = false
			}
		}
	}

	sort.SliceStable(inv.Disks, func(i, j int) bool {
		if inv.Disks[i].System != inv.Disks[j].System {
			return inv.Disks[i].System
		}
		return inv.Disks[i].Name < inv.Disks[j].Name
	})
	inv.Summary = summarize(inv.Disks)
	return inv, nil
}

func skipTopLevel(n lsblkNode) bool {
	switch n.Type {
	case "loop", "rom", "ram":
		return true
	}
	if strings.HasPrefix(n.Name, "zram") || strings.HasPrefix(n.Name, "ram") {
		return true
	}
	return n.Size == 0
}

func buildDisk(n lsblkNode, systemSources map[string]bool, mounts map[string]mountInfo) Disk {
	d := Disk{
		ID:         firstNonEmpty(n.KName, n.Name),
		Name:       n.Name,
		Path:       firstNonEmpty(n.Path, "/dev/"+n.Name),
		Model:      strings.TrimSpace(n.Model),
		Vendor:     strings.TrimSpace(n.Vendor),
		Serial:     strings.TrimSpace(n.Serial),
		Transport:  n.Tran,
		SizeBytes:  int64(n.Size),
		Rotational: bool(n.Rota),
		Removable:  bool(n.RM) || bool(n.Hotplug),
		ReadOnly:   bool(n.RO),
		Volumes:    []Volume{},
		Health:     health.UnknownReport("SMART not checked"),
	}
	d.Kind = kindOf(n)
	d.DisplayName = displayName(d)

	// Is any node in this subtree backing an OS mount?
	var systemHits, deviceHits []string
	var walk func(lsblkNode)
	walk = func(x lsblkNode) {
		for _, m := range x.mounts() {
			if isSystemMount(m) {
				systemHits = append(systemHits, m)
			}
		}
		for _, p := range []string{x.Path, "/dev/" + x.KName, "/dev/mapper/" + x.Name} {
			if systemSources[p] {
				deviceHits = append(deviceHits, p)
			}
		}
		for _, c := range x.Children {
			walk(c)
		}
	}
	walk(n)
	if len(systemHits) == 0 {
		// lsblk could not see the mounts; name the devices findmnt matched.
		systemHits = deviceHits
	}
	if len(systemHits) > 0 {
		d.System = true
		d.Protected = true
		d.ProtectedReason = "This drive runs Omarchy (" + strings.Join(dedupe(systemHits), ", ") + "). Vault will never use it for storage."
	}

	collectVolumes(n, &d, d.ReadOnly, mounts)

	for _, v := range d.Volumes {
		if v.Adoptable {
			d.Adoptable = true
		}
	}
	d.Status = diskStatus(d)
	if d.Removable && !d.System {
		d.Notes = append(d.Notes, "Removable drive: Vault storage disappears when it is unplugged.")
	}
	return d
}

func collectVolumes(n lsblkNode, d *Disk, parentRO bool, mountTable map[string]mountInfo) {
	ro := parentRO || bool(n.RO)
	isTop := n.Type == "disk"
	hasFS := n.FSType != ""
	mounts := n.mounts()

	if !isTop || hasFS || len(mounts) > 0 {
		v := Volume{
			Name:        n.Name,
			Path:        firstNonEmpty(n.Path, "/dev/"+n.KName),
			Type:        n.Type,
			FSType:      n.FSType,
			Label:       firstNonEmpty(n.Label, n.PartLabel),
			UUID:        n.UUID,
			SizeBytes:   int64(n.Size),
			Mountpoints: mounts,
			FSSize:      int64(n.FSSize),
			FSUsed:      int64(n.FSUsed),
			FSAvail:     int64(n.FSAvail),
		}
		if v.Mountpoints == nil {
			v.Mountpoints = []string{}
		}
		// lsblk reads filesystem types from udev, which is not always
		// available; the kernel mount table always knows mounted ones.
		if v.FSType == "" {
			for _, m := range mounts {
				if mi, ok := mountTable[m]; ok && mi.FSType != "" {
					v.FSType = mi.FSType
					break
				}
			}
		}
		classifyVolume(&v, n, d.System, ro)
		d.Volumes = append(d.Volumes, v)
	}
	for _, c := range n.Children {
		collectVolumes(c, d, ro, mountTable)
	}
}

func classifyVolume(v *Volume, n lsblkNode, system, ro bool) {
	switch {
	case system:
		v.Status = VolSystem
		for _, m := range v.Mountpoints {
			if isSystemMount(m) {
				v.Notes = append(v.Notes, "Used by the operating system.")
				break
			}
		}
		return
	case v.FSType == "swap":
		v.Status = VolSwap
		v.Notes = append(v.Notes, "Swap space. Not usable for files.")
		return
	}
	if desc, ok := containerFS[v.FSType]; ok {
		if len(n.Children) > 0 {
			v.Status = VolContainer
			v.Notes = append(v.Notes, desc+"; see the volume inside it.")
		} else {
			v.Status = VolUnsupported
			v.Notes = append(v.Notes, desc+" that is not unlocked or assembled. Vault will not change it.")
		}
		return
	}
	if v.FSType == "" {
		switch {
		case len(n.Children) > 0:
			v.Status = VolContainer
		case len(v.Mountpoints) > 0:
			v.Status = VolUnsupported
			v.Notes = append(v.Notes, "Filesystem type could not be identified.")
		default:
			v.Status = VolUnsupported
			v.Notes = append(v.Notes, "No filesystem. Vault never formats drives.")
		}
		return
	}
	caveat, supported := fsSupport[v.FSType]
	if !supported {
		v.Status = VolUnsupported
		v.Notes = append(v.Notes, fmt.Sprintf("%s is not a supported Vault filesystem.", v.FSType))
		return
	}
	if len(v.Mountpoints) == 0 {
		v.Status = VolUnmounted
		v.Notes = append(v.Notes, "Not mounted. Mount it (for example with your file manager) and Vault can use it.")
		return
	}
	v.Status = VolMounted
	if caveat != "" {
		v.Notes = append(v.Notes, caveat+".")
	}
	if ro {
		v.Notes = append(v.Notes, "Read-only device.")
		return
	}
	for _, m := range v.Mountpoints {
		if isReservedTarget(m) {
			v.Notes = append(v.Notes, fmt.Sprintf("Mounted at %s, a location Vault never uses for storage.", m))
			return
		}
	}
	v.Adoptable = true
}

func diskStatus(d Disk) DiskStatus {
	if d.System {
		return StatusSystem
	}
	if d.Adoptable {
		return StatusAvailable
	}
	if d.ReadOnly {
		return StatusReadOnly
	}
	for _, v := range d.Volumes {
		if v.Status == VolUnmounted {
			return StatusUnmounted
		}
	}
	return StatusUnsupported
}

func summarize(ds []Disk) Summary {
	s := Summary{Total: len(ds)}
	for _, d := range ds {
		switch d.Status {
		case StatusSystem:
			s.System++
		case StatusAvailable:
			s.Available++
		case StatusUnmounted:
			s.Unmounted++
		default:
			s.Unsupported++
		}
		switch d.Health.Status {
		case health.Healthy:
			s.Healthy++
		case health.Warning:
			s.Warning++
		case health.Critical:
			s.Critical++
		default:
			s.Unknown++
		}
	}
	return s
}

func kindOf(n lsblkNode) string {
	switch {
	case n.Tran == "nvme" || strings.HasPrefix(n.Name, "nvme"):
		return "nvme"
	case n.Tran == "usb":
		return "usb"
	case n.Tran == "virtio" || strings.HasPrefix(n.Name, "vd"):
		return "virtual"
	case strings.HasPrefix(n.Name, "mmcblk"):
		return "sd"
	case bool(n.Rota):
		return "hdd"
	default:
		return "ssd"
	}
}

func displayName(d Disk) string {
	if strings.HasPrefix(d.Vendor, "0x") {
		d.Vendor = "" // PCI vendor IDs (e.g. virtio's 0x1af4) are not names
	}
	name := strings.TrimSpace(strings.Join(strings.Fields(d.Vendor+" "+d.Model), " "))
	if d.Vendor != "" && strings.HasPrefix(strings.ToLower(d.Model), strings.ToLower(d.Vendor)) {
		name = d.Model
	}
	if name != "" && !strings.EqualFold(d.Vendor, "ATA") {
		return name
	}
	if d.Model != "" {
		return d.Model
	}
	switch d.Kind {
	case "nvme":
		return "NVMe drive"
	case "usb":
		return "USB drive"
	case "virtual":
		return "Virtual disk"
	case "sd":
		return "SD card"
	case "hdd":
		return "Hard drive"
	default:
		return "Drive " + d.Name
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Scanner runs discovery against the live system and caches results so the
// dashboard never triggers a scan storm.
type Scanner struct {
	Run sysexec.Runner
	// TTL is how long an inventory is reused. Defaults to 30s.
	TTL time.Duration
	// MinRefresh bounds how often a forced refresh may rescan. Defaults to 5s.
	MinRefresh time.Duration
	// SmartTTL is how long SMART data is cached per drive. Defaults to 30m.
	SmartTTL time.Duration
	// SMART enables smartctl health queries.
	SMART bool

	mu    sync.Mutex
	last  *Inventory
	smart map[string]smartEntry
	nowFn func() time.Time
}

type smartEntry struct {
	report health.Report
	at     time.Time
}

func (s *Scanner) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// Inventory returns a cached inventory, rescanning when stale. force asks
// for a fresh scan but is still bounded by MinRefresh.
func (s *Scanner) Inventory(ctx context.Context, force bool) (*Inventory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ttl, minRefresh := s.TTL, s.MinRefresh
	if ttl == 0 {
		ttl = 30 * time.Second
	}
	if minRefresh == 0 {
		minRefresh = 5 * time.Second
	}
	if s.last != nil {
		age := s.now().Sub(s.last.ScannedAt)
		if age < minRefresh || (!force && age < ttl) {
			return s.last, nil
		}
	}
	inv, err := s.scan(ctx)
	if err != nil {
		return nil, err
	}
	s.last = inv
	return inv, nil
}

func (s *Scanner) scan(ctx context.Context) (*Inventory, error) {
	lsblkOut, err := s.Run.Output(ctx, "lsblk", "-J", "-b", "-o", lsblkColumns)
	if err != nil {
		var legacyErr error
		lsblkOut, legacyErr = s.Run.Output(ctx, "lsblk", "-J", "-b", "-o", lsblkColumnsLegacy)
		if legacyErr != nil {
			return nil, fmt.Errorf("disks: lsblk failed: %w", errors.Join(err, legacyErr))
		}
	}
	findmntOut, fmErr := s.Run.Output(ctx, "findmnt", "-J", "-o", "TARGET,SOURCE,FSTYPE")
	if fmErr != nil {
		findmntOut = nil
	}
	inv, err := Build(lsblkOut, findmntOut)
	if err != nil {
		return nil, err
	}
	if fmErr != nil {
		inv.Warnings = append(inv.Warnings, "findmnt was unavailable; system disk detection relied on lsblk only.")
	}
	inv.ScannedAt = s.now()
	s.attachHealth(ctx, inv)
	return inv, nil
}

func (s *Scanner) attachHealth(ctx context.Context, inv *Inventory) {
	if !s.SMART {
		for i := range inv.Disks {
			inv.Disks[i].Health = health.UnknownReport("Health monitoring is off")
		}
		inv.Summary = summarize(inv.Disks)
		return
	}
	if !s.Run.Available("smartctl") {
		for i := range inv.Disks {
			inv.Disks[i].Health = health.UnknownReport("smartctl is not installed (package: smartmontools)")
		}
		inv.Summary = summarize(inv.Disks)
		return
	}
	smartTTL := s.SmartTTL
	if smartTTL == 0 {
		smartTTL = 30 * time.Minute
	}
	if s.smart == nil {
		s.smart = map[string]smartEntry{}
	}
	for i := range inv.Disks {
		d := &inv.Disks[i]
		if d.Kind == "virtual" {
			d.Health = health.UnknownReport("Virtual disks do not report SMART data")
			continue
		}
		if !validDevicePath(d.Path) {
			d.Health = health.UnknownReport("Unrecognised device path")
			continue
		}
		if e, ok := s.smart[d.Path]; ok && s.now().Sub(e.at) < smartTTL {
			d.Health = e.report
			continue
		}
		// Read-only query: identity, overall health, attributes. -n standby
		// avoids spinning up sleeping hard drives just to poll them.
		out, err := s.Run.Output(ctx, "smartctl", "-j", "-n", "standby", "-i", "-H", "-A", d.Path)
		var rep health.Report
		if len(out) > 0 {
			rep, _ = health.Parse(out)
		} else {
			rep = health.UnknownReport("SMART data unavailable")
			if err != nil {
				rep.Message = "SMART query failed"
			}
		}
		s.smart[d.Path] = smartEntry{report: rep, at: s.now()}
		d.Health = rep
	}
	inv.Summary = summarize(inv.Disks)
}

// validDevicePath guards the one place a discovered value is passed as a
// command argument: it must be a plain /dev node name.
func validDevicePath(p string) bool {
	if !strings.HasPrefix(p, "/dev/") || path.Clean(p) != p {
		return false
	}
	name := strings.TrimPrefix(p, "/dev/")
	if name == "" || strings.Contains(name, "/") {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
