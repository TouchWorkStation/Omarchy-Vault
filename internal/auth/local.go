// Package auth protects Vault's state-changing endpoints.
//
// Until user accounts arrive (Milestone 3), Vault trusts "the desktop user":
// whoever can read ~/.config/omarchy-vault/secrets/local-token. Other Unix
// users on the same machine can reach 127.0.0.1:8788 but cannot read that
// file, so they cannot change anything.
//
//   - The CLI sends the token in the X-Vault-Token header.
//   - Browsers never see the token. `vaultctl open` exchanges it for a
//     single-use login code (valid 30 s) and opens /login?code=…, which sets
//     an HttpOnly, SameSite=Strict session cookie.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	tokenFile    = "local-token"
	tokenBytes   = 32
	codeTTL      = 30 * time.Second
	sessionTTL   = 12 * time.Hour
	maxSessions  = 64
	maxCodes     = 16
	CookieName   = "vault_session"
	HeaderToken  = "X-Vault-Token"
	HeaderIntent = "X-Vault-Request" // required on cookie-authenticated writes
)

// Random returns n random bytes, base64url encoded without padding.
func Random(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SecretsDir returns the private secrets directory inside the config dir.
func SecretsDir(configDir string) string { return filepath.Join(configDir, "secrets") }

// LoadOrCreateToken returns the local token, creating it (0600, in a 0700
// directory) if needed. Loose permissions are tightened.
func LoadOrCreateToken(secretsDir string) (string, error) {
	if err := os.MkdirAll(secretsDir, 0o700); err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}
	if err := os.Chmod(secretsDir, 0o700); err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}
	tok, err := ReadToken(secretsDir)
	if err == nil {
		return tok, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	tok, err = Random(tokenBytes)
	if err != nil {
		return "", err
	}
	p := filepath.Join(secretsDir, tokenFile)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("auth: create token: %w", err)
	}
	if _, err := f.WriteString(tok + "\n"); err != nil {
		f.Close()
		return "", err
	}
	return tok, f.Close()
}

// ReadToken reads the local token, fixing its mode to 0600 if needed.
func ReadToken(secretsDir string) (string, error) {
	p := filepath.Join(secretsDir, tokenFile)
	info, err := os.Lstat(p)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("auth: %s is not a regular file", p)
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(p, 0o600); err != nil {
			return "", fmt.Errorf("auth: tighten %s: %w", p, err)
		}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(b))
	if len(tok) < 32 {
		return "", fmt.Errorf("auth: %s is malformed", p)
	}
	return tok, nil
}

// Equal compares secrets in constant time.
func Equal(a, b string) bool {
	return len(a) > 0 && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return string(h[:])
}

// Identity is who a session belongs to.
type Identity struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	// Local is true for sessions opened from this computer with the local
	// token (vaultctl open) rather than a password.
	Local bool `json:"local"`
}

type entry struct {
	exp time.Time
	id  Identity
}

// Local manages login codes and browser sessions. Only hashes are kept.
type Local struct {
	Token string
	Now   func() time.Time

	mu       sync.Mutex
	codes    map[string]entry
	sessions map[string]entry
}

// NewLocal returns a Local for token.
func NewLocal(token string) *Local {
	return &Local{Token: token, codes: map[string]entry{}, sessions: map[string]entry{}}
}

func (l *Local) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Local) gc(now time.Time) {
	for k, e := range l.codes {
		if now.After(e.exp) {
			delete(l.codes, k)
		}
	}
	for k, e := range l.sessions {
		if now.After(e.exp) {
			delete(l.sessions, k)
		}
	}
}

// CheckToken reports whether tok is the local token.
func (l *Local) CheckToken(tok string) bool { return Equal(tok, l.Token) }

// NewCode issues a single-use login code that signs in as id.
func (l *Local) NewCode(id Identity) (string, time.Duration, error) {
	code, err := Random(24)
	if err != nil {
		return "", 0, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.gc(now)
	if len(l.codes) >= maxCodes {
		return "", 0, errors.New("auth: too many pending login codes")
	}
	l.codes[hash(code)] = entry{exp: now.Add(codeTTL), id: id}
	return code, codeTTL, nil
}

// Redeem consumes a login code and opens a session for its identity.
func (l *Local) Redeem(code string) (string, time.Duration, Identity, bool) {
	if code == "" {
		return "", 0, Identity{}, false
	}
	l.mu.Lock()
	h := hash(code)
	e, ok := l.codes[h]
	now := l.now()
	if ok {
		delete(l.codes, h) // single use
	}
	l.mu.Unlock()
	if !ok || now.After(e.exp) {
		return "", 0, Identity{}, false
	}
	sid, ttl, err := l.NewSession(e.id)
	if err != nil {
		return "", 0, Identity{}, false
	}
	return sid, ttl, e.id, true
}

// NewSession opens a session for id and returns its id.
func (l *Local) NewSession(id Identity) (string, time.Duration, error) {
	sid, err := Random(32)
	if err != nil {
		return "", 0, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.gc(now)
	if len(l.sessions) >= maxSessions {
		var oldest string
		var oldestExp time.Time
		for k, e := range l.sessions {
			if oldest == "" || e.exp.Before(oldestExp) {
				oldest, oldestExp = k, e.exp
			}
		}
		delete(l.sessions, oldest)
	}
	l.sessions[hash(sid)] = entry{exp: now.Add(sessionTTL), id: id}
	return sid, sessionTTL, nil
}

// Session returns the identity of a live session.
func (l *Local) Session(sid string) (Identity, bool) {
	if sid == "" {
		return Identity{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.sessions[hash(sid)]
	if !ok || !l.now().Before(e.exp) {
		return Identity{}, false
	}
	return e.id, true
}

// ValidSession reports whether sid is a live session.
func (l *Local) ValidSession(sid string) bool {
	_, ok := l.Session(sid)
	return ok
}

// EndSession removes sid.
func (l *Local) EndSession(sid string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.sessions, hash(sid))
}

// EndSessionsFor signs a user out everywhere (after a password reset,
// role change or when the account is disabled).
func (l *Local) EndSessionsFor(username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, e := range l.sessions {
		if e.id.Username == username {
			delete(l.sessions, k)
		}
	}
}
