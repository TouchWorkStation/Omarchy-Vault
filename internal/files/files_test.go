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
	admin, _ := Desired(users.User{Username: "chris", Role: users.Admin, PasswordHash: "$argon2id$h"}, "/data/current", "/homes")
	if admin.HomeDir != "/data/current" || !slices.Equal(admin.Permissions["/"], permReadWrite) || len(admin.VirtualFolders) != 0 {
		t.Errorf("admin = %+v", admin)
	}
	for _, p := range admin.Permissions["/"] {
		if p == "*" || p == "create_symlinks" || p == "chmod" || p == "chown" {
			t.Errorf("admin has dangerous permission %s", p)
		}
	}

	fam, folders := Desired(users.User{Username: "ann", Role: users.Family, PasswordHash: "h", Disabled: true,
		Folders: []users.Folder{{Name: "Photos", Access: users.ReadWrite}, {Name: "Documents", Access: users.ReadOnly}}}, "/data/current", "/homes")
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
		Folders: []users.Folder{{Name: "Shared", Access: users.ReadWrite}}}, "/data/current", "/homes")
	if !slices.Equal(guest.Permissions["/Shared"], permReadOnly) || !slices.Contains(guest.Filters.WebClient, "write-disabled") {
		t.Errorf("guest must stay read-only: %+v", guest)
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
	if err := Sync(ctx, c, list, root, p.HomesDir); err != nil {
		t.Fatal(err)
	}

	// SFTPGo accepts Vault's argon2id hash: web SSO works, wrong passwords fail.
	if ck, err := c.WebLogin(ctx, "ann", "family-pass-123"); err != nil || ck.Path != WebRoot+"/web/client" || !ck.HttpOnly {
		t.Fatalf("web login: %v %+v", err, ck)
	}
	if _, err := c.WebLogin(ctx, "ann", "wrong-pass-123"); err == nil {
		t.Fatal("wrong password accepted")
	}

	// Family sees only their folders.
	if names := userDirs(t, "ann", "family-pass-123"); !slices.Equal(names, []string{"Photos"}) {
		t.Errorf("ann sees %v", names)
	}
	if names := userDirs(t, "chris", "admin-pass-123"); !slices.Contains(names, "Documents") {
		t.Errorf("admin sees %v", names)
	}
	// Guest cannot upload into a read-only folder.
	if code := upload(t, "gus", "guest-pass-123", "/Shared"); code < 400 {
		t.Errorf("guest upload status %d", code)
	}
	if code := upload(t, "ann", "family-pass-123", "/Photos"); code >= 300 {
		t.Errorf("family upload status %d", code)
	}

	// Disable ann, remove gus: sync reflects both.
	list[1].Disabled = true
	if err := Sync(ctx, c, list[:2], root, p.HomesDir); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WebLogin(ctx, "ann", "family-pass-123"); err == nil {
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
