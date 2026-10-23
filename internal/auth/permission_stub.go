package auth

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// RequirePermission is a minimal stub of the middleware that ticket 06 adds
// (shared with ticket 06: replace this file with 06's version). It fails closed:
// every request gets 403 until the permission table exists, so no route can
// accidentally open up by using it early.
func RequirePermission(perms ...string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "forbidden"})
		}
	}
}
