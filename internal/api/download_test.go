package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vaultWithFiles adopts the test drive and puts a few files in the Vault.
func vaultWithFiles(t *testing.T, e *env, admin map[string]string) string {
	t.Helper()
	if w := e.req("POST", "/api/pool", map[string]any{"volume": "sda1"}, admin); w.Code != 200 {
		t.Fatalf("adopt = %d %s", w.Code, w.Body)
	}
	data := filepath.Join(e.mount, "Vault")
	os.MkdirAll(filepath.Join(data, "Photos", "2024"), 0o755)
	os.WriteFile(filepath.Join(data, "Photos", "2024", "beach.jpg"), []byte("sand"), 0o644)
	os.WriteFile(filepath.Join(data, "Documents", "tax.pdf"), []byte("numbers"), 0o644)
	os.WriteFile(filepath.Join(data, "Photos", ".hidden"), []byte("x"), 0o644)
	os.Symlink("/etc", filepath.Join(data, "Photos", "etc"))
	return data
}

func TestBrowse(t *testing.T) {
	e := newEnv(t)
	admin := bootstrap(t, e)
	vaultWithFiles(t, e, admin)
	e.req("POST", "/api/users", map[string]any{"username": "ann", "password": "family-pass-123", "role": "family",
		"folders": []map[string]string{{"name": "Photos", "access": "ro"}}}, admin)
	ann := session(t, e.login(t, "ann", "family-pass-123", ""))

	var top struct{ Entries []BrowseEntry }
	json.Unmarshal(e.req("GET", "/api/browse", nil, ann).Body.Bytes(), &top)
	if len(top.Entries) != 1 || top.Entries[0].Name != "Photos" {
		t.Fatalf("ann's top level = %+v", top.Entries)
	}
	var photos struct{ Entries []BrowseEntry }
	json.Unmarshal(e.req("GET", "/api/browse?path=Photos", nil, ann).Body.Bytes(), &photos)
	if len(photos.Entries) != 1 || photos.Entries[0].Path != "Photos/2024" || !photos.Entries[0].Dir {
		t.Fatalf("Photos = %+v (no hidden files, no symlinks)", photos.Entries)
	}
	for _, p := range []string{"Documents", "Photos/etc", "Photos/../Documents", "/etc"} {
		if w := e.req("GET", "/api/browse?path="+url.QueryEscape(p), nil, ann); w.Code != http.StatusNotFound {
			t.Errorf("browse %q = %d", p, w.Code)
		}
	}
	json.Unmarshal(e.req("GET", "/api/browse", nil, admin).Body.Bytes(), &top)
	if len(top.Entries) < 5 {
		t.Errorf("admin top level = %+v", top.Entries)
	}
}

func TestDownloadLinkEndToEnd(t *testing.T) {
	e := newEnv(t)
	admin := bootstrap(t, e)
	vaultWithFiles(t, e, admin)

	w := e.req("POST", "/api/download-session", map[string]any{"path": "Photos/2024/beach.jpg"}, admin)
	var link LinkView
	json.Unmarshal(w.Body.Bytes(), &link)
	if w.Code != 200 || !strings.Contains(link.URL, "/d/") || link.MaxFiles != 1 || link.QRSVG == "" {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), e.mount) {
		t.Error("response exposes the local path")
	}
	resp, err := http.Get(link.URL + "/file")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(b) != "sand" {
		t.Fatalf("phone download = %d %q", resp.StatusCode, b)
	}
	json.Unmarshal(e.req("GET", "/api/download-session/"+link.ID, nil, admin).Body.Bytes(), &link)
	if link.State != "full" || len(link.Received) != 1 {
		t.Errorf("after download: %s %+v", link.State, link.Received)
	}
	// Used up: the listener closes on the next idle check.
	e.srv.Transfer.StopIfIdle(t.Context())
	if e.srv.Transfer.Running() != "" {
		t.Error("listener still open with no usable link")
	}
	// Kinds don't mix.
	if w := e.req("GET", "/api/upload-session/"+link.ID, nil, admin); w.Code != http.StatusNotFound {
		t.Errorf("download link via upload endpoint = %d", w.Code)
	}
	for _, bad := range []string{"", "../etc/passwd", "Photos/etc/passwd", "Nope/x.jpg"} {
		if w := e.req("POST", "/api/download-session", map[string]any{"path": bad}, admin); w.Code < 400 {
			t.Errorf("path %q = %d", bad, w.Code)
		}
	}
}

