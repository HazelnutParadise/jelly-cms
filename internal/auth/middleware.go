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
			allowed := false
			for _, role := range effectiveRoles(p) {
				for _, required := range roles {
					if role == required {
						allowed = true
						break
					}
				}
			}
			// Read-only API scopes are intentionally not projected as an author or
			// admin role, since handlers also use User.Role for write decisions.
			// Let them pass only the matching GET collection gate.
			if !allowed && p.Source == principalAPIToken && c.Request().Method == http.MethodGet {
				path := c.Request().URL.Path
				if hasScope(p, "content_read") && path == "/api/admin/posts" && requiresAnyRole(roles, "admin", "editor", "author") {
					allowed = p.User.Role == "admin" || p.User.Role == "editor" || p.User.Role == "author"
				}
				if hasScope(p, "orders_read") && path == "/api/admin/orders" && requiresAnyRole(roles, "admin") {
					allowed = p.User.Role == "admin"
				}
			}

			if !allowed {
				return c.JSON(http.StatusForbidden, map[string]string{"error": "Forbidden: Insufficient permissions"})
			}

			c.Set("user", projectedUser(p))
			c.Set("auth.principal", p)
			return ProtectCookieWrites(next)(c)
		}
	}
}

// RequireInteractive rejects API token principals on endpoints that only a
// browser session or JWT may use (ticket 06, 互動限定).
func RequireInteractive() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			p, err := resolvePrincipal(c, data.DB, true)
			if err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
			}
			if p.Source == principalAPIToken {
				return c.JSON(http.StatusForbidden, map[string]string{"error": "Interactive login required"})
			}
			c.Set("user", p.User)
			c.Set("auth.principal", p)
			return next(c)
		}
	}
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
			if c.Request().Method != http.MethodGet && p.Source == principalAPIToken {
				for _, permission := range perms {
					if permission == "content_read" || permission == "orders_read" {
						return c.JSON(http.StatusForbidden, map[string]string{"error": "Read-only token"})
					}
				}
			}
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

func hasScope(p *principal, scope string) bool {
	if p == nil {
		return false
	}
	for _, candidate := range p.Scopes {
		if candidate == scope {
			return true
		}
	}
	return false
}

func effectiveRoles(p *principal) []string {
	if p == nil || p.User == nil {
		return nil
	}
	if p.Source != principalAPIToken {
		return []string{p.User.Role}
	}
	allowed := make(map[string]bool)
	for _, scope := range p.Scopes {
		if !knownTokenScope(scope) || !scopeWithinOwner(scope, p.User.Role) {
			continue
		}
		if scope == "admin" || scope == "editor" || scope == "author" {
			allowed[scope] = true
		}
	}
	roles := make([]string, 0, 3)
	for _, role := range []string{"admin", "editor", "author"} {
		if allowed[role] {
			roles = append(roles, role)
		}
	}
	return roles
}

func knownTokenScope(scope string) bool {
	switch scope {
	case "admin", "editor", "author", "content_read", "orders_read":
		return true
	default:
		return false
	}
}

func scopeWithinOwner(scope, ownerRole string) bool {
	switch ownerRole {
	case "admin":
		return scope == "admin" || scope == "editor" || scope == "author" || scope == "content_read" || scope == "orders_read"
	case "editor":
		return scope == "editor" || scope == "author" || scope == "content_read"
	case "author":
		return scope == "author" || scope == "content_read"
	default:
		return false
	}
}

func principalHasPermission(p *principal, permission string) bool {
	if p == nil || p.User == nil {
		return false
	}
	if p.Source != principalAPIToken {
		switch permission {
		case "content_read":
			return p.User.Role == "admin" || p.User.Role == "editor" || p.User.Role == "author"
		case "orders_read":
			return p.User.Role == "admin"
		default:
			return false
		}
	}
	if !scopeWithinOwner(permission, p.User.Role) {
		return false
	}
	return hasScope(p, "admin") || hasScope(p, permission)
}

func projectedUser(p *principal) *core.User {
	if p == nil || p.User == nil {
		return nil
	}
	user := *p.User
	if p.Source == principalAPIToken {
		roles := effectiveRoles(p)
		if len(roles) == 0 {
			user.Role = "customer"
		} else {
			user.Role = roles[0]
		}
		// A permission-only token stays outside the staff role surface. Its
		// matching GET gate is authorized by RequirePermission itself.
		if len(roles) == 0 {
			user.Role = "customer"
		}
	}
	return &user
}

// GetUserFromContext retrieves the user from echo context
func GetUserFromContext(c echo.Context) (*core.User, bool) {
	user, ok := c.Get("user").(*core.User)
	return user, ok
}
