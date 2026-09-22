package transfer

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
)

//go:embed web
var webFS embed.FS

// DefaultPort is where phones reach Vault on the local network.
const DefaultPort = 8790

// reserveBytes is kept free on the drive: uploads stop before it fills.
const reserveBytes = 1 << 30

// LANAddress picks this computer's address on the local network: the
// private IPv4 address of the interface used for outbound traffic. No
// packet is sent (UDP "connect" only selects a route).
func LANAddress() (string, error) {
	if conn, err := net.Dial("udp4", "192.0.2.1:9"); err == nil {
		ip := conn.LocalAddr().(*net.UDPAddr).IP
		conn.Close()
		if ip.IsPrivate() {
			return ip.String(), nil
		}
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && ipn.IP.IsPrivate() {
				return ipn.IP.String(), nil
			}
		}
	}
	return "", errors.New("this computer is not connected to a local network (Wi-Fi or Ethernet)")
}

// Destination tells the server where the Vault's files are. It returns
// the Vault data folder (through the data link) or an error when storage
// is not ready.
type Destination func() (string, error)

// Server receives phone transfers. It listens only while at least one
// transfer link is active, and only serves transfer pages.
type Server struct {
	Store *Store
	Dest  Destination
	Log   *slog.Logger
	// Host/Port override the address phones use (config transfer.host/port).
	Host string
	Port int

	mu      sync.Mutex
	ln      net.Listener
	srv     *http.Server
	addr    string
	limiter *auth.LoginLimiter
	limOnce sync.Once
	// inflight counts uploads in progress, so a link expiring mid-upload
	// does not cut off a video that is still arriving.
	inflight atomic.Int32

	gmu sync.Mutex
	// counted remembers which device already downloaded what, so resuming
	// or re-opening a file doesn't use up a link (key: session|ip|file).
	counted map[string]time.Time
	// unlocked holds share password grants (key: cookie value).
	unlocked map[string]grant
}

type grant struct {
	session string
	expires time.Time
}

// Address returns the host:port phones should use, detecting the LAN
// address if none is configured.
func (s *Server) Address() (string, error) {
	host := s.Host
	if host == "" {
		var err error
		if host, err = LANAddress(); err != nil {
			return "", err
		}
	}
	port := s.Port
	switch {
	case port == 0:
		port = DefaultPort
	case port < 0:
		port = 0 // any free port (tests)
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// Running reports the listening address, if any.
func (s *Server) Running() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// Ensure starts listening if needed and returns the base URL for links.
func (s *Server) Ensure() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		return "http://" + s.addr, nil
	}
	addr, err := s.Address()
	if err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return "", fmt.Errorf("port %s is in use by another program", addr)
		}
		return "", fmt.Errorf("could not listen on %s: %w", addr, err)
	}
	s.ln, s.addr = ln, ln.Addr().String()
	s.srv = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	go func(srv *http.Server, ln net.Listener) {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.Log.Warn("transfer listener stopped", "err", err)
		}
	}(s.srv, ln)
	s.Log.Info("transfer listener on", "addr", s.addr)
	return "http://" + s.addr, nil
}

// StopIfIdle closes the listener when no link is active and no upload is
// still arriving.
func (s *Server) StopIfIdle(ctx context.Context) {
	if s.inflight.Load() > 0 {
		return
	}
	active, err := s.Store.Active(ctx)
	if err != nil || len(active) > 0 {
		return
	}
	s.Stop()
}

// Stop closes the listener (in-flight uploads get a short grace period).
func (s *Server) Stop() {
	s.mu.Lock()
	srv := s.srv
	s.ln, s.srv, s.addr = nil, nil, ""
	s.mu.Unlock()
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	s.Log.Info("transfer listener off")
}

// Run closes the listener after links expire, and prunes old records.
func (s *Server) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	_ = s.Store.Prune(ctx)
	for {
		select {
		case <-ctx.Done():
			s.Stop()
			return
		case <-t.C:
			s.StopIfIdle(ctx)
			s.pruneGrants()
		}
	}
}

func securityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer") // the token is in the URL
	h.Set("Cache-Control", "no-store")
	h.Set("Permissions-Policy", "geolocation=(), microphone=()")
}

var pages = template.Must(template.ParseFS(webFS, "web/*.html"))

// Handler serves only transfer pages: /u/ (upload), /d/ (download) and
// /s/ (share) for a valid token, and static assets.
func (s *Server) Handler() http.Handler {
	s.limOnce.Do(func() {
		if s.limiter == nil {
			s.limiter = auth.NewLoginLimiter()
		}
	})
	mux := http.NewServeMux()
	static, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /t/", http.StripPrefix("/t/", http.FileServerFS(static)))
	mux.HandleFunc("GET /u/{token}", s.page)
	mux.HandleFunc("GET /u/{token}/info", s.info)
	mux.HandleFunc("POST /u/{token}/files", s.receive)
	mux.HandleFunc("GET /d/{token}", s.downloadPage)
	mux.HandleFunc("GET /d/{token}/file", s.downloadFile)
	mux.HandleFunc("GET /s/{token}", s.sharePage)
	mux.HandleFunc("POST /s/{token}/unlock", s.shareUnlock)
	mux.HandleFunc("GET /s/{token}/file", s.shareFile)
	mux.HandleFunc("GET /s/{token}/zip", s.shareZip)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w)
		http.NotFound(w, r)
	})
	rl := newIPLimiter(10, 40)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(clientIP(r)) {
			securityHeaders(w)
			http.Error(w, "Too many requests", http.StatusTooManyRequests)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// lookup validates an upload token, slowing down clients that guess.
func (s *Server) lookup(r *http.Request) (Session, error) {
	return s.lookupKind(r, KindUpload)
}

// lookupKind validates a token of one kind. Wrong tokens count towards a
// per-address lockout.
func (s *Server) lookupKind(r *http.Request, kind Kind) (Session, error) {
	ip := "ip:" + clientIP(r)
	if s.limiter != nil {
		if _, blocked := s.limiter.Blocked(ip); blocked {
			return Session{}, ErrNotFound
		}
	}
	sess, err := s.Store.Lookup(r.Context(), r.PathValue("token"), kind)
	if errors.Is(err, ErrNotFound) && s.limiter != nil {
		s.limiter.Fail(ip)
	}
	return sess, err
}

func friendly(err error) string {
	switch {
	case errors.Is(err, ErrExpired):
		return "This upload link has expired."
	case errors.Is(err, ErrRevoked):
		return "This upload link was stopped."
	case errors.Is(err, ErrUsedUp):
		return "This upload link is full."
	default:
		return "This upload link is not valid."
	}
}

type pageData struct {
	Folder  string
	Expires int64
	Message string
	Hint    string
	// Downloads and shares.
	Token    string
	Name     string
	Size     string
	IsDir    bool
	Count    int
	Files    []fileRow
	More     int
	Error    string
	LimitMsg string
}

type fileRow struct {
	Path string
	Size string
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	sess, err := s.lookup(r)
	if err != nil {
		w.WriteHeader(http.StatusGone)
		_ = pages.ExecuteTemplate(w, "ended.html", pageData{Message: friendly(err), Hint: "Ask for a new code: press Super + Shift + U on the Vault computer."})
		return
	}
	_ = pages.ExecuteTemplate(w, "upload.html", pageData{Folder: sess.Folder, Expires: sess.ExpiresAt.UnixMilli()})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	sess, err := s.lookup(r)
	if err != nil {
		writeJSON(w, http.StatusGone, map[string]string{"error": friendly(err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"folder": sess.Folder, "expires_at": sess.ExpiresAt.UnixMilli(),
		"files_left": sess.MaxFiles - sess.Files,
	})
}

// Saved is one received file.
type Saved struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// receive streams a multipart upload straight to the drive, one part at a
// time, enforcing the link's limits and the drive's free space.
func (s *Server) receive(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	sess, err := s.lookup(r)
	if err != nil {
		writeJSON(w, http.StatusGone, map[string]string{"error": friendly(err)})
		return
	}
	root, err := s.Dest()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "The Vault drive is offline. Try again when it is connected."})
		return
	}
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "multipart/form-data" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Unexpected upload format."})
		return
	}
	s.inflight.Add(1)
	defer s.inflight.Add(-1)
	// Uploads can be large and slow on mobile networks.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(6 * time.Hour))
	_ = rc.SetWriteDeadline(time.Now().Add(6 * time.Hour))

	vroot, err := os.OpenRoot(root)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "The Vault drive is offline."})
		return
	}
	defer vroot.Close()

	mr, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Unexpected upload format."})
		return
	}
	var saved []Saved
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.fail(w, saved, "The upload was interrupted.")
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			part.Close()
			continue
		}
		item, msg := s.saveOne(r.Context(), sess, vroot, root, part)
		part.Close()
		if msg != "" {
			s.fail(w, saved, msg)
			return
		}
		saved = append(saved, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": saved, "folder": sess.Folder})
}

