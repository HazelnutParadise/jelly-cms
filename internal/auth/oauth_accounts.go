package auth

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/log"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	oauthIntentKey     = "jelly-cms-intent"
	oauthIntentLink    = "link"
	oauthIntentRecover = "recover"
	oauthProviderMax   = 255
)

type oauthLinkIntent struct {
	Purpose   string          `json:"purpose"`
	Provider  string          `json:"provider"`
	State     string          `json:"state"`
	UserID    uint            `json:"user_id"`
	LoginID   string          `json:"login_id"`
	Source    principalSource `json:"source"`
	ExpiresAt int64           `json:"expires_at"`
}

func auditOAuthIdentityAction(action string, next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		err := next(c)
		status := c.Response().Status
		if status == 0 {
			if err != nil {
				status = http.StatusInternalServerError
			} else {
				status = http.StatusOK
			}
		}
		recordOAuthIdentityAction(c, action, c.Param("provider"), status, authenticatedActorID(c))
		return err
	}
}

func authenticatedActorID(c echo.Context) any {
	user, ok := GetUserFromContext(c)
	if !ok || user == nil {
		return nil
	}
	return user.ID
}

func recordOAuthIdentityAction(c echo.Context, action, provider string, status int, actorID any) {
	if c == nil || c.Logger() == nil {
		return
	}
	result := "success"
	if status >= http.StatusBadRequest {
		result = "failure"
	}
	requestIDBytes := make([]byte, 16)
	requestID := ""
	if _, err := rand.Read(requestIDBytes); err == nil {
		requestID = hex.EncodeToString(requestIDBytes)
	} else {
		requestID = strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	defer func() { _ = recover() }()
	c.Logger().Printj(log.JSON{
		"event":      "oauth_identity",
		"action":     action,
		"result":     result,
		"status":     status,
		"actor_id":   actorID,
		"provider":   provider,
		"request_id": requestID,
	})
}

const oauthRecoveryTargetContextKey = "jelly-cms-oauth-recovery-target"
const oauthRecoveryProviderContextKey = "jelly-cms-oauth-recovery-provider"

func auditOAuthRecoveryAction(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		err := next(c)
		status := c.Response().Status
		if status == 0 {
			status = http.StatusInternalServerError
		}
		provider, _ := c.Get(oauthRecoveryProviderContextKey).(string)
		targetID := c.Get(oauthRecoveryTargetContextKey)
		recordOAuthRecoveryAction(c, "start", provider, targetID, status)
		return err
	}
}

func recordOAuthRecoveryAction(c echo.Context, action, provider string, targetID any, status int) {
	if c == nil || c.Logger() == nil {
		return
	}
	result := "success"
	if status >= http.StatusBadRequest {
		result = "failure"
	}
	requestIDBytes := make([]byte, 16)
	requestID := ""
	if _, err := rand.Read(requestIDBytes); err == nil {
		requestID = hex.EncodeToString(requestIDBytes)
	} else {
		requestID = strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	defer func() { _ = recover() }()
	c.Logger().Printj(log.JSON{
		"event":      "oauth_identity",
		"action":     action,
		"result":     result,
		"status":     status,
		"actor_id":   nil,
		"target_id":  targetID,
		"provider":   provider,
		"request_id": requestID,
	})
}

// HandleIdentityList returns only the caller's login-method status.
func HandleIdentityList(c echo.Context) error {
	user, ok := GetUserFromContext(c)
	if !ok || user == nil || data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	var current core.User
	db := oauthIdentityDB(c.Request().Context())
	if err := db.First(&current, user.ID).Error; err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	type providerState struct {
		Provider string `json:"provider"`
		Enabled  bool   `json:"enabled"`
		Linked   bool   `json:"linked"`
	}
	providers := make([]providerState, 0, 2)
	for _, name := range []string{"google", "github"} {
		_, enabledErr := goth.GetProvider(name)
		var count int64
		if err := db.Model(&core.OAuthIdentity{}).Where("user_id = ? AND provider = ?", current.ID, name).Count(&count).Error; err != nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
		}
		providers = append(providers, providerState{Provider: name, Enabled: enabledErr == nil, Linked: count > 0})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"local_login": current.Provider == "local" && current.Password != "",
		"providers":   providers,
	})
}

