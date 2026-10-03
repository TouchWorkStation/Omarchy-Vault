package transfer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

// withPrimitives swaps the publishing primitives for one test.
func withPrimitives(t *testing.T, rename, link func(int, string, string) error) {
	t.Helper()
	oldRename, oldLink := renameNoReplace, linkNoReplace
	if rename != nil {
		renameNoReplace = rename
	}
	if link != nil {
		linkNoReplace = link
	}
	t.Cleanup(func() { renameNoReplace, linkNoReplace = oldRename, oldLink })
}

var noRename = func(int, string, string) error { return unix.EINVAL }

func uploadsDir(t *testing.T, existing map[string]string) (string, *os.Root) {
	t.Helper()
	dir := t.TempDir()
	up := filepath.Join(dir, "Phone Uploads")
	if err := os.Mkdir(up, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range existing {
		os.WriteFile(filepath.Join(up, name), []byte(data), 0o644)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return up, root
}

// contents returns name -> data for every file in dir, failing on any
// leftover partial file.
func contents(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if isPartial(e.Name()) {
			t.Errorf("partial file left behind: %s", e.Name())
			continue
		}
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		out[e.Name()] = string(b)
	}
	return out
}

// Same-name uploads at the same time: each one lands under its own name
// and none replaces another, both with RENAME_NOREPLACE and with the
// hard-link fallback.
func TestConcurrentSameNameUploads(t *testing.T) {
	for _, mode := range []string{"renameat2", "link fallback"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "link fallback" {
				withPrimitives(t, noRename, nil)
			}
			up, root := uploadsDir(t, map[string]string{"cat.jpg": "original"})
			const n = 24
			var wg sync.WaitGroup
			errs := make(chan error, n)
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, _, err := Save(root, "Phone Uploads", "cat.jpg", strings.NewReader(fmt.Sprintf("upload %d", i)), 1<<20)
					errs <- err
				}(i)
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			got := contents(t, up)
			if got["cat.jpg"] != "original" {
				t.Fatalf("existing file replaced: %q", got["cat.jpg"])
			}
			var bodies []string
			for name, data := range got {
				if name != "cat.jpg" {
					bodies = append(bodies, data)
				}
			}
			sort.Strings(bodies)
			if len(bodies) != n {
				t.Fatalf("%d uploads kept, want %d: %v", len(bodies), n, got)
			}
			for i := 1; i < len(bodies); i++ {
				if bodies[i] == bodies[i-1] {
					t.Fatalf("an upload was lost (duplicate %q)", bodies[i])
				}
			}
		})
	}
}

// A file appears under the chosen name in the instant before Vault
// publishes (the window a check-then-rename would lose). The link
// fallback must notice and move on rather than replace it.
func TestFallbackNeverReplacesLateArrival(t *testing.T) {
	up, root := uploadsDir(t, nil)
	realLink := linkNoReplace
	raced := false
	withPrimitives(t, noRename, func(dfd int, from, to string) error {
		if !raced {
			raced = true
			os.WriteFile(filepath.Join(up, to), []byte("arrived first"), 0o644)
		}
		return realLink(dfd, from, to)
	})
	name, _, err := Save(root, "Phone Uploads", "doc.pdf", strings.NewReader("upload"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	got := contents(t, up)
	if got["doc.pdf"] != "arrived first" || name != "doc (1).pdf" || got["doc (1).pdf"] != "upload" {
		t.Fatalf("name %q, files %v", name, got)
	}
}

// A filesystem with neither RENAME_NOREPLACE nor hard links: the upload
// is refused, nothing is replaced and no partial file is left.
func TestNoSafePublishRefuses(t *testing.T) {
	for _, linkErr := range []error{unix.EPERM, unix.EOPNOTSUPP} {
		up, root := uploadsDir(t, map[string]string{"cat.jpg": "original"})
		withPrimitives(t, noRename, func(int, string, string) error { return linkErr })
		_, _, err := Save(root, "Phone Uploads", "cat.jpg", strings.NewReader("new"), 1<<20)
		if !errors.Is(err, ErrNoSafePublish) {
			t.Fatalf("link %v: err = %v", linkErr, err)
		}
		if got := contents(t, up); len(got) != 1 || got["cat.jpg"] != "original" {
			t.Fatalf("files after refusal: %v", got)
		}
	}
}
