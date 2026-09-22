package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/users"
)

// session returns request headers carrying the session cookie from w.
func session(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName && c.Value != "" {
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Fatalf("weak session cookie %+v", c)
			}
			return map[string]string{"Cookie": auth.CookieName + "=" + c.Value, auth.HeaderIntent: "1"}
		}
	}
	t.Fatalf("no session cookie (status %d: %s)", w.Code, w.Body)
	return nil
}

func (e *env) login(t *testing.T, user, pass, totp string) *httptest.ResponseRecorder {
	t.Helper()
	return e.req("POST", "/api/auth/login", map[string]string{"username": user, "password": pass, "totp": totp}, nil)
}

func errCode(w *httptest.ResponseRecorder) string {
	var e struct{ Error string }
	json.Unmarshal(w.Body.Bytes(), &e)
	return e.Error
}

// bootstrap creates the first admin through the local token.
func bootstrap(t *testing.T, e *env) map[string]string {
	t.Helper()
	w := e.req("POST", "/api/users", map[string]any{"username": "chris", "password": "admin-pass-123", "role": "family"}, tokenHdr)
	if w.Code != 200 {
		t.Fatalf("bootstrap = %d %s", w.Code, w.Body)
	}
	return session(t, w)
}

func TestBootstrapFirstAdmin(t *testing.T) {
	e := newEnv(t)
	if w := e.req("POST", "/api/users", map[string]any{"username": "x", "password": "admin-pass-123"}, nil); w.Code != 401 {
		t.Fatalf("bootstrap without local token = %d", w.Code)
	}
	admin := bootstrap(t, e)
	u, _ := e.users.Get("chris")
	if u.Role != users.Admin {
		t.Fatalf("first account must be admin, got %s", u.Role)
	}
	if strings.Contains(u.PasswordHash, "admin-pass") || !strings.HasPrefix(u.PasswordHash, "$argon2id$") {
		t.Fatal("password not hashed")
	}
	var s SessionResponse
	json.Unmarshal(e.req("GET", "/api/session", nil, admin).Body.Bytes(), &s)
	if !s.SignedIn || !s.CanChange || s.User == nil || s.User.Username != "chris" {
		t.Fatalf("session = %+v", s)
	}
	// Once accounts exist, reads need a sign-in too.
	if w := e.req("GET", "/api/status", nil, nil); w.Code != 401 {
		t.Errorf("anonymous status after accounts = %d", w.Code)
	}
	// The local token still acts as the owner.
	if w := e.req("GET", "/api/disks", nil, tokenHdr); w.Code != 200 {
		t.Errorf("local token disks = %d", w.Code)
	}
}

func TestPasswordLoginAndLockout(t *testing.T) {
	e := newEnv(t)
	bootstrap(t, e)
	for i := 0; i < 5; i++ {
		if w := e.login(t, "chris", "wrong-password", ""); w.Code != 401 || errCode(w) != "bad_credentials" {
			t.Fatalf("attempt %d = %d %s", i, w.Code, w.Body)
		}
	}
	if w := e.login(t, "chris", "admin-pass-123", ""); w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("locked login = %d", w.Code)
	}
	// Unknown users get the same answer as wrong passwords.
	e2 := newEnv(t)
	bootstrap(t, e2)
	a, b := e2.login(t, "nobody", "whatever-123", ""), e2.login(t, "chris", "whatever-123", "")
	if a.Code != b.Code || errCode(a) != errCode(b) {
		t.Errorf("user enumeration: %d/%s vs %d/%s", a.Code, errCode(a), b.Code, errCode(b))
	}
	if w := e2.login(t, "CHRIS ", "admin-pass-123", ""); w.Code != 200 {
		t.Errorf("username should be case/space-insensitive: %d", w.Code)
	}
}

func TestRolesAndImmediateRevocation(t *testing.T) {
	e := newEnv(t)
	admin := bootstrap(t, e)
	w := e.req("POST", "/api/users", map[string]any{"username": "ann", "password": "family-pass-123", "role": "family",
		"folders": []map[string]string{{"name": "Photos", "access": "rw"}}}, admin)
	if w.Code != 200 {
		t.Fatalf("create family = %d %s", w.Code, w.Body)
	}
	ann := session(t, e.login(t, "ann", "family-pass-123", ""))

	if w := e.req("GET", "/api/status", nil, ann); w.Code != 200 {
		t.Errorf("family status = %d", w.Code)
	}
	for _, c := range []struct{ m, p string }{{"GET", "/api/disks"}, {"GET", "/api/users"}, {"POST", "/api/pool"}, {"POST", "/api/users"}} {
		if w := e.req(c.m, c.p, map[string]any{}, ann); w.Code != http.StatusForbidden {
			t.Errorf("family %s %s = %d", c.m, c.p, w.Code)
		}
	}
	// Cookie writes need the intent header.
	noIntent := map[string]string{"Cookie": admin["Cookie"]}
	if w := e.req("POST", "/api/users", map[string]any{"username": "zed", "password": "zed-pass-1234", "role": "guest"}, noIntent); w.Code != http.StatusForbidden {
		t.Errorf("write without intent = %d", w.Code)
	}

	// Disabling ann ends her session on the very next request.
	if w := e.req("PUT", "/api/users/ann", map[string]any{"disabled": true}, admin); w.Code != 200 {
		t.Fatalf("disable = %d %s", w.Code, w.Body)
	}
	if w := e.req("GET", "/api/status", nil, ann); w.Code != 401 {
		t.Errorf("disabled user still in = %d", w.Code)
	}
	if w := e.login(t, "ann", "family-pass-123", ""); w.Code != 401 {
		t.Errorf("disabled user logged in = %d", w.Code)
	}

	// Re-enable, reset password: old password stops working.
	e.req("PUT", "/api/users/ann", map[string]any{"disabled": false}, admin)
	if w := e.req("POST", "/api/users/ann/password", map[string]string{"password": "short"}, admin); w.Code != 400 {
		t.Errorf("weak reset = %d", w.Code)
	}
	if w := e.req("POST", "/api/users/ann/password", map[string]string{"password": "new-family-pass"}, admin); w.Code != 200 {
		t.Fatalf("reset = %d", w.Code)
	}
	if w := e.login(t, "ann", "family-pass-123", ""); w.Code != 401 {
		t.Error("old password still works")
	}
	if w := e.login(t, "ann", "new-family-pass", ""); w.Code != 200 {
		t.Error("new password rejected")
	}

	// The last admin cannot lock themselves out.
	if w := e.req("PUT", "/api/users/chris", map[string]any{"disabled": true}, admin); w.Code != http.StatusConflict {
		t.Errorf("disable last admin = %d", w.Code)
	}
	if w := e.req("DELETE", "/api/users/ann", nil, admin); w.Code != 200 {
		t.Errorf("delete = %d", w.Code)
	}
	var list UsersResponse
	json.Unmarshal(e.req("GET", "/api/users", nil, admin).Body.Bytes(), &list)
	if len(list.Users) != 1 || strings.Contains(e.req("GET", "/api/users", nil, admin).Body.String(), "argon2") {
		t.Errorf("users = %+v", list)
	}
}

