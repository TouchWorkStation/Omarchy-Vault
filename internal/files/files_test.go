package files

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/users"
)

func TestFolderObjectName(t *testing.T) {
	re := regexp.MustCompile(`^[a-zA-Z0-9-_.~]+$`)
	a, b := FolderObjectName("Phone Uploads"), FolderObjectName("phone-uploads")
	if !re.MatchString(a) || !strings.HasPrefix(a, "vault-phone-uploads-") {
		t.Errorf("name = %q", a)
	}
	if a == b {
		t.Error("different folders must not collide")
	}
}

func TestDesired(t *testing.T) {
	admin, _ := Desired(users.User{Username: "chris", Role: users.Admin, PasswordHash: "$argon2id$h"}, "files-only-pw", "/data/current", "/homes")
	if admin.Password != "files-only-pw" {
		t.Errorf("SFTPGo must get the files-only password, not the Vault hash: %q", admin.Password)
	}
	if admin.HomeDir != "/data/current" || !slices.Equal(admin.Permissions["/"], permReadWrite) || len(admin.VirtualFolders) != 0 {
		t.Errorf("admin = %+v", admin)
	}
	for _, p := range admin.Permissions["/"] {
		if p == "*" || p == "create_symlinks" || p == "chmod" || p == "chown" {
			t.Errorf("admin has dangerous permission %s", p)
		}
	}

	fam, folders := Desired(users.User{Username: "ann", Role: users.Family, PasswordHash: "h", Disabled: true,
		Folders: []users.Folder{{Name: "Photos", Access: users.ReadWrite}, {Name: "Documents", Access: users.ReadOnly}}}, "pw", "/data/current", "/homes")
	if fam.HomeDir != "/homes/ann" || fam.Status != 0 {
		t.Errorf("family home/status = %s %d", fam.HomeDir, fam.Status)
	}
	if !slices.Equal(fam.Permissions["/"], permListOnly) || !slices.Equal(fam.Permissions["/Photos"], permReadWrite) || !slices.Equal(fam.Permissions["/Documents"], permReadOnly) {
		t.Errorf("family perms = %v", fam.Permissions)
	}
	if len(folders) != 2 || folders[0].MappedPath != "/data/current/Photos" {
		t.Errorf("folders = %+v", folders)
	}
	if !slices.Contains(fam.Filters.DeniedProtocols, "SSH") || !slices.Contains(fam.Filters.WebClient, "shares-disabled") {
		t.Errorf("filters = %+v", fam.Filters)
	}

	guest, _ := Desired(users.User{Username: "gus", Role: users.Guest, PasswordHash: "h",
		Folders: []users.Folder{{Name: "Shared", Access: users.ReadWrite}}}, "pw", "/data/current", "/homes")
	if !slices.Equal(guest.Permissions["/Shared"], permReadOnly) || !slices.Contains(guest.Filters.WebClient, "write-disabled") {
		t.Errorf("guest must stay read-only: %+v", guest)
	}
}

func TestUserPassword(t *testing.T) {
	if _, err := (&Client{}).UserPassword("ann"); err == nil {
		t.Fatal("a client without a key must not derive passwords")
	}
	c := &Client{UserKey: []byte(strings.Repeat("k", 43))}
	a1, _ := c.UserPassword("ann")
	a2, _ := c.UserPassword("ann")
	b, _ := c.UserPassword("bob")
	other, _ := (&Client{UserKey: []byte(strings.Repeat("x", 43))}).UserPassword("ann")
	if a1 != a2 || a1 == b || a1 == other || len(a1) != 64 {
		t.Errorf("passwords: %q %q %q %q", a1, a2, b, other)
	}
}

