package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/golang-jwt/jwt/v5"
)

const (
	oauthRecoveryPurpose       = "oauth-recovery"
	oauthRecoveryKeyDerivation = "jelly-oauth-recovery-v1"
	oauthRecoveryIssuer        = "jelly-cms"
	oauthRecoveryAudience      = "jelly-oauth-recovery"
	oauthRecoveryLifetime      = 10 * time.Minute
)

// InitOAuthRecovery loads the effective JWT secret without creating or changing
// the site's auth-secrets.json. Recovery grants derive their key from this same
// secret as application JWTs.
func InitOAuthRecovery() error {
	jwtSecret = nil

	secret := []byte(os.Getenv("JWT_SECRET"))
	if len(secret) == 0 {
		_, fileJWT, err := readSecretsFile(secretFilePath())
		if err != nil {
			return fmt.Errorf("load OAuth recovery key")
		}
		secret = fileJWT
	}

	jwtSecret = append([]byte(nil), secret...)
	return nil
}

// IssueOAuthRecoveryGrant returns a short-lived grant only when the target is
// still an eligible legacy OAuth account for the requested provider.
func IssueOAuthRecoveryGrant(ctx context.Context, userID uint, provider string) (string, time.Time, error) {
	if ctx == nil {
		return "", time.Time{}, fmt.Errorf("context is required")
	}
	if userID == 0 || (provider != "google" && provider != "github") {
		return "", time.Time{}, fmt.Errorf("invalid recovery target")
	}
	if err := ctx.Err(); err != nil {
		return "", time.Time{}, err
	}
	if data.DB == nil {
		return "", time.Time{}, fmt.Errorf("database is not initialized")
	}

	var eligible int
	row := data.DB.WithContext(ctx).Raw(`
		SELECT 1
		FROM users AS u
		WHERE u.id = ?
		  AND u.provider = ?
		  AND u.provider IN ('google', 'github')
		  AND NOT (u.provider = 'local' AND COALESCE(u.password, '') <> '')
		  AND NOT EXISTS (
			  SELECT 1
			  FROM oauth_identities AS identity
			  WHERE identity.user_id = u.id AND identity.deleted_at IS NULL
		  )
		LIMIT 1`, userID, provider).Row()
	if err := row.Scan(&eligible); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", time.Time{}, fmt.Errorf("recovery target is not eligible")
		}
		return "", time.Time{}, fmt.Errorf("unable to verify recovery target")
	}
	if err := ctx.Err(); err != nil {
		return "", time.Time{}, err
	}

	key := currentOAuthRecoveryKey()
	if len(key) == 0 {
		return "", time.Time{}, fmt.Errorf("OAuth recovery key is not initialized")
	}
	loginID, err := newLoginID()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("unable to create recovery identifier")
	}

	issuedAt := time.Now().UTC().Truncate(time.Second)
	expiresAt := issuedAt.Add(oauthRecoveryLifetime)
	claims := jwt.MapClaims{
		"purpose":  oauthRecoveryPurpose,
		"sub":      strconv.FormatUint(uint64(userID), 10),
		"provider": provider,
		"jti":      loginID,
		"iat":      issuedAt.Unix(),
		"exp":      expiresAt.Unix(),
		"iss":      oauthRecoveryIssuer,
		"aud":      oauthRecoveryAudience,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	grant, err := token.SignedString(key)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("unable to sign recovery grant")
	}
	return grant, expiresAt, nil
}

func currentOAuthRecoveryKey() []byte {
	if len(jwtSecret) == 0 {
		return nil
	}
	mac := hmac.New(sha256.New, jwtSecret)
	_, _ = mac.Write([]byte(oauthRecoveryKeyDerivation))
	return mac.Sum(nil)
}
