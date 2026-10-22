package auth_test

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/golang-jwt/jwt/v5"
)

const (
	testJWTSecret     = "jwt-validation-test-secret"
	testSessionSecret = "session-validation-test-secret"
)

func TestJWTValidation(t *testing.T) {
	t.Setenv("SESSION_SECRET", testSessionSecret)
	t.Setenv("JWT_SECRET", testJWTSecret)

	previousDB := data.DB
	data.DB = nil
	t.Cleanup(func() { data.DB = previousDB })
	auth.Init()
	if data.DB != nil {
		t.Fatal("auth.Init unexpectedly initialized the database")
	}

	t.Run("generated token accepts positive user ID", func(t *testing.T) {
		const userID = uint(73)
		token, err := auth.GenerateJWT(userID, "reader@example.test", "author")
		if err != nil {
			t.Fatalf("GenerateJWT() error = %v", err)
		}

		claims, err := auth.ValidateJWT(token)
		if err != nil {
			t.Fatalf("ValidateJWT(GenerateJWT token) error = %v", err)
		}
		if got, ok := claims["user_id"].(float64); !ok || got != float64(userID) {
			t.Fatalf("ValidateJWT() user_id = %#v, want %d", claims["user_id"], userID)
		}
	})

	t.Run("GenerateJWT rejects zero and unsafe user IDs", func(t *testing.T) {
		t.Run("zero", func(t *testing.T) {
			token, err := auth.GenerateJWT(0, "reader@example.test", "author")
			if err == nil {
				t.Fatal("GenerateJWT(0) error = nil, want rejection")
			}
			if token != "" {
				t.Fatalf("GenerateJWT(0) token = %q, want empty token", token)
			}
		})

		t.Run("above safe integer range", func(t *testing.T) {
			if strconv.IntSize < 64 {
				t.Skip("uint cannot represent a value above the safe integer range on this platform")
			}
			unsafeID64 := uint64(9007199254740991)
			unsafeID := uint(unsafeID64 + 1)
			token, err := auth.GenerateJWT(unsafeID, "reader@example.test", "author")
			if err == nil {
				t.Fatalf("GenerateJWT(%d) error = nil, want rejection", unsafeID)
			}
			if token != "" {
				t.Fatalf("GenerateJWT(%d) token = %q, want empty token", unsafeID, token)
			}
		})
	})

	t.Run("expiration is required and valid", func(t *testing.T) {
		tests := []struct {
			name   string
			claims jwt.MapClaims
		}{
			{name: "missing exp", claims: jwt.MapClaims{"user_id": 1}},
			{name: "expired exp", claims: jwt.MapClaims{"user_id": 1, "exp": time.Unix(1, 0).Unix()}},
			{name: "invalid exp type", claims: jwt.MapClaims{"user_id": 1, "exp": "tomorrow"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				token := signJWT(t, jwt.SigningMethodHS256, tt.claims, []byte(testJWTSecret))
				assertJWTRejected(t, token)
			})
		}
	})

	t.Run("only HS256 signed with the configured secret is accepted", func(t *testing.T) {
		claims := validJWTClaims(17)
		tests := []struct {
			name   string
			method jwt.SigningMethod
			key    []byte
		}{
			{name: "wrong signature", method: jwt.SigningMethodHS256, key: []byte("different-test-secret")},
			{name: "HS384", method: jwt.SigningMethodHS384, key: []byte(testJWTSecret)},
			{name: "HS512", method: jwt.SigningMethodHS512, key: []byte(testJWTSecret)},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				token := signJWT(t, tt.method, claims, tt.key)
				assertJWTRejected(t, token)
			})
		}

		t.Run("none", func(t *testing.T) {
			token := signJWT(t, jwt.SigningMethodNone, claims, jwt.UnsafeAllowNoneSignatureType)
			assertJWTRejected(t, token)
		})
	})

	t.Run("user_id must be a safely representable positive integer", func(t *testing.T) {
		maxAcceptedID := uint64(9007199254740991)
		maxUint := uint64(^uint(0))
		if maxUint < maxAcceptedID {
			maxAcceptedID = maxUint
		}
		tests := []struct {
			name       string
			includeID  bool
			userID     any
			wantAccept bool
		}{
			{name: "missing", includeID: false},
			{name: "zero", includeID: true, userID: 0},
			{name: "negative", includeID: true, userID: -1},
			{name: "fraction", includeID: true, userID: 1.5},
			{name: "string", includeID: true, userID: "17"},
			{name: "fraction near safe integer limit", includeID: true, userID: json.Number("9007199254740990.5")},
			{name: "fraction that rounds to integer", includeID: true, userID: json.Number("1.00000000000000001")},
			{name: "above safe integer range", includeID: true, userID: int64(9007199254740992)},
			{name: "above platform-safe maximum", includeID: true, userID: int64(maxAcceptedID + 1)},
			{name: "one", includeID: true, userID: 1, wantAccept: true},
			{name: "largest platform-safe integer", includeID: true, userID: int64(maxAcceptedID), wantAccept: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				claims := validJWTClaims(21)
				if tt.includeID {
					claims["user_id"] = tt.userID
				} else {
					delete(claims, "user_id")
				}
				token := signJWT(t, jwt.SigningMethodHS256, claims, []byte(testJWTSecret))
				gotClaims, err := auth.ValidateJWT(token)
				if tt.wantAccept {
					if err != nil {
						t.Fatalf("ValidateJWT() error = %v, want accepted token", err)
					}
					if gotClaims == nil {
						t.Fatal("ValidateJWT() claims = nil for accepted token")
					}
					return
				}
				if err == nil {
					t.Fatalf("ValidateJWT() claims = %#v, want rejection", gotClaims)
				}
				if gotClaims != nil {
					t.Fatalf("ValidateJWT() claims = %#v with error %v, want nil claims", gotClaims, err)
				}
			})
		}
	})
}

func validJWTClaims(userID any) jwt.MapClaims {
	return jwt.MapClaims{
		"user_id": userID,
		"exp":     time.Now().Add(time.Hour).Unix(),
	}
}

func signJWT(t *testing.T, method jwt.SigningMethod, claims jwt.MapClaims, key any) string {
	t.Helper()
	token, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatalf("sign %s test token: %v", method.Alg(), err)
	}
	return token
}

func assertJWTRejected(t *testing.T, token string) {
	t.Helper()
	claims, err := auth.ValidateJWT(token)
	if err == nil {
		t.Fatalf("ValidateJWT() claims = %#v, want rejection", claims)
	}
	if claims != nil {
		t.Fatalf("ValidateJWT() claims = %#v with error %v, want nil claims", claims, err)
	}
}
