package users

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cfg", "users.json")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}

func TestCreatePersistPrivate(t *testing.T) {
	s, p := newStore(t)
	if err := s.Create(User{Username: "chris", Role: Admin, PasswordHash: "$argon2id$x", Folders: []Folder{{Name: "Photos", Access: ReadOnly}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(User{Username: "ann", Role: Family, PasswordHash: "$argon2id$y", Folders: []Folder{{Name: "Photos", Access: ReadWrite}}}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Count() != 2 {
		t.Fatalf("count = %d", s2.Count())
	}
	admin, _ := s2.Get("chris")
	if admin.Folders != nil {
		t.Error("admins should not carry folder grants")
	}
	v := admin.View()
	if !v.AllFolders {
		t.Error("admin view should say all folders")
	}
	if strings.Contains(strings.ToLower(string(mustJSON(v))), "argon") {
		t.Error("view leaked the hash")
	}
}

func TestValidation(t *testing.T) {
	s, _ := newStore(t)
	bad := []User{
		{Username: "Ann", Role: Family, PasswordHash: "h"},
		{Username: "a", Role: Family, PasswordHash: "h"},
		{Username: "local", Role: Family, PasswordHash: "h"},
		{Username: "ann", Role: "boss", PasswordHash: "h"},
		{Username: "ann", Role: Family},
		{Username: "ann", Role: Family, PasswordHash: "h", Folders: []Folder{{Name: "../etc", Access: ReadWrite}}},
		{Username: "ann", Role: Family, PasswordHash: "h", Folders: []Folder{{Name: "Photos/x", Access: ReadWrite}}},
		{Username: "ann", Role: Family, PasswordHash: "h", Folders: []Folder{{Name: "Photos", Access: "all"}}},
		{Username: "ann", Role: Family, PasswordHash: "h", Folders: []Folder{{Name: ".ssh", Access: ReadOnly}}},
	}
	for _, u := range bad {
		if err := s.Create(u); err == nil {
			t.Errorf("accepted %+v", u)
		}
	}
	s.Create(User{Username: "ann", Role: Family, PasswordHash: "h"})
	if err := s.Create(User{Username: "ann", Role: Guest, PasswordHash: "h"}); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate: %v", err)
	}
}

func TestLastAdminProtected(t *testing.T) {
	s, _ := newStore(t)
	s.Create(User{Username: "chris", Role: Admin, PasswordHash: "h"})
	s.Create(User{Username: "ann", Role: Family, PasswordHash: "h"})

	if _, err := s.Update("chris", func(u *User) error { u.Disabled = true; return nil }); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("disable last admin: %v", err)
	}
	if _, err := s.Update("chris", func(u *User) error { u.Role = Family; return nil }); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("demote last admin: %v", err)
	}
	if err := s.Delete("chris"); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("delete last admin: %v", err)
	}
	// With a second admin it is fine.
	s.Update("ann", func(u *User) error { u.Role = Admin; return nil })
	if _, err := s.Update("chris", func(u *User) error { u.Disabled = true; return nil }); err != nil {
		t.Errorf("disable with another admin: %v", err)
	}
	if _, err := s.Update("ann", func(u *User) error { u.Username = "bob"; return nil }); err == nil {
		t.Error("rename allowed")
	}
}

func TestDefaultFolders(t *testing.T) {
	avail := []string{"Documents", "Photos", "Shared"}
	if f := DefaultFolders(Family, avail); len(f) != 3 || f[0].Access != ReadWrite {
		t.Errorf("family = %+v", f)
	}
	if f := DefaultFolders(Guest, avail); len(f) != 1 || f[0].Name != "Shared" || f[0].Access != ReadOnly {
		t.Errorf("guest = %+v", f)
	}
	if f := DefaultFolders(Admin, avail); len(f) != 0 {
		t.Errorf("admin = %+v", f)
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
