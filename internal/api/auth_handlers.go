package api

import (
	"net/http"
	"strings"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
)

const changeHint = "To make changes, open Vault on this computer with Super+Shift+V or `vaultctl open`."

// requireAuth allows a request that carries the local token (CLI) or a
// valid session cookie plus the X-Vault-Request header (browser). The
// custom header cannot be sent cross-site without a CORS preflight, which
// Vault never approves, so it doubles as CSRF protection.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Demo {
			// Demo mode writes only into its throwaway sandbox.
			next.ServeHTTP(w, r)
			return
		}
		if s.Auth == nil {
			writeError(w, http.StatusForbidden, "changes_disabled", "Changes are disabled in this mode.")
			return
		}
		if tok := r.Header.Get(auth.HeaderToken); tok != "" {
			if s.Auth.CheckToken(tok) {
				next.ServeHTTP(w, r)
				return
			}
			writeError(w, http.StatusUnauthorized, "bad_token", "The Vault token is not valid.")
			return
		}
		if c, err := r.Cookie(auth.CookieName); err == nil && s.Auth.ValidSession(c.Value) {
			if r.Header.Get(auth.HeaderIntent) != "1" {
				writeError(w, http.StatusForbidden, "missing_intent", "Request refused.")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, "login_required", changeHint)
	})
}

// SessionResponse is returned by GET /api/session.
type SessionResponse struct {
	CanChange bool   `json:"can_change"`
	Hint      string `json:"hint,omitempty"`
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	ok := s.Demo
	if !ok && s.Auth != nil {
		if c, err := r.Cookie(auth.CookieName); err == nil {
			ok = s.Auth.ValidSession(c.Value)
		}
	}
	resp := SessionResponse{CanChange: ok}
	if !ok {
		resp.Hint = changeHint
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleLocalLogin trades the local token for a single-use login code.
func (s *Server) handleLocalLogin(w http.ResponseWriter, r *http.Request) {
	if s.Auth == nil || !s.Auth.CheckToken(r.Header.Get(auth.HeaderToken)) {
		writeError(w, http.StatusUnauthorized, "bad_token", "The Vault token is not valid.")
		return
	}
	code, ttl, err := s.Auth.NewCode()
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

// handleLogin redeems a login code and sets the session cookie.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	next := safeNext(r.URL.Query().Get("next"))
	if s.Auth == nil {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	sid, ttl, ok := s.Auth.Redeem(r.URL.Query().Get("code"))
	if !ok {
		http.Redirect(w, r, "/?login=expired", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    sid,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, next, http.StatusSeeOther)
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
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
