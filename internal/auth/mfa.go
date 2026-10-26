package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrMFAInvalid         = errors.New("invalid, expired, consumed, or wrong-purpose MFA token or code")
	ErrMFALocked          = errors.New("MFA verification is locked")
	ErrTOTPAlreadyEnabled = errors.New("two-step verification is already enabled")
	ErrTOTPNotEnabled     = errors.New("two-step verification is not enabled")
	ErrPasswordInvalid    = errors.New("password is invalid")
)

const mfaPurpose = "mfa_login"

func mfaCredentialVersion(user core.User) string {
	mac := hmac.New(sha256.New, jwtSecret)
	_, _ = fmt.Fprintf(mac, "%d\x00%s\x00", user.ID, user.Password)
	if user.PasswordChangedAt == nil {
		_, _ = mac.Write([]byte("nil"))
	} else {
		_, _ = mac.Write([]byte(user.PasswordChangedAt.UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func newMFAToken(user core.User, now time.Time) (string, error) {
	id, err := newLoginID()
	if err != nil {
		return "", err
	}
	claims := jwt.MapClaims{
		"user_id": user.ID, "email": user.Email, "role": user.Role,
		"purpose": mfaPurpose, "credential_version": mfaCredentialVersion(user),
		"jti": id, "iat": now.Unix(), "exp": now.Add(mfaTokenLifetime).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecret)
}

func TOTPEnabled(userID uint) (bool, error) {
	if data.DB == nil || userID == 0 {
		return false, fmt.Errorf("database is not initialized or user ID is invalid")
	}
	var row core.UserTOTP
	err := data.DB.First(&row, "user_id = ?", userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return row.EnabledAt != nil, err
}

func EnrollTOTP(userID uint) (string, string, error) {
	if data.DB == nil || userID == 0 {
		return "", "", fmt.Errorf("database is not initialized or user ID is invalid")
	}
	key, err := loadOrCreateTOTPKey(secretFilePath())
	if err != nil {
		return "", "", err
	}
	secret := GenerateTOTPSecret()
	enc, err := encryptTOTPSecret(key, userID, secret)
	if err != nil {
		return "", "", err
	}
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		var user core.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
			return err
		}
		var old core.UserTOTP
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&old, "user_id = ?", userID).Error
		if err == nil && old.EnabledAt != nil {
			return ErrTOTPAlreadyEnabled
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		row := core.UserTOTP{UserID: userID, SecretEnc: enc}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoUpdates: clause.Assignments(map[string]any{"secret_enc": enc, "enabled_at": nil, "last_step": int64(0), "failed_count": 0, "locked_until": nil})}).Create(&row).Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", userID).Delete(&core.RecoveryCode{}).Error
	})
	if err != nil {
		return "", "", err
	}
	account := "user-" + strconv.FormatUint(uint64(userID), 10)
	var user core.User
	if data.DB.Select("username", "email").First(&user, userID).Error == nil {
		if user.Email != "" {
			account = user.Email
		} else if user.Username != "" {
			account = user.Username
		}
	}
	q := url.Values{"secret": {secret}, "issuer": {"Jelly CMS"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return secret, "otpauth://totp/" + url.PathEscape("Jelly CMS:"+account) + "?" + q.Encode(), nil
}

func VerifyTOTPEnrollment(userID uint, code string) ([]string, error) {
	return verifyEnrollment(userID, code)
}
func verifyEnrollment(userID uint, code string) ([]string, error) {
	return setUpRecoveryCodes(userID, code, false)
}
func ReplaceTOTPRecoveryCodes(userID uint, code string) ([]string, error) {
	return setUpRecoveryCodes(userID, code, true)
}
func setUpRecoveryCodes(userID uint, code string, enabled bool) ([]string, error) {
	if data.DB == nil || userID == 0 {
		return nil, fmt.Errorf("database is not initialized or user ID is invalid")
	}
	key, err := loadOrCreateTOTPKey(secretFilePath())
	if err != nil {
		return nil, err
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	var outcome error
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		var user core.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
			return err
		}
		var row core.UserTOTP
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "user_id = ?", userID).Error; err != nil {
			return err
		}
		if enabled && row.EnabledAt == nil {
			return ErrTOTPNotEnabled
		}
		if !enabled && row.EnabledAt != nil {
			return ErrTOTPAlreadyEnabled
		}
		if locked(row.LockedUntil, time.Now()) {
			outcome = ErrMFALocked
			return nil
		}
		secret, err := decryptTOTPSecret(key, userID, row.SecretEnc)
		if err != nil {
			return err
		}
		ok, step := VerifyTOTP(secret, code, time.Now(), row.LastStep)
		if !ok {
			failureErr := recordTOTPFailure(tx, &row)
			if !errors.Is(failureErr, ErrMFAInvalid) {
				return failureErr
			}
			outcome = failureErr
			return nil
		}
		now := time.Now().UTC()
		row.LastStep = step
		row.FailedCount = 0
		row.LockedUntil = nil
		if !enabled {
			row.EnabledAt = &now
		}
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&core.RecoveryCode{}).Error; err != nil {
			return err
		}
		for _, h := range hashes {
			if err := tx.Create(&core.RecoveryCode{UserID: userID, CodeHash: h}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if outcome != nil {
		return nil, outcome
	}
	return codes, nil
}

func DisableTOTP(userID uint, currentPassword, code, recoveryCode string) error {
	if data.DB == nil || userID == 0 {
		return fmt.Errorf("database is not initialized or user ID is invalid")
	}
	key, err := loadOrCreateTOTPKey(secretFilePath())
	if err != nil {
		return err
	}
	var outcome error
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		var user core.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
			return err
		}
		if user.Password == "" || bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(currentPassword)) != nil {
			return ErrPasswordInvalid
		}
		var row core.UserTOTP
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "user_id = ?", userID).Error; err != nil {
			return err
		}
		if row.EnabledAt == nil {
			return ErrTOTPNotEnabled
		}
		if locked(row.LockedUntil, time.Now()) {
			outcome = ErrMFALocked
			return nil
		}
		secret, err := decryptTOTPSecret(key, userID, row.SecretEnc)
		if err != nil {
			return err
		}
		ok, step := VerifyTOTP(secret, code, time.Now(), row.LastStep)
		if ok {
			row.LastStep = step
		} else if recoveryCode != "" {
			ok, err = consumeRecoveryCode(tx, userID, recoveryCode)
			if err != nil {
				return err
			}
		}
		if !ok {
			failureErr := recordTOTPFailure(tx, &row)
			if !errors.Is(failureErr, ErrMFAInvalid) {
				return failureErr
			}
			outcome = failureErr
			return nil
		}
		if err := tx.Delete(&row).Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", userID).Delete(&core.RecoveryCode{}).Error
	})
	if err != nil {
		return err
	}
	return outcome
}

