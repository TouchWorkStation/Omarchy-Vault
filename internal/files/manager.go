package files

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
)

// Addr is where SFTPGo listens. Loopback only; Vault proxies /files/.
const (
	Host    = "127.0.0.1"
	Port    = "8789"
	WebRoot = "/files"
	// ClientPath is the web client's entry point behind Vault's proxy.
	ClientPath = WebRoot + "/web/client/files"

	adminUser = "vault-admin"
)

// State of the file service.
type State string

const (
	StateNotInstalled State = "not_installed"
	StateWaiting      State = "waiting_for_storage"
	StateStarting     State = "starting"
	StateRunning      State = "running"
	StateError        State = "error"
)

// Status is reported to the dashboard.
type Status struct {
	State     State  `json:"state"`
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version,omitempty"`
	URL       string `json:"url"`
	Message   string `json:"message,omitempty"`
	SFTP      bool   `json:"sftp"`
	WebDAV    bool   `json:"webdav"`
}

// Manager supervises the SFTPGo process.
type Manager struct {
	Paths Paths
	Log   *slog.Logger
	// OnReady runs after SFTPGo becomes healthy (Vault syncs users here).
	OnReady func(ctx context.Context) error

	mu      sync.Mutex
	want    bool
	state   State
	message string
	wake    chan struct{}
	client  *Client
	running bool
}

// NewManager prepares a manager; call Run to supervise.
func NewManager(p Paths, log *slog.Logger) *Manager {
	m := &Manager{Paths: p, Log: log, wake: make(chan struct{}, 1), state: StateWaiting}
	if p.Binary == "" {
		m.state = StateNotInstalled
	}
	return m
}

// Installed reports whether an SFTPGo binary was found.
func (m *Manager) Installed() bool { return m.Paths.Binary != "" }

// Client returns the admin API client once SFTPGo has started.
func (m *Manager) Client() *Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running {
		return nil
	}
	return m.client
}

// SetWanted asks the supervisor to run (storage ready) or stop SFTPGo.
func (m *Manager) SetWanted(want bool) {
	m.mu.Lock()
	changed := m.want != want
	m.want = want
	m.mu.Unlock()
	if changed {
		select {
		case m.wake <- struct{}{}:
		default:
		}
	}
}

// Status returns the current state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{State: m.state, Installed: m.Installed(), Running: m.running, URL: ClientPath, Message: m.message}
	if !st.Installed {
		st.State = StateNotInstalled
		st.Message = "The file service (SFTPGo) is not installed. Run the Vault installer again to add it."
	} else if !m.want && m.state != StateError {
		st.State = StateWaiting
		st.Message = "Files start once your Vault storage is ready."
	}
	return st
}

func (m *Manager) set(s State, msg string, running bool) {
	m.mu.Lock()
	m.state, m.message, m.running = s, msg, running
	m.mu.Unlock()
}

// secret returns a persistent random secret stored 0600 in SecretsDir.
func (m *Manager) secret(name string) (string, error) {
	if err := os.MkdirAll(m.Paths.SecretsDir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(m.Paths.SecretsDir, name)
	if b, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(b))) >= 32 {
		_ = os.Chmod(p, 0o600)
		return strings.TrimSpace(string(b)), nil
	}
	v, err := auth.Random(32)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(v+"\n"), 0o600); err != nil {
		return "", err
	}
	return v, nil
}

