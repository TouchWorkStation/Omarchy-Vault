// Package demo provides canned system output so the dashboard can be
// developed and demonstrated on machines without spare drives. It is only
// used when vaultd is started with --demo, and the UI labels it clearly.
package demo

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/sysexec"
)

//go:embed data
var data embed.FS

func read(name string) []byte {
	b, err := data.ReadFile("data/" + name)
	if err != nil {
		panic(err)
	}
	return b
}

// demoMounts are the sample drives' mount points. In demo mode they are
// moved inside a throwaway sandbox so choosing storage really works without
// touching this machine.
var demoMounts = []string{"/mnt/wdred", "/run/media/user/STICK"}

// Sandbox is a temporary world for demo mode.
type Sandbox struct {
	Dir        string
	ConfigPath string
	DataLink   string
	Mounts     []string // "/" plus the sandboxed drive mount points
}

// NewSandbox creates the sandbox under ~/.cache/omarchy-vault (not /tmp,
// which Vault never accepts as storage).
func NewSandbox() (*Sandbox, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	base = filepath.Join(base, "omarchy-vault")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(base, "demo-")
	if err != nil {
		return nil, err
	}
	sb := &Sandbox{
		Dir:        dir,
		ConfigPath: filepath.Join(dir, "config", "config.json"),
		DataLink:   filepath.Join(dir, "share", "current"),
		Mounts:     []string{"/"},
	}
	for _, m := range demoMounts {
		p := filepath.Join(dir, "drives", filepath.Base(m))
		if err := os.MkdirAll(p, 0o755); err != nil {
			return nil, err
		}
		sb.Mounts = append(sb.Mounts, p)
	}
	return sb, nil
}

func (sb *Sandbox) rewrite(b []byte) []byte {
	s := string(b)
	for _, m := range demoMounts {
		s = strings.ReplaceAll(s, `"`+m+`"`, `"`+filepath.Join(sb.Dir, "drives", filepath.Base(m))+`"`)
	}
	return []byte(s)
}

// Runner returns a fake runner with a sample Omarchy machine: an NVMe
// system disk, an 8 TB WD Red, an unmounted 4 TB Seagate and more. With a
// sandbox, the sample drives are mounted inside it.
func Runner(sb *Sandbox) *sysexec.Fake {
	smart := func(dev string) string { return "smartctl -j -n standby -i -H -A /dev/" + dev }
	lsblk, findmnt := read("lsblk.json"), read("findmnt.json")
	if sb != nil {
		lsblk, findmnt = sb.rewrite(lsblk), sb.rewrite(findmnt)
	}
	return &sysexec.Fake{
		Installed: map[string]bool{"lsblk": true, "findmnt": true, "smartctl": true},
		Outputs: map[string][]byte{
			"lsblk -J -b -o NAME,KNAME,PATH,PKNAME,TYPE,SIZE,MODEL,SERIAL,VENDOR,FSTYPE,UUID,LABEL,PARTLABEL,MOUNTPOINTS,FSSIZE,FSUSED,FSAVAIL,RM,RO,ROTA,TRAN,HOTPLUG": lsblk,
			"findmnt -J -o TARGET,SOURCE,FSTYPE": findmnt,
			smart("sda"):                         read("smart_sda.json"),
			smart("sdb"):                         read("smart_sdb.json"),
			smart("nvme0n1"):                     read("smart_nvme0n1.json"),
		},
	}
}

// HyprConfig writes the demo Hyprland config into dir and returns its path.
func HyprConfig(dir string) (string, error) {
	b, err := fs.ReadFile(data, "data/hypr/hyprland.conf")
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "hyprland.conf")
	return p, os.WriteFile(p, b, 0o600)
}