func IssueMFAToken(userID uint) (string, error) {
	if data.DB == nil || userID == 0 {
		return "", fmt.Errorf("database is not initialized or user ID is invalid")
	}
	var row core.UserTOTP
	if err := data.DB.First(&row, "user_id = ?", userID).Error; err != nil {
		return "", err
	}
	if row.EnabledAt == nil {
		return "", ErrTOTPNotEnabled
	}
	var user core.User
	if err := data.DB.First(&user, userID).Error; err != nil {
		return "", err
	}
	if user.Status != "" && user.Status != "active" {
		return "", ErrMFAInvalid
	}
	return newMFAToken(user, time.Now().UTC())
}

// issueMFATokenForVerifiedUser issues a challenge only while the password and
// password-change cutoff still match the snapshot that passed bcrypt. Holding
// the user row lock through token signing orders the challenge before any
// concurrent password reset or revoke-all operation.
func issueMFATokenForVerifiedUser(verified core.User) (string, error) {
	if data.DB == nil || verified.ID == 0 {
		return "", fmt.Errorf("database is not initialized or user ID is invalid")
	}
	var challenge string
	err := data.DB.Transaction(func(tx *gorm.DB) error {
		var current core.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, verified.ID).Error; err != nil {
			return err
		}
		if (current.Status != "" && current.Status != "active") || current.Password != verified.Password || !sameNullableTime(current.PasswordChangedAt, verified.PasswordChangedAt) {
			return ErrMFAInvalid
		}
		var row core.UserTOTP
		if err := tx.First(&row, "user_id = ?", verified.ID).Error; err != nil {
			return err
		}
		if row.EnabledAt == nil {
			return ErrTOTPNotEnabled
		}
		var err error
		challenge, err = newMFAToken(current, time.Now().UTC())
		return err
	})
	return challenge, err
}

