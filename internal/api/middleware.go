package api

import (
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxBodyBytes = 1 << 20 // API request bodies; uploads get their own limit later

// contentSecurityPolicy allows only same-origin resources. The UI loads no
// third-party scripts, fonts or images.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// isFiles reports requests for SFTPGo's web client, which sets its own
// security headers and handles large uploads itself.
func isFiles(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/files/") }

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if isFiles(r) {
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			next.ServeHTTP(w, r)
			return
		}
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// hostGuard rejects requests whose Host header is not an expected name.
// This defeats DNS-rebinding attacks, where a malicious web page resolves
// its own domain to 127.0.0.1 to read a local service.
type hostGuard struct {
	allowed map[string]bool
}

func newHostGuard(extra []string) *hostGuard {
	g := &hostGuard{allowed: map[string]bool{
		"localhost": true,
		"127.0.0.1": true,
		"::1":       true,
	}}
	for _, h := range extra {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			g.allowed[h] = true
		}
	}
	return g
}

func hostOnly(hostport string) string {
	h := hostport
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		h = host
	}
	return strings.ToLower(strings.Trim(h, "[]"))
}

func (g *hostGuard) ok(hostport string) bool {
	return g.allowed[hostOnly(hostport)]
}

func (g *hostGuard) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.ok(r.Host) {
			writeError(w, http.StatusMisdirectedRequest, "bad_host", "This address is not recognised by Vault.")
			return
		}
		// State-changing requests must come from Vault's own pages. Browsers
		// always send Origin on cross-site POST/PUT/DELETE, so a mismatch
		// is a cross-site request forgery attempt.
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !g.ok(u.Host) || hostOnly(u.Host) != hostOnly(r.Host) {
					writeError(w, http.StatusForbidden, "bad_origin", "Cross-site request refused.")
					return
				}
			}
			if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
				writeError(w, http.StatusForbidden, "bad_origin", "Cross-site request refused.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && !isFiles(r) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// logRequests records method, path and status. Query strings are never
// logged because later milestones carry tokens there.
func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if !strings.HasPrefix(r.URL.Path, "/api/") && rec.status < 400 {
			return // do not log every static asset
		}
		log.Info("request", "method", r.Method, "path", redactPath(r.URL.Path), "status", rec.status, "ms", time.Since(start).Milliseconds())
	})
}

// redactPath hides token segments of transfer URLs (Milestones 4-5).
func redactPath(p string) string {
	for _, prefix := range []string{"/u/", "/d/", "/s/"} {
		if strings.HasPrefix(p, prefix) {
			return prefix + "[redacted]"
		}
	}
	return p
}

func recoverPanics(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Error("panic serving request", "path", redactPath(r.URL.Path), "panic", v)
				writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// rateLimiter is a small per-client token bucket.
type rateLimiter struct {
	mu      sync.Mutex
	rate    float64 // tokens per second
	burst   float64
	clients map[string]*bucket
	now     func() time.Time
	lastGC  time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(ratePerSec, burst float64) *rateLimiter {
	return &rateLimiter{rate: ratePerSec, burst: burst, clients: map[string]*bucket{}, now: time.Now}
}

func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.lastGC) > time.Minute {
		for k, b := range l.clients {
			if now.Sub(b.last) > 10*time.Minute {
				delete(l.clients, k)
			}
		}
		l.lastGC = now
	}
	b, ok := l.clients[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.clients[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *rateLimiter) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			key = r.RemoteAddr
		}
		// The file browser loads many assets at once; it is behind Vault's
		// sign-in and SFTPGo's own limits.
		if isFiles(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !l.allow(key) {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Slow down a little.")
			return
		}
		next.ServeHTTP(w, r)
	})
}
