// Package control is the private channel between vaultctl and vaultd: a
// Unix socket in a folder only this user can open.
//
// The local token (admin rights without a password) travels only over
// this socket, never over TCP. Anyone on the computer can bind
// 127.0.0.1:8788 while Vault is off, so a TCP listener there proves
// nothing about who runs it. The socket, by contrast, lives in a 0700
// folder owned by this user, and both ends check the other's user id
// with SO_PEERCRED, so vaultctl only ever talks to this user's vaultd.
package control

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
)

const (
	dirName  = "omarchy-vault"
	sockName = "control.sock"
	maxPath  = 108 // sizeof(sun_path) on Linux, including the final NUL
)

// ErrUnsafe means the socket or its folder is not private to this user.
var ErrUnsafe = errors.New("control: socket is not private to this user")

// Path returns the control socket's path: $XDG_RUNTIME_DIR/omarchy-vault/
// control.sock (a per-user 0700 tmpfs folder on systemd systems), or a
// "run" folder inside Vault's own config folder when that is unset.
func Path() (string, error) {
	if d := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(d) {
		return filepath.Join(d, dirName, sockName), nil
	}
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "run", sockName), nil
}

// checkPrivate requires path to be owned by this user, not a symlink, and
// closed to group and others.
func checkPrivate(path string, wantDir bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	switch {
	case !ok, int(st.Uid) != os.Getuid():
		return fmt.Errorf("%w (%s is owned by someone else)", ErrUnsafe, path)
	case info.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("%w (%s is a symlink)", ErrUnsafe, path)
	case wantDir && !info.IsDir():
		return fmt.Errorf("%w (%s is not a folder)", ErrUnsafe, path)
	case !wantDir && info.Mode()&fs.ModeSocket == 0:
		return fmt.Errorf("%w (%s is not a socket)", ErrUnsafe, path)
	case info.Mode().Perm()&0o077 != 0:
		return fmt.Errorf("%w (%s is open to other users)", ErrUnsafe, path)
	}
	return nil
}

// peerUID returns the user id of the process at the other end of c.
func peerUID(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return -1, errors.New("control: not a Unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return -1, err
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return -1, err
	}
	if credErr != nil {
		return -1, credErr
	}
	return int(cred.Uid), nil
}

// Listen creates the socket at path for vaultd. The folder is created 0700
// (or must already be private to this user), a stale socket from a
// previous run is replaced, and the socket itself is 0600. Connections
// from any other user are closed before a byte is read.
func Listen(path string) (net.Listener, error) {
	if len(path) >= maxPath {
		return nil, fmt.Errorf("control: socket path is too long for a Unix socket (%d bytes, limit %d): %s", len(path), maxPath-1, path)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := checkPrivate(dir, true); err != nil {
		return nil, err
	}
	if err := checkPrivate(path, false); err == nil {
		// Our own socket from an earlier run. A live vaultd would still
		// answer on it; refuse rather than steal it.
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return nil, fmt.Errorf("another Vault is already running (%s)", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	old := syscall.Umask(0o177)
	ln, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(true)
	return ownerOnly{ln}, nil
}

type ownerOnly struct{ net.Listener }

func (l ownerOnly) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if uid, err := peerUID(c); err == nil && uid == os.Getuid() {
			return c, nil
		}
		c.Close()
	}
}

// Dial connects vaultctl to vaultd, after checking that the socket and its
// folder are private to this user and that the process listening on it
// runs as this user.
func Dial(ctx context.Context, path string) (net.Conn, error) {
	if err := checkPrivate(filepath.Dir(path), true); err != nil {
		return nil, err
	}
	if err := checkPrivate(path, false); err != nil {
		return nil, err
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	if uid, err := peerUID(c); err != nil || uid != os.Getuid() {
		c.Close()
		return nil, fmt.Errorf("%w (Vault's socket is served by another user)", ErrUnsafe)
	}
	return c, nil
}

// Client is an HTTP client whose every request goes to vaultd over the
// control socket at path, whatever host the URL names. It never uses a
// proxy or TCP.
func Client(path string, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return Dial(ctx, path)
			},
			DisableKeepAlives: true,
		},
	}
}

type ctxKey struct{}

// ConnContext marks requests that arrived on the control socket. Use it as
// http.Server.ConnContext for the socket's server only.
func ConnContext(ctx context.Context, _ net.Conn) context.Context {
	return context.WithValue(ctx, ctxKey{}, true)
}

// FromSocket reports whether r arrived on the control socket, the only
// place the local token is accepted.
func FromSocket(r *http.Request) bool {
	v, _ := r.Context().Value(ctxKey{}).(bool)
	return v
}

// WithSocket marks ctx as coming from the control socket (for tests).
func WithSocket(ctx context.Context) context.Context { return ConnContext(ctx, nil) }
