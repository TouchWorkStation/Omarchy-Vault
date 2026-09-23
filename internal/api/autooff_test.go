package api

import (
	"testing"
	"time"
)

func TestAutoOffWaitsForIdleAndLinks(t *testing.T) {
	e := newEnv(t)
	admin := bootstrap(t, e)
	e.req("POST", "/api/pool", map[string]any{"volume": "sda1"}, admin)
	ctx := t.Context()

	if e.srv.idle(ctx, time.Minute) {
		t.Fatal("idle right after being used")
	}
	// Pretend the last use was long ago.
	e.srv.used.last.Store(time.Now().Add(-time.Hour).UnixNano())
	if !e.srv.idle(ctx, time.Minute) {
		t.Fatal("not idle after an hour with nothing to do")
	}
	// An active phone code keeps Vault on.
	e.req("POST", "/api/upload-session", nil, admin)
	e.srv.used.last.Store(time.Now().Add(-time.Hour).UnixNano())
	if e.srv.idle(ctx, time.Minute) {
		t.Fatal("idle while a phone code is active")
	}

	// RunAutoOff calls PowerOff once idle.
	off := make(chan struct{})
	e.srv.PowerOff = func() { close(off) }
	cfg := e.srv.config()
	cfg.AutoOffMinutes = 5
	e.srv.setConfig(cfg)
	for _, l := range mustActive(t, e) {
		e.srv.Transfers.Revoke(ctx, l)
	}
	go e.srv.RunAutoOff(ctx, 10*time.Millisecond)
	time.Sleep(30 * time.Millisecond) // RunAutoOff marks itself used on start
	e.srv.used.last.Store(time.Now().Add(-time.Hour).UnixNano())
	select {
	case <-off:
	case <-time.After(2 * time.Second):
		t.Fatal("auto-off did not turn Vault off")
	}
}

func mustActive(t *testing.T, e *env) []string {
	t.Helper()
	active, err := e.srv.Transfers.Active(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range active {
		ids = append(ids, s.ID)
	}
	return ids
}
