package transfer

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
)

const maxNameBytes = 200

// CleanName turns a phone-supplied filename into a safe single name: no
// directories, no control or separator characters, no leading dots, no
// reserved names, at most 200 bytes, extension kept.
func CleanName(raw string) string {
	// Browsers may send a path (older ones: "C:\fakepath\x.jpg").
	raw = strings.ReplaceAll(raw, "\\", "/")
	raw = path.Base(raw)
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r == utf8.RuneError, r == '/', r == 0:
			b.WriteRune('_')
		case unicode.IsControl(r), r == '\u202e', r == '\u202d': // no bidi tricks
			// drop
		default:
			b.WriteRune(r)
		}
	}
	name := strings.TrimSpace(b.String())
	name = strings.TrimLeft(name, ". ")
	if name == "" || name == "." || name == ".." {
		name = "upload"
	}
	if len(name) > maxNameBytes {
		ext := path.Ext(name)
		if len(ext) > 16 {
			ext = ""
		}
		stem := name[:len(name)-len(ext)]
		for len(stem)+len(ext) > maxNameBytes {
			_, size := utf8.DecodeLastRuneInString(stem)
			stem = stem[:len(stem)-size]
		}
		name = stem + ext
	}
	return name
}

// candidate returns name, then "stem (1).ext", "stem (2).ext", …
func candidate(name string, i int) string {
	if i == 0 {
		return name
	}
	ext := path.Ext(name)
	if ext == name {
		ext = ""
	}
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), i, ext)
}

// ErrTooLarge means the file exceeded the allowed size.
var ErrTooLarge = errors.New("transfer: file too large")

// Save streams r into dir/folder under a cleaned, unique name, never
// replacing an existing file. The data goes to a hidden temporary file
// first and is moved into place only when complete. At most limit bytes
// are accepted. It returns the final name and size.
func Save(root *os.Root, folder, rawName string, r io.Reader, limit int64) (string, int64, error) {
	name := CleanName(rawName)
	info, err := root.Lstat(folder)
	if err != nil {
		return "", 0, fmt.Errorf("transfer: destination: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return "", 0, errors.New("transfer: destination is not a plain folder")
	}
	dir, err := root.Open(folder)
	if err != nil {
		return "", 0, err
	}
	defer dir.Close()

	suffix, err := auth.Random(9)
	if err != nil {
		return "", 0, err
	}
	tmpName := ".vault-partial-" + suffix
	tmpRel := path.Join(folder, tmpName)
	f, err := root.OpenFile(tmpRel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return "", 0, err
	}
	cleanup := func() { f.Close(); root.Remove(tmpRel) }

	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	if err != nil {
		cleanup()
		return "", 0, err
	}
	if n > limit {
		cleanup()
		return "", 0, ErrTooLarge
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return "", 0, err
	}
	if err := f.Close(); err != nil {
		root.Remove(tmpRel)
		return "", 0, err
	}

	dfd := int(dir.Fd())
	for i := 0; i < 10000; i++ {
		final := candidate(name, i)
		err := unix.Renameat2(dfd, tmpName, dfd, final, unix.RENAME_NOREPLACE)
		switch {
		case err == nil:
			return final, n, nil
		case errors.Is(err, unix.EEXIST):
			continue
		case errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EOPNOTSUPP):
			// Filesystems without RENAME_NOREPLACE (e.g. some FUSE/exFAT):
			// check, then rename. Vault is the only writer here.
			if _, statErr := root.Lstat(path.Join(folder, final)); statErr == nil {
				continue
			}
			if err := unix.Renameat(dfd, tmpName, dfd, final); err != nil {
				root.Remove(tmpRel)
				return "", 0, err
			}
			return final, n, nil
		default:
			root.Remove(tmpRel)
			return "", 0, err
		}
	}
	root.Remove(tmpRel)
	return "", 0, errors.New("transfer: could not find a free name")
}
