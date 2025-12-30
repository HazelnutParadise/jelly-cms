package auth

import (
	"fmt"
	"net/http"
	"os"
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

func Init() {
	sessionSecret := os.Getenv("SESSION_SECRET")
	if sessionSecret == "" {
		sessionSecret = "jelly-cms-secret-key-change-in-production" // Default for development
	}
	sessionStore = sessions.NewCookieStore([]byte(sessionSecret))
	gothic.Store = sessionStore

	jwtSecretStr := os.Getenv("JWT_SECRET")
	if jwtSecretStr == "" {
		jwtSecretStr = sessionSecret // Use same secret if not specified
	}
	jwtSecret = []byte(jwtSecretStr)

	// Load OAuth providers from database
	ReloadOAuthProviders()
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
	return sessionStore.Get(c.Request(), "jelly-cms-session")
}

// SetUserSession sets the user ID in the session
func SetUserSession(c echo.Context, userID uint) error {
	session, err := GetSession(c)
	if err != nil {
		return err
	}
	session.Values["user_id"] = userID
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
	claims := jwt.MapClaims{
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
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return jwtSecret, nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
}

// GetUserFromJWT extracts user information from JWT token in Authorization header or cookie
func GetUserFromJWT(c echo.Context) (*core.User, error) {
	var tokenString string

	// Try Authorization header first
	authHeader := c.Request().Header.Get("Authorization")
	if authHeader != "" && len(authHeader) >= 7 && authHeader[:7] == "Bearer " {
		tokenString = authHeader[7:]
	} else {
		// Try cookie
		cookie, err := c.Cookie("jwt_token")
		if err != nil || cookie == nil {
			return nil, fmt.Errorf("authorization header or cookie missing")
		}
		tokenString = cookie.Value
	}

	claims, err := ValidateJWT(tokenString)
	if err != nil {
		return nil, err
	}

	userID, ok := claims["user_id"].(float64)
	if !ok {
		return nil, fmt.Errorf("invalid token claims")
	}

	var user core.User
	if err := data.DB.First(&user, uint(userID)).Error; err != nil {
		return nil, err
	}

	return &user, nil
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

	// Set session
	if err := SetUserSession(c, user.ID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to set session"})
	}

	// Generate JWT token
	token, err := GenerateJWT(user.ID, user.Email, user.Role)
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
	redirectURL := c.QueryParam("redirect")
	if redirectURL == "" {
		redirectURL = "/admin"
	}

	// Redirect to stored URL or admin dashboard
	return c.Redirect(http.StatusFound, redirectURL)
}

func HandleAuth(c echo.Context) error {
	provider := c.Param("provider")
	if provider == "" {
		return c.String(http.StatusBadRequest, "Provider not specified")
	}

	// Store redirect URL in session
	redirectURL := c.QueryParam("redirect")
	if redirectURL == "" {
		redirectURL = "/admin"
	}
	session, err := GetSession(c)
	if err == nil {
		session.Values["oauth_redirect"] = redirectURL
		session.Save(c.Request(), c.Response())
	}

	// Hack to make gothic work with Echo
	req := c.Request()
	q := req.URL.Query()
	q.Add("provider", provider)
	req.URL.RawQuery = q.Encode()

	gothic.BeginAuthHandler(c.Response(), req)
	return nil
}

func HandleCallback(c echo.Context) error {
	provider := c.Param("provider")
	req := c.Request()
	q := req.URL.Query()
	q.Add("provider", provider)
	req.URL.RawQuery = q.Encode()

	user, err := gothic.CompleteUserAuth(c.Response(), req)
	if err != nil {
		return c.String(http.StatusInternalServerError, fmt.Sprintf("Auth failed: %v", err))
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
			Provider:  provider,
			Role:      "author", // Default role
		}
		if dbUser.Username == "" {
			dbUser.Username = user.Name
		}
		if err := data.DB.Create(&dbUser).Error; err != nil {
			return c.String(http.StatusInternalServerError, "Failed to create user")
		}
	}

	// Set session
	if err := SetUserSession(c, dbUser.ID); err != nil {
		return c.String(http.StatusInternalServerError, "Failed to set session")
	}

	// Generate JWT token
	token, err := GenerateJWT(dbUser.ID, dbUser.Email, dbUser.Role)
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
		if storedRedirect, ok := session.Values["oauth_redirect"].(string); ok && storedRedirect != "" {
			redirectURL = storedRedirect
			delete(session.Values, "oauth_redirect")
			session.Save(c.Request(), c.Response())
		}
	}

	// Redirect to stored URL or admin dashboard
	return c.Redirect(http.StatusFound, redirectURL)
}

// HandleLogout handles user logout
func HandleLogout(c echo.Context) error {
	// Clear session
	session, err := GetSession(c)
	if err == nil {
		session.Values = make(map[interface{}]interface{})
		session.Options.MaxAge = -1
		session.Save(c.Request(), c.Response())
	}

	// Clear JWT cookie
	c.SetCookie(&http.Cookie{
		Name:     "jwt_token",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	return c.JSON(http.StatusOK, map[string]string{"message": "Logged out successfully"})
}

// GetCurrentUser retrieves the current authenticated user from session or JWT
func GetCurrentUser(c echo.Context) (*core.User, error) {
	// Try to get from JWT first (for API requests)
	if user, err := GetUserFromJWT(c); err == nil {
		return user, nil
	}

	// Try to get from session (for web requests)
	userID, err := GetUserIDFromSession(c)
	if err != nil {
		return nil, err
	}

	var user core.User
	if err := data.DB.First(&user, userID).Error; err != nil {
		return nil, err
	}

	return &user, nil
}
