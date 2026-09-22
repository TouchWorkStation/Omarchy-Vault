package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters: the OWASP-recommended 19 MiB, 2 passes, 1 lane
// (equivalent strength to 46 MiB/1 pass, with a smaller memory spike on a
// machine that is also a desktop). The hash format is the PHC string used
// by alexedwards/argon2id, which SFTPGo accepts as-is, so Vault hands
// SFTPGo a hash instead of a password. Existing hashes keep verifying with
// the parameters stored inside them.
const (
	argonMemory  = 19 * 1024
	argonTime    = 2
	argonThreads = 1
	argonSaltLen = 16
	argonKeyLen  = 32

	MinPasswordLen = 10
	MaxPasswordLen = 256
)

// hashSem bounds concurrent hashing so a burst of logins cannot exhaust RAM.
var hashSem = make(chan struct{}, 2)

// ValidatePassword enforces the password policy.
func ValidatePassword(pw string) error {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < MinPasswordLen:
		return fmt.Errorf("use at least %d characters", MinPasswordLen)
	case len(pw) > MaxPasswordLen:
		return fmt.Errorf("use at most %d characters", MaxPasswordLen)
	case strings.TrimSpace(pw) == "":
		return errors.New("password cannot be blank")
	}
	return nil
}

// HashPassword returns a PHC-format argon2id hash.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hashSem <- struct{}{}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	<-hashSem
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword checks pw against a PHC argon2id hash in constant time.
func VerifyPassword(pw, hash string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || m > 1<<20 || t == 0 || t > 16 || p == 0 {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 || len(want) > 128 {
		return false
	}
	hashSem <- struct{}{}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	<-hashSem
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is verified against when a username does not exist, so a
// failed login takes the same time whether or not the user exists.
var dummyHash, _ = HashPassword("omarchy-vault-timing-equaliser")

// VerifyDummy burns the same work as a real verification.
func VerifyDummy(pw string) { VerifyPassword(pw, dummyHash) }