func (s *Server) fail(w http.ResponseWriter, saved []Saved, msg string) {
	code := http.StatusBadRequest
	if len(saved) > 0 {
		code = http.StatusPartialContent
	}
	if saved == nil {
		saved = []Saved{}
	}
	writeJSON(w, code, map[string]any{"error": msg, "saved": saved})
}

func (s *Server) saveOne(ctx context.Context, sess Session, root *os.Root, rootPath string, part *multipart.Part) (Saved, string) {
	if err := s.Store.Reserve(ctx, sess.ID); err != nil {
		return Saved{}, "This upload link is full."
	}
	remaining, err := s.Store.Remaining(ctx, sess.ID)
	if err != nil {
		s.Store.Release(ctx, sess.ID)
		return Saved{}, "Something went wrong."
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(rootPath, &st); err == nil {
		free := int64(st.Bavail) * int64(st.Bsize)
		room := free - reserveBytes
		if room <= 0 {
			s.Store.Release(ctx, sess.ID)
			return Saved{}, "The Vault is full."
		}
		if room < remaining {
			remaining = room
		}
	}
	name, size, err := Save(root, sess.Folder, part.FileName(), part, remaining)
	if err != nil {
		s.Store.Release(ctx, sess.ID)
		if errors.Is(err, ErrTooLarge) {
			return Saved{}, "That file is larger than this link or the Vault's free space allows."
		}
		s.Log.Warn("upload failed", "err", err)
		return Saved{}, "The file could not be saved."
	}
	if err := s.Store.Record(ctx, sess, name, size); err != nil {
		s.Log.Warn("recording upload failed", "err", err)
	}
	s.Log.Info("received upload", "folder", sess.Folder, "bytes", size)
	return Saved{Name: name, Size: size}, ""
}

// ipLimiter is a small per-address token bucket.
type ipLimiter struct {
	mu    sync.Mutex
	rate  float64
	burst float64
	b     map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newIPLimiter(rate, burst float64) *ipLimiter {
	return &ipLimiter{rate: rate, burst: burst, b: map[string]*bucket{}}
}

func (l *ipLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.b) > 1000 {
		l.b = map[string]*bucket{}
	}
	b, ok := l.b[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.b[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Link builds the phone URL for a token.
func Link(base, token string) string {
	return LinkFor(base, KindUpload, token)
}

// LinkFor builds the phone URL for a link of the given kind.
func LinkFor(base string, kind Kind, token string) string {
	prefix := map[Kind]string{KindUpload: "/u/", KindDownload: "/d/", KindShare: "/s/"}[kind]
	return strings.TrimRight(base, "/") + prefix + token
}
