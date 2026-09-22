package transfer

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
)

func vaultTree(t *testing.T, dir string) {
	t.Helper()
	mk := func(p, content string) {
		full := filepath.Join(dir, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(content), 0o644)
	}
	mk("Photos/2024/beach.jpg", "sand and sea")
	mk("Photos/2024/Trip Day 2/sunset é.jpg", "orange")
	mk("Photos/2024/.vault-partial-abc", "unfinished")
	mk("Documents/tax.pdf", "secret numbers")
	os.Symlink(filepath.Join(dir, "Documents"), filepath.Join(dir, "Photos", "2024", "sneaky"))
	os.Symlink("/etc/passwd", filepath.Join(dir, "Photos", "passwd"))
}

func get(h http.Handler, target string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	req.RemoteAddr = "192.168.1.50:5555"
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestCleanRelAndJoin(t *testing.T) {
	for _, bad := range []string{"", "/", "..", "a/../b", "a//b", "./a", "a/\x00", "\xff"} {
		if _, err := CleanRel(bad); err == nil {
			t.Errorf("CleanRel(%q) accepted", bad)
		}
	}
	if got, err := CleanRel("/Photos/2024/"); err != nil || got != "Photos/2024" {
		t.Errorf("CleanRel = %q %v", got, err)
	}
	if _, err := Join("Photos/2024", "../../Documents/tax.pdf"); err == nil {
		t.Error("Join escaped the shared folder")
	}
	if got, _ := Join("Photos/2024", "Trip Day 2/sunset é.jpg"); got != "Photos/2024/Trip Day 2/sunset é.jpg" {
		t.Errorf("Join = %q", got)
	}
}

func TestStatRefusesSymlinksAndPartials(t *testing.T) {
	dir := t.TempDir()
	vaultTree(t, dir)
	root, _ := os.OpenRoot(dir)
	defer root.Close()
	if _, err := Stat(root, "Photos/2024/sneaky/tax.pdf"); err == nil {
		t.Error("followed a symlinked folder")
	}
	if _, err := Stat(root, "Photos/passwd"); err == nil {
		t.Error("followed a symlink out of the Vault")
	}
	if _, err := Stat(root, "Photos/2024/.vault-partial-abc"); err == nil {
		t.Error("offered an unfinished upload")
	}
	files, total, err := Walk(root, "Photos")
	if err != nil || len(files) != 2 || total != int64(len("sand and sea")+len("orange")) {
		t.Fatalf("walk = %+v %d %v", files, total, err)
	}
}

func TestDownloadLinkFileCountsPerDevice(t *testing.T) {
	s, dir := testServer(t)
	vaultTree(t, dir)
	ctx := context.Background()
	_, token, _ := s.Store.Create(ctx, NewSession{Kind: KindDownload, Folder: "Photos", Path: "Photos/2024/beach.jpg",
		CreatedBy: "chris", TTL: 10 * time.Minute, MaxFiles: 1, MaxBytes: NoByteLimit})
	h := s.Handler()

	w := get(h, "/d/"+token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "beach.jpg") || strings.Contains(w.Body.String(), dir) {
		t.Fatalf("page = %d %s", w.Code, w.Body)
	}
	// A wrong kind of token is not a download link.
	if w := get(h, "/s/"+token); w.Code != http.StatusGone {
		t.Errorf("download token as share = %d", w.Code)
	}

	// The phone probes with a small range, then downloads: one count.
	w = get(h, "/d/"+token+"/file", "Range", "bytes=0-1")
	if w.Code != http.StatusPartialContent || w.Body.String() != "sa" {
		t.Fatalf("range = %d %q", w.Code, w.Body)
	}
	w = get(h, "/d/"+token+"/file")
	if w.Code != 200 || w.Body.String() != "sand and sea" {
		t.Fatalf("file = %d", w.Code)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, "beach.jpg") {
		t.Errorf("disposition = %q", cd)
	}
	// Another device: the single download is used up.
	req := httptest.NewRequest("GET", "/d/"+token+"/file", nil)
	req.RemoteAddr = "192.168.1.77:1234"
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req)
	if w2.Code != http.StatusGone {
		t.Errorf("second device = %d", w2.Code)
	}
	if items, _ := s.Store.Recent(ctx, 10); len(items) != 1 || items[0].Kind != KindDownload {
		t.Errorf("activity = %+v", items)
	}
}