func TestTwoFactor(t *testing.T) {
	e := newEnv(t)
	admin := bootstrap(t, e)
	w := e.req("POST", "/api/account/totp/setup", nil, admin)
	var setup struct {
		Secret string `json:"secret"`
		QRSVG  string `json:"qr_svg"`
	}
	json.Unmarshal(w.Body.Bytes(), &setup)
	if w.Code != 200 || setup.Secret == "" || !strings.HasPrefix(setup.QRSVG, "<svg") {
		t.Fatalf("setup = %d %s", w.Code, w.Body)
	}
	if w := e.req("POST", "/api/account/totp/enable", map[string]string{"code": "000000"}, admin); w.Code != 400 {
		t.Errorf("wrong enable code = %d", w.Code)
	}
	code, _ := auth.TOTPCode(setup.Secret, time.Now())
	if w := e.req("POST", "/api/account/totp/enable", map[string]string{"code": code}, admin); w.Code != 200 {
		t.Fatalf("enable = %d %s", w.Code, w.Body)
	}

	if w := e.login(t, "chris", "admin-pass-123", ""); errCode(w) != "totp_required" {
		t.Fatalf("login without code = %d %s", w.Code, w.Body)
	}
	// The code used to enable cannot be replayed to sign in.
	if w := e.login(t, "chris", "admin-pass-123", code); errCode(w) != "bad_totp" {
		t.Errorf("replayed code = %d %s", w.Code, w.Body)
	}
	next, _ := auth.TOTPCode(setup.Secret, time.Now().Add(30*time.Second))
	if w := e.login(t, "chris", "admin-pass-123", next); w.Code != 200 {
		t.Fatalf("login with next code = %d %s", w.Code, w.Body)
	}
	if w := e.req("POST", "/api/account/totp/disable", map[string]string{"password": "nope-nope-nope"}, admin); w.Code != 401 {
		t.Errorf("disable with wrong password = %d", w.Code)
	}
	if w := e.req("POST", "/api/account/totp/disable", map[string]string{"password": "admin-pass-123"}, admin); w.Code != 200 {
		t.Errorf("disable = %d", w.Code)
	}
}

func TestChangeOwnPassword(t *testing.T) {
	e := newEnv(t)
	admin := bootstrap(t, e)
	if w := e.req("POST", "/api/account/password", map[string]string{"current": "wrong-one-123", "new": "brand-new-pass"}, admin); w.Code != 401 {
		t.Errorf("wrong current = %d", w.Code)
	}
	w := e.req("POST", "/api/account/password", map[string]string{"current": "admin-pass-123", "new": "brand-new-pass"}, admin)
	if w.Code != 200 {
		t.Fatalf("change = %d %s", w.Code, w.Body)
	}
	// Old sessions end; this browser gets a fresh one.
	if w := e.req("GET", "/api/users", nil, admin); w.Code != 401 {
		t.Errorf("old session survived password change = %d", w.Code)
	}
	fresh := session(t, w)
	if w := e.req("GET", "/api/users", nil, fresh); w.Code != 200 {
		t.Errorf("new session = %d", w.Code)
	}
	// The local owner has no password to change.
	if w := e.req("POST", "/api/account/password", map[string]string{"current": "a", "new": "b"}, tokenHdr); w.Code != 400 {
		t.Errorf("local change = %d", w.Code)
	}
}

func TestFilesProxyRequiresSignIn(t *testing.T) {
	e := newEnv(t)
	admin := bootstrap(t, e)
	w := e.req("GET", "/files/web/client/files", nil, nil)
	if w.Code != http.StatusSeeOther || !strings.HasPrefix(w.Header().Get("Location"), "/signin?next=") {
		t.Fatalf("anonymous files = %d %q", w.Code, w.Header().Get("Location"))
	}
	w = e.req("GET", "/files/web/client/files", nil, admin)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "not installed") {
		t.Fatalf("files without SFTPGo = %d %s", w.Code, w.Body)
	}
}

func TestFilesPageIsNotProxied(t *testing.T) {
	e := newEnv(t)
	if w := e.req("GET", "/files", nil, nil); w.Code != 200 || w.Header().Get("Location") != "" {
		t.Fatalf("/files = %d %q (should be the dashboard page)", w.Code, w.Header().Get("Location"))
	}
}
