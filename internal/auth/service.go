package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/github"
	"github.com/markbates/goth/providers/google"
	"golang.org/x/crypto/bcrypt"
)

var (
	sessionStore *sessions.CookieStore
	jwtSecret    []byte
)

func Init() error {
	sessionEnv := os.Getenv("SESSION_SECRET")
	jwtEnv := os.Getenv("JWT_SECRET")
	sessionFromEnv := sessionEnv != ""
	jwtFromEnv := jwtEnv != ""

	var sessionBytes, jwtBytes []byte
	if sessionFromEnv && jwtFromEnv {
		sessionBytes = []byte(sessionEnv)
		jwtBytes = []byte(jwtEnv)
	} else {
		fileSession, fileJWT, err := loadOrCreateSecretsFile(secretFilePath())
		if err != nil {
			return err
		}
		if sessionFromEnv {
			sessionBytes = []byte(sessionEnv)
		} else {
			sessionBytes = fileSession
		}
		if jwtFromEnv {
			jwtBytes = []byte(jwtEnv)
		} else {
			jwtBytes = fileJWT
		}
	}

	sessionStore = sessions.NewCookieStore(sessionBytes)
	configureAuthCookieStore(sessionStore, 604800)
	gothStore := sessions.NewCookieStore(sessionBytes)
	configureAuthCookieStore(gothStore, oauthStateLifetimeSeconds)
	gothic.Store = gothStore
	jwtSecret = jwtBytes

	// Load OAuth providers from database
	ReloadOAuthProviders()
	return nil
}

// ReloadOAuthProviders reloads OAuth provider configurations from database
func ReloadOAuthProviders() {
	// Clear existing providers
	goth.ClearProviders()

	// Get app URL
	appURL := os.Getenv("APP_URL")
	if appURL == "" {
		appURL = "http://localhost:8080"
	}

	// Load from database if available
	if data.DB != nil {
		var opt core.Option

		// Google OAuth
		if err := data.DB.Where("key = ?", "oauth_google_enabled").First(&opt).Error; err == nil && opt.Value == "true" {
			var clientID, clientSecret string
			if err := data.DB.Where("key = ?", "oauth_google_client_id").First(&opt).Error; err == nil {
				clientID = opt.Value
			}
			if err := data.DB.Where("key = ?", "oauth_google_client_secret").First(&opt).Error; err == nil {
				clientSecret = opt.Value
			}
			if clientID != "" && clientSecret != "" {
				goth.UseProviders(google.New(clientID, clientSecret, appURL+"/auth/google/callback"))
			}
		}

		// GitHub OAuth
		if err := data.DB.Where("key = ?", "oauth_github_enabled").First(&opt).Error; err == nil && opt.Value == "true" {
			var clientID, clientSecret string
			if err := data.DB.Where("key = ?", "oauth_github_client_id").First(&opt).Error; err == nil {
				clientID = opt.Value
			}
			if err := data.DB.Where("key = ?", "oauth_github_client_secret").First(&opt).Error; err == nil {
				clientSecret = opt.Value
			}
			if clientID != "" && clientSecret != "" {
				goth.UseProviders(github.New(clientID, clientSecret, appURL+"/auth/github/callback"))
			}
		}
	}
}

// GetSession retrieves the session for the current request
func GetSession(c echo.Context) (*sessions.Session, error) {
	if sessionStore == nil {
		return nil, fmt.Errorf("session store not initialized")
	}
	return sessionStore.Get(c.Request(), "jelly-cms-session")
}

// SetUserSession sets the user ID in the session
func SetUserSession(c echo.Context, userID uint) error {
	loginID, err := newLoginID()
	if err != nil {
		return err
	}
	return setUserSessionWithLoginID(c, userID, loginID)
}

func setUserSessionWithLoginID(c echo.Context, userID uint, loginID string) error {
	if sessionStore == nil {
		return fmt.Errorf("session store not initialized")
	}
	if userID == 0 || !validLoginID(loginID) {
		return fmt.Errorf("invalid login")
	}

	session, err := sessionStore.Get(c.Request(), "jelly-cms-session")
	if err != nil {
		session = sessions.NewSession(sessionStore, "jelly-cms-session")
		opts := *sessionStore.Options
		session.Options = &opts
		session.IsNew = true
	}
	session.Values["user_id"] = userID
	session.Values["login_id"] = loginID
	session.Values["login_expires_at"] = time.Now().Add(7 * 24 * time.Hour).Unix()
	session.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7, // 7 days
		HttpOnly: true,
		Secure:   os.Getenv("APP_ENV") == "production",
		SameSite: http.SameSiteLaxMode,
	}
	return session.Save(c.Request(), c.Response())
}