// env builds SFTPGo's configuration. Everything is set explicitly so the
// service is locked down regardless of SFTPGo's defaults.
func (m *Manager) env(adminPass, signing string) []string {
	p := m.Paths
	kv := map[string]string{
		// Storage of SFTPGo's own state.
		"SFTPGO_DATA_PROVIDER__DRIVER":                                "sqlite",
		"SFTPGO_DATA_PROVIDER__NAME":                                  filepath.Join(p.DataDir, "sftpgo.db"),
		"SFTPGO_DATA_PROVIDER__CREATE_DEFAULT_ADMIN":                  "true",
		"SFTPGO_DATA_PROVIDER__PASSWORD_HASHING__ALGO":                "argon2id",
		"SFTPGO_DATA_PROVIDER__BACKUPS_PATH":                          filepath.Join(p.DataDir, "backups"),
		"SFTPGO_DEFAULT_ADMIN_USERNAME":                               adminUser,
		"SFTPGO_DEFAULT_ADMIN_PASSWORD":                               adminPass,
		"SFTPGO_HTTPD__SIGNING_PASSPHRASE":                            signing,
		"SFTPGO_HTTPD__WEB_ROOT":                                      WebRoot,
		"SFTPGO_HTTPD__TEMPLATES_PATH":                                filepath.Join(p.Assets, "templates"),
		"SFTPGO_HTTPD__STATIC_FILES_PATH":                             filepath.Join(p.Assets, "static"),
		"SFTPGO_HTTPD__OPENAPI_PATH":                                  "",
		"SFTPGO_SMTP__TEMPLATES_PATH":                                 filepath.Join(p.Assets, "templates"),
		"SFTPGO_HTTPD__BINDINGS__0__ADDRESS":                          Host,
		"SFTPGO_HTTPD__BINDINGS__0__PORT":                             Port,
		"SFTPGO_HTTPD__BINDINGS__0__ENABLE_WEB_ADMIN":                 "false",
		"SFTPGO_HTTPD__BINDINGS__0__ENABLE_WEB_CLIENT":                "true",
		"SFTPGO_HTTPD__BINDINGS__0__ENABLE_REST_API":                  "true",
		"SFTPGO_HTTPD__BINDINGS__0__RENDER_OPENAPI":                   "false",
		"SFTPGO_HTTPD__BINDINGS__0__HIDE_LOGIN_URL":                   "0",
		"SFTPGO_HTTPD__BINDINGS__0__BRANDING__WEB_CLIENT__NAME":       "Omarchy Vault",
		"SFTPGO_HTTPD__BINDINGS__0__BRANDING__WEB_CLIENT__SHORT_NAME": "Vault",
		// Everything else stays closed. SFTP and WebDAV are opt-in (later).
		"SFTPGO_SFTPD__BINDINGS__0__PORT":   "0",
		"SFTPGO_FTPD__BINDINGS__0__PORT":    "0",
		"SFTPGO_WEBDAVD__BINDINGS__0__PORT": "0",
		"SFTPGO_TELEMETRY__BIND_PORT":       "0",
		"SFTPGO_COMMON__DEFENDER__ENABLED":  "false", // all traffic arrives from Vault's proxy
		// Keep the Go runtime's heap near its working set on a desktop.
		"GOMEMLIMIT": "64MiB",
	}
	out := make([]string, 0, len(kv)+4)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "SFTPGO_") && !strings.HasPrefix(e, "GOMEMLIMIT=") {
			out = append(out, e)
		}
	}
	for k, v := range kv {
		out = append(out, k+"="+v)
	}
	return out
}

func (m *Manager) command(ctx context.Context, env []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, m.Paths.Binary, args...)
	cmd.Env = env
	cmd.Dir = m.Paths.DataDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM, Setpgid: true}
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second
	return cmd
}

// Run supervises SFTPGo until ctx ends: it runs while wanted, restarts
// with backoff when it crashes, and stops when unwanted.
func (m *Manager) Run(ctx context.Context) {
	if !m.Installed() {
		m.Log.Info("file service not installed; Files stays off")
		<-ctx.Done()
		return
	}
	for _, d := range []string{m.Paths.ConfigDir, m.Paths.DataDir, m.Paths.HomesDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			m.set(StateError, "Could not prepare the file service folders.", false)
			m.Log.Error("files: mkdir", "dir", d, "err", err)
		}
	}
	adminPass, err := m.secret("sftpgo-admin")
	if err == nil {
		var signing string
		signing, err = m.secret("sftpgo-signing")
		if err == nil {
			m.mu.Lock()
			m.client = &Client{Base: "http://" + Host + ":" + Port, WebRoot: WebRoot, User: adminUser, Password: adminPass}
			m.mu.Unlock()
			m.supervise(ctx, m.env(adminPass, signing))
			return
		}
	}
	m.set(StateError, "Could not create the file service's secrets.", false)
	m.Log.Error("files: secrets", "err", err)
	<-ctx.Done()
}

