package storage

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// MountTable lists mount points from /proc/self/mountinfo. It is used to
// find which filesystem really contains a path, so Vault never writes to
// the system disk through an empty mount point of an unplugged drive.
type MountTable []string

// ReadMountTable parses /proc/self/mountinfo.
func ReadMountTable() (MountTable, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var t MountTable
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 {
			continue
		}
		t = append(t, unescapeMount(fields[4]))
	}
	return t, sc.Err()
}

// unescapeMount decodes the octal escapes the kernel uses for spaces,
// tabs, newlines and backslashes in mount points.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1:i+4]) {
			v := (s[i+1]-'0')*64 + (s[i+2]-'0')*8 + (s[i+3] - '0')
			b.WriteByte(v)
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctal(s string) bool {
	if len(s) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		if s[i] < '0' || s[i] > '7' {
			return false
		}
	}
	return true
}

// Containing returns the mount point of the filesystem holding path (the
// longest mount point that is path or one of its parents).
func (t MountTable) Containing(path string) string {
	path = filepath.Clean(path)
	best := ""
	for _, m := range t {
		if m == path || m == "/" || strings.HasPrefix(path, strings.TrimSuffix(m, "/")+"/") {
			if len(m) > len(best) {
				best = m
			}
		}
	}
	return best
}

// IsMountPoint reports whether path is itself a mount point.
func (t MountTable) IsMountPoint(path string) bool {
	path = filepath.Clean(path)
	for _, m := range t {
		if m == path {
			return true
		}
	}
	return false
}