// GetUserIDFromSession retrieves the user ID from the session
func GetUserIDFromSession(c echo.Context) (uint, error) {
	session, err := GetSession(c)
	if err != nil {
		return 0, err
	}
	userID, ok := session.Values["user_id"].(uint)
	if !ok {
		return 0, fmt.Errorf("user not authenticated")
	}
	return userID, nil
}

// GenerateJWT generates a JWT token for the user
func GenerateJWT(userID uint, email string, role string) (string, error) {
	loginID, err := newLoginID()
	if err != nil {
		return "", err
	}
	return generateJWTWithLoginID(userID, email, role, loginID)
}

func generateJWTWithLoginID(userID uint, email string, role string, loginID string) (string, error) {
	const maxSafeUserID = 9007199254740991 // 2^53 - 1
	if userID == 0 || uint64(userID) > maxSafeUserID {
		return "", fmt.Errorf("invalid user ID")
	}
	if !validLoginID(loginID) {
		return "", fmt.Errorf("invalid login ID")
	}

	claims := jwt.MapClaims{
		"jti":     loginID,
		"user_id": userID,
		"email":   email,
		"role":    role,
		"exp":     time.Now().Add(time.Hour * 24 * 7).Unix(), // 7 days
		"iat":     time.Now().Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

// ValidateJWT validates a JWT token and returns the claims
func ValidateJWT(tokenString string) (jwt.MapClaims, error) {
	const maxSafeUserID = int64(9007199254740991) // 2^53 - 1

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return jwtSecret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithJSONNumber())

	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	rawUserID, ok := claims["user_id"]
	if !ok {
		return nil, fmt.Errorf("missing user_id")
	}
	userIDNumber, ok := rawUserID.(json.Number)
	if !ok {
		return nil, fmt.Errorf("invalid user_id")
	}
	userID, err := userIDNumber.Int64()
	if err != nil {
		return nil, fmt.Errorf("invalid user_id")
	}
	if userID <= 0 {
		return nil, fmt.Errorf("invalid user_id")
	}
	if userID > maxSafeUserID {
		return nil, fmt.Errorf("invalid user_id")
	}
	if uint64(userID) > uint64(^uint(0)) {
		return nil, fmt.Errorf("invalid user_id")
	}

	// Preserve the existing float64 contract only after all validation succeeds.
	claims["user_id"] = float64(userID)
	for key, value := range claims {
		if number, ok := value.(json.Number); ok && key != "user_id" {
			parsed, err := number.Float64()
			if err != nil {
				return nil, fmt.Errorf("invalid %s", key)
			}
			claims[key] = parsed
		}
	}

	return claims, nil
}

// GetUserFromJWT extracts user information from JWT token in Authorization header or cookie
func GetUserFromJWT(c echo.Context) (*core.User, error) {
	var tokenString string
	provided := false

	// Try Authorization header first
	authHeader := c.Request().Header.Get("Authorization")
	if authHeader != "" {
		if len(authHeader) < 7 || authHeader[:7] != "Bearer " {
			return nil, fmt.Errorf("invalid authorization header")
		}
		tokenString = authHeader[7:]
		provided = true
	} else {
		// Try cookie
		cookie, err := c.Cookie("jwt_token")
		if err == nil && cookie != nil && cookie.Value != "" {
			tokenString = cookie.Value
			provided = true
		}
	}

	if !provided || tokenString == "" {
		return nil, fmt.Errorf("authorization header or cookie missing")
	}

	claims, err := ValidateJWT(tokenString)
	if err != nil {
		return nil, err
	}

	loginID, ok := claims["jti"].(string)
	if !ok || !validLoginID(loginID) {
		return nil, fmt.Errorf("invalid login ID")
	}

	userIDValue, ok := claims["user_id"].(float64)
	if !ok {
		return nil, fmt.Errorf("invalid token claims")
	}
	userID := uint(userIDValue)
	if userID == 0 {
		return nil, fmt.Errorf("invalid user ID")
	}

	if data.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	revoked, err := isLoginRevoked(loginID)
	if err != nil {
		return nil, err
	}
	if revoked {
		return nil, fmt.Errorf("login revoked")
	}

	var user core.User
	if err := data.DB.First(&user, userID).Error; err != nil {
		return nil, err
	}

	return &user, nil
}

// issueLogin creates one login ID and issues the paired JWT and session.
func issueLogin(c echo.Context, user core.User) (string, error) {
	loginID, err := newLoginID()
	if err != nil {
		return "", err
	}

	token, err := generateJWTWithLoginID(user.ID, user.Email, user.Role, loginID)
	if err != nil {
		return "", err
	}

	if err := setUserSessionWithLoginID(c, user.ID, loginID); err != nil {
		return "", err
	}

	return token, nil
}

// HandleLocalLogin handles local username/password login
func HandleLocalLogin(c echo.Context) error {
	var req struct {
		Username string `json:"username" form:"username"`
		Password string `json:"password" form:"password"`
	}

	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}

	if req.Username == "" || req.Password == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Username and password are required"})
	}

	// Find user by username or email
	var user core.User
	result := data.DB.Where("username = ? OR email = ?", req.Username, req.Username).First(&user)
	if result.Error != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Invalid credentials"})
	}

	// Check if user is local user (has password)
	if user.Provider != "local" || user.Password == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "This account uses OAuth login. Please use OAuth to sign in."})
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Invalid credentials"})
	}

	// Issue the paired login credentials. Prepare the JWT before saving the
	// session so both carry the same login ID.
	token, err := issueLogin(c, user)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to generate token"})
	}

	// Set token in cookie for web requests
	c.SetCookie(&http.Cookie{
		Name:     "jwt_token",
		Value:    token,
		Path:     "/",
		MaxAge:   86400 * 7, // 7 days
		HttpOnly: true,
		Secure:   os.Getenv("APP_ENV") == "production",
		SameSite: http.SameSiteLaxMode,
	})

	// Return JSON for API requests or redirect for web requests
	if c.Request().Header.Get("Accept") == "application/json" {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"message": "Login successful",
			"user":    user,
			"token":   token,
		})
	}

	// Get redirect URL from query parameter or default to admin
	redirectURL := SafeRedirectURL(c.QueryParam("redirect"))

	// Redirect to stored URL or admin dashboard
	return c.Redirect(http.StatusFound, redirectURL)
}

