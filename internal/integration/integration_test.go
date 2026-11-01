package integration

// Unit tests for ticket 14 slot A1 (docs/tickets/14-integration-api.md, `internal/integration` 套件 and 名詞).
// They pin the key format, hashing, display prefix, IP masking, the permission catalogue and the
// rotation grace bounds. Introduced against the draft skeleton, they now define the regression
// contract for these helpers. They run without a database.

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"regexp"
	"testing"
)

var integrationKeyPattern = regexp.MustCompile(`^jly_[A-Za-z0-9_-]{43}$`)

func TestIntegrationGenerateKeyFormatAndUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		key, err := GenerateKey()
		if err != nil {
			t.Fatalf("GenerateKey() error = %v", err)
		}
		if !integrationKeyPattern.MatchString(key) {
			t.Fatalf("GenerateKey() = %q, want jly_ followed by 43 base64url characters (32 random bytes, no padding)", key)
		}
		if seen[key] {
			t.Fatalf("GenerateKey() returned %q twice in 64 calls", key)
		}
		seen[key] = true
	}
}

func TestIntegrationHashKeyIsSHA256HexOfWholeKey(t *testing.T) {
	key := "jla_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	sum := sha256.Sum256([]byte(key))
	want := hex.EncodeToString(sum[:])
	if got := HashKey(key); got != want {
		t.Errorf("HashKey(%q) = %q, want lowercase hex SHA-256 of the whole key %q", key, got, want)
	}
	if HashKey(key) == HashKey(key+"x") {
		t.Error("HashKey ignores trailing characters")
	}
}

func TestIntegrationPrefixOfKeepsTwelveCharacters(t *testing.T) {
	key := "jla_Qm3d8sKeRESTOFTHEKEYxxxxxxxxxxxxxxxxxxxxx"
	if got := PrefixOf(key); got != "jla_Qm3d8sKe" {
		t.Errorf("PrefixOf(%q) = %q, want jla_Qm3d8sKe (jla_ plus 8 characters)", key, got)
	}
	if got := PrefixOf("jla_short"); got != "" {
		t.Errorf("PrefixOf(short key) = %q, want empty", got)
	}
	// Keys issued from ticket 22 on use jly_; both prefixes keep 12 characters.
	jlyKey := "jly_Qm3d8sKeRESTOFTHEKEYxxxxxxxxxxxxxxxxxxxxx"
	if got := PrefixOf(jlyKey); got != "jly_Qm3d8sKe" {
		t.Errorf("PrefixOf(%q) = %q, want jly_Qm3d8sKe (jly_ plus 8 characters)", jlyKey, got)
	}
	if got := PrefixOf("jly_short"); got != "" {
		t.Errorf("PrefixOf(short jly_ key) = %q, want empty", got)
	}
	if DisplayPrefixLength != 12 {
		t.Errorf("DisplayPrefixLength = %d, want 12", DisplayPrefixLength)
	}
}

func TestIntegrationMaskIP(t *testing.T) {
	cases := map[string]string{
		"198.51.100.77":              "198.51.100.0",
		"203.0.113.5":                "203.0.113.0",
		"2001:db8:abcd:12::5":        "2001:db8:abcd::",
		"2001:db8:abcd:ffff:1:2:3:4": "2001:db8:abcd::",
		"::ffff:198.51.100.77":       "198.51.100.0",
		"not an ip":                  "",
		"":                           "",
	}
	for in, want := range cases {
		if got := MaskIP(in); got != want {
			t.Errorf("MaskIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIntegrationPermissionCatalogue(t *testing.T) {
	want := []Permission{
		"posts.read", "posts.write", "pages.read", "pages.write", "media.read", "media.write",
		"products.read", "products.write", "orders.read", "themes.read", "themes.write",
		"plugins.read", "plugins.write", "settings.read", "settings.write", "logs.read",
	}
	if got := AllPermissions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("AllPermissions() = %v, want %v", got, want)
	}
	for _, p := range want {
		if !ValidPermission(string(p)) {
			t.Errorf("ValidPermission(%q) = false, want true", p)
		}
	}
	for _, bad := range []string{"", "posts", "posts.delete", "orders.write", "logs.write", "agents.read", "agents.write", "POSTS.READ", " posts.read"} {
		if ValidPermission(bad) {
			t.Errorf("ValidPermission(%q) = true, want false", bad)
		}
	}
	resources := map[Permission]string{"posts.write": "posts", "orders.read": "orders", "logs.read": "logs", "settings.write": "settings"}
	for p, want := range resources {
		if got := p.Resource(); got != want {
			t.Errorf("Permission(%q).Resource() = %q, want %q", p, got, want)
		}
	}
}

func TestIntegrationGraceMinutesBounds(t *testing.T) {
	if MinGraceMinutes != 1 || MaxGraceMinutes != 1440 {
		t.Fatalf("grace bounds = %d..%d, want 1..1440", MinGraceMinutes, MaxGraceMinutes)
	}
	for n, want := range map[int]bool{-1: false, 0: false, 1: true, 30: true, 1440: true, 1441: false, 10000: false} {
		if got := ValidGraceMinutes(n); got != want {
			t.Errorf("ValidGraceMinutes(%d) = %v, want %v", n, got, want)
		}
	}
}
