package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/files"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/users"
)

const changeHint = "To make changes, open Vault on this computer with Super+Shift+V or `vaultctl open`."

// localIdentity is who the local token (or a vaultctl-open session) acts
// as: the owner of this computer, with admin rights.
var localIdentity = auth.Identity{Username: "local", Role: string(users.Admin), Local: true}

// accountsExist reports whether any Vault account has been created. Until
// then the dashboard is open read-only on loopback and changes need the
// local token (first-run setup).
func (s *Server) accountsExist() bool { return s.Users != nil && s.Users.Count() > 0 }

// identity returns who is making the request. intentOK reports whether a
// cookie-authenticated request carried X-Vault-Request (CSRF protection).
func (s *Server) identity(r *http.Request) (id auth.Identity, ok bool, viaCookie bool) {
	if s.Demo && !s.accountsExist() {
		// Demo mode: the sandbox's first-run setup needs no local token.
		// Once an account exists, the demo signs in like the real thing.
		return localIdentity, true, false
	}
	if s.Auth == nil {
		return auth.Identity{}, false, false
	}
	if tok := r.Header.Get(auth.HeaderToken); tok != "" {
		if s.Auth.CheckToken(tok) {
			return localIdentity, true, false
		}
		return auth.Identity{}, false, false
	}
	if c, err := r.Cookie(auth.CookieName); err == nil {
		if id, ok := s.Auth.Session(c.Value); ok {
			// Re-check the account on every request: a disabled or deleted
			// user loses access immediately, even mid-session.
			if !id.Local && s.Users != nil {
				u, found := s.Users.Get(id.Username)
				if !found || u.Disabled || string(u.Role) != id.Role {
					s.Auth.EndSession(c.Value)
					return auth.Identity{}, false, false
				}
			}
			return id, true, true
		}
	}
	return auth.Identity{}, false, false
}

type ctxKey struct{}

func identityFrom(r *http.Request) auth.Identity {
	id, _ := r.Context().Value(ctxKey{}).(auth.Identity)
	return id
}

// gate protects an endpoint.
//
//	admin:  must be an admin (or the local owner).
//	!admin: any signed-in user.
//
// Reads (GET/HEAD) are open on loopback until the first account exists, so
// first-run setup works; writes always need an identity. Cookie writes
// must also carry X-Vault-Request.
func (s *Server) gate(admin bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		read := r.Method == http.MethodGet || r.Method == http.MethodHead
		id, ok, viaCookie := s.identity(r)
		if !ok {
			if read && !s.accountsExist() {
				next(w, r)
				return
			}
			if s.accountsExist() {
				writeError(w, http.StatusUnauthorized, "login_required", "Please sign in.")
			} else {
				writeError(w, http.StatusUnauthorized, "login_required", changeHint)
			}
			return
		}
		if viaCookie && !read && r.Header.Get(auth.HeaderIntent) != "1" {
			writeError(w, http.StatusForbidden, "missing_intent", "Request refused.")
			return
		}
		if admin && id.Role != string(users.Admin) {
			writeError(w, http.StatusForbidden, "admin_only", "Only a Vault admin can do that.")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	}
}

