package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP (RFC 6238): SHA-1, 6 digits, 30 s steps, which every authenticator
// app supports.
const (
	totpStep   = 30
	totpDigits = 6
	totpSkew   = 1 // accept one step either side for clock drift
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random 160-bit base32 secret.
func NewTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b32.EncodeToString(b), nil
}

func totpAt(key []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, v%1_000_000)
}

// TOTPCode returns the code for secret at t (used by tests).
func TOTPCode(secret string, t time.Time) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	return totpAt(key, uint64(t.Unix())/totpStep), nil
}

// VerifyTOTP checks code at time t. lastCounter is the last accepted time
// step; codes at or before it are rejected so a code cannot be replayed.
// It returns the accepted step.
func VerifyTOTP(secret, code string, t time.Time, lastCounter uint64) (uint64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0, false
	}
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return 0, false
	}
	now := uint64(t.Unix()) / totpStep
	for d := -totpSkew; d <= totpSkew; d++ {
		c := uint64(int64(now) + int64(d))
		if c <= lastCounter {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpAt(key, c)), []byte(code)) == 1 {
			return c, true
		}
	}
	return 0, false
}

// OTPAuthURI builds the otpauth:// link authenticator apps scan.
func OTPAuthURI(secret, account, issuer string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return "otpauth://totp/" + label + "?" + q.Encode()
}
