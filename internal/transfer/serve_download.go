package transfer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
)

// shareCookie carries a share password grant, scoped to one link's path.
const shareCookie = "vault_share"

// maxShown caps how many files a folder page lists; the zip has them all.
const maxShown = 500

func friendlyOut(err error) string {
	switch {
	case errors.Is(err, ErrExpired):
		return "This link has expired."
	case errors.Is(err, ErrRevoked):
		return "This link was stopped."
	case errors.Is(err, ErrUsedUp):
		return "This link has been used the number of times it allows."
	default:
		return "This link is not valid."
	}
}

func (s *Server) ended(w http.ResponseWriter, err error, kind Kind) {
	hint := "Ask for a new code: press the Vault download shortcut on the computer."
	if kind == KindShare {
		hint = "Ask the person who shared it for a new link."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusGone)
	_ = pages.ExecuteTemplate(w, "ended.html", pageData{Message: friendlyOut(err), Hint: hint})
}

func (s *Server) problem(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_ = pages.ExecuteTemplate(w, "ended.html", pageData{Message: msg})
}

// usable looks a link up for serving a file: a device that already
// downloaded key may resume even when the link's count is used up.
func (s *Server) usable(r *http.Request, kind Kind, key string) (Session, bool, error) {
	sess, err := s.lookupKind(r, kind)
	if errors.Is(err, ErrUsedUp) && s.wasCounted(sess.ID, clientIP(r), key) {
		return sess, true, nil
	}
	if err != nil {
		return sess, false, err
	}
	return sess, s.wasCounted(sess.ID, clientIP(r), key), nil
}

func (s *Server) wasCounted(id, ip, key string) bool {
	s.gmu.Lock()
	defer s.gmu.Unlock()
	exp, ok := s.counted[id+"|"+ip+"|"+key]
	return ok && time.Now().Before(exp)
}

// count uses up one download of sess for this device and file, once.
func (s *Server) count(ctx context.Context, sess Session, ip, key, name string, size int64) error {
	if err := s.Store.Reserve(ctx, sess.ID); err != nil {
		return ErrUsedUp
	}
	s.gmu.Lock()
	if s.counted == nil {
		s.counted = map[string]time.Time{}
	}
	s.counted[sess.ID+"|"+ip+"|"+key] = sess.ExpiresAt
	s.gmu.Unlock()
	if err := s.Store.Record(ctx, sess, name, size); err != nil {
		s.Log.Warn("recording download failed", "err", err)
	}
	s.Log.Info("sending download", "kind", sess.Kind, "folder", sess.Folder, "bytes", size)
	return nil
}

func (s *Server) pruneGrants() {
	now := time.Now()
	s.gmu.Lock()
	defer s.gmu.Unlock()
	for k, exp := range s.counted {
		if !now.Before(exp) {
			delete(s.counted, k)
		}
	}
	for k, g := range s.unlocked {
		if !now.Before(g.expires) {
			delete(s.unlocked, k)
		}
	}
}

// openVault opens the Vault data folder, or reports it offline.
func (s *Server) openVault(w http.ResponseWriter) (*os.Root, bool) {
	dir, err := s.Dest()
	if err == nil {
		var root *os.Root
		if root, err = os.OpenRoot(dir); err == nil {
			return root, true
		}
	}
	s.problem(w, http.StatusServiceUnavailable, "The Vault drive is offline. Try again when it is connected.")
	return nil, false
}

func (s *Server) missing(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNoSuch) || errors.Is(err, ErrBadPath) || errors.Is(err, errNotInside) {
		s.problem(w, http.StatusNotFound, "That file is no longer in the Vault.")
		return
	}
	if errors.Is(err, ErrTooMany) {
		s.problem(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("This folder has more than %d files; share a smaller folder.", maxListed))
		return
	}
	s.Log.Warn("download failed", "err", err)
	s.problem(w, http.StatusInternalServerError, "The file could not be read.")
}

// describe fills the page fields for the linked file or folder.
func describe(root *os.Root, sess Session, token string) (pageData, error) {
	info, err := Stat(root, sess.Path)
	if err != nil {
		return pageData{}, err
	}
	d := pageData{Token: token, Name: path.Base(sess.Path), Expires: sess.ExpiresAt.UnixMilli(), IsDir: info.IsDir()}
	if sess.MaxFiles < Unlimited {
		left := sess.MaxFiles - sess.Files
		d.LimitMsg = fmt.Sprintf("Can be downloaded %d more time%s.", left, plural(left))
	}
	if !info.IsDir() {
		d.Size = humanBytes(info.Size())
		return d, nil
	}
	files, total, err := Walk(root, sess.Path)
	if err != nil {
		return pageData{}, err
	}
	d.Count, d.Size = len(files), humanBytes(total)
	for i, f := range files {
		if i == maxShown {
			d.More = len(files) - maxShown
			break
		}
		d.Files = append(d.Files, fileRow{Path: f.Path, Size: humanBytes(f.Size)})
	}
	return d, nil
}

// --- Download links (/d/) ---

