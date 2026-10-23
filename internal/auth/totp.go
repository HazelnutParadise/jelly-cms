package auth

import "time"

// Two-step verification (docs/tickets/06-accounts.md, D6). Stubs only: the
// constants fix the policy, the functions return zero values until implemented.
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
// Stub: returns "".
func GenerateTOTPSecret() string {
	return ""
}

// TOTPCodeAt returns the six-digit code for secret in the time slice that
// contains t. Exported for tests. Stub: returns "".
func TOTPCodeAt(secret string, t time.Time) string {
	_ = secret
	_ = t
	return ""
}

// VerifyTOTP checks code against the slices within totpWindow of now and
// rejects any slice at or before lastStep, so one code cannot be replayed.
// It returns the matched slice, which the caller stores as the new last_step.
// Stub: always fails.
func VerifyTOTP(secret, code string, now time.Time, lastStep int64) (ok bool, step int64) {
	_ = secret
	_ = code
	_ = now
	_ = lastStep
	return false, 0
}
