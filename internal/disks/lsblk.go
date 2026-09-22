package disks

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// lsblkColumns is the column set requested from lsblk. FSSIZE/FSUSED/FSAVAIL
// and MOUNTPOINTS need util-linux >= 2.37, which every supported Arch/Omarchy
// install has; lsblkColumnsLegacy is tried if they are rejected.
const (
	lsblkColumns       = "NAME,KNAME,PATH,PKNAME,TYPE,SIZE,MODEL,SERIAL,VENDOR,FSTYPE,UUID,LABEL,PARTLABEL,MOUNTPOINTS,FSSIZE,FSUSED,FSAVAIL,RM,RO,ROTA,TRAN,HOTPLUG"
	lsblkColumnsLegacy = "NAME,KNAME,PATH,PKNAME,TYPE,SIZE,MODEL,SERIAL,VENDOR,FSTYPE,UUID,LABEL,PARTLABEL,MOUNTPOINT,RM,RO,ROTA,TRAN,HOTPLUG"
)

type lsblkOutput struct {
	BlockDevices []lsblkNode `json:"blockdevices"`
}

type lsblkNode struct {
	Name        string      `json:"name"`
	KName       string      `json:"kname"`
	Path        string      `json:"path"`
	PKName      string      `json:"pkname"`
	Type        string      `json:"type"`
	Size        flexInt     `json:"size"`
	Model       string      `json:"model"`
	Serial      string      `json:"serial"`
	Vendor      string      `json:"vendor"`
	FSType      string      `json:"fstype"`
	UUID        string      `json:"uuid"`
	Label       string      `json:"label"`
	PartLabel   string      `json:"partlabel"`
	Mountpoints []*string   `json:"mountpoints"`
	Mountpoint  *string     `json:"mountpoint"`
	FSSize      flexInt     `json:"fssize"`
	FSUsed      flexInt     `json:"fsused"`
	FSAvail     flexInt     `json:"fsavail"`
	RM          flexBool    `json:"rm"`
	RO          flexBool    `json:"ro"`
	Rota        flexBool    `json:"rota"`
	Tran        string      `json:"tran"`
	Hotplug     flexBool    `json:"hotplug"`
	Children    []lsblkNode `json:"children"`
}

// mounts returns the node's non-empty mountpoints, supporting both the
// modern MOUNTPOINTS array and the legacy MOUNTPOINT column.
func (n lsblkNode) mounts() []string {
	var out []string
	seen := map[string]bool{}
	add := func(p *string) {
		if p == nil || *p == "" || seen[*p] {
			return
		}
		seen[*p] = true
		out = append(out, *p)
	}
	for _, p := range n.Mountpoints {
		add(p)
	}
	add(n.Mountpoint)
	return out
}

func parseLsblk(data []byte) ([]lsblkNode, error) {
	var out lsblkOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("disks: parse lsblk: %w", err)
	}
	return out.BlockDevices, nil
}

// flexBool accepts true/false, "0"/"1", 0/1 and null. Older lsblk releases
// emit booleans as strings.
type flexBool bool

func (b *flexBool) UnmarshalJSON(d []byte) error {
	switch strings.Trim(string(d), `"`) {
	case "true", "1":
		*b = true
	case "false", "0", "null", "":
		*b = false
	default:
		return fmt.Errorf("invalid boolean %s", d)
	}
	return nil
}

// flexInt accepts numbers, numeric strings and null.
type flexInt int64

func (i *flexInt) UnmarshalJSON(d []byte) error {
	s := strings.Trim(string(d), `"`)
	if s == "null" || s == "" {
		*i = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid integer %s", d)
	}
	*i = flexInt(v)
	return nil
}

type findmntOutput struct {
	Filesystems []findmntNode `json:"filesystems"`
}

type findmntNode struct {
	Target   string        `json:"target"`
	Source   string        `json:"source"`
	FSType   string        `json:"fstype"`
	Children []findmntNode `json:"children"`
}

type mountInfo struct {
	Source string
	FSType string
}

// parseFindmnt flattens `findmnt -J -o TARGET,SOURCE,FSTYPE` into a
// target -> mount map. Bind/subvolume suffixes such as "/dev/sda2[/@home]"
// are stripped to the device path.
func parseFindmnt(data []byte) (map[string]mountInfo, error) {
	var out findmntOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("disks: parse findmnt: %w", err)
	}
	m := map[string]mountInfo{}
	var walk func([]findmntNode)
	walk = func(nodes []findmntNode) {
		for _, n := range nodes {
			src := n.Source
			if i := strings.IndexByte(src, '['); i > 0 {
				src = src[:i]
			}
			m[n.Target] = mountInfo{Source: src, FSType: n.FSType}
			walk(n.Children)
		}
	}
	walk(out.Filesystems)
	return m, nil
}
