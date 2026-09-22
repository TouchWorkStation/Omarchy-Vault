package api

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/files"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/storage"
)

// filesCookie is SFTPGo's web-client session cookie.
const filesCookie = "jwt"

var filesTarget = &url.URL{Scheme: "http", Host: files.Host + ":" + files.Port}

// filesProxy serves /files/ (SFTPGo's web client) through Vault. Only
// signed-in Vault users get through, so Vault's sign-in, 2FA and lockout
// protect Files too, and there is one address to expose remotely later.
func (s *Server) filesProxy() http.Handler {
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(filesTarget)
			pr.Out.Host = pr.In.Host
			pr.Out.Header.Del(auth.HeaderToken)
			pr.Out.Header.Del(auth.HeaderIntent)
			// Only SFTPGo's own cookie goes upstream, never Vault's session.
			var keep []string
			for _, c := range pr.In.Cookies() {
				if c.Name == filesCookie {
					keep = append(keep, c.Name+"="+c.Value)
				}
			}
			pr.Out.Header.Del("Cookie")
			if len(keep) > 0 {
				pr.Out.Header.Set("Cookie", strings.Join(keep, "; "))
			}
		},
		FlushInterval: 100 * time.Millisecond,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.Log.Warn("files proxy", "err", err)
			s.filesUnavailable(w, "Files is starting or has stopped. Try again in a moment.")
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok, _ := s.identity(r); !ok {
			if r.Method == http.MethodGet {
				http.Redirect(w, r, "/signin?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
				return
			}
			http.Error(w, "sign in first", http.StatusUnauthorized)
			return
		}
		if s.Files == nil || !s.Files.Status().Running {
			msg := "The file service (SFTPGo) is not installed. Run the Vault installer again to add it."
			if s.Files != nil {
				msg = s.Files.Status().Message
				if msg == "" {
					msg = "Files is starting. Try again in a moment."
				}
			}
			s.filesUnavailable(w, msg)
			return
		}
		// Large uploads and downloads must not hit the server-wide
		// timeouts; SFTPGo enforces its own.
		rc := http.NewResponseController(w)
		_ = rc.SetReadDeadline(time.Time{})
		_ = rc.SetWriteDeadline(time.Time{})
		rp.ServeHTTP(w, r)
	})
}

var unavailablePage = template.Must(template.New("u").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>Files · Vault</title>
<link rel="stylesheet" href="/unavailable.css"></head>
<body><main><h1>FILES</h1><p>{{.}}</p><p><a href="/">Back to Vault</a></p></main></body></html>`))

func (s *Server) filesUnavailable(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = unavailablePage.Execute(w, msg)
}

// FilesResponse is returned by GET /api/files.
type FilesResponse struct {
	files.Status
	SignedIn bool `json:"signed_in"`
}

func (s *Server) handleFilesStatus(w http.ResponseWriter, r *http.Request) {
	resp := FilesResponse{Status: files.Status{State: files.StateNotInstalled, URL: files.ClientPath,
		Message: "The file service (SFTPGo) is not installed. Run the Vault installer again to add it."}}
	if s.Files != nil {
		resp.Status = s.Files.Status()
	}
	if c, err := r.Cookie(filesCookie); err == nil && c.Value != "" {
		resp.SignedIn = true
	}
	writeJSON(w, http.StatusOK, resp)
}

// checkStorage keeps the file service and the data link in step with the
// drive. When the drive is missing, SFTPGo is stopped and the link removed,
// so nothing can be written to an empty mount point on the system disk.
func (s *Server) checkStorage(ctx context.Context) {
	inv, err := s.Disks.Inventory(ctx, false)
	if err != nil {
		inv = nil
	}
	st := s.storageStatus(ctx, inv)
	ready := st.State == storage.StateReady
	if s.DataLink != "" && st.Configured {
		if ready {
			want := st.Sources[0].DataDir
			if cur, err := readLink(s.DataLink); err != nil || cur != want {
				if err := storage.SetLink(s.DataLink, want); err != nil {
					s.Log.Warn("could not restore data link", "err", err)
				}
			}
		} else if _, err := readLink(s.DataLink); err == nil {
			s.Log.Warn("vault storage offline; pausing files", "state", st.State)
			if err := storage.RemoveLink(s.DataLink); err != nil {
				s.Log.Warn("could not remove data link", "err", err)
			}
		}
	}
	if s.Files != nil {
		s.Files.SetWanted(ready)
	}
}

// RunMonitor re-checks storage every interval until ctx ends.
func (s *Server) RunMonitor(ctx context.Context, interval time.Duration) {
	s.checkStorage(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.checkStorage(ctx)
		}
	}
}

func readLink(p string) (string, error) { return os.Readlink(p) }
