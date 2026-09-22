package auth

import (
	"sync"
	"time"
)

// LoginLimiter slows down password guessing. After 5 failures for a key
// (a username or a client address) the key is locked, starting at one
// minute and doubling up to 15 minutes. A success clears the key.
type LoginLimiter struct {
	Now func() time.Time

	mu    sync.Mutex
	state map[string]*limitState
}

type limitState struct {
	failures int
	until    time.Time
	last     time.Time
}

const (
	freeAttempts = 5
	baseLockout  = time.Minute
	maxLockout   = 15 * time.Minute
)

// NewLoginLimiter returns an empty limiter.
func NewLoginLimiter() *LoginLimiter { return &LoginLimiter{state: map[string]*limitState{}} }

func (l *LoginLimiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Blocked reports whether any key is locked and for how long.
func (l *LoginLimiter) Blocked(keys ...string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	var wait time.Duration
	for _, k := range keys {
		if s, ok := l.state[k]; ok && now.Before(s.until) {
			if d := s.until.Sub(now); d > wait {
				wait = d
			}
		}
	}
	return wait, wait > 0
}

// Fail records a failed attempt for every key.
func (l *LoginLimiter) Fail(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for k, s := range l.state {
		if now.Sub(s.last) > time.Hour {
			delete(l.state, k)
		}
	}
	for _, k := range keys {
		s, ok := l.state[k]
		if !ok {
			s = &limitState{}
			l.state[k] = s
		}
		s.failures++
		s.last = now
		if s.failures >= freeAttempts {
			d := baseLockout << (s.failures - freeAttempts)
			if d > maxLockout || d <= 0 {
				d = maxLockout
			}
			s.until = now.Add(d)
		}
	}
}

// Succeed clears the keys.
func (l *LoginLimiter) Succeed(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		delete(l.state, k)
	}
}
