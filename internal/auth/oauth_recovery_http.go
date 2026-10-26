package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxOAuthRecoveryRequestBytes = 8 << 10

var (
	errInvalidOAuthRecoveryGrant  = errors.New("invalid OAuth recovery grant")
	errExpiredOAuthRecoveryGrant  = errors.New("expired OAuth recovery grant")
	errConsumedOAuthRecoveryGrant = errors.New("consumed OAuth recovery grant")
	errOAuthRecoveryConflict      = errors.New("OAuth recovery target conflict")
	errOAuthRecoveryUnavailable   = errors.New("OAuth recovery database unavailable")
)

type oauthRecoveryIntent struct {
	Purpose  string `json:"purpose"`
	Provider string `json:"provider"`
	State    string `json:"state"`
	Grant    string `json:"grant"`
}

type oauthRecoveryGrant struct {
	UserID    uint
	Provider  string
	JTI       string
	IssuedAt  int64
	ExpiresAt int64
}

// HandleOAuthRecovery starts a provider authorization using only a validated
// recovery grant supplied in the POST body.
func HandleOAuthRecovery(c echo.Context) error {
	grantText, err := recoveryGrantFromRequest(c)
	if err != nil {
		return c.String(http.StatusBadRequest, "Invalid recovery authorization")
	}
	grant, err := parseOAuthRecoveryGrant(grantText)
	if err != nil {
		return c.String(http.StatusBadRequest, "Invalid recovery authorization")
	}
	c.Set(oauthRecoveryTargetContextKey, grant.UserID)
	c.Set(oauthRecoveryProviderContextKey, grant.Provider)
	if err := checkOAuthRecoveryEntry(c.Request().Context(), grant); err != nil {
		return c.String(oauthRecoveryErrorStatus(err), oauthRecoveryErrorMessage(err))
	}
	provider, err := goth.GetProvider(grant.Provider)
	if err != nil {
		return c.String(http.StatusBadGateway, "OAuth provider unavailable")
	}
	if gothic.Store == nil {
		return c.String(http.StatusServiceUnavailable, "OAuth service unavailable")
	}

	stateBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, stateBytes); err != nil {
		return c.String(http.StatusServiceUnavailable, "OAuth service unavailable")
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)
	gothSession, err := provider.BeginAuth(state)
	if err != nil {
		return c.String(http.StatusBadGateway, "OAuth provider unavailable")
	}
	authURL, err := gothSession.GetAuthURL()
	if err != nil {
		return c.String(http.StatusBadGateway, "OAuth provider unavailable")
	}

	providerValue, err := gzipOAuthSessionValue(gothSession.Marshal())
	if err != nil {
		return c.String(http.StatusServiceUnavailable, "OAuth service unavailable")
	}
	intentValue, err := json.Marshal(oauthRecoveryIntent{
		Purpose: oauthIntentRecover, Provider: grant.Provider, State: state, Grant: grantText,
	})
	if err != nil {
		return c.String(http.StatusServiceUnavailable, "OAuth service unavailable")
	}
	gothRequest := c.Request().Clone(c.Request().Context())
	gothRequest.Header = c.Request().Header.Clone()
	gothRequest.Header.Del("Cookie")
	signed, err := gothic.Store.New(gothRequest, gothic.SessionName)
	if err != nil {
		return c.String(http.StatusServiceUnavailable, "OAuth service unavailable")
	}
	signed.Values = map[interface{}]interface{}{
		grant.Provider: providerValue,
		oauthIntentKey: string(intentValue),
	}
	if signed.Options == nil {
		signed.Options = &sessions.Options{Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode}
	}
	signed.Options.MaxAge = oauthStateLifetimeSeconds
	if err := signed.Save(gothRequest, c.Response()); err != nil {
		return c.String(http.StatusServiceUnavailable, "OAuth service unavailable")
	}

	if c.Request().Header.Get("Accept") == echo.MIMEApplicationJSON {
		return c.JSON(http.StatusOK, map[string]string{"auth_url": authURL})
	}
	return c.Redirect(http.StatusSeeOther, authURL)
}