func (s *Server) downloadPage(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	sess, err := s.lookupKind(r, KindDownload)
	if err != nil {
		s.ended(w, err, KindDownload)
		return
	}
	root, ok := s.openVault(w)
	if !ok {
		return
	}
	defer root.Close()
	d, err := describe(root, sess, r.PathValue("token"))
	if err != nil {
		s.missing(w, err)
		return
	}
	d.Files, d.More = nil, 0 // a download link is one item; no listing
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pages.ExecuteTemplate(w, "download.html", d)
}

func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	sess, counted, err := s.usable(r, KindDownload, "")
	if err != nil {
		s.ended(w, err, KindDownload)
		return
	}
	s.send(w, r, sess, sess.Path, "", counted)
}

// send streams rel (a file, or a folder as zip) for sess, counting it
// once per device and key.
func (s *Server) send(w http.ResponseWriter, r *http.Request, sess Session, rel, key string, counted bool) {
	root, ok := s.openVault(w)
	if !ok {
		return
	}
	defer root.Close()
	info, err := Stat(root, rel)
	if err != nil {
		s.missing(w, err)
		return
	}
	s.inflight.Add(1)
	defer s.inflight.Add(-1)
	if !counted && r.Method == http.MethodGet {
		name, size := path.Base(rel), info.Size()
		if info.IsDir() {
			name += ".zip"
			_, size, _ = Walk(root, rel)
		}
		if err := s.count(r.Context(), sess, clientIP(r), key, name, size); err != nil {
			s.ended(w, err, sess.Kind)
			return
		}
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(12 * time.Hour))
	if info.IsDir() {
		err = ServeZip(w, root, rel)
	} else {
		err = ServeFile(w, r, root, rel)
	}
	if err != nil {
		s.Log.Info("download ended early", "err", err)
	}
}

// --- Share links (/s/) ---

func (s *Server) shareUnlockedFor(r *http.Request, sess Session) bool {
	if !sess.HasPassword {
		return true
	}
	c, err := r.Cookie(shareCookie)
	if err != nil {
		return false
	}
	s.gmu.Lock()
	defer s.gmu.Unlock()
	g, ok := s.unlocked[c.Value]
	return ok && g.session == sess.ID && time.Now().Before(g.expires)
}

func (s *Server) sharePage(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	sess, err := s.lookupKind(r, KindShare)
	if err != nil {
		s.ended(w, err, KindShare)
		return
	}
	token := r.PathValue("token")
	if !s.shareUnlockedFor(r, sess) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.ExecuteTemplate(w, "password.html", pageData{Token: token})
		return
	}
	root, ok := s.openVault(w)
	if !ok {
		return
	}
	defer root.Close()
	d, err := describe(root, sess, token)
	if err != nil {
		s.missing(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pages.ExecuteTemplate(w, "share.html", d)
}

func (s *Server) shareUnlock(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	sess, err := s.lookupKind(r, KindShare)
	if err != nil {
		s.ended(w, err, KindShare)
		return
	}
	token := r.PathValue("token")
	key := "share:" + clientIP(r)
	if wait, blocked := s.limiter.Blocked(key); blocked {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = pages.ExecuteTemplate(w, "password.html", pageData{Token: token, Error: fmt.Sprintf("Too many wrong passwords. Try again in %d minutes.", int(wait.Minutes())+1)})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	pw := ""
	if err := r.ParseForm(); err == nil {
		pw = r.PostForm.Get("password")
	}
	if !sess.CheckPassword(pw) {
		s.limiter.Fail(key)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = pages.ExecuteTemplate(w, "password.html", pageData{Token: token, Error: "Wrong password."})
		return
	}
	s.limiter.Succeed(key)
	value, err := auth.Random(32)
	if err != nil {
		s.problem(w, http.StatusInternalServerError, "Something went wrong.")
		return
	}
	exp := sess.ExpiresAt
	if limit := time.Now().Add(12 * time.Hour); limit.Before(exp) {
		exp = limit
	}
	s.gmu.Lock()
	if s.unlocked == nil || len(s.unlocked) > 10000 {
		s.unlocked = map[string]grant{}
	}
	s.unlocked[value] = grant{session: sess.ID, expires: exp}
	s.gmu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: shareCookie, Value: value, Path: "/s/" + token, Expires: exp,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/s/"+token, http.StatusSeeOther)
}

func (s *Server) shareFile(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	sub := r.URL.Query().Get("p")
	sess, counted, err := s.usable(r, KindShare, "f:"+sub)
	if err != nil {
		s.ended(w, err, KindShare)
		return
	}
	if !s.shareUnlockedFor(r, sess) {
		http.Redirect(w, r, "/s/"+r.PathValue("token"), http.StatusSeeOther)
		return
	}
	rel := sess.Path
	if sub != "" {
		if rel, err = Join(sess.Path, sub); err != nil {
			s.missing(w, err)
			return
		}
	}
	s.send(w, r, sess, rel, "f:"+sub, counted)
}

func (s *Server) shareZip(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	sess, counted, err := s.usable(r, KindShare, "zip")
	if err != nil {
		s.ended(w, err, KindShare)
		return
	}
	if !s.shareUnlockedFor(r, sess) {
		http.Redirect(w, r, "/s/"+r.PathValue("token"), http.StatusSeeOther)
		return
	}
	s.send(w, r, sess, sess.Path, "zip", counted)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
