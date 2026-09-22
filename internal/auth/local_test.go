package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTokenCreatedPrivateAndStable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	a, err := LoadOrCreateToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreateToken(dir)
	if err != nil || a != b {
		t.Fatalf("token not stable: %v", err)
	}
	info, _ := os.Stat(filepath.Join(dir, tokenFile))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("token mode = %v", info.Mode().Perm())
	}
	dinfo, _ := os.Stat(dir)
	if dinfo.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v", dinfo.Mode().Perm())
	}
	// Loose permissions are tightened on read.
	os.Chmod(filepath.Join(dir, tokenFile), 0o644)
	if _, err := ReadToken(dir); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Stat(filepath.Join(dir, tokenFile))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode not tightened: %v", info.Mode().Perm())
	}
}

func TestCodeIsSingleUseAndExpires(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewLocal("tok")
	l.Now = func() time.Time { return now }

	code, _, err := l.NewCode(Identity{Username: "me", Role: "admin", Local: true})
	if err != nil {
		t.Fatal(err)
	}
	sid, _, id, ok := l.Redeem(code)
	if !ok || !l.ValidSession(sid) || id.Username != "me" {
		t.Fatal("first redeem should work")
	}
	if _, _, _, ok := l.Redeem(code); ok {
		t.Fatal("code reused")
	}

	late, _, _ := l.NewCode(Identity{})
	now = now.Add(31 * time.Second)
	if _, _, _, ok := l.Redeem(late); ok {
		t.Fatal("expired code accepted")
	}

	now = now.Add(13 * time.Hour)
	if l.ValidSession(sid) {
		t.Fatal("expired session accepted")
	}
}

func TestCheckToken(t *testing.T) {
	l := NewLocal("secret-token")
	if !l.CheckToken("secret-token") || l.CheckToken("") || l.CheckToken("secret-tokeN") {
		t.Fatal("token comparison wrong")
	}
	if Equal("", "") {
		t.Fatal("empty secrets must never match")
	}
}

func TestEndSessionsFor(t *testing.T) {
	l := NewLocal("tok")
	a, _, _ := l.NewSession(Identity{Username: "ann", Role: "family"})
	b, _, _ := l.NewSession(Identity{Username: "bob", Role: "admin"})
	l.EndSessionsFor("ann")
	if l.ValidSession(a) || !l.ValidSession(b) {
		t.Fatal("EndSessionsFor removed the wrong sessions")
	}
}