func recoveryGrantFromRequest(c echo.Context) (string, error) {
	mediaType, _, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
	if err != nil {
		return "", errInvalidOAuthRecoveryGrant
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Response().Writer, c.Request().Body, maxOAuthRecoveryRequestBytes))
	if err != nil {
		return "", errInvalidOAuthRecoveryGrant
	}
	var grant string
	switch mediaType {
	case echo.MIMEApplicationJSON:
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		var request struct {
			Grant string `json:"grant"`
		}
		if err := decoder.Decode(&request); err != nil {
			return "", errInvalidOAuthRecoveryGrant
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return "", errInvalidOAuthRecoveryGrant
		}
		grant = request.Grant
	case echo.MIMEApplicationForm:
		values, err := url.ParseQuery(string(body))
		if err != nil || len(values) != 1 || len(values["grant"]) != 1 {
			return "", errInvalidOAuthRecoveryGrant
		}
		grant = values.Get("grant")
	default:
		return "", errInvalidOAuthRecoveryGrant
	}
	if grant == "" || grant != strings.TrimSpace(grant) {
		return "", errInvalidOAuthRecoveryGrant
	}
	return grant, nil
}

func parseOAuthRecoveryGrant(raw string) (oauthRecoveryGrant, error) {
	if raw == "" || len(raw) > maxOAuthRecoveryRequestBytes {
		return oauthRecoveryGrant{}, errInvalidOAuthRecoveryGrant
	}
	key := currentOAuthRecoveryKey()
	if len(key) == 0 {
		return oauthRecoveryGrant{}, errInvalidOAuthRecoveryGrant
	}
	token, err := jwt.Parse(raw, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errInvalidOAuthRecoveryGrant
		}
		return key, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithJSONNumber())
	if err != nil || token == nil || !token.Valid {
		return oauthRecoveryGrant{}, errInvalidOAuthRecoveryGrant
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || claims["purpose"] != oauthRecoveryPurpose || claims["iss"] != oauthRecoveryIssuer || claims["aud"] != oauthRecoveryAudience {
		return oauthRecoveryGrant{}, errInvalidOAuthRecoveryGrant
	}
	if _, ok := claims["user_id"]; ok {
		return oauthRecoveryGrant{}, errInvalidOAuthRecoveryGrant
	}
	if _, ok := claims["role"]; ok {
		return oauthRecoveryGrant{}, errInvalidOAuthRecoveryGrant
	}
	subject, ok := claims["sub"].(string)
	if !ok {
		return oauthRecoveryGrant{}, errInvalidOAuthRecoveryGrant
	}
	userID, err := strconv.ParseUint(subject, 10, strconv.IntSize)
	if err != nil || userID == 0 || strconv.FormatUint(userID, 10) != subject {
		return oauthRecoveryGrant{}, errInvalidOAuthRecoveryGrant
	}
	provider, ok := claims["provider"].(string)
	if !ok || (provider != "google" && provider != "github") {
		return oauthRecoveryGrant{}, errInvalidOAuthRecoveryGrant
	}
	jti, ok := claims["jti"].(string)
	if !ok || !validLoginID(jti) {
		return oauthRecoveryGrant{}, errInvalidOAuthRecoveryGrant
	}
	issuedAt, ok := recoveryNumericDate(claims["iat"])
	if !ok {
		return oauthRecoveryGrant{}, errInvalidOAuthRecoveryGrant
	}
	expiresAt, ok := recoveryNumericDate(claims["exp"])
	if !ok {
		return oauthRecoveryGrant{}, errInvalidOAuthRecoveryGrant
	}
	if expiresAt <= issuedAt || expiresAt-issuedAt > int64(oauthRecoveryLifetime/time.Second) || issuedAt > time.Now().Unix() {
		return oauthRecoveryGrant{}, errInvalidOAuthRecoveryGrant
	}
	grant := oauthRecoveryGrant{UserID: uint(userID), Provider: provider, JTI: jti, IssuedAt: issuedAt, ExpiresAt: expiresAt}
	if !oauthRecoveryGrantUnexpired(grant) {
		return oauthRecoveryGrant{}, errExpiredOAuthRecoveryGrant
	}
	return grant, nil
}

