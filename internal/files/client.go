package files

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Client talks to SFTPGo's REST API as the Vault-managed admin.
type Client struct {
	Base     string // http://127.0.0.1:8789
	WebRoot  string // /files
	User     string
	Password string
	HTTP     *http.Client

	mu    sync.Mutex
	token string
	exp   time.Time
}

// APIError is a non-2xx SFTPGo response.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("sftpgo: %d %s", e.Status, e.Message) }

// IsNotFound reports a 404 from SFTPGo.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (c *Client) getToken(ctx context.Context, force bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !force && c.token != "" && time.Now().Before(c.exp) {
		return c.token, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/api/v2/token", nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.User, c.Password)
	resp, err := c.http().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", &APIError{Status: resp.StatusCode, Message: "admin login failed"}
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresAt   string `json:"expires_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&tr); err != nil {
		return "", err
	}
	c.token = tr.AccessToken
	c.exp = time.Now().Add(10 * time.Minute) // SFTPGo admin tokens last 20 minutes
	if t, err := time.Parse(time.RFC3339, tr.ExpiresAt); err == nil {
		c.exp = t.Add(-time.Minute)
	}
	return c.token, nil
}

// do sends an authenticated request, retrying once with a fresh token.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		tok, err := c.getToken(ctx, attempt > 0)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, method, c.Base+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http().Do(req)
		if err != nil {
			return err
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			var e struct {
				Error   string `json:"error"`
				Message string `json:"message"`
			}
			_ = json.Unmarshal(data, &e)
			msg := strings.TrimSpace(e.Message + " " + e.Error)
			return &APIError{Status: resp.StatusCode, Message: msg}
		}
		if out != nil && len(data) > 0 {
			return json.Unmarshal(data, out)
		}
		return nil
	}
	return &APIError{Status: http.StatusUnauthorized, Message: "admin token rejected"}
}

// sftpUser is the subset of SFTPGo's user object Vault manages. Updates
// replace the whole object, so Vault always sends every field it owns.
type sftpUser struct {
	Status         int                 `json:"status"`
	Username       string              `json:"username"`
	Password       string              `json:"password,omitempty"`
	HomeDir        string              `json:"home_dir"`
	Permissions    map[string][]string `json:"permissions"`
	VirtualFolders []virtualFolder     `json:"virtual_folders"`
	Filters        userFilters         `json:"filters"`
	AdditionalInfo string              `json:"additional_info"`
	Description    string              `json:"description,omitempty"`
}

type virtualFolder struct {
	Name        string `json:"name"`
	VirtualPath string `json:"virtual_path"`
}

type userFilters struct {
	WebClient []string `json:"web_client"`
	// DeniedProtocols keeps SFTP/WebDAV/FTP closed per user even if a
	// listener were enabled; only HTTP (web client) is allowed.
	DeniedProtocols []string `json:"denied_protocols"`
}

type folder struct {
	Name       string `json:"name"`
	MappedPath string `json:"mapped_path"`
}

func (c *Client) listUsers(ctx context.Context) ([]sftpUser, error) {
	var out []sftpUser
	err := c.do(ctx, http.MethodGet, "/api/v2/users?limit=500", nil, &out)
	return out, err
}

func (c *Client) getUser(ctx context.Context, name string) (*sftpUser, error) {
	var u sftpUser
	if err := c.do(ctx, http.MethodGet, "/api/v2/users/"+url.PathEscape(name), nil, &u); err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (c *Client) putUser(ctx context.Context, u sftpUser, exists bool) error {
	if exists {
		return c.do(ctx, http.MethodPut, "/api/v2/users/"+url.PathEscape(u.Username), u, nil)
	}
	return c.do(ctx, http.MethodPost, "/api/v2/users", u, nil)
}

func (c *Client) deleteUser(ctx context.Context, name string) error {
	err := c.do(ctx, http.MethodDelete, "/api/v2/users/"+url.PathEscape(name), nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

func (c *Client) ensureFolder(ctx context.Context, f folder) error {
	var existing folder
	err := c.do(ctx, http.MethodGet, "/api/v2/folders/"+url.PathEscape(f.Name), nil, &existing)
	switch {
	case IsNotFound(err):
		return c.do(ctx, http.MethodPost, "/api/v2/folders", f, nil)
	case err != nil:
		return err
	case existing.MappedPath != f.MappedPath:
		return c.do(ctx, http.MethodPut, "/api/v2/folders/"+url.PathEscape(f.Name), f, nil)
	}
	return nil
}

var formTokenRe = regexp.MustCompile(`name="_form_token"[^>]*value="([^"]+)"|value="([^"]+)"[^>]*name="_form_token"`)

// ErrBadCredentials is returned by WebLogin when SFTPGo refuses them.
var ErrBadCredentials = errors.New("files: sign-in refused")

// WebLogin signs a user into SFTPGo's web client on their behalf and
// returns the session cookie to hand to their browser. SFTPGo binds the
// token to the client address; both this request and later proxied ones
// come from 127.0.0.1, so it stays valid through Vault's proxy.
func (c *Client) WebLogin(ctx context.Context, username, password string) (*http.Cookie, error) {
	loginURL := c.Base + c.WebRoot + "/web/client/login"
	noRedirect := &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loginURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		return nil, err
	}
	page, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	m := formTokenRe.FindSubmatch(page)
	if m == nil {
		return nil, errors.New("files: login form token not found")
	}
	token := string(m[1])
	if token == "" {
		token = string(m[2])
	}
	loginCookies := resp.Cookies()

	form := url.Values{"username": {username}, "password": {password}, "_form_token": {token}}
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range loginCookies {
		req.AddCookie(ck)
	}
	resp, err = noRedirect.Do(req)
	if err != nil {
		return nil, err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	// Success is a redirect into the client. A failed login re-renders the
	// login page (200), which also sets a login-only "jwt" cookie.
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		return nil, ErrBadCredentials
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "/web/client") || strings.Contains(loc, "/login") {
		return nil, ErrBadCredentials
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == "jwt" && ck.Value != "" {
			return ck, nil
		}
	}
	return nil, ErrBadCredentials
}