// HandleIdentityLinkStart creates a signed, single-provider OAuth intent.
func HandleIdentityLinkStart(c echo.Context) error {
	if headerPresent(c.Request().Header, "Authorization") {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Cookie login required"})
	}
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	principal, err := resolvePrincipal(c, data.DB, true)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
	}
	req, provider, _, err := oauthRequestForPath(c.Request(), c.Param("provider"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid OAuth provider"})
	}
	stateBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, stateBytes); err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)
	gothSession, err := provider.BeginAuth(state)
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "OAuth provider unavailable"})
	}
	authURL, err := gothSession.GetAuthURL()
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "OAuth provider unavailable"})
	}
	intent := oauthLinkIntent{
		Purpose: oauthIntentLink, Provider: provider.Name(), State: state,
		UserID: principal.User.ID, LoginID: principal.LoginID, Source: principal.Source,
		ExpiresAt: principal.ExpiresAt.Unix(),
	}
	providerValue, err := gzipOAuthSessionValue(gothSession.Marshal())
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	intentValue, err := json.Marshal(intent)
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	gothRequest := req.Clone(req.Context())
	gothRequest.Header = req.Header.Clone()
	gothRequest.Header.Del("Cookie")
	signed, err := gothic.Store.New(gothRequest, gothic.SessionName)
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	signed.Values = map[interface{}]interface{}{provider.Name(): providerValue, oauthIntentKey: string(intentValue)}
	if signed.Options == nil {
		signed.Options = &sessions.Options{Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode}
	}
	signed.Options.MaxAge = oauthStateLifetimeSeconds
	if err := signed.Save(gothRequest, c.Response()); err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	return c.JSON(http.StatusOK, map[string]string{"auth_url": authURL})
}

// HandleIdentityUnlink soft-deletes a provider mapping while retaining another login method.
func HandleIdentityUnlink(c echo.Context) error {
	provider := c.Param("provider")
	if provider != "google" && provider != "github" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid OAuth provider"})
	}
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	principal, err := resolvePrincipal(c, data.DB, true)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
	}
	err = oauthIdentityDB(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		var locked core.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, principal.User.ID).Error; err != nil {
			return err
		}
		if _, err := revalidatePrincipal(tx, c, oauthLinkIntent{UserID: principal.User.ID, LoginID: principal.LoginID, Source: principal.Source, ExpiresAt: principal.ExpiresAt.Unix()}); err != nil {
			return errOAuthLinkPrincipalInvalid
		}
		var identity core.OAuthIdentity
		err := tx.Where("user_id = ? AND provider = ?", locked.ID, provider).First(&identity).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if _, err := revalidatePrincipal(tx, c, principalIntent(principal)); err != nil {
				return errOAuthLinkPrincipalInvalid
			}
			return nil
		}
		if err != nil {
			return err
		}
		ways := 0
		if locked.Provider == "local" && locked.Password != "" {
			ways++
		}
		var others []core.OAuthIdentity
		if err := tx.Where("user_id = ? AND provider <> ?", locked.ID, provider).Find(&others).Error; err != nil {
			return err
		}
		for _, other := range others {
			if _, err := goth.GetProvider(other.Provider); err == nil {
				ways++
			}
		}
		if _, err := revalidatePrincipal(tx, c, principalIntent(principal)); err != nil {
			return errOAuthLinkPrincipalInvalid
		}
		if ways == 0 {
			return errOAuthLastLoginMethod
		}
		if err := tx.Exec("SELECT set_config('jelly.actor_id', ?, true)", strconv.FormatUint(uint64(locked.ID), 10)).Error; err != nil {
			return err
		}
		if _, err := revalidatePrincipal(tx, c, principalIntent(principal)); err != nil {
			return errOAuthLinkPrincipalInvalid
		}
		return tx.Delete(&identity).Error
	})
	if errors.Is(err, errOAuthLastLoginMethod) {
		return c.JSON(http.StatusConflict, map[string]string{"error": "At least one login method must remain"})
	}
	if errors.Is(err, errOAuthLinkPrincipalInvalid) {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
	}
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Login method removed"})
}

var errOAuthLastLoginMethod = errors.New("last login method")

func principalIntent(value *principal) oauthLinkIntent {
	return oauthLinkIntent{UserID: value.User.ID, LoginID: value.LoginID, Source: value.Source, ExpiresAt: value.ExpiresAt.Unix()}
}

