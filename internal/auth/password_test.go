package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected format %q (SFTPGo needs alexedwards/argon2id PHC)", h)
	}
	if !VerifyPassword("correct horse battery", h) || VerifyPassword("wrong horse battery", h) {
		t.Fatal("verification wrong")
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Fatal("salt not random")
	}
	for _, bad := range []string{"", "$argon2id$", "$bcrypt$x", strings.Replace(h, "m=19456", "m=999999999", 1)} {
		if VerifyPassword("x", bad) {
			t.Errorf("accepted malformed hash %q", bad)
		}
	}
}

func TestPasswordPolicy(t *testing.T) {
	if ValidatePassword("short") == nil || ValidatePassword("          ") == nil {
		t.Error("weak passwords accepted")
	}
	if err := ValidatePassword("a good long passphrase"); err != nil {
		t.Error(err)
	}
}

func TestTOTP(t *testing.T) {
	// RFC 6238 test vector: secret "12345678901234567890", T=59 -> 94287082 (8 digits) -> 287082.
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	now := time.Unix(59, 0)
	code, _ := TOTPCode(secret, now)
	if code != "287082" {
		t.Fatalf("code = %s, want 287082", code)
	}
	c, ok := VerifyTOTP(secret, code, now, 0)
	if !ok {
		t.Fatal("valid code rejected")
	}
	if _, ok := VerifyTOTP(secret, code, now, c); ok {
		t.Fatal("replayed code accepted")
	}
	if _, ok := VerifyTOTP(secret, code, now.Add(2*time.Minute), 0); ok {
		t.Fatal("stale code accepted")
	}
	real := time.Unix(1_790_000_000, 0)
	prev, _ := TOTPCode(secret, real.Add(-30*time.Second))
	if _, ok := VerifyTOTP(secret, prev, real, 0); !ok {
		t.Fatal("one step of clock drift should be accepted")
	}
	if !strings.HasPrefix(OTPAuthURI(secret, "ann", "Omarchy Vault"), "otpauth://totp/Omarchy%20Vault:ann?") {
		t.Error("otpauth uri")
	}
}

func TestLoginLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLoginLimiter()
	l.Now = func() time.Time { return now }
	for i := 0; i < 4; i++ {
		l.Fail("user:ann", "ip:1")
	}
	if _, blocked := l.Blocked("user:ann"); blocked {
		t.Fatal("blocked too early")
	}
	l.Fail("user:ann", "ip:1")
	if d, blocked := l.Blocked("user:ann"); !blocked || d != time.Minute {
		t.Fatalf("lockout = %v %v", d, blocked)
	}
	l.Fail("user:ann")
	if d, _ := l.Blocked("user:ann"); d != 2*time.Minute {
		t.Fatalf("second lockout = %v", d)
	}
	now = now.Add(3 * time.Minute)
	if _, blocked := l.Blocked("user:ann"); blocked {
		t.Fatal("lockout did not expire")
	}
	l.Succeed("user:ann")
	for i := 0; i < 30; i++ {
		l.Fail("ip:2")
	}
	if d, _ := l.Blocked("ip:2"); d != 15*time.Minute {
		t.Fatalf("cap = %v", d)
	}
}