func HandleAuth(c echo.Context) error {
	req, provider, query, err := oauthRequestForPath(c.Request(), c.Param("provider"))
	if err != nil {
		return c.String(http.StatusBadRequest, "Invalid OAuth provider or request")
	}

	stateBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, stateBytes); err != nil {
		return c.String(http.StatusServiceUnavailable, "OAuth service unavailable")
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)
	query.Set("state", state)
	req.URL.RawQuery = query.Encode()

	// Store redirect URL in the app session without changing login expiry.
	session, err := GetSession(c)
	if err != nil {
		return c.String(http.StatusServiceUnavailable, "OAuth service unavailable")
	}
	session.Values["oauth_redirect"] = SafeRedirectURL(query.Get("redirect"))
	if err := session.Save(c.Request(), c.Response()); err != nil {
		return c.String(http.StatusServiceUnavailable, "OAuth service unavailable")
	}

	// Use Goth's public provider/session APIs so random state and persistence errors
	// remain under this handler's control.
	gothSession, err := provider.BeginAuth(state)
	if err != nil {
		return c.String(http.StatusBadGateway, "OAuth provider unavailable")
	}
	authURL, err := gothSession.GetAuthURL()
	if err != nil {
		return c.String(http.StatusBadGateway, "OAuth provider unavailable")
	}
	// Start from an empty Goth cookie so an entry for another provider cannot
	// remain valid or have its signing lifetime extended by this save.
	req.Header.Del("Cookie")
	if err := gothic.StoreInSession(provider.Name(), gothSession.Marshal(), req, c.Response()); err != nil {
		return c.String(http.StatusServiceUnavailable, "OAuth service unavailable")
	}
	return c.Redirect(http.StatusTemporaryRedirect, authURL)
}

