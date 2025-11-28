package auth

import (
	"fmt"
	"net/http"
	"os"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/github"
	"github.com/markbates/goth/providers/google"
)

func Init() {
	store := sessions.NewCookieStore([]byte(os.Getenv("SESSION_SECRET")))
	gothic.Store = store

	goth.UseProviders(
		github.New(os.Getenv("GITHUB_KEY"), os.Getenv("GITHUB_SECRET"), os.Getenv("APP_URL")+"/auth/github/callback"),
		google.New(os.Getenv("GOOGLE_KEY"), os.Getenv("GOOGLE_SECRET"), os.Getenv("APP_URL")+"/auth/google/callback"),
	)
}

func HandleAuth(c echo.Context) error {
	provider := c.Param("provider")
	if provider == "" {
		return c.String(http.StatusBadRequest, "Provider not specified")
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

	// TODO: Issue JWT or set session cookie for the app
	// For now, just return success
	return c.JSON(http.StatusOK, map[string]interface{}{
		"message": "Login successful",
		"user":    dbUser,
	})
}
