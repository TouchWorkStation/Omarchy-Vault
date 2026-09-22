package transfer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "Phone Uploads"), 0o755)
	st, err := Open(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Server{
		Store: st, Host: "127.0.0.1", Port: -1,
		Dest: func() (string, error) { return dir, nil },
		Log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, dir
}

func multipartBody(t *testing.T, files map[string]string) (*bytes.Buffer, string) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, content := range files {
		w, _ := mw.CreateFormFile("file", name)
		w.Write([]byte(content))
	}
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func TestUploadFlow(t *testing.T) {
	s, dir := testServer(t)
	ctx := context.Background()
	sess, token, _ := s.Store.Create(ctx, NewSession{Kind: KindUpload, Folder: "Phone Uploads", CreatedBy: "chris", TTL: 10 * time.Minute, MaxFiles: 3, MaxBytes: 1 << 20})
	h := s.Handler()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/u/"+token, nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Phone Uploads") || !strings.Contains(w.Body.String(), "Take Photo") {
		t.Fatalf("page = %d", w.Code)
	}
	if w.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Errorf("headers = %v", w.Header())
	}
	if strings.Contains(w.Body.String(), dir) {
		t.Fatal("page exposes the local path")
	}

	body, ct := multipartBody(t, map[string]string{"IMG_1.jpg": "one", "../../evil.sh": "two"})
	req := httptest.NewRequest("POST", "/u/"+token+"/files", body)
	req.Header.Set("Content-Type", ct)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var resp struct {
		Saved  []Saved
		Folder string
		Error  string
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if w.Code != 200 || len(resp.Saved) != 2 {
		t.Fatalf("upload = %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, "Phone Uploads", "evil.sh")); err != nil {
		t.Error("cleaned file not in the folder")
	}
	if strings.Contains(w.Body.String(), dir) {
		t.Error("response exposes the local path")
	}
	got, _ := s.Store.Received(ctx, sess.ID)
	if len(got) != 2 {
		t.Errorf("activity = %d", len(got))
	}

	// Third file fits, fourth exceeds max_files.
	body, ct = multipartBody(t, map[string]string{"a.txt": "a"})
	req = httptest.NewRequest("POST", "/u/"+token+"/files", body)
	req.Header.Set("Content-Type", ct)
	h.ServeHTTP(httptest.NewRecorder(), req)
	body, ct = multipartBody(t, map[string]string{"b.txt": "b"})
	req = httptest.NewRequest("POST", "/u/"+token+"/files", body)
	req.Header.Set("Content-Type", ct)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusGone {
		t.Errorf("over limit = %d %s", w.Code, w.Body)
	}

	// Revoked links end immediately.
	s2, tok2, _ := s.Store.Create(ctx, NewSession{Kind: KindUpload, Folder: "Phone Uploads", TTL: time.Minute, MaxFiles: 5, MaxBytes: 100})
	s.Store.Revoke(ctx, s2.ID)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/u/"+tok2, nil))
	if w.Code != http.StatusGone || !strings.Contains(w.Body.String(), "stopped") {
		t.Errorf("revoked page = %d", w.Code)
	}

	// Nothing else is served on the transfer port.
	for _, p := range []string{"/", "/api/status", "/files/web/client", "/login"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 404 {
			t.Errorf("%s = %d on transfer port", p, w.Code)
		}
	}
}

func TestMaxBytesAndOfflineDrive(t *testing.T) {
	s, _ := testServer(t)
	_, token, _ := s.Store.Create(context.Background(), NewSession{Kind: KindUpload, Folder: "Phone Uploads", TTL: time.Minute, MaxFiles: 5, MaxBytes: 4})
	h := s.Handler()
	body, ct := multipartBody(t, map[string]string{"big.bin": "0123456789"})
	req := httptest.NewRequest("POST", "/u/"+token+"/files", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == 200 {
		t.Fatalf("oversize accepted: %s", w.Body)
	}

	s.Dest = func() (string, error) { return "", os.ErrNotExist }
	body, ct = multipartBody(t, map[string]string{"x.txt": "x"})
	req = httptest.NewRequest("POST", "/u/"+token+"/files", body)
	req.Header.Set("Content-Type", ct)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("offline drive = %d", w.Code)
	}
}

func TestGuessingIsSlowedDown(t *testing.T) {
	s, _ := testServer(t)
	s.Ensure() // initialises the limiter
	defer s.Stop()
	h := s.Handler()
	_, token, _ := s.Store.Create(context.Background(), NewSession{Kind: KindUpload, Folder: "Phone Uploads", TTL: time.Minute, MaxFiles: 5, MaxBytes: 100})
	for i := 0; i < 6; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/u/"+strings.Repeat("A", 43), nil))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/u/"+token, nil))
	if w.Code != http.StatusGone {
		t.Errorf("after guessing, even a valid token should be refused for a while: %d", w.Code)
	}
}

func TestListenerOnlyWhileActive(t *testing.T) {
	s, _ := testServer(t)
	ctx := context.Background()
	if s.Running() != "" {
		t.Fatal("listening before any link")
	}
	sess, _, _ := s.Store.Create(ctx, NewSession{Kind: KindUpload, Folder: "Phone Uploads", TTL: time.Minute, MaxFiles: 5, MaxBytes: 100})
	base, err := s.Ensure()
	if err != nil || !strings.HasPrefix(base, "http://127.0.0.1:") {
		t.Fatalf("ensure = %q %v", base, err)
	}
	resp, err := http.Get(base + "/t/transfer.css")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("asset over the listener: %v", err)
	}
	resp.Body.Close()
	s.StopIfIdle(ctx)
	if s.Running() == "" {
		t.Fatal("stopped while a link is active")
	}
	s.Store.Revoke(ctx, sess.ID)
	s.inflight.Add(1) // an upload still arriving keeps the listener up
	s.StopIfIdle(ctx)
	if s.Running() == "" {
		t.Fatal("stopped during an upload")
	}
	s.inflight.Add(-1)
	s.StopIfIdle(ctx)
	if s.Running() != "" {
		t.Fatal("still listening with no active link")
	}
	if _, err := http.Get(base + "/t/transfer.css"); err == nil {
		t.Fatal("port still open")
	}
}
