package auth

import (
	"net/http"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// RequireAuth middleware requires authentication
func RequireAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		user, err := GetCurrentUser(c)
		if err != nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		}
		c.Set("user", user)
		return next(c)
	}
}

// RequireAdmin middleware requires admin role
func RequireAdmin(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		user, err := GetCurrentUser(c)
		if err != nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		}
		if user.Role != "admin" {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "Forbidden: Admin access required"})
		}
		c.Set("user", user)
		return next(c)
	}
}

// RequireRole middleware requires specific role
func RequireRole(roles ...string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			user, err := GetCurrentUser(c)
			if err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
			}

			allowed := false
			for _, role := range roles {
				if user.Role == role {
					allowed = true
					break
				}
			}

			if !allowed {
				return c.JSON(http.StatusForbidden, map[string]string{"error": "Forbidden: Insufficient permissions"})
			}

			c.Set("user", user)
			return next(c)
		}
	}
}

// GetUserFromContext retrieves the user from echo context
func GetUserFromContext(c echo.Context) (*core.User, bool) {
	user, ok := c.Get("user").(*core.User)
	return user, ok
}