func HandleCallback(c echo.Context) error {
	req, provider, query, err := oauthRequestForPath(c.Request(), c.Param("provider"))
	if err != nil {
		return c.String(http.StatusBadRequest, "Invalid OAuth callback")
	}
	callbackState, ok := uniqueNonBlankOAuthState(query)
	if !ok {
		return c.String(http.StatusBadRequest, "Invalid OAuth callback")
	}

	codeValues, hasCode := query["code"]
	errorValues, hasError := query["error"]
	validCode := hasCode && len(codeValues) == 1 && strings.TrimSpace(codeValues[0]) != "" && !hasError
	validDenial := hasError && len(errorValues) == 1 && strings.TrimSpace(errorValues[0]) != "" && !hasCode
	if !validCode && !validDenial {
		return c.String(http.StatusBadRequest, "Invalid OAuth callback")
	}

	// Validate the signed Goth session and its original authorization URL before
	// consuming state or contacting the provider.
	gothSession, err := gothic.Store.Get(req, gothic.SessionName)
	if err != nil {
		return c.String(http.StatusBadRequest, "Invalid OAuth callback")
	}
	rawSession, ok := gothSession.Values[provider.Name()].(string)
	if !ok || rawSession == "" {
		return c.String(http.StatusBadRequest, "Invalid OAuth callback")
	}
	serialized, err := gothic.GetFromSession(provider.Name(), req)
	if err != nil {
		return c.String(http.StatusBadRequest, "Invalid OAuth callback")
	}
	providerSession, err := provider.UnmarshalSession(serialized)
	if err != nil {
		return c.String(http.StatusBadRequest, "Invalid OAuth callback")
	}
	authURL, err := providerSession.GetAuthURL()
	if err != nil {
		return c.String(http.StatusBadRequest, "Invalid OAuth callback")
	}
	parsedAuthURL, err := url.Parse(authURL)
	if err != nil {
		return c.String(http.StatusBadRequest, "Invalid OAuth callback")
	}
	originalQuery, err := url.ParseQuery(parsedAuthURL.RawQuery)
	if err != nil {
		return c.String(http.StatusBadRequest, "Invalid OAuth callback")
	}
	originalState, ok := uniqueNonBlankOAuthState(originalQuery)
	if !ok || callbackState != originalState {
		return c.String(http.StatusBadRequest, "Invalid OAuth callback")
	}

	consumed, err := consumeOAuthState(req.Context(), provider.Name(), callbackState)
	if err != nil {
		return c.String(http.StatusServiceUnavailable, "OAuth service unavailable")
	}
	if !consumed {
		return c.String(http.StatusBadRequest, "Invalid OAuth callback")
	}
	gothSession.Options.MaxAge = -1
	gothSession.Values = make(map[interface{}]interface{})
	if err := gothSession.Save(req, c.Response()); err != nil {
		return c.String(http.StatusServiceUnavailable, "OAuth service unavailable")
	}
	if validDenial {
		return c.String(http.StatusBadRequest, "OAuth authorization denied")
	}

	if _, err := providerSession.Authorize(provider, query); err != nil {
		return c.String(http.StatusBadGateway, "OAuth provider authentication failed")
	}
	user, err := provider.FetchUser(providerSession)
	if err != nil {
		return c.String(http.StatusBadGateway, "OAuth provider authentication failed")
	}
	if user.Provider != provider.Name() {
		return c.String(http.StatusBadRequest, "Invalid OAuth callback")
	}

	// Auto-register or Login
	var dbUser core.User
	result := data.DB.Where("email = ?", user.Email).First(&dbUser)

	if result.Error != nil {
		// User not found, create new
		dbUser = core.User{
			Username:  user.NickName,
			Email:     user.Email,
			AvatarURL: user.AvatarURL,
			Provider:  provider.Name(),
			Role:      "author", // Default role
		}
		if dbUser.Username == "" {
			dbUser.Username = user.Name
		}
		if err := data.DB.Create(&dbUser).Error; err != nil {
			return c.String(http.StatusInternalServerError, "Failed to create user")
		}
	}

	// Issue the paired login credentials. Prepare the JWT before saving the
	// session so both carry the same login ID.
	token, err := issueLogin(c, dbUser)
	if err != nil {
		return c.String(http.StatusInternalServerError, "Failed to generate token")
	}

	// Redirect to admin or return JSON based on request
	if c.Request().Header.Get("Accept") == "application/json" {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"message": "Login successful",
			"user":    dbUser,
			"token":   token,
		})
	}

	// Set token in cookie for web requests
	c.SetCookie(&http.Cookie{
		Name:     "jwt_token",
		Value:    token,
		Path:     "/",
		MaxAge:   86400 * 7, // 7 days
		HttpOnly: true,
		Secure:   os.Getenv("APP_ENV") == "production",
		SameSite: http.SameSiteLaxMode,
	})

	// Get redirect URL from session
	redirectURL := "/admin"
	session, err := GetSession(c)
	if err == nil {
		if storedRedirect, ok := session.Values["oauth_redirect"].(string); ok {
			redirectURL = SafeRedirectURL(storedRedirect)
			delete(session.Values, "oauth_redirect")
			session.Save(c.Request(), c.Response())
		}
	}

	// Redirect to stored URL or admin dashboard
	return c.Redirect(http.StatusFound, redirectURL)
}

