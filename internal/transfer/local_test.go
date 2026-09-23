package transfer

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestCheckLocal(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519"), []byte("key"), 0o600)
	os.MkdirAll(filepath.Join(home, "Pictures"), 0o755)
	pic := filepath.Join(home, "Pictures", "cat.jpg")
	os.WriteFile(pic, []byte("cat"), 0o644)
	os.Symlink(pic, filepath.Join(home, "Pictures", "link.jpg"))

	if _, _, err := CheckLocal(pic, home); err != nil {
		t.Errorf("ordinary file refused: %v", err)
	}
	for p, want := range map[string]error{
		filepath.Join(home, ".ssh", "id_ed25519"): ErrPrivate,
		filepath.Join(home, ".ssh"):               ErrPrivate,
		home:                                      ErrPrivate, // contains .ssh
		"/etc/passwd":                             ErrPrivate,
		"/":                                       ErrPrivate,
		filepath.Join(home, "Pictures", "link.jpg"): ErrNotPlain,
		filepath.Join(home, "nope.txt"):             ErrNoSuch,
		"relative/file":                             ErrBadPath,
	} {
		if _, _, err := CheckLocal(p, home); !errors.Is(err, want) {
			t.Errorf("CheckLocal(%s) = %v, want %v", p, err, want)
		}
	}
}

func TestLocalDownloads(t *testing.T) {
	s, _ := testServer(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "Desktop")
	os.MkdirAll(filepath.Join(dir, "Trip", "day 2"), 0o755)
	os.WriteFile(filepath.Join(dir, "report.pdf"), []byte("pdf-bytes"), 0o644)
	os.WriteFile(filepath.Join(dir, "Trip", "a.jpg"), []byte("aaa"), 0o644)
	os.WriteFile(filepath.Join(dir, "Trip", "day 2", "b.jpg"), []byte("bb"), 0o644)
	h := s.Handler()
	mk := func(paths ...string) string {
		_, tok, _ := s.Store.Create(context.Background(), NewSession{Kind: KindDownload, Path: strings.Join(paths, "\n"),
			TTL: 10 * time.Minute, MaxFiles: 1, MaxBytes: NoByteLimit})
		return tok
	}
	names := func(b []byte) string {
		zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			t.Fatal(err)
		}
		var n []string
		for _, f := range zr.File {
			n = append(n, f.Name)
		}
		sort.Strings(n)
		return strings.Join(n, ",")
	}

	// One file: sent as itself; the page shows its name, not its folder.
	tok := mk(filepath.Join(dir, "report.pdf"))
	if w := get(h, "/d/"+tok); !strings.Contains(w.Body.String(), "report.pdf") || strings.Contains(w.Body.String(), dir) {
		t.Errorf("page: %s", w.Body)
	}
	if w := get(h, "/d/"+tok+"/file"); w.Code != 200 || w.Body.String() != "pdf-bytes" {
		t.Errorf("file = %d %q", w.Code, w.Body)
	}
	// A folder: one zip named after it.
	tok = mk(filepath.Join(dir, "Trip"))
	w := get(h, "/d/"+tok+"/file")
	if w.Code != 200 || names(w.Body.Bytes()) != "Trip/a.jpg,Trip/day 2/b.jpg" {
		t.Errorf("folder zip = %d %s", w.Code, names(w.Body.Bytes()))
	}
	// Several copied items: one zip with all of them.
	tok = mk(filepath.Join(dir, "report.pdf"), filepath.Join(dir, "Trip"))
	if w := get(h, "/d/"+tok); !strings.Contains(w.Body.String(), "2 items") {
		t.Errorf("multi page: %s", w.Body)
	}
	w = get(h, "/d/"+tok+"/file")
	if w.Code != 200 || names(w.Body.Bytes()) != "Trip/a.jpg,Trip/day 2/b.jpg,report.pdf" {
		t.Errorf("multi zip = %d %s", w.Code, names(w.Body.Bytes()))
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "report and 1 more.zip") {
		t.Errorf("zip name = %q", cd)
	}
	// A file deleted after copying: a clear message, not an error page.
	os.Remove(filepath.Join(dir, "report.pdf"))
	if w := get(h, "/d/"+mk(filepath.Join(dir, "report.pdf"))); w.Code != http.StatusNotFound {
		t.Errorf("deleted = %d", w.Code)
	}
}