func TestSharePermissionsAndOptions(t *testing.T) {
	e := newEnv(t)
	admin := bootstrap(t, e)
	vaultWithFiles(t, e, admin)
	e.req("POST", "/api/users", map[string]any{"username": "ann", "password": "family-pass-123", "role": "family",
		"folders": []map[string]string{{"name": "Photos", "access": "ro"}}}, admin)
	e.req("POST", "/api/users", map[string]any{"username": "gus", "password": "guest-pass-1234", "role": "guest",
		"folders": []map[string]string{{"name": "Photos", "access": "ro"}}}, admin)
	ann := session(t, e.login(t, "ann", "family-pass-123", ""))
	gus := session(t, e.login(t, "gus", "guest-pass-1234", ""))

	if w := e.req("POST", "/api/share", map[string]any{"path": "Photos/2024", "minutes": 60, "password": "short"}, ann); w.Code != http.StatusBadRequest {
		t.Errorf("weak share password = %d", w.Code)
	}
	w := e.req("POST", "/api/share", map[string]any{"path": "Photos/2024", "minutes": 60, "max_downloads": 3, "password": "a good password"}, ann)
	var link LinkView
	json.Unmarshal(w.Body.Bytes(), &link)
	if w.Code != 200 || !link.HasPassword || link.MaxFiles != 3 || !strings.Contains(link.URL, "/s/") {
		t.Fatalf("share = %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "argon2") {
		t.Fatal("password hash leaked")
	}
	if w := e.req("POST", "/api/share", map[string]any{"path": "Documents/tax.pdf"}, ann); w.Code != http.StatusNotFound {
		t.Errorf("share outside her folders = %d", w.Code)
	}
	if w := e.req("POST", "/api/share", map[string]any{"path": "Photos/2024"}, gus); w.Code != http.StatusForbidden {
		t.Errorf("guest share = %d", w.Code)
	}
	if w := e.req("POST", "/api/download-session", map[string]any{"path": "Photos/2024/beach.jpg"}, gus); w.Code != 200 {
		t.Errorf("guest download to own phone = %d %s", w.Code, w.Body)
	}
	// Unlimited by default; the admin sees ann's share, gus doesn't.
	w = e.req("POST", "/api/share", map[string]any{"path": "Documents/tax.pdf"}, admin)
	json.Unmarshal(w.Body.Bytes(), &link)
	if link.MaxFiles < 1<<30 || link.HasPassword {
		t.Errorf("default share = %+v", link.Session)
	}
	var all, guest struct{ Links []LinkView }
	json.Unmarshal(e.req("GET", "/api/shares", nil, admin).Body.Bytes(), &all)
	json.Unmarshal(e.req("GET", "/api/shares", nil, gus).Body.Bytes(), &guest)
	if len(all.Links) != 2 || len(guest.Links) != 0 {
		t.Errorf("admin sees %d, guest sees %d", len(all.Links), len(guest.Links))
	}
	if w := e.req("DELETE", "/api/share/"+link.ID, nil, admin); w.Code != 200 {
		t.Errorf("revoke = %d", w.Code)
	}
	if resp, err := http.Get(link.URL); err == nil {
		if resp.StatusCode != http.StatusGone {
			t.Errorf("revoked share = %d", resp.StatusCode)
		}
		resp.Body.Close()
	}
	// Beam: local token only.
	if w := e.req("POST", "/api/v1/beam/share", map[string]any{"path": "Photos/2024", "client": "beam"}, tokenHdr); w.Code != 200 {
		t.Errorf("beam share = %d %s", w.Code, w.Body)
	}
	if w := e.req("POST", "/api/v1/beam/download-session", map[string]any{"path": "Photos"}, ann); w.Code != http.StatusForbidden {
		t.Errorf("beam as family = %d", w.Code)
	}
}

func TestLocalFilesNeedTheLocalToken(t *testing.T) {
	e := newEnv(t)
	admin := bootstrap(t, e)
	home := t.TempDir()
	t.Setenv("HOME", home)
	doc := filepath.Join(home, "Documents", "notes.txt")
	os.MkdirAll(filepath.Dir(doc), 0o755)
	os.WriteFile(doc, []byte("hello"), 0o644)
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(home, ".ssh", "id_rsa"), []byte("key"), 0o600)

	body := map[string]any{"local_paths": []string{doc}}
	// A browser session, even an admin's, can't send files outside the Vault.
	if w := e.req("POST", "/api/download-session", body, admin); w.Code != http.StatusForbidden {
		t.Fatalf("admin session = %d %s", w.Code, w.Body)
	}
	// The owner's local token (vaultctl) can, and nothing needs the Vault.
	w := e.req("POST", "/api/download-session", body, tokenHdr)
	var link LinkView
	json.Unmarshal(w.Body.Bytes(), &link)
	if w.Code != 200 || !strings.Contains(link.URL, "/d/") {
		t.Fatalf("local token = %d %s", w.Code, w.Body)
	}
	resp, err := http.Get(link.URL + "/file")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "hello" {
		t.Errorf("phone got %q", b)
	}
	for _, p := range []string{filepath.Join(home, ".ssh", "id_rsa"), home, "/etc/passwd"} {
		if w := e.req("POST", "/api/download-session", map[string]any{"local_paths": []string{p}}, tokenHdr); w.Code != http.StatusForbidden {
			t.Errorf("%s = %d", p, w.Code)
		}
	}
}
