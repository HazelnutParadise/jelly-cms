package auth

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
)

// RequireAuth middleware requires authentication
func RequireAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		user, err := GetCurrentUser(c)
		if err != nil {
			// API requests always receive a machine-readable authentication error,
			// regardless of the client's content negotiation headers.
			if strings.HasPrefix(c.Request().URL.Path, "/api/") ||
				c.Request().Header.Get("Accept") == "application/json" ||
				c.Request().Header.Get("Content-Type") == "application/json" {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
			}
			// For web requests, redirect to login with return URL
			returnURL := c.Request().URL.Path
			if c.Request().URL.RawQuery != "" {
				returnURL += "?" + c.Request().URL.RawQuery
			}
			return c.Redirect(http.StatusFound, "/login?redirect="+url.QueryEscape(returnURL))
		}
		c.Set("user", user)
		if p, err := resolvePrincipal(c, data.DB, true); err == nil {
			c.Set("auth.principal", p)
		}
		return ProtectCookieWrites(next)(c)
	}
}

// RequireAdmin middleware requires admin role
func RequireAdmin(next echo.HandlerFunc) echo.HandlerFunc {
	return RequireRole("admin")(next)
}

// RequireRole middleware requires specific role
func RequireRole(roles ...string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			_, err := GetCurrentUser(c)
			if err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
			}

			p, err := resolvePrincipal(c, data.DB, true)
			if err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
			}
			if !requiresAnyRole(roles, p.User.Role) {
				return c.JSON(http.StatusForbidden, map[string]string{"error": "Forbidden: Insufficient permissions"})
			}

			c.Set("user", projectedUser(p))
			c.Set("auth.principal", p)
			return ProtectCookieWrites(next)(c)
		}
	}
}

// RequireInteractive is the only interactive-login rule (ticket 23). It answers
// 401 when no login can be resolved and 403 when the login came from an
// Authorization Bearer token. Access keys never reach it because resolvePrincipal
// rejects jla_ and jly_ (ticket 22). Writes also need a non-empty X-Jelly-CSRF
// header, which a plain cross-site form cannot send.
func RequireInteractive() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			p, err := resolvePrincipal(c, data.DB, true)
			if err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
			}
			if p.Source == principalBearer {
				return interactiveLoginRequired(c)
			}
			if !isSafeMethod(c.Request().Method) && c.Request().Header.Get("X-Jelly-CSRF") == "" {
				return interactiveLoginRequired(c)
			}
			c.Set("user", p.User)
			c.Set("auth.principal", p)
			return next(c)
		}
	}
}

func interactiveLoginRequired(c echo.Context) error {
	return c.JSON(http.StatusForbidden, map[string]string{"error": "Interactive login required"})
}

func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// RequirePermission allows a request when its principal holds every listed
// permission, such as content_read or orders_read (ticket 06).
func RequirePermission(perms ...string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			p, err := resolvePrincipal(c, data.DB, true)
			if err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
			}
			for _, permission := range perms {
				if !principalHasPermission(p, permission) {
					return c.JSON(http.StatusForbidden, map[string]string{"error": "Forbidden: insufficient permissions"})
				}
			}
			c.Set("user", projectedUser(p))
			c.Set("auth.principal", p)
			return ProtectCookieWrites(next)(c)
		}
	}
}

func requiresAnyRole(roles []string, want ...string) bool {
	for _, role := range roles {
		for _, candidate := range want {
			if role == candidate {
				return true
			}
		}
	}
	return false
}

func principalHasPermission(p *principal, permission string) bool {
	if p == nil || p.User == nil {
		return false
	}
	switch permission {
	case "content_read":
		return requiresAnyRole([]string{"admin", "editor", "author"}, p.User.Role)
	case "orders_read":
		return p.User.Role == "admin"
	default:
		return false
	}
}

// GetUserFromContext retrieves the user from echo context
func GetUserFromContext(c echo.Context) (*core.User, bool) {
	user, ok := c.Get("user").(*core.User)
	return user, ok
}