// HandleLogout handles user logout
func HandleLogout(c echo.Context) error {
	collected := make([]string, 0)
	hasValid := false

	authHeader := c.Request().Header.Get("Authorization")
	if authHeader != "" {
		if len(authHeader) >= 7 && authHeader[:7] == "Bearer " {
			if claims, err := ValidateJWT(authHeader[7:]); err == nil {
				if loginID, ok := claims["jti"].(string); ok && validLoginID(loginID) {
					collected = append(collected, loginID)
					hasValid = true
				}
			}
		}
	}

	if cookie, err := c.Cookie("jwt_token"); err == nil && cookie != nil && cookie.Value != "" {
		if claims, err := ValidateJWT(cookie.Value); err == nil {
			if loginID, ok := claims["jti"].(string); ok && validLoginID(loginID) {
				collected = append(collected, loginID)
				hasValid = true
			}
		}
	}

	if session, err := GetSession(c); err == nil {
		if loginID, ok := session.Values["login_id"].(string); ok && validLoginID(loginID) {
			collected = append(collected, loginID)
			hasValid = true
		}
	}

	unique := make([]string, 0, len(collected))
	seen := make(map[string]bool)
	for _, id := range collected {
		if seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}

	if hasValid {
		if err := revokeLoginIDs(unique); err != nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Failed to revoke login"})
		}
	}
	if !hasValid && !hasLoginCookie(c.Request()) {
		return c.JSON(http.StatusOK, map[string]string{"message": "Logged out successfully"})
	}

	session, err := GetSession(c)
	if err == nil {
		session.Values = make(map[interface{}]interface{})
		session.Options.MaxAge = -1
		if err := session.Save(c.Request(), c.Response()); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to clear session"})
		}
	} else {
		c.SetCookie(&http.Cookie{
			Name:     "jelly-cms-session",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   os.Getenv("APP_ENV") == "production",
			SameSite: http.SameSiteLaxMode,
		})
	}

	c.SetCookie(&http.Cookie{
		Name:     "jwt_token",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   os.Getenv("APP_ENV") == "production",
		SameSite: http.SameSiteLaxMode,
	})

	return c.JSON(http.StatusOK, map[string]string{"message": "Logged out successfully"})
}

// GetCurrentUser retrieves the current authenticated user from session or JWT
func GetCurrentUser(c echo.Context) (*core.User, error) {
	authHeader := c.Request().Header.Get("Authorization")
	jwtCookie, cookieErr := c.Cookie("jwt_token")
	jwtProvided := authHeader != "" || (cookieErr == nil && jwtCookie != nil)

	if jwtProvided {
		return GetUserFromJWT(c)
	}

	session, err := GetSession(c)
	if err != nil {
		return nil, err
	}

	userID, ok := session.Values["user_id"].(uint)
	if !ok || userID == 0 {
		return nil, fmt.Errorf("user not authenticated")
	}

	expiresAt, ok := session.Values["login_expires_at"].(int64)
	if !ok || expiresAt <= time.Now().Unix() {
		return nil, fmt.Errorf("session expired")
	}

	loginID, _ := session.Values["login_id"].(string)
	if !validLoginID(loginID) {
		return nil, fmt.Errorf("invalid login ID")
	}

	if data.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	revoked, err := isLoginRevoked(loginID)
	if err != nil {
		return nil, err
	}
	if revoked {
		return nil, fmt.Errorf("login revoked")
	}

	var user core.User
	if err := data.DB.First(&user, userID).Error; err != nil {
		return nil, err
	}

	return &user, nil
}
