package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"time"
)

// Two-step verification (docs/tickets/06-accounts.md, D6).
const (
	// totpStep is the time slice of one code (RFC 6238, 30 seconds).
	totpStep = 30 * time.Second
	// totpWindow is how many slices either side of the current one are accepted.
	totpWindow = 1
	// totpMaxFailures is the number of wrong codes before the account locks.
	totpMaxFailures = 5
	// totpLockDuration is how long a locked account refuses codes.
	totpLockDuration = 15 * time.Minute
	// mfaTokenLifetime bounds the short-lived token issued between the password
	// step and the code step of a login.
	mfaTokenLifetime = 5 * time.Minute
)

// GenerateTOTPSecret returns a new base32 secret for an authenticator app.
func GenerateTOTPSecret() string {
	var secret [20]byte
	if _, err := rand.Read(secret[:]); err != nil {
		panic("auth: generate TOTP secret: " + err.Error())
	}
	return base32.StdEncoding.EncodeToString(secret[:])
}

// TOTPCodeAt returns the six-digit code for secret in the time slice that
// contains t. Exported for tests.
func TOTPCodeAt(secret string, t time.Time) string {
	key, err := base32.StdEncoding.DecodeString(secret)
	if err != nil || len(key) != 20 || t.Unix() < 0 {
		return ""
	}
	return totpCodeForStep(key, t.Unix()/int64(totpStep/time.Second))
}

// VerifyTOTP checks code against the slices within totpWindow of now and
// rejects any slice at or before lastStep, so one code cannot be replayed.
// It returns the matched slice, which the caller stores as the new last_step.
func VerifyTOTP(secret, code string, now time.Time, lastStep int64) (ok bool, step int64) {
	key, err := base32.StdEncoding.DecodeString(secret)
	if err != nil || len(key) != 20 || now.Unix() < 0 || len(code) != 6 {
		return false, 0
	}
	for _, digit := range code {
		if digit < '0' || digit > '9' {
			return false, 0
		}
	}

	currentStep := now.Unix() / int64(totpStep/time.Second)
	matchedStep := int64(-1)
	for offset := -totpWindow; offset <= totpWindow; offset++ {
		candidateStep := currentStep + int64(offset)
		if candidateStep < 0 || candidateStep <= lastStep {
			continue
		}
		expected := totpCodeForStep(key, candidateStep)
		if subtle.ConstantTimeCompare([]byte(code), []byte(expected)) == 1 && candidateStep > matchedStep {
			matchedStep = candidateStep
		}
	}
	if matchedStep < 0 {
		return false, 0
	}
	return true, matchedStep
}

func totpCodeForStep(key []byte, step int64) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))

	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}