// TestIntegration runs against a real SFTPGo. Set VAULT_TEST_SFTPGO to the
// binary and VAULT_TEST_SFTPGO_ASSETS to a directory with templates/ and
// static/ (e.g. the Go module cache copy).
func TestIntegration(t *testing.T) {
	bin := os.Getenv("VAULT_TEST_SFTPGO")
	assets := os.Getenv("VAULT_TEST_SFTPGO_ASSETS")
	if bin == "" || assets == "" {
		t.Skip("VAULT_TEST_SFTPGO not set")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "vault")
	for _, f := range []string{"Photos", "Documents", "Shared"} {
		os.MkdirAll(filepath.Join(root, f), 0o755)
	}
	os.WriteFile(filepath.Join(root, "Documents", "tax.pdf"), []byte("secret"), 0o644)
	os.WriteFile(filepath.Join(root, "Shared", "readme.txt"), []byte("hello"), 0o644)

	p := Paths{Binary: bin, Assets: assets, ConfigDir: filepath.Join(dir, "cfg"), DataDir: filepath.Join(dir, "data"),
		HomesDir: filepath.Join(dir, "homes"), SecretsDir: filepath.Join(dir, "secrets")}
	m := NewManager(p, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ready := make(chan struct{}, 1)
	m.OnReady = func(context.Context) error { ready <- struct{}{}; return nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	if st := m.Status(); st.State != StateWaiting {
		t.Fatalf("before storage: %s", st.State)
	}
	m.SetWanted(true)
	select {
	case <-ready:
	case <-time.After(60 * time.Second):
		t.Fatalf("SFTPGo did not start: %+v", m.Status())
	}
	c := m.Client()

	hash := func(pw string) string { h, _ := auth.HashPassword(pw); return h }
	list := []users.User{
		{Username: "chris", Role: users.Admin, PasswordHash: hash("admin-pass-123")},
		{Username: "ann", Role: users.Family, PasswordHash: hash("family-pass-123"), Folders: []users.Folder{{Name: "Photos", Access: users.ReadWrite}}},
		{Username: "gus", Role: users.Guest, PasswordHash: hash("guest-pass-123"), Folders: []users.Folder{{Name: "Shared", Access: users.ReadOnly}}},
	}
	// An account synced by an older Vault, which sent the Vault hash.
	old, _ := Desired(list[0], list[0].PasswordHash, root, p.HomesDir)
	if err := c.putUser(ctx, old, false); err != nil {
		t.Fatal(err)
	}
	if tok := userToken(t, "chris", "admin-pass-123"); tok == "" {
		t.Fatal("setup: old-style account should accept the Vault password")
	}
	if err := Sync(ctx, c, list, root, p.HomesDir); err != nil {
		t.Fatal(err)
	}
	// Syncing replaces it, so the Vault password stops working there.
	if tok := userToken(t, "chris", "admin-pass-123"); tok != "" {
		t.Fatal("old Vault password still accepted after sync")
	}

	pw := func(name string) string { p, _ := c.UserPassword(name); return p }

	// Vault signs users in with their files-only password.
	if ck, err := c.WebLogin(ctx, "ann", pw("ann")); err != nil || ck.Path != WebRoot+"/web/client" || !ck.HttpOnly {
		t.Fatalf("web login: %v %+v", err, ck)
	}
	// The Vault password alone does not open SFTPGo directly, so 2FA and
	// lockout can't be skipped by going to 127.0.0.1:8789.
	if _, err := c.WebLogin(ctx, "ann", "family-pass-123"); err == nil {
		t.Fatal("Vault password accepted by SFTPGo web login")
	}
	if tok := userToken(t, "ann", "family-pass-123"); tok != "" {
		t.Fatal("Vault password accepted by SFTPGo REST API")
	}
	if _, err := c.WebLogin(ctx, "ann", "wrong-pass-123"); err == nil {
		t.Fatal("wrong password accepted")
	}

	// Family sees only their folders.
	if names := userDirs(t, "ann", pw("ann")); !slices.Equal(names, []string{"Photos"}) {
		t.Errorf("ann sees %v", names)
	}
	if names := userDirs(t, "chris", pw("chris")); !slices.Contains(names, "Documents") {
		t.Errorf("admin sees %v", names)
	}
	// Guest cannot upload into a read-only folder.
	if code := upload(t, "gus", pw("gus"), "/Shared"); code < 400 {
		t.Errorf("guest upload status %d", code)
	}
	if code := upload(t, "ann", pw("ann"), "/Photos"); code >= 300 {
		t.Errorf("family upload status %d", code)
	}

	// Disable ann, remove gus: sync reflects both.
	list[1].Disabled = true
	if err := Sync(ctx, c, list[:2], root, p.HomesDir); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WebLogin(ctx, "ann", pw("ann")); err == nil {
		t.Error("disabled user signed in")
	}
	if u, _ := c.getUser(ctx, "gus"); u != nil {
		t.Error("removed user still in SFTPGo")
	}
	// A hand-made SFTPGo user is never touched.
	c.putUser(ctx, sftpUser{Username: "manual", Password: "manual-pass-123", Status: 1, HomeDir: dir, Permissions: map[string][]string{"/": {"list"}}}, false)
	if err := Sync(ctx, c, list[:2], root, p.HomesDir); err != nil {
		t.Fatal(err)
	}
	if u, _ := c.getUser(ctx, "manual"); u == nil {
		t.Error("sync deleted a user Vault did not create")
	}

	// Stopping when storage goes away.
	m.SetWanted(false)
	deadline := time.Now().Add(15 * time.Second)
	for m.Status().Running && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if m.Status().Running {
		t.Error("SFTPGo kept running after storage went away")
	}
}

func userToken(t *testing.T, user, pass string) string {
	t.Helper()
	req, _ := http.NewRequest("GET", "http://127.0.0.1:8789/api/v2/user/token", nil)
	req.SetBasicAuth(user, pass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var tr struct {
		AccessToken string `json:"access_token"`
	}
	json.NewDecoder(resp.Body).Decode(&tr)
	return tr.AccessToken
}

func userDirs(t *testing.T, user, pass string) []string {
	t.Helper()
	req, _ := http.NewRequest("GET", "http://127.0.0.1:8789/api/v2/user/dirs?path=/", nil)
	req.Header.Set("Authorization", "Bearer "+userToken(t, user, pass))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var entries []struct{ Name string }
	json.NewDecoder(resp.Body).Decode(&entries)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	slices.Sort(names)
	return names
}

func upload(t *testing.T, user, pass, dir string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", "http://127.0.0.1:8789/api/v2/user/files/upload?path="+dir+"/test.txt", strings.NewReader("data"))
	req.Header.Set("Authorization", "Bearer "+userToken(t, user, pass))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}
