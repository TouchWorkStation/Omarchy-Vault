package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/disks"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/shortcuts"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/storage"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/sysexec"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/users"
)

const testToken = "test-token-0123456789abcdefghijklmnopqrstuvwxyz"

type env struct {
	h        http.Handler
	mount    string
	cfgPath  string
	link     string
	fake     *sysexec.Fake
	lsblkKey string
	users    *users.Store
}

// newEnv serves a machine with a system NVMe and a data drive "mounted" at
// a temp dir.
func newEnv(t *testing.T) *env {
	t.Helper()
	// Not t.TempDir(): /tmp is a location Vault refuses to use as storage.
	dir, err := os.MkdirTemp(".", ".testmnt-")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.Abs(dir)
	t.Cleanup(func() { os.RemoveAll(dir) })
	mount := filepath.Join(dir, "mnt", "wdred")
	os.MkdirAll(mount, 0o755)
	lsblk := `{"blockdevices":[
	 {"name":"nvme0n1","path":"/dev/nvme0n1","type":"disk","size":512000000000,"model":"System SSD","tran":"nvme","mountpoints":[null],
	  "children":[{"name":"nvme0n1p2","path":"/dev/nvme0n1p2","type":"part","size":500000000000,"fstype":"ext4","uuid":"sys","mountpoints":["/"]}]},
	 {"name":"sda","path":"/dev/sda","type":"disk","size":8000000000000,"model":"WD Red","tran":"sata","rota":true,"mountpoints":[null],
	  "children":[{"name":"sda1","path":"/dev/sda1","type":"part","size":8000000000000,"fstype":"ext4","uuid":"data-uuid","mountpoints":["` + mount + `"],"fssize":8000000000000,"fsavail":7000000000000}]}]}`
	key := "lsblk -J -b -o NAME,KNAME,PATH,PKNAME,TYPE,SIZE,MODEL,SERIAL,VENDOR,FSTYPE,UUID,LABEL,PARTLABEL,MOUNTPOINTS,FSSIZE,FSUSED,FSAVAIL,RM,RO,ROTA,TRAN,HOTPLUG"
	fake := &sysexec.Fake{
		Installed: map[string]bool{"lsblk": true, "findmnt": true},
		Outputs: map[string][]byte{
			key:                                  []byte(lsblk),
			"findmnt -J -o TARGET,SOURCE,FSTYPE": []byte(`{"filesystems":[{"target":"/","source":"/dev/nvme0n1p2","fstype":"ext4"}]}`),
		},
	}
	cfg := config.Default()
	cfg.VaultRoot = filepath.Join(dir, "srv-vault")
	e := &env{mount: mount, cfgPath: filepath.Join(dir, "cfg", "config.json"), link: filepath.Join(dir, "share", "current"), fake: fake, lsblkKey: key}
	us, err := users.Open(filepath.Join(dir, "cfg", "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	e.users = us
	s := &Server{
		Config:     cfg,
		ConfigPath: e.cfgPath,
		DataLink:   e.link,
		Mounts:     func() (storage.MountTable, error) { return storage.MountTable{"/", mount}, nil },
		Auth:       auth.NewLocal(testToken),
		Users:      e.users,
		Disks:      &disks.Scanner{Run: fake, MinRefresh: time.Nanosecond},
		Run:        fake,
		Shortcuts:  shortcuts.Inspector{},
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	e.h = s.Handler()
	return e
}

func (e *env) req(method, path string, body any, hdr map[string]string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, rd)
	r.Host = "127.0.0.1:8788"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

var tokenHdr = map[string]string{auth.HeaderToken: testToken}

func TestAdoptRequiresAuth(t *testing.T) {
	e := newEnv(t)
	body := map[string]any{"volume": "sda1"}
	if w := e.req("POST", "/api/pool", body, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no auth = %d", w.Code)
	}
	if w := e.req("POST", "/api/pool", body, map[string]string{auth.HeaderToken: "wrong"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token = %d", w.Code)
	}
	if _, err := os.Stat(e.cfgPath); err == nil {
		t.Fatal("config written without auth")
	}
}

func TestAdoptFlow(t *testing.T) {
	e := newEnv(t)

	w := e.req("POST", "/api/pool", map[string]any{"volume": "sda1"}, tokenHdr)
	if w.Code != 200 {
		t.Fatalf("adopt = %d %s", w.Code, w.Body)
	}
	var resp PoolResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Storage.State != storage.StateReady || resp.Result.DataDir != filepath.Join(e.mount, "Vault") {
		t.Fatalf("resp = %+v", resp)
	}
	if _, err := os.Stat(filepath.Join(e.mount, "Vault", "Phone Uploads")); err != nil {
		t.Error("default folders not created")
	}
	if target, _ := os.Readlink(e.link); target != filepath.Join(e.mount, "Vault") {
		t.Errorf("data link -> %q", target)
	}
	cfg, found, err := config.Load(e.cfgPath)
	if err != nil || !found || cfg.Pool.Mode != "single" || cfg.Sources[0].UUID != "data-uuid" {
		t.Fatalf("saved config = %+v %v", cfg, err)
	}

	// Status reflects it.
	var st StatusResponse
	json.Unmarshal(e.req("GET", "/api/status", nil, nil).Body.Bytes(), &st)
	if !st.SetupComplete || st.Storage.TotalBytes == 0 {
		t.Errorf("status storage = %+v", st.Storage)
	}

	// Switching requires explicit confirmation.
	if w := e.req("POST", "/api/pool", map[string]any{"volume": "sda1", "folder": "Other"}, tokenHdr); w.Code != http.StatusConflict {
		t.Errorf("switch without replace = %d", w.Code)
	}

	// Forget keeps every file.
	os.WriteFile(filepath.Join(e.mount, "Vault", "Photos", "a.jpg"), []byte("x"), 0o644)
	if w := e.req("DELETE", "/api/pool", nil, tokenHdr); w.Code != 200 {
		t.Fatalf("forget = %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(e.mount, "Vault", "Photos", "a.jpg")); err != nil {
		t.Fatal("forget deleted files")
	}
	if _, err := os.Lstat(e.link); err == nil {
		t.Error("data link not removed")
	}
	if cfg, _, _ := config.Load(e.cfgPath); cfg.Pool.Mode != "none" {
		t.Error("config not reset")
	}
}

func TestAdoptRefusesSystemDiskAndBadInput(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		body map[string]any
		code int
	}{
		{map[string]any{"volume": "nvme0n1p2"}, http.StatusForbidden},
		{map[string]any{"volume": "sdz1"}, http.StatusNotFound},
		{map[string]any{"volume": "sda1", "folder": "../../etc"}, http.StatusBadRequest},
		{map[string]any{"volume": "sda1", "mode": "combined"}, http.StatusNotImplemented},
		{map[string]any{"volume": "sda1", "path": "/etc"}, http.StatusBadRequest}, // unknown field
		{map[string]any{}, http.StatusBadRequest},
	}
	for _, c := range cases {
		if w := e.req("POST", "/api/pool", c.body, tokenHdr); w.Code != c.code {
			t.Errorf("%v = %d %s, want %d", c.body, w.Code, w.Body, c.code)
		}
	}
	if _, err := os.Stat(e.cfgPath); err == nil {
		t.Fatal("config written on refusal")
	}
}

func TestBrowserLoginFlow(t *testing.T) {
	e := newEnv(t)

	// A code can only be issued with the token.
	if w := e.req("POST", "/api/local-login", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("code without token = %d", w.Code)
	}
	w := e.req("POST", "/api/local-login", nil, tokenHdr)
	var lr struct{ Path string }
	json.Unmarshal(w.Body.Bytes(), &lr)
	if !strings.HasPrefix(lr.Path, "/login?code=") {
		t.Fatalf("login path = %q", lr.Path)
	}

	w = e.req("GET", lr.Path+"&next=//evil.example", nil, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("login redirect = %d %q", w.Code, w.Header().Get("Location"))
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie = %+v", cookie)
	}

	// Codes are single use.
	if w := e.req("GET", lr.Path, nil, nil); w.Header().Get("Location") != "/?login=expired" {
		t.Errorf("reused code -> %q", w.Header().Get("Location"))
	}

	sessionHdr := map[string]string{"Cookie": auth.CookieName + "=" + cookie.Value}
	var sess SessionResponse
	json.Unmarshal(e.req("GET", "/api/session", nil, sessionHdr).Body.Bytes(), &sess)
	if !sess.CanChange {
		t.Fatal("session not recognised")
	}

	// Cookie alone is not enough for a write: the intent header is required.
	if w := e.req("POST", "/api/pool", map[string]any{"volume": "sda1"}, sessionHdr); w.Code != http.StatusForbidden {
		t.Fatalf("cookie without intent = %d", w.Code)
	}
	sessionHdr[auth.HeaderIntent] = "1"
	if w := e.req("POST", "/api/pool", map[string]any{"volume": "sda1"}, sessionHdr); w.Code != 200 {
		t.Fatalf("browser adopt = %d %s", w.Code, w.Body)
	}

	if w := e.req("POST", "/api/logout", nil, sessionHdr); w.Code != 200 {
		t.Fatalf("logout = %d", w.Code)
	}
	json.Unmarshal(e.req("GET", "/api/session", nil, sessionHdr).Body.Bytes(), &sess)
	if sess.CanChange {
		t.Fatal("session survived logout")
	}
}
