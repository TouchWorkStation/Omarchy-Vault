package transfer

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// readFlags refuse a symlink that appeared after Stat, and never block on
// a FIFO swapped in for a file.
const readFlags = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK

// Errors from resolving a Vault path.
var (
	ErrBadPath   = errors.New("transfer: not a valid Vault path")
	ErrNoSuch    = errors.New("transfer: no such file or folder")
	ErrNotPlain  = errors.New("transfer: links and special files can't be shared")
	ErrTooMany   = errors.New("transfer: folder has too many files")
	errNotInside = errors.New("transfer: path leaves the shared folder")
)

// maxListed caps how many files a folder link lists or zips.
const maxListed = 20000

// CleanRel validates a Vault-relative path such as "Photos/2024/cat.jpg":
// forward slashes, no empty, "." or ".." parts, no leading slash, no NUL,
// valid UTF-8. It never touches the disk.
func CleanRel(rel string) (string, error) {
	rel = strings.Trim(rel, "/")
	if rel == "" || len(rel) > 4096 || !utf8.ValidString(rel) || strings.ContainsRune(rel, 0) {
		return "", ErrBadPath
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." {
			return "", ErrBadPath
		}
	}
	return rel, nil
}

// TopFolder returns the first element of a clean relative path.
func TopFolder(rel string) string {
	top, _, _ := strings.Cut(rel, "/")
	return top
}

// Stat resolves rel inside root without following any symlink on the way
// and returns what it names: a regular file or a directory.
func Stat(root *os.Root, rel string) (fs.FileInfo, error) {
	rel, err := CleanRel(rel)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(rel, "/")
	var info fs.FileInfo
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		info, err = root.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNoSuch
		}
		if err != nil {
			return nil, err
		}
		if isPartial(info.Name()) {
			return nil, ErrNoSuch
		}
		last := i == len(parts)-1
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			return nil, ErrNotPlain
		case !last && !info.IsDir():
			return nil, ErrNoSuch
		case last && !info.IsDir() && !info.Mode().IsRegular():
			return nil, ErrNotPlain
		}
	}
	return info, nil
}

func isPartial(name string) bool { return strings.HasPrefix(name, ".vault-partial-") }

// Entry is one file inside a shared folder.
type Entry struct {
	Path     string    `json:"path"` // relative to the shared folder
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// Walk lists the regular files under dir (a clean relative path), skipping
// symlinks, special files and unfinished uploads, sorted by path.
func Walk(root *os.Root, dir string) ([]Entry, int64, error) {
	var out []Entry
	var total int64
	err := fs.WalkDir(root.FS(), dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			return nil // unreadable subfolder: skip it
		}
		if isPartial(d.Name()) || d.Type()&fs.ModeSymlink != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if len(out) >= maxListed {
			return ErrTooMany
		}
		out = append(out, Entry{Path: strings.TrimPrefix(p, dir+"/"), Size: info.Size(), Modified: info.ModTime()})
		total += info.Size()
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, total, err
}

// Join resolves sub (a path inside a shared folder, as listed by Walk)
// against the shared folder base, refusing anything that leaves it.
func Join(base, sub string) (string, error) {
	sub, err := CleanRel(sub)
	if err != nil {
		return "", err
	}
	full := path.Join(base, sub)
	if !strings.HasPrefix(full, base+"/") {
		return "", errNotInside
	}
	return full, nil
}

// attachment builds a Content-Disposition header that works on iOS and
// Android for any file name.
func attachment(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, url.PathEscape(name))
}

// ServeFile streams one regular file (with Range support, so phones can
// resume) as a download.
func ServeFile(w http.ResponseWriter, r *http.Request, root *os.Root, rel string) error {
	if _, err := Stat(root, rel); err != nil {
		return err
	}
	f, err := root.OpenFile(rel, readFlags, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return ErrNotPlain
	}
	name := path.Base(rel)
	ctype := mime.TypeByExtension(path.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", attachment(name))
	http.ServeContent(w, r, "", info.ModTime(), f)
	return nil
}

// ServeZip streams the folder dir as a zip built on the fly. Entries are
// stored uncompressed (photos and videos are compressed already), so it
// is fast and needs no temporary space. Names inside start with the
// folder's own name.
func ServeZip(w http.ResponseWriter, root *os.Root, dir string) error {
	files, _, err := Walk(root, dir)
	if err != nil {
		return err
	}
	name := path.Base(dir)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", attachment(name+".zip"))
	zw := zip.NewWriter(w)
	if err := zipFiles(zw, root, dir, name, files); err != nil {
		return err
	}
	return zw.Close()
}

// zipFiles adds files (as listed by Walk under dir) to zw, named
// prefix/<path>.
func zipFiles(zw *zip.Writer, root *os.Root, dir, prefix string, files []Entry) error {
	for _, e := range files {
		src := e.Path
		if dir != "" {
			src = dir + "/" + e.Path
		}
		name := e.Path
		if prefix != "" {
			name = prefix + "/" + e.Path
		}
		if err := zipOne(zw, root, src, name, e.Modified); err != nil {
			return err
		}
	}
	return nil
}

// zipOne adds one regular file; a file that vanished or changed type since
// it was listed is left out.
func zipOne(zw *zip.Writer, root *os.Root, src, name string, mod time.Time) error {
	f, err := root.OpenFile(src, readFlags, 0)
	if err != nil {
		return nil
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil
	}
	hdr := &zip.FileHeader{Name: name, Method: zip.Store, Modified: mod}
	hdr.SetMode(0o644)
	dst, err := zw.CreateHeader(hdr)
	if err == nil {
		_, err = io.Copy(dst, f)
	}
	return err // non-nil: the phone went away
}