func (m *Manager) wanted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.want
}

func (m *Manager) supervise(ctx context.Context, env []string) {
	backoff := time.Second
	initialised := false
	for ctx.Err() == nil {
		if !m.wanted() {
			m.set(StateWaiting, "", false)
			select {
			case <-ctx.Done():
				return
			case <-m.wake:
				continue
			}
		}
		if !initialised {
			out, err := m.command(ctx, env, "initprovider", "--config-dir", m.Paths.ConfigDir).CombinedOutput()
			if err != nil {
				m.set(StateError, "The file service database could not be prepared.", false)
				m.Log.Error("files: initprovider failed", "err", err, "output", lastLines(string(out), 5))
				if !m.sleep(ctx, &backoff) {
					return
				}
				continue
			}
			initialised = true
		}

		m.set(StateStarting, "", false)
		runCtx, stop := context.WithCancel(ctx)
		cmd := m.command(runCtx, env, "serve", "--config-dir", m.Paths.ConfigDir, "--log-file-path", "", "--log-level", "info")
		logs, _ := cmd.StdoutPipe()
		cmd.Stderr = cmd.Stdout
		if err := cmd.Start(); err != nil {
			stop()
			m.set(StateError, "The file service could not start.", false)
			m.Log.Error("files: start failed", "err", err)
			if !m.sleep(ctx, &backoff) {
				return
			}
			continue
		}
		go m.forwardLogs(logs)
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()

		if err := m.waitHealthy(runCtx, exited); err != nil {
			stop()
			<-exited
			m.set(StateError, "The file service did not start correctly.", false)
			m.Log.Error("files: not healthy", "err", err)
			if !m.sleep(ctx, &backoff) {
				return
			}
			continue
		}
		m.set(StateRunning, "", true)
		m.Log.Info("files: SFTPGo running", "addr", Host+":"+Port)
		backoff = time.Second
		if m.OnReady != nil {
			if err := m.OnReady(ctx); err != nil {
				m.Log.Error("files: user sync failed", "err", err)
				m.set(StateRunning, "Some accounts could not be prepared for Files. Check `vaultctl logs`.", true)
			}
		}

		crashed := m.waitWhileRunning(ctx, exited, stop)
		if ctx.Err() != nil {
			return
		}
		if crashed {
			m.set(StateError, "The file service stopped unexpectedly; restarting.", false)
			if !m.sleep(ctx, &backoff) {
				return
			}
		}
	}
}

// waitWhileRunning blocks until the process exits (crashed = true), it
// becomes unwanted and is stopped, or ctx ends.
func (m *Manager) waitWhileRunning(ctx context.Context, exited chan error, stop context.CancelFunc) (crashed bool) {
	for {
		select {
		case err := <-exited:
			stop()
			if ctx.Err() == nil {
				m.Log.Error("files: SFTPGo exited", "err", err)
			}
			return ctx.Err() == nil
		case <-m.wake:
			if !m.wanted() {
				m.Log.Info("files: stopping SFTPGo (storage not ready)")
				stop()
				<-exited
				return false
			}
		case <-ctx.Done():
			stop()
			<-exited
			return false
		}
	}
}

func (m *Manager) sleep(ctx context.Context, backoff *time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(*backoff):
	case <-m.wake:
	}
	*backoff *= 2
	if *backoff > time.Minute {
		*backoff = time.Minute
	}
	return true
}

func (m *Manager) waitHealthy(ctx context.Context, exited chan error) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			exited <- err
			return fmt.Errorf("exited during startup: %v", err)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
		resp, err := client.Get("http://" + Host + ":" + Port + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
	return errors.New("timed out waiting for /healthz")
}

// forwardLogs relays SFTPGo's errors only. Its warnings are mostly
// per-request access lines (every 4xx) and would drown Vault's own log.
func (m *Manager) forwardLogs(r interface{ Read([]byte) (int, error) }) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if line := sc.Text(); strings.Contains(line, `"level":"error"`) {
			m.Log.Warn("sftpgo", "line", truncate(line, 400))
		}
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