func TestDownloadLinkFolderZip(t *testing.T) {
	s, dir := testServer(t)
	vaultTree(t, dir)
	_, token, _ := s.Store.Create(context.Background(), NewSession{Kind: KindDownload, Folder: "Photos", Path: "Photos/2024",
		TTL: 10 * time.Minute, MaxFiles: 1, MaxBytes: NoByteLimit})
	h := s.Handler()
	w := get(h, "/d/"+token)
	if !strings.Contains(w.Body.String(), "2024.zip") || !strings.Contains(w.Body.String(), "2 files") {
		t.Errorf("folder page: %s", w.Body)
	}
	w = get(h, "/d/"+token+"/file")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("zip = %d", w.Code)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		if f.Name == "2024/beach.jpg" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			if string(b) != "sand and sea" {
				t.Errorf("zip content = %q", b)
			}
		}
	}
	if strings.Join(names, ",") != "2024/Trip Day 2/sunset é.jpg,2024/beach.jpg" {
		t.Errorf("zip names = %v (no symlinks, no partials, no tax.pdf)", names)
	}
}

func TestShareLinkWithPassword(t *testing.T) {
	s, dir := testServer(t)
	vaultTree(t, dir)
	hash, _ := auth.HashPassword("open sesame 1")
	_, token, _ := s.Store.Create(context.Background(), NewSession{Kind: KindShare, Folder: "Photos", Path: "Photos/2024",
		TTL: time.Hour, MaxFiles: Unlimited, MaxBytes: NoByteLimit, PasswordHash: hash})
	h := s.Handler()

	w := get(h, "/s/"+token)
	if !strings.Contains(w.Body.String(), "needs a password") || strings.Contains(w.Body.String(), "beach.jpg") {
		t.Fatalf("locked page shows files: %s", w.Body)
	}
	if w := get(h, "/s/"+token+"/file?p=beach.jpg"); w.Code != http.StatusSeeOther {
		t.Errorf("locked file = %d", w.Code)
	}
	post := func(pw string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/s/"+token+"/unlock", strings.NewReader(url.Values{"password": {pw}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "192.168.1.50:5555"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := post("wrong"); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong password = %d", w.Code)
	}
	w = post("open sesame 1")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("unlock = %d", w.Code)
	}
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || c.Path != "/s/"+token {
		t.Errorf("cookie = %+v", c)
	}
	cookie := c.Name + "=" + c.Value

	w = get(h, "/s/"+token, "Cookie", cookie)
	if !strings.Contains(w.Body.String(), "beach.jpg") || !strings.Contains(w.Body.String(), "Trip Day 2/sunset é.jpg") {
		t.Fatalf("unlocked page: %s", w.Body)
	}
	if w := get(h, "/s/"+token+"/file?p="+url.QueryEscape("Trip Day 2/sunset é.jpg"), "Cookie", cookie); w.Code != 200 || w.Body.String() != "orange" {
		t.Errorf("file = %d %q", w.Code, w.Body)
	}
	for _, p := range []string{"../../Documents/tax.pdf", "sneaky/tax.pdf", ".vault-partial-abc"} {
		if w := get(h, "/s/"+token+"/file?p="+url.QueryEscape(p), "Cookie", cookie); w.Code == 200 {
			t.Errorf("served %q", p)
		}
	}
	if w := get(h, "/s/"+token+"/zip", "Cookie", cookie); w.Code != 200 {
		t.Errorf("zip = %d", w.Code)
	}
	// The cookie of one share does not open another.
	_, other, _ := s.Store.Create(context.Background(), NewSession{Kind: KindShare, Folder: "Documents", Path: "Documents",
		TTL: time.Hour, MaxFiles: Unlimited, MaxBytes: NoByteLimit, PasswordHash: hash})
	if w := get(h, "/s/"+other, "Cookie", cookie); strings.Contains(w.Body.String(), "tax.pdf") {
		t.Error("grant reused across shares")
	}
}

func TestShareLinkLimitAndRevoke(t *testing.T) {
	s, dir := testServer(t)
	vaultTree(t, dir)
	ctx := context.Background()
	sess, token, _ := s.Store.Create(ctx, NewSession{Kind: KindShare, Folder: "Documents", Path: "Documents/tax.pdf",
		TTL: time.Hour, MaxFiles: 2, MaxBytes: NoByteLimit})
	h := s.Handler()
	for i, ip := range []string{"10.0.0.1:1", "10.0.0.2:1", "10.0.0.3:1"} {
		req := httptest.NewRequest("GET", "/s/"+token+"/file", nil)
		req.RemoteAddr = ip
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if want := map[bool]int{true: 200, false: http.StatusGone}[i < 2]; w.Code != want {
			t.Errorf("device %d = %d, want %d", i, w.Code, want)
		}
	}
	s.Store.Revoke(ctx, sess.ID)
	if w := get(h, "/s/"+token); w.Code != http.StatusGone || !strings.Contains(w.Body.String(), "stopped") {
		t.Errorf("revoked = %d", w.Code)
	}
}
