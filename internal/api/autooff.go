package api

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// activity remembers when someone last used Vault, for auto-off.
type activity struct{ last atomic.Int64 }

func (a *activity) touch() { a.last.Store(time.Now().UnixNano()) }
func (a *activity) since() time.Duration {
	return time.Since(time.Unix(0, a.last.Load()))
}

// trackActivity marks every dashboard, API or Files request as use. The
// dashboard only polls while its tab is visible.
func (s *Server) trackActivity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.used.touch()
		next.ServeHTTP(w, r)
	})
}

// idle reports whether Vault has nothing to do: nobody used it for d, no
// phone link is active and no transfer is in progress.
func (s *Server) idle(ctx context.Context, d time.Duration) bool {
	if s.used.since() < d {
		return false
	}
	if s.Transfer != nil && s.Transfer.Busy() {
		return false
	}
	if s.Transfers != nil {
		active, err := s.Transfers.Active(ctx)
		if err != nil || len(active) > 0 {
			return false
		}
	}
	return true
}

// RunAutoOff turns Vault off after it has been idle for the configured
// time, so it never keeps running in the background by accident.
func (s *Server) RunAutoOff(ctx context.Context, every time.Duration) {
	s.used.touch()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		minutes := s.config().AutoOffMinutes
		if minutes <= 0 || s.PowerOff == nil {
			continue
		}
		if s.idle(ctx, time.Duration(minutes)*time.Minute) {
			s.Log.Info("turning off: nothing to do", "idle_minutes", minutes)
			s.PowerOff()
			return
		}
	}
}
