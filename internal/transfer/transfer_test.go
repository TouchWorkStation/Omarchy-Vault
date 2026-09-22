package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCleanName(t *testing.T) {
	cases := map[string]string{
		"IMG_0001.HEIC":         "IMG_0001.HEIC",
		"../../etc/passwd":      "passwd",
		`C:\fakepath\photo.jpg`: "photo.jpg",
		".bashrc":               "bashrc",
		"...":                   "upload",
		"":                      "upload",
		"a\x00b.txt":            "a_b.txt",
		"evil\u202egpj.exe":     "evilgpj.exe",
		"line\nbreak.png":       "linebreak.png",
		"  spaced name .mov ":   "spaced name .mov",
		"folder/":               "folder",
		"my photo (2023).jpg":   "my photo (2023).jpg",
	}
	for in, want := range cases {
		if got := CleanName(in); got != want {
			t.Errorf("CleanName(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("é", 300) + ".jpeg"
	got := CleanName(long)
	if len(got) > maxNameBytes || !strings.HasSuffix(got, ".jpeg") || !strings.HasPrefix(got, "é") {
		t.Errorf("long name = %d bytes %q…", len(got), got[:10])
	}
}

func TestSaveNeverOverwritesAndStaysInside(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "Phone Uploads"), 0o755)
	os.WriteFile(filepath.Join(dir, "Phone Uploads", "cat.jpg"), []byte("original"), 0o644)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	name, n, err := Save(root, "Phone Uploads", "cat.jpg", strings.NewReader("new cat"), 1<<20)
	if err != nil || name != "cat (1).jpg" || n != 7 {
		t.Fatalf("save = %q %d %v", name, n, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "Phone Uploads", "cat.jpg")); string(b) != "original" {
		t.Fatal("existing file overwritten")
	}
	name, _, _ = Save(root, "Phone Uploads", "cat.jpg", strings.NewReader("x"), 1<<20)
	if name != "cat (2).jpg" {
		t.Errorf("second duplicate = %q", name)
	}
	// Path tricks end up as a plain name inside the folder.
	name, _, err = Save(root, "Phone Uploads", "../../escape.txt", strings.NewReader("x"), 1<<20)
	if err != nil || name != "escape.txt" {
		t.Fatalf("escape = %q %v", name, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "escape.txt")); err == nil {
		t.Fatal("file written outside")
	}
	// Too large: nothing left behind.
	if _, _, err := Save(root, "Phone Uploads", "big.bin", strings.NewReader("0123456789"), 5); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too large = %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "Phone Uploads"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".vault-partial-") || e.Name() == "big.bin" {
			t.Errorf("leftover %s", e.Name())
		}
	}
	// A symlinked destination is refused.
	os.Symlink(t.TempDir(), filepath.Join(dir, "Linked"))
	if _, _, err := Save(root, "Linked", "x.txt", strings.NewReader("x"), 10); err == nil {
		t.Fatal("wrote through a symlinked folder")
	}
}

func TestStoreLifecycle(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	s, err := Open(filepath.Join(t.TempDir(), "db", "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Now = func() time.Time { return now }
	ctx := context.Background()

	sess, token, err := s.Create(ctx, NewSession{Kind: KindUpload, Folder: "Phone Uploads", CreatedBy: "chris", TTL: 10 * time.Minute, MaxFiles: 2, MaxBytes: 100})
	if err != nil || len(token) < 40 {
		t.Fatalf("create: %v %q", err, token)
	}
	if got, err := s.Lookup(ctx, token, KindUpload); err != nil || got.ID != sess.ID {
		t.Fatalf("lookup: %v", err)
	}
	if _, err := s.Lookup(ctx, token+"x", KindUpload); !errors.Is(err, ErrNotFound) {
		t.Errorf("wrong token: %v", err)
	}
	if _, err := s.Lookup(ctx, token, "download"); !errors.Is(err, ErrNotFound) {
		t.Errorf("wrong kind: %v", err)
	}

	// The token is not stored anywhere in the database file.
	var n int
	s.db.QueryRow(`SELECT count(*) FROM sessions WHERE token_hash = ?`, []byte(token)).Scan(&n)
	if n != 0 {
		t.Fatal("raw token stored")
	}

	// Limits: two files max.
	for i := 0; i < 2; i++ {
		if err := s.Reserve(ctx, sess.ID); err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
		s.Record(ctx, sess, "f.jpg", 10)
	}
	if err := s.Reserve(ctx, sess.ID); !errors.Is(err, ErrLimit) {
		t.Errorf("third file: %v", err)
	}
	if _, err := s.Lookup(ctx, token, KindUpload); !errors.Is(err, ErrUsedUp) {
		t.Errorf("used up: %v", err)
	}
	if got, _ := s.Received(ctx, sess.ID); len(got) != 2 {
		t.Errorf("received = %d", len(got))
	}

	// Expiry and revocation.
	s2, tok2, _ := s.Create(ctx, NewSession{Kind: KindUpload, Folder: "Phone Uploads", TTL: time.Minute, MaxFiles: 10, MaxBytes: 100})
	if act, _ := s.Active(ctx); len(act) != 1 || act[0].ID != s2.ID {
		t.Errorf("active = %+v", act)
	}
	now = now.Add(2 * time.Minute)
	if _, err := s.Lookup(ctx, tok2, KindUpload); !errors.Is(err, ErrExpired) {
		t.Errorf("expired: %v", err)
	}
	s3, tok3, _ := s.Create(ctx, NewSession{Kind: KindUpload, Folder: "Phone Uploads", TTL: time.Hour, MaxFiles: 10, MaxBytes: 100})
	if err := s.Revoke(ctx, s3.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(ctx, tok3, KindUpload); !errors.Is(err, ErrRevoked) {
		t.Errorf("revoked: %v", err)
	}
	if err := s.Revoke(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoke unknown: %v", err)
	}

	now = now.Add(8 * 24 * time.Hour)
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, s2.ID); !errors.Is(err, ErrNotFound) {
		t.Error("old session not pruned")
	}
}