func CompleteMFALogin(c echo.Context, mfaToken, code, recoveryCode string) (string, *core.User, error) {
	claims, err := parseMFAToken(mfaToken)
	if err != nil {
		return "", nil, ErrMFAInvalid
	}
	issuedAt, err := claims.GetIssuedAt()
	if err != nil || issuedAt == nil {
		return "", nil, ErrMFAInvalid
	}
	u64, ok := claims["user_id"].(float64)
	if !ok || u64 <= 0 {
		return "", nil, ErrMFAInvalid
	}
	userID := uint(u64)
	credentialVersion, ok := claims["credential_version"].(string)
	if !ok || credentialVersion == "" {
		return "", nil, ErrMFAInvalid
	}
	loginID, ok := claims["jti"].(string)
	if !ok || !validLoginID(loginID) {
		return "", nil, ErrMFAInvalid
	}
	key, err := loadOrCreateTOTPKey(secretFilePath())
	if err != nil {
		return "", nil, err
	}
	var user core.User
	var outcome error
	now := time.Now().UTC()
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
			return err
		}
		// Bind the challenge to the exact credential snapshot. Comparing JWT iat
		// to password_changed_at is incorrect because iat has only second precision.
		if !hmac.Equal([]byte(credentialVersion), []byte(mfaCredentialVersion(user))) {
			outcome = ErrMFAInvalid
			return nil
		}
		if user.Status != "" && user.Status != "active" {
			outcome = ErrMFAInvalid
			return nil
		}
		var consumed core.RevokedLogin
		consumeErr := tx.First(&consumed, "id = ?", hashMFATokenID(loginID)).Error
		if consumeErr == nil {
			outcome = ErrMFAInvalid
			return nil
		}
		if !errors.Is(consumeErr, gorm.ErrRecordNotFound) {
			return consumeErr
		}
		var row core.UserTOTP
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "user_id = ?", userID).Error; err != nil {
			return err
		}
		if row.EnabledAt == nil {
			outcome = ErrMFAInvalid
			return nil
		}
		if locked(row.LockedUntil, now) {
			outcome = ErrMFALocked
			return nil
		}
		valid := false
		if recoveryCode != "" {
			var e error
			valid, e = consumeRecoveryCode(tx, userID, recoveryCode)
			if e != nil {
				return e
			}
		} else {
			secret, e := decryptTOTPSecret(key, userID, row.SecretEnc)
			if e != nil {
				return e
			}
			var step int64
			valid, step = VerifyTOTP(secret, code, now, row.LastStep)
			if valid {
				row.LastStep = step
			}
		}
		if !valid {
			failureErr := recordTOTPFailure(tx, &row)
			if !errors.Is(failureErr, ErrMFAInvalid) {
				return failureErr
			}
			outcome = failureErr
			return nil
		}
		row.FailedCount = 0
		row.LockedUntil = nil
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		if err := tx.Create(&core.RevokedLogin{ID: hashMFATokenID(loginID), ExpiresAt: now.Add(mfaTokenLifetime)}).Error; err != nil {
			return ErrMFAInvalid
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	if outcome != nil {
		return "", nil, outcome
	}
	jwtToken, err := issueLogin(c, user)
	if err != nil {
		return "", nil, err
	}
	return jwtToken, &user, nil
}

func hashMFATokenID(id string) string {
	sum := sha256.Sum256([]byte("mfa_login:" + id))
	return hex.EncodeToString(sum[:])
}

func parseMFAToken(raw string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, ErrMFAInvalid
		}
		return jwtSecret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil || token == nil || !token.Valid {
		return nil, ErrMFAInvalid
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || claims["purpose"] != mfaPurpose {
		return nil, ErrMFAInvalid
	}
	return claims, nil
}

func encryptTOTPSecret(key []byte, userID uint, secret string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return append(nonce, gcm.Seal(nil, nonce, []byte(secret), []byte(strconv.FormatUint(uint64(userID), 10)))...), nil
}
func decryptTOTPSecret(key []byte, userID uint, blob []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(blob) < gcm.NonceSize() {
		return "", fmt.Errorf("invalid TOTP ciphertext")
	}
	plain, err := gcm.Open(nil, blob[:gcm.NonceSize()], blob[gcm.NonceSize():], []byte(strconv.FormatUint(uint64(userID), 10)))
	return string(plain), err
}
func newRecoveryCodes() ([]string, []string, error) {
	codes := make([]string, 10)
	hashes := make([]string, 10)
	for i := range codes {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}
		codes[i] = strings.ToUpper(hex.EncodeToString(b))
		sum := sha256.Sum256([]byte(codes[i]))
		hashes[i] = hex.EncodeToString(sum[:])
	}
	return codes, hashes, nil
}
func consumeRecoveryCode(tx *gorm.DB, userID uint, code string) (bool, error) {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	sum := sha256.Sum256([]byte(normalized))
	hash := hex.EncodeToString(sum[:])
	var row core.RecoveryCode
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND code_hash = ? AND used_at IS NULL", userID, hash).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	return true, tx.Model(&row).Update("used_at", now).Error
}
func locked(until *time.Time, now time.Time) bool { return until != nil && until.After(now) }
func recordTOTPFailure(tx *gorm.DB, row *core.UserTOTP) error {
	row.FailedCount++
	if row.FailedCount >= totpMaxFailures {
		until := time.Now().UTC().Add(totpLockDuration)
		row.LockedUntil = &until
		row.FailedCount = 0
	}
	if err := tx.Save(row).Error; err != nil {
		return err
	}
	// The attempt that reaches the threshold is still reported as invalid;
	// subsequent attempts see LockedUntil before checking a code.
	return ErrMFAInvalid
}
