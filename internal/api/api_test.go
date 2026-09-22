package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/disks"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/shortcuts"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/sysexec"
)

func testServer(t *testing.T) http.Handler {
	t.Helper()
	lsblk, err := os.ReadFile("../disks/testdata/omarchy_lsblk.json")
	if err != nil {
		t.Fatal(err)
	}
	findmnt, err := os.ReadFile("../disks/testdata/omarchy_findmnt.json")
	if err != nil {
		t.Fatal(err)
	}
	fake := &sysexec.Fake{
		Installed: map[string]bool{"lsblk": true, "findmnt": true},
		Outputs: map[string][]byte{
			"lsblk -J -b -o NAME,KNAME,PATH,PKNAME,TYPE,SIZE,MODEL,SERIAL,VENDOR,FSTYPE,UUID,LABEL,PARTLABEL,MOUNTPOINTS,FSSIZE,FSUSED,FSAVAIL,RM,RO,ROTA,TRAN,HOTPLUG": lsblk,
			"findmnt -J -o TARGET,SOURCE,FSTYPE": findmnt,
		},
	}
	cfg := config.Default()
	cfg.VaultRoot = t.TempDir() + "/vault"
	s := &Server{
		Config:    cfg,
		Disks:     &disks.Scanner{Run: fake},
		Run:       fake,
		Shortcuts: shortcuts.Inspector{ConfigPath: "../shortcuts/testdata/hypr/hyprland.conf"},
		Static: fstest.MapFS{
			"index.html":        {Data: []byte("<!doctype html><title>Vault</title>")},
			"assets/app-abc.js": {Data: []byte("console.log(1)")},
		},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Started: time.Now(),
	}
	return s.Handler()
}

func do(h http.Handler, method, target string, mod func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Host = "127.0.0.1:8788"
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestStatus(t *testing.T) {
	h := testServer(t)
	w := do(h, "GET", "/api/status", nil)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var resp StatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Name != "Omarchy Vault" || resp.Drives == nil || resp.Drives.System != 1 || !resp.SystemDisk {
		t.Errorf("unexpected status: %+v", resp)
	}
	if resp.SetupComplete {
		t.Error("setup should not be complete in milestone 1")
	}
	if len(resp.Shortcuts) != 3 {
		t.Errorf("shortcuts = %+v", resp.Shortcuts)
	}
	// The v1 alias serves the same endpoint.
	if w := do(h, "GET", "/api/v1/status", nil); w.Code != 200 {
		t.Errorf("v1 alias status %d", w.Code)
	}
}

func TestDisksAndStorage(t *testing.T) {
	h := testServer(t)
	w := do(h, "GET", "/api/disks", nil)
	var inv disks.Inventory
	if err := json.Unmarshal(w.Body.Bytes(), &inv); err != nil || w.Code != 200 {
		t.Fatalf("disks: %d %v", w.Code, err)
	}
	if inv.Disks[0].Status != disks.StatusSystem || !inv.Disks[0].Protected {
		t.Errorf("first disk should be protected system disk: %+v", inv.Disks[0])
	}

	w = do(h, "GET", "/api/storage", nil)
	var st StorageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Configured || len(st.Candidates) != 2 {
		t.Errorf("storage = %+v", st)
	}
	for _, c := range st.Candidates {
		if c.Disk == "nvme0n1" {
			t.Error("system disk offered as a candidate")
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := testServer(t)
	for _, p := range []string{"/api/status", "/", "/storage"} {
		w := do(h, "GET", p, nil)
		for _, hdr := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
			if w.Header().Get(hdr) == "" {
				t.Errorf("%s missing %s", p, hdr)
			}
		}
	}
	if cc := do(h, "GET", "/api/disks", nil).Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("api cache-control = %q", cc)
	}
}

func TestHostGuardBlocksDNSRebinding(t *testing.T) {
	h := testServer(t)
	w := do(h, "GET", "/api/disks", func(r *http.Request) { r.Host = "attacker.example:8788" })
	if w.Code != http.StatusMisdirectedRequest {
		t.Fatalf("foreign host got %d", w.Code)
	}
	for _, host := range []string{"localhost:8788", "[::1]:8788", "127.0.0.1"} {
		if w := do(h, "GET", "/api/status", func(r *http.Request) { r.Host = host }); w.Code != 200 {
			t.Errorf("host %s got %d", host, w.Code)
		}
	}
}

func TestCrossSiteWritesRefused(t *testing.T) {
	h := testServer(t)
	w := do(h, "POST", "/api/upload-session", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") })
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST got %d", w.Code)
	}
	w = do(h, "POST", "/api/upload-session", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") })
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-site POST got %d", w.Code)
	}
}

func TestPlannedEndpointsAre501(t *testing.T) {
	h := testServer(t)
	cases := map[string]string{
		"/api/upload-session":           "POST",
		"/api/download-session":         "POST",
		"/api/share":                    "POST",
		"/api/share/abc":                "DELETE",
		"/api/remote":                   "POST",
		"/api/v1/beam/upload-session":   "POST",
		"/api/v1/beam/download-session": "POST",
		"/api/v1/beam/share":            "POST",
	}
	for p, m := range cases {
		w := do(h, m, p, func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:8788") })
		if w.Code != http.StatusNotImplemented {
			t.Errorf("%s %s = %d", m, p, w.Code)
			continue
		}
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["milestone"] == nil {
			t.Errorf("%s %s missing milestone", m, p)
		}
	}
}

func TestMethodAndNotFound(t *testing.T) {
	h := testServer(t)
	if w := do(h, "DELETE", "/api/disks", nil); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE /api/disks = %d", w.Code)
	}
	if w := do(h, "GET", "/api/nope", nil); w.Code != 404 || !strings.Contains(w.Header().Get("Content-Type"), "json") {
		t.Errorf("unknown api = %d %s", w.Code, w.Header().Get("Content-Type"))
	}
}

func TestStaticSPA(t *testing.T) {
	h := testServer(t)
	w := do(h, "GET", "/storage", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<title>Vault") {
		t.Errorf("SPA fallback failed: %d", w.Code)
	}
	w = do(h, "GET", "/assets/app-abc.js", nil)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("asset: %d %q", w.Code, w.Header().Get("Cache-Control"))
	}
	w = do(h, "GET", "/../../etc/passwd", nil)
	if strings.Contains(w.Body.String(), "root:") {
		t.Fatal("path traversal served a system file")
	}
}

func TestRateLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := newRateLimiter(1, 3)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if !l.allow("a") {
			t.Fatalf("request %d should pass", i)
		}
	}
	if l.allow("a") {
		t.Fatal("burst exceeded but allowed")
	}
	if !l.allow("b") {
		t.Fatal("other client should be independent")
	}
	now = now.Add(time.Second)
	if !l.allow("a") {
		t.Fatal("token should refill")
	}
}

func TestRedactPath(t *testing.T) {
	if got := redactPath("/u/SECRET-TOKEN"); strings.Contains(got, "SECRET") {
		t.Errorf("token leaked: %s", got)
	}
}