func recoveryNumericDate(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := number.Int64()
	return parsed, err == nil
}

func oauthRecoveryGrantUnexpired(grant oauthRecoveryGrant) bool {
	return time.Now().Unix() < grant.ExpiresAt
}

func validOAuthRecoverySubject(subject string) bool {
	return utf8.ValidString(subject) && strings.TrimSpace(subject) != "" && utf8.RuneCountInString(subject) <= oauthProviderMax
}

func readOAuthRecoveryIntent(session *sessions.Session) (oauthRecoveryIntent, error) {
	value, present := session.Values[oauthIntentKey]
	if !present {
		return oauthRecoveryIntent{}, errInvalidOAuthRecoveryGrant
	}
	raw, ok := value.(string)
	if !ok {
		return oauthRecoveryIntent{}, errInvalidOAuthRecoveryGrant
	}
	return parseOAuthRecoveryIntent(raw)
}

func parseOAuthRecoveryIntent(raw string) (oauthRecoveryIntent, error) {
	var intent oauthRecoveryIntent
	if err := json.Unmarshal([]byte(raw), &intent); err != nil || intent.Purpose != oauthIntentRecover ||
		(intent.Provider != "google" && intent.Provider != "github") || intent.State == "" || intent.Grant == "" {
		return oauthRecoveryIntent{}, errInvalidOAuthRecoveryGrant
	}
	return intent, nil
}

func checkOAuthRecoveryEntry(ctx context.Context, grant oauthRecoveryGrant) error {
	if data.DB == nil {
		return errOAuthRecoveryUnavailable
	}
	db := oauthIdentityDB(ctx)
	var consumed int64
	if err := db.Model(&core.RevokedLogin{}).Where("id = ?", oauthRecoveryDigest(grant.JTI)).Count(&consumed).Error; err != nil {
		return errOAuthRecoveryUnavailable
	}
	if consumed != 0 {
		return errConsumedOAuthRecoveryGrant
	}
	if err := checkOAuthRecoveryEligibilityDB(db, grant); err != nil {
		return err
	}
	if !oauthRecoveryGrantUnexpired(grant) {
		return errExpiredOAuthRecoveryGrant
	}
	return nil
}

func checkOAuthRecoveryEligibility(ctx context.Context, grant oauthRecoveryGrant) error {
	if data.DB == nil {
		return errOAuthRecoveryUnavailable
	}
	return checkOAuthRecoveryEligibilityDB(oauthIdentityDB(ctx), grant)
}

func checkOAuthRecoveryEligibilityDB(db *gorm.DB, grant oauthRecoveryGrant) error {
	var account struct {
		Provider string
		Password string
	}
	err := db.Model(&core.User{}).Select("provider", "password").Where("id = ?", grant.UserID).Take(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errOAuthRecoveryConflict
	}
	if err != nil {
		return errOAuthRecoveryUnavailable
	}
	if account.Provider != grant.Provider || account.Password != "" {
		return errOAuthRecoveryConflict
	}
	var identities int64
	if err := db.Model(&core.OAuthIdentity{}).Where("user_id = ? AND deleted_at IS NULL", grant.UserID).Count(&identities).Error; err != nil {
		return errOAuthRecoveryUnavailable
	}
	if identities != 0 {
		return errOAuthRecoveryConflict
	}
	return nil
}

