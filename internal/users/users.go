// Package users stores Vault accounts.
//
// Accounts live in ~/.config/omarchy-vault/users.json (0600). Each holds an
// argon2id password hash (never the password), a role, the folders the user
// may open, and optional TOTP 2FA. Vault mirrors every account into SFTPGo
// (see internal/files) so the same username and password open Files.
package users

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
)

// Role decides what a user can do.
type Role string

const (
	// Admin manages Vault (storage, users, settings) and sees every file.
	Admin Role = "admin"
	// Family reads and writes the folders they are given.
	Family Role = "family"
	// Guest can only read and download the folders they are given.
	Guest Role = "guest"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == Admin || r == Family || r == Guest }

// Access is a folder permission.
type Access string

const (
	ReadWrite Access = "rw"
	ReadOnly  Access = "ro"
)

// Folder grants access to one top-level Vault folder.
type Folder struct {
	Name   string `json:"name"`
	Access Access `json:"access"`
}

// User is a stored account.
type User struct {
	Username     string   `json:"username"`
	Role         Role     `json:"role"`
	PasswordHash string   `json:"password_hash"`
	Disabled     bool     `json:"disabled,omitempty"`
	Folders      []Folder `json:"folders,omitempty"`
	TOTPSecret   string   `json:"totp_secret,omitempty"`
	TOTPEnabled  bool     `json:"totp_enabled,omitempty"`
	TOTPLast     uint64   `json:"totp_last,omitempty"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

// View is the public shape of a user: no hash, no secret.
type View struct {
	Username    string   `json:"username"`
	Role        Role     `json:"role"`
	Disabled    bool     `json:"disabled"`
	Folders     []Folder `json:"folders"`
	AllFolders  bool     `json:"all_folders"`
	TOTPEnabled bool     `json:"totp_enabled"`
	CreatedAt   string   `json:"created_at"`
}

// View returns the public shape of u.
func (u User) View() View {
	f := u.Folders
	if f == nil {
		f = []Folder{}
	}
	return View{Username: u.Username, Role: u.Role, Disabled: u.Disabled, Folders: f,
		AllFolders: u.Role == Admin, TOTPEnabled: u.TOTPEnabled, CreatedAt: u.CreatedAt}
}

// Errors returned by the store.
var (
	ErrNotFound  = errors.New("no such user")
	ErrExists    = errors.New("that username is taken")
	ErrLastAdmin = errors.New("Vault needs at least one active admin")
)

var usernameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)

// reserved names clash with Vault's own identities or SFTPGo internals.
var reserved = map[string]bool{"local": true, "root": true, "vault": true, "vault-admin": true, "system": true}

// ValidateUsername enforces lowercase names SFTPGo also accepts.
func ValidateUsername(name string) error {
	if !usernameRe.MatchString(name) {
		return errors.New("use 2–32 characters: lowercase letters, digits, - or _, starting with a letter")
	}
	if reserved[name] {
		return fmt.Errorf("%q is reserved", name)
	}
	return nil
}

// ValidateFolders checks folder grants: plain top-level names, known access.
func ValidateFolders(fs []Folder) error {
	seen := map[string]bool{}
	for _, f := range fs {
		if f.Name == "" || strings.Contains(f.Name, "/") || strings.HasPrefix(f.Name, ".") {
			return fmt.Errorf("folder %q is not a top-level Vault folder", f.Name)
		}
		if err := config.ValidateFolder(f.Name); err != nil {
			return err
		}
		if f.Access != ReadWrite && f.Access != ReadOnly {
			return fmt.Errorf("folder %q: access must be rw or ro", f.Name)
		}
		if seen[f.Name] {
			return fmt.Errorf("folder %q listed twice", f.Name)
		}
		seen[f.Name] = true
	}
	return nil
}

// Store is a file-backed, concurrency-safe user list.
type Store struct {
	path string
	now  func() time.Time

	mu    sync.RWMutex
	users []User
}

type fileFormat struct {
	Version int    `json:"version"`
	Users   []User `json:"users"`
}

// Open loads path, or starts empty if it does not exist.
func Open(path string) (*Store, error) {
	s := &Store{path: path, now: time.Now}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("users: %w", err)
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("users: tighten %s: %w", path, err)
		}
	}
	var f fileFormat
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("users: parse %s: %w", path, err)
	}
	if f.Version != 1 {
		return nil, fmt.Errorf("users: unsupported version %d", f.Version)
	}
	s.users = f.Users
	return s, nil
}

func (s *Store) save(list []User) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(fileFormat{Version: 1, Users: list}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".users-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// List returns every user sorted by name.
func (s *Store) List() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]User(nil), s.users...)
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

// Count returns the number of users.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users)
}

// Get returns one user.
func (s *Store) Get(name string) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.Username == name {
			return u, true
		}
	}
	return User{}, false
}

func activeAdmins(list []User) int {
	n := 0
	for _, u := range list {
		if u.Role == Admin && !u.Disabled {
			n++
		}
	}
	return n
}

// Create adds u. PasswordHash must already be set.
func (s *Store) Create(u User) error {
	if err := ValidateUsername(u.Username); err != nil {
		return err
	}
	if !u.Role.Valid() {
		return errors.New("choose a role: admin, family or guest")
	}
	if err := ValidateFolders(u.Folders); err != nil {
		return err
	}
	if u.PasswordHash == "" {
		return errors.New("a password is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.users {
		if x.Username == u.Username {
			return ErrExists
		}
	}
	ts := s.now().UTC().Format(time.RFC3339)
	u.CreatedAt, u.UpdatedAt = ts, ts
	if u.Role == Admin {
		u.Folders = nil // admins see everything
	}
	next := append(append([]User(nil), s.users...), u)
	if err := s.save(next); err != nil {
		return err
	}
	s.users = next
	return nil
}

// Update applies fn to a copy of the user and saves it if valid. It
// refuses changes that would leave Vault without an active admin.
func (s *Store) Update(name string, fn func(u *User) error) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, u := range s.users {
		if u.Username == name {
			idx = i
		}
	}
	if idx < 0 {
		return User{}, ErrNotFound
	}
	next := append([]User(nil), s.users...)
	u := next[idx]
	if err := fn(&u); err != nil {
		return User{}, err
	}
	if u.Username != name {
		return User{}, errors.New("usernames cannot be changed")
	}
	if !u.Role.Valid() {
		return User{}, errors.New("choose a role: admin, family or guest")
	}
	if err := ValidateFolders(u.Folders); err != nil {
		return User{}, err
	}
	if u.Role == Admin {
		u.Folders = nil
	}
	u.UpdatedAt = s.now().UTC().Format(time.RFC3339)
	next[idx] = u
	if activeAdmins(s.users) > 0 && activeAdmins(next) == 0 {
		return User{}, ErrLastAdmin
	}
	if err := s.save(next); err != nil {
		return User{}, err
	}
	s.users = next
	return u, nil
}

// Delete removes a user (never their files).
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]User, 0, len(s.users))
	found := false
	for _, u := range s.users {
		if u.Username == name {
			found = true
			continue
		}
		next = append(next, u)
	}
	if !found {
		return ErrNotFound
	}
	if activeAdmins(s.users) > 0 && activeAdmins(next) == 0 {
		return ErrLastAdmin
	}
	if err := s.save(next); err != nil {
		return err
	}
	s.users = next
	return nil
}

// DefaultFolders suggests grants for a new user of role r given the
// Vault's top-level folders.
func DefaultFolders(r Role, available []string) []Folder {
	var out []Folder
	for _, name := range available {
		switch r {
		case Family:
			out = append(out, Folder{Name: name, Access: ReadWrite})
		case Guest:
			if name == "Shared" {
				out = append(out, Folder{Name: name, Access: ReadOnly})
			}
		}
	}
	return out
}
