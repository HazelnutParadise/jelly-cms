package auth

import (
	"encoding/base32"
	"regexp"
	"testing"
	"time"
)

// Pure acceptance tests for the TOTP primitives (docs/tickets/06-accounts.md,
// D6, 登入兩步驟): RFC 6238 vectors, the ±1 slice window, replay protection
// through lastStep, and the shape of generated secrets. No database is used.

const totpRFCSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" // base32 of "12345678901234567890"

func TestTOTPCodeAtMatchesRFC6238Vectors(t *testing.T) {
	if got := base32.StdEncoding.EncodeToString([]byte("12345678901234567890")); got != totpRFCSecret {
		t.Fatalf("fixture secret encodes to %q, want %q", got, totpRFCSecret)
	}
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1234567890, "005924"},
	}
	for _, c := range cases {
		if got := TOTPCodeAt(totpRFCSecret, time.Unix(c.unix, 0)); got != c.want {
			t.Errorf("TOTPCodeAt(T=%d) = %q, want %q", c.unix, got, c.want)
		}
	}
}

func TestVerifyTOTPWindowAndReplay(t *testing.T) {
	base := time.Unix(1700000000, 0)
	step := base.Unix() / 30
	code := func(slices int) string {
		return TOTPCodeAt(totpRFCSecret, base.Add(time.Duration(slices)*30*time.Second))
	}

	for _, offset := range []int{-1, 0, 1} {
		ok, got := VerifyTOTP(totpRFCSecret, code(offset), base, 0)
		if !ok {
			t.Errorf("code of slice %+d was rejected, want accepted within the ±1 window", offset)
			continue
		}
		if got != step+int64(offset) {
			t.Errorf("code of slice %+d matched step %d, want %d", offset, got, step+int64(offset))
		}
	}
	for _, offset := range []int{-2, 2} {
		if ok, _ := VerifyTOTP(totpRFCSecret, code(offset), base, 0); ok {
			t.Errorf("code of slice %+d was accepted, want rejected outside the ±1 window", offset)
		}
	}

	if ok, _ := VerifyTOTP(totpRFCSecret, code(0), base, step); ok {
		t.Errorf("code of the current slice accepted when lastStep is that slice")
	}
	if ok, _ := VerifyTOTP(totpRFCSecret, code(-1), base, step); ok {
		t.Errorf("code of an earlier slice accepted when lastStep is the current slice")
	}
	ok, got := VerifyTOTP(totpRFCSecret, code(1), base, step)
	if !ok || got != step+1 {
		t.Errorf("code of the next slice with lastStep=%d = ok %v step %d, want ok and step %d", step, ok, got, step+1)
	}

	// Same step twice: the first success stores its step, so the second is rejected.
	ok, first := VerifyTOTP(totpRFCSecret, code(0), base, 0)
	if !ok {
		t.Fatalf("first use of the current code was rejected")
	}
	if ok, _ := VerifyTOTP(totpRFCSecret, code(0), base, first); ok {
		t.Errorf("second use of the same code accepted after lastStep=%d", first)
	}
}

func TestTOTPRejectsEmptyAndShortSecrets(t *testing.T) {
	now := time.Unix(1700000000, 0)
	shortSecret := base32.StdEncoding.EncodeToString([]byte("1234567890123456789")) // 152 bits
	for _, secret := range []string{"", shortSecret} {
		if got := TOTPCodeAt(secret, now); got != "" {
			t.Errorf("TOTPCodeAt(%q) = %q, want empty for a secret shorter than 160 bits", secret, got)
		}
	}
	emptyCode := TOTPCodeAt("", now)
	if ok, _ := VerifyTOTP("", emptyCode, now, 0); ok {
		t.Error("VerifyTOTP accepted an empty secret")
	}
	shortCode := TOTPCodeAt(shortSecret, now)
	if ok, _ := VerifyTOTP(shortSecret, shortCode, now, 0); ok {
		t.Error("VerifyTOTP accepted a 152-bit secret")
	}
}

func TestGenerateTOTPSecretIsBase32And160Bits(t *testing.T) {
	shape := regexp.MustCompile(`^[A-Z2-7]+=*$`)
	a := GenerateTOTPSecret()
	b := GenerateTOTPSecret()
	for _, secret := range []string{a, b} {
		if !shape.MatchString(secret) {
			t.Errorf("secret %q is not upper-case base32", secret)
			continue
		}
		raw, err := base32.StdEncoding.DecodeString(secret)
		if err != nil {
			t.Errorf("secret %q does not decode as base32: %v", secret, err)
			continue
		}
		if len(raw) != 20 {
			t.Errorf("secret decodes to %d bytes, want 20 (160 bits)", len(raw))
		}
	}
	if a == b {
		t.Errorf("two generated secrets are identical: %q", a)
	}
}
