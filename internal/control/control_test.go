package control

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func serve(t *testing.T, path string) net.Listener {
	t.Helper()
	ln, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{
		ConnContext: ConnContext,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if FromSocket(r) {
				io.WriteString(w, "socket")
			}
		}),
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "omarchy-vault", sockName)
	serve(t, path)

	for p, want := range map[string]os.FileMode{filepath.Dir(path): 0o700, path: 0o600} {
		info, err := os.Lstat(p)
		if err != nil || info.Mode().Perm() != want {
			t.Errorf("%s mode = %v (%v), want %v", p, info.Mode().Perm(), err, want)
		}
	}
	resp, err := Client(path, 5*time.Second).Get("http://127.0.0.1:8788/api/session")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "socket" {
		t.Fatalf("server did not see the request as from the socket: %q", body)
	}
}

func TestSecondDaemonRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d", sockName)
	serve(t, path)
	if _, err := Listen(path); err == nil {
		t.Fatal("a second vaultd took over a live socket")
	}
}

func TestStaleSocketReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d", sockName)
	ln, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	ln.(ownerOnly).Listener.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close() // leaves the socket file behind, like a crash
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("setup: %v", err)
	}
	serve(t, path)
}

func TestDialRefusesOpenFolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d", sockName)
	serve(t, path)
	if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Dial(context.Background(), path)
	if !errors.Is(err, ErrUnsafe) {
		t.Fatalf("dial through a folder others can open = %v", err)
	}
	if _, err := Client(path, time.Second).Get("http://127.0.0.1:8788/"); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("client error = %v", err)
	}
}

func TestListenRefusesOpenFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "d")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0o777)
	if _, err := Listen(filepath.Join(dir, sockName)); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("listen in a shared folder = %v", err)
	}
}

func TestDialRefusesSymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real", sockName)
	serve(t, real)
	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Dir(real), link); err != nil {
		t.Fatal(err)
	}
	if _, err := Dial(context.Background(), filepath.Join(link, sockName)); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("dial through a symlinked folder = %v", err)
	}
}

func TestNotRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d", sockName)
	if _, err := Dial(context.Background(), path); err == nil || errors.Is(err, ErrUnsafe) {
		t.Fatalf("missing socket = %v, want a plain not-found", err)
	}
}

func TestPath(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if p, _ := Path(); p != "/run/user/1000/omarchy-vault/control.sock" {
		t.Errorf("path = %s", p)
	}
	t.Setenv("XDG_RUNTIME_DIR", "relative")
	t.Setenv("XDG_CONFIG_HOME", "/home/u/.config")
	if p, _ := Path(); p != "/home/u/.config/omarchy-vault/run/control.sock" {
		t.Errorf("fallback path = %s", p)
	}
}

func TestLongPathClearError(t *testing.T) {
	long := filepath.Join(t.TempDir(), "a-folder-name-long-enough-to-push-the-socket-path-past-the-unix-limit-of-108-bytes", sockName)
	if _, err := Listen(long); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("long path = %v", err)
	}
}