func completeOAuthRecovery(ctx context.Context, grant oauthRecoveryGrant, profile goth.User) (core.User, error) {
	if data.DB == nil {
		return core.User{}, errOAuthRecoveryUnavailable
	}
	if profile.Provider != grant.Provider || !validOAuthRecoverySubject(profile.UserID) {
		return core.User{}, errInvalidOAuthRecoveryGrant
	}
	var target core.User
	err := oauthIdentityDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&target, grant.UserID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errOAuthRecoveryConflict
			}
			return err
		}
		if !oauthRecoveryGrantUnexpired(grant) {
			return errExpiredOAuthRecoveryGrant
		}
		if target.Provider != grant.Provider || target.Password != "" {
			return errOAuthRecoveryConflict
		}
		if err := tx.Exec("SELECT set_config('jelly.actor_id', '', true)").Error; err != nil {
			return err
		}
		var identities int64
		if err := tx.Model(&core.OAuthIdentity{}).
			Where("deleted_at IS NULL AND (user_id = ? OR (provider = ? AND subject = ?))", target.ID, grant.Provider, profile.UserID).
			Count(&identities).Error; err != nil {
			return err
		}
		var consumed int64
		if err := tx.Model(&core.RevokedLogin{}).Where("id = ?", oauthRecoveryDigest(grant.JTI)).Count(&consumed).Error; err != nil {
			return err
		}
		if !oauthRecoveryGrantUnexpired(grant) {
			return errExpiredOAuthRecoveryGrant
		}
		if identities != 0 {
			return errOAuthRecoveryConflict
		}
		if consumed != 0 {
			return errConsumedOAuthRecoveryGrant
		}

		identity := core.OAuthIdentity{UserID: target.ID, Provider: grant.Provider, Subject: profile.UserID}
		if err := tx.Create(&identity).Error; err != nil {
			if isUniqueViolation(err) {
				return errOAuthRecoveryConflict
			}
			return err
		}
		if !oauthRecoveryGrantUnexpired(grant) {
			return errExpiredOAuthRecoveryGrant
		}
		now := time.Now().UTC()
		if err := tx.Where("expires_at <= ?", now).Delete(&core.RevokedLogin{}).Error; err != nil {
			return err
		}
		if !oauthRecoveryGrantUnexpired(grant) {
			return errExpiredOAuthRecoveryGrant
		}
		row := core.RevokedLogin{
			ID:        oauthRecoveryDigest(grant.JTI),
			ExpiresAt: time.Unix(grant.ExpiresAt, 0).UTC().Add(time.Second),
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errConsumedOAuthRecoveryGrant
		}
		if !oauthRecoveryGrantUnexpired(grant) {
			return errExpiredOAuthRecoveryGrant
		}
		return nil
	})
	if errors.Is(err, errOAuthRecoveryConflict) || errors.Is(err, errConsumedOAuthRecoveryGrant) || isUniqueViolation(err) {
		return core.User{}, err
	}
	if errors.Is(err, errExpiredOAuthRecoveryGrant) {
		return core.User{}, errExpiredOAuthRecoveryGrant
	}
	if err != nil {
		return core.User{}, errOAuthRecoveryUnavailable
	}
	return target, nil
}

func oauthRecoveryDigest(jti string) string {
	digest := sha256.Sum256([]byte("oauth-recovery\x00" + jti))
	return hex.EncodeToString(digest[:])
}

func oauthRecoveryErrorStatus(err error) int {
	switch {
	case errors.Is(err, errInvalidOAuthRecoveryGrant), errors.Is(err, errExpiredOAuthRecoveryGrant):
		return http.StatusBadRequest
	case errors.Is(err, errOAuthRecoveryConflict), errors.Is(err, errConsumedOAuthRecoveryGrant):
		return http.StatusConflict
	default:
		return http.StatusServiceUnavailable
	}
}

func oauthRecoveryErrorMessage(err error) string {
	switch oauthRecoveryErrorStatus(err) {
	case http.StatusBadRequest:
		return "Invalid recovery authorization"
	case http.StatusConflict:
		return "Recovery authorization is no longer valid"
	default:
		return "Service unavailable"
	}
}
