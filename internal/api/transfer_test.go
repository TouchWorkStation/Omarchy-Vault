package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func phoneUpload(t *testing.T, url, name, content string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	w, _ := mw.CreateFormFile("file", name)
	w.Write([]byte(content))
	mw.Close()
	resp, err := http.Post(url+"/files", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestUploadLinkEndToEnd(t *testing.T) {
	e := newEnv(t)
	admin := bootstrap(t, e)

	// No storage yet: refused.
	if w := e.req("POST", "/api/upload-session", nil, admin); w.Code != http.StatusConflict {
		t.Fatalf("without storage = %d %s", w.Code, w.Body)
	}
	if w := e.req("POST", "/api/pool", map[string]any{"volume": "sda1"}, admin); w.Code != 200 {
		t.Fatalf("adopt = %d %s", w.Code, w.Body)
	}

	w := e.req("POST", "/api/upload-session", map[string]any{"minutes": 5}, admin)
	var link LinkView
	json.Unmarshal(w.Body.Bytes(), &link)
	if w.Code != 200 || link.Folder != "Phone Uploads" || !strings.HasPrefix(link.QRSVG, "<svg") || !strings.Contains(link.URL, "/u/") {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), e.mount) {
		t.Error("link response exposes the local path")
	}
	if e.srv.Transfer.Running() == "" {
		t.Fatal("listener not started")
	}

	// The phone uploads over the transfer listener.
	resp := phoneUpload(t, link.URL, "IMG_0042.JPG", "jpeg-bytes")
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("phone upload = %d", resp.StatusCode)
	}
	if b, err := os.ReadFile(filepath.Join(e.mount, "Vault", "Phone Uploads", "IMG_0042.JPG")); err != nil || string(b) != "jpeg-bytes" {
		t.Fatalf("file not on the drive: %v", err)
	}

	// The QR window polls this.
	w = e.req("GET", "/api/upload-session/"+link.ID, nil, admin)
	json.Unmarshal(w.Body.Bytes(), &link)
	if link.State != "active" || len(link.Received) != 1 || link.Received[0].Name != "IMG_0042.JPG" || link.QRSVG == "" {
		t.Fatalf("status = %s", w.Body)
	}
	var act struct{ Items []struct{ Name string } }
	json.Unmarshal(e.req("GET", "/api/activity", nil, admin).Body.Bytes(), &act)
	if len(act.Items) != 1 {
		t.Errorf("activity = %+v", act)
	}

	// Stop: the link dies and the listener closes.
	if w := e.req("DELETE", "/api/upload-session/"+link.ID, nil, admin); w.Code != 200 {
		t.Fatalf("stop = %d", w.Code)
	}
	if e.srv.Transfer.Running() != "" {
		t.Fatal("listener still open after the last link stopped")
	}
	if _, err := http.Get(link.URL); err == nil {
		t.Fatal("phone can still reach the transfer port")
	}
}

func TestUploadLinkPermissions(t *testing.T) {
	e := newEnv(t)
	admin := bootstrap(t, e)
	e.req("POST", "/api/pool", map[string]any{"volume": "sda1"}, admin)
	e.req("POST", "/api/users", map[string]any{"username": "ann", "password": "family-pass-123", "role": "family",
		"folders": []map[string]string{{"name": "Photos", "access": "rw"}, {"name": "Documents", "access": "ro"}}}, admin)
	e.req("POST", "/api/users", map[string]any{"username": "gus", "password": "guest-pass-1234", "role": "guest",
		"folders": []map[string]string{{"name": "Shared", "access": "ro"}}}, admin)
	ann := session(t, e.login(t, "ann", "family-pass-123", ""))
	gus := session(t, e.login(t, "gus", "guest-pass-1234", ""))

	if w := e.req("POST", "/api/upload-session", map[string]any{"folder": "Photos"}, ann); w.Code != 200 {
		t.Errorf("family into rw folder = %d %s", w.Code, w.Body)
	}
	for _, c := range []struct {
		who    map[string]string
		folder string
	}{{ann, "Documents"}, {ann, "Phone Uploads"}, {gus, "Shared"}} {
		if w := e.req("POST", "/api/upload-session", map[string]any{"folder": c.folder}, c.who); w.Code != http.StatusForbidden {
			t.Errorf("upload into %s = %d", c.folder, w.Code)
		}
	}
	for _, bad := range []string{"../etc", "Photos/../..", ".ssh"} {
		if w := e.req("POST", "/api/upload-session", map[string]any{"folder": bad}, admin); w.Code != http.StatusBadRequest {
			t.Errorf("folder %q = %d", bad, w.Code)
		}
	}
	// Ann only sees her own links; the admin sees all.
	var mine, all struct{ Links []LinkView }
	json.Unmarshal(e.req("GET", "/api/upload-sessions", nil, ann).Body.Bytes(), &mine)
	e.req("POST", "/api/upload-session", nil, admin)
	json.Unmarshal(e.req("GET", "/api/upload-sessions", nil, admin).Body.Bytes(), &all)
	if len(mine.Links) != 1 || len(all.Links) != 2 {
		t.Errorf("ann sees %d, admin sees %d", len(mine.Links), len(all.Links))
	}
	// Beam uses the local token.
	if w := e.req("POST", "/api/v1/beam/upload-session", map[string]any{"client": "beam"}, tokenHdr); w.Code != 200 {
		t.Errorf("beam = %d %s", w.Code, w.Body)
	}
	if w := e.req("POST", "/api/v1/beam/upload-session", nil, ann); w.Code != http.StatusForbidden {
		t.Errorf("beam endpoint as family = %d", w.Code)
	}
}