// SessionResponse is returned by GET /api/session.
type SessionResponse struct {
	SignedIn      bool        `json:"signed_in"`
	CanChange     bool        `json:"can_change"`
	User          *users.View `json:"user,omitempty"`
	Local         bool        `json:"local"`
	AccountsExist bool        `json:"accounts_exist"`
	FilesSignedIn bool        `json:"files_signed_in"`
	Demo          bool        `json:"demo,omitempty"`
	Hint          string      `json:"hint,omitempty"`
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	id, ok, _ := s.identity(r)
	resp := SessionResponse{AccountsExist: s.accountsExist(), Demo: s.Demo}
	if ok {
		resp.SignedIn = true
		resp.Local = id.Local
		resp.CanChange = id.Role == string(users.Admin)
		if !id.Local && s.Users != nil {
			if u, found := s.Users.Get(id.Username); found {
				v := u.View()
				resp.User = &v
			}
		}
		if c, err := r.Cookie(filesCookie); err == nil && c.Value != "" {
			resp.FilesSignedIn = true
		}
	}
	if !resp.CanChange {
		if resp.AccountsExist {
			resp.Hint = "Sign in as a Vault admin to make changes."
		} else {
			resp.Hint = changeHint
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleLocalLogin trades the local token for a single-use login code.
func (s *Server) handleLocalLogin(w http.ResponseWriter, r *http.Request) {
	if s.Auth == nil || !s.Auth.CheckToken(r.Header.Get(auth.HeaderToken)) {
		writeError(w, http.StatusUnauthorized, "bad_token", "The Vault token is not valid.")
		return
	}
	code, ttl, err := s.Auth.NewCode(localIdentity)
	if err != nil {
		writeError(w, http.StatusTooManyRequests, "busy", "Too many pending logins. Try again shortly.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code":       code,
		"path":       "/login?code=" + code,
		"expires_in": int(ttl.Seconds()),
	})
}

// safeNext only allows same-site absolute paths.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n") {
		return "/"
	}
	return next
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, sid string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    sid,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func isHTTPS(r *http.Request) bool { return r.TLS != nil }

// handleLogin redeems a local login code (from vaultctl open).
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	next := safeNext(r.URL.Query().Get("next"))
	if s.Auth == nil {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	sid, ttl, _, ok := s.Auth.Redeem(r.URL.Query().Get("code"))
	if !ok {
		http.Redirect(w, r, "/?login=expired", http.StatusSeeOther)
		return
	}
	s.setSessionCookie(w, r, sid, ttl)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// LoginRequest is the body of POST /api/auth/login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TOTP     string `json:"totp"`
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

const badCredentials = "Wrong username or password."

// handlePasswordLogin signs a user in with username, password and (if
// enabled) a TOTP code. It also signs them into Files when it can.
func (s *Server) handlePasswordLogin(w http.ResponseWriter, r *http.Request) {
	if s.Users == nil || s.Auth == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Sign-in is not available.")
		return
	}
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "The request could not be read.")
		return
	}
	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	keys := []string{"user:" + req.Username, "ip:" + clientIP(r)}
	if wait, blocked := s.Limiter.Blocked(keys...); blocked {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "locked", "Too many attempts. Try again in a few minutes.")
		return
	}

	u, found := s.Users.Get(req.Username)
	if !found || len(req.Password) > auth.MaxPasswordLen {
		auth.VerifyDummy(req.Password)
		s.Limiter.Fail(keys...)
		writeError(w, http.StatusUnauthorized, "bad_credentials", badCredentials)
		return
	}
	if !auth.VerifyPassword(req.Password, u.PasswordHash) || u.Disabled {
		s.Limiter.Fail(keys...)
		writeError(w, http.StatusUnauthorized, "bad_credentials", badCredentials)
		return
	}
	if u.TOTPEnabled {
		if strings.TrimSpace(req.TOTP) == "" {
			writeError(w, http.StatusUnauthorized, "totp_required", "Enter the 6-digit code from your authenticator app.")
			return
		}
		counter, ok := auth.VerifyTOTP(u.TOTPSecret, req.TOTP, time.Now(), u.TOTPLast)
		if !ok {
			s.Limiter.Fail(keys...)
			writeError(w, http.StatusUnauthorized, "bad_totp", "That code is not right. Codes change every 30 seconds.")
			return
		}
		if _, err := s.Users.Update(u.Username, func(x *users.User) error { x.TOTPLast = counter; return nil }); err != nil {
			s.Log.Error("recording totp use failed", "err", err)
		}
	}
	s.Limiter.Succeed(keys...)
	s.signIn(w, r, u, req.Password)
	v := u.View()
	writeJSON(w, http.StatusOK, map[string]any{"user": v})
}

// signIn opens a fresh session (the id is new on every login) and, when
// the file service runs, signs the user into Files too.
func (s *Server) signIn(w http.ResponseWriter, r *http.Request, u users.User, password string) {
	sid, ttl, err := s.Auth.NewSession(auth.Identity{Username: u.Username, Role: string(u.Role)})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
		return
	}
	if old, err := r.Cookie(auth.CookieName); err == nil {
		s.Auth.EndSession(old.Value)
	}
	s.setSessionCookie(w, r, sid, ttl)
	s.Log.Info("signed in", "user", u.Username)
	if s.Files != nil {
		if c := s.Files.Client(); c != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			ck, err := c.WebLogin(ctx, u.Username, password)
			if err == nil {
				ck.Secure = isHTTPS(r)
				ck.Name = filesCookie
				http.SetCookie(w, ck)
			} else if !errors.Is(err, files.ErrBadCredentials) {
				s.Log.Warn("files sign-in failed", "user", u.Username, "err", err)
			}
		}
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(auth.HeaderIntent) != "1" {
		writeError(w, http.StatusForbidden, "missing_intent", "Request refused.")
		return
	}
	if c, err := r.Cookie(auth.CookieName); err == nil && s.Auth != nil {
		s.Auth.EndSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	http.SetCookie(w, &http.Cookie{Name: filesCookie, Value: "", Path: files.WebRoot + "/web/client", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