func readOAuthLinkIntent(session *sessions.Session) (oauthLinkIntent, bool, error) {
	value, present := session.Values[oauthIntentKey]
	if !present {
		return oauthLinkIntent{}, false, nil
	}
	raw, ok := value.(string)
	if !ok {
		return oauthLinkIntent{}, true, errors.New("invalid OAuth intent")
	}
	var intent oauthLinkIntent
	if err := json.Unmarshal([]byte(raw), &intent); err != nil {
		return oauthLinkIntent{}, true, err
	}
	if intent.Purpose != oauthIntentLink || !validLoginID(intent.LoginID) || intent.UserID == 0 || intent.State == "" ||
		(intent.Source != principalBearer && intent.Source != principalJWTCookie && intent.Source != principalSession) || intent.ExpiresAt <= 0 {
		return oauthLinkIntent{}, true, errors.New("invalid OAuth intent")
	}
	return intent, true, nil
}

func readOAuthIntentPurpose(session *sessions.Session) (string, bool, error) {
	value, present := session.Values[oauthIntentKey]
	if !present {
		return "", false, nil
	}
	raw, ok := value.(string)
	if !ok {
		return "", true, errors.New("invalid OAuth intent")
	}
	var intent struct {
		Purpose string `json:"purpose"`
	}
	if err := json.Unmarshal([]byte(raw), &intent); err != nil || intent.Purpose == "" {
		return "", true, errors.New("invalid OAuth intent")
	}
	return intent.Purpose, true, nil
}

func revalidatePrincipal(db *gorm.DB, c echo.Context, intent oauthLinkIntent) (*principal, error) {
	if time.Now().Unix() >= intent.ExpiresAt {
		return nil, errOAuthLinkPrincipalInvalid
	}
	actual, err := resolvePrincipal(c, db, true)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errOAuthLinkPrincipalInvalid, err)
	}
	if !principalMatchesIntent(actual, intent) {
		return nil, errOAuthLinkPrincipalInvalid
	}
	return actual, nil
}

var errOAuthLinkPrincipalInvalid = errors.New("OAuth link principal invalid")

func linkOAuthIdentity(c echo.Context, intent oauthLinkIntent, profile goth.User) (int, uint) {
	if data.DB == nil {
		return http.StatusServiceUnavailable, 0
	}
	if profile.Provider != intent.Provider || strings.TrimSpace(profile.UserID) == "" || utf8.RuneCountInString(profile.UserID) > oauthProviderMax {
		return http.StatusBadRequest, 0
	}
	var actorID uint
	err := oauthIdentityDB(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		var target core.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&target, intent.UserID).Error; err != nil {
			return err
		}
		actual, err := revalidatePrincipal(tx, c, intent)
		if err != nil {
			return errOAuthLinkPrincipalInvalid
		}
		actorID = actual.User.ID
		if err := tx.Exec("SELECT set_config('jelly.actor_id', ?, true)", strconv.FormatUint(uint64(target.ID), 10)).Error; err != nil {
			return err
		}
		var bySubject core.OAuthIdentity
		lookupErr := tx.Where("provider = ? AND subject = ?", intent.Provider, profile.UserID).First(&bySubject).Error
		actual, err = revalidatePrincipal(tx, c, intent)
		if err != nil {
			actorID = 0
			return errOAuthLinkPrincipalInvalid
		}
		actorID = actual.User.ID
		if lookupErr == nil {
			if bySubject.UserID == target.ID {
				return nil
			}
			return errOAuthIdentityConflict
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return lookupErr
		}
		var byProvider core.OAuthIdentity
		lookupErr = tx.Where("user_id = ? AND provider = ?", target.ID, intent.Provider).First(&byProvider).Error
		actual, err = revalidatePrincipal(tx, c, intent)
		if err != nil {
			actorID = 0
			return errOAuthLinkPrincipalInvalid
		}
		actorID = actual.User.ID
		if lookupErr == nil {
			return errOAuthIdentityConflict
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return lookupErr
		}
		identity := core.OAuthIdentity{UserID: target.ID, Provider: intent.Provider, Subject: profile.UserID}
		if err := tx.Create(&identity).Error; err != nil {
			if isUniqueViolation(err) {
				return errOAuthIdentityConflict
			}
			return err
		}
		return nil
	})
	if errors.Is(err, errOAuthIdentityConflict) {
		return http.StatusConflict, actorID
	}
	if errors.Is(err, errOAuthLinkPrincipalInvalid) {
		return http.StatusUnauthorized, 0
	}
	if err != nil {
		return http.StatusServiceUnavailable, actorID
	}
	return http.StatusOK, actorID
}

var errOAuthIdentityConflict = errors.New("OAuth identity conflict")

func gzipOAuthSessionValue(value string) (string, error) {
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := io.WriteString(writer, value); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	return buffer.String(), nil
}
