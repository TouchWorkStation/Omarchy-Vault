// Package demo provides canned system output so the dashboard can be
// developed and demonstrated on machines without spare drives. It is only
// used when vaultd is started with --demo, and the UI labels it clearly.
package demo

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"

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

// Runner returns a fake runner with a sample Omarchy machine: an NVMe
// system disk, an 8 TB WD Red, an unmounted 4 TB Seagate and more.
func Runner() *sysexec.Fake {
	smart := func(dev string) string { return "smartctl -j -n standby -i -H -A /dev/" + dev }
	return &sysexec.Fake{
		Installed: map[string]bool{"lsblk": true, "findmnt": true, "smartctl": true},
		Outputs: map[string][]byte{
			"lsblk -J -b -o NAME,KNAME,PATH,PKNAME,TYPE,SIZE,MODEL,SERIAL,VENDOR,FSTYPE,UUID,LABEL,PARTLABEL,MOUNTPOINTS,FSSIZE,FSUSED,FSAVAIL,RM,RO,ROTA,TRAN,HOTPLUG": read("lsblk.json"),
			"findmnt -J -o TARGET,SOURCE,FSTYPE": read("findmnt.json"),
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
