package server

import (
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Maintenance mode (ticket 05, B3).
//
// Options (table options, written through PUT /api/admin/settings or setMaintenance):
//   - maintenance_enabled     "true" or "false"
//   - maintenance_message     text shown to visitors
//   - maintenance_started_at  RFC3339 time, written when mode is turned on and cleared when it is turned off
//
// Behaviour:
//   - While enabled, public pages answer 503 with a Retry-After header (default maintenanceRetryAfter, 600
//     seconds) and render maintenance.html with Message, StartedAt, ElapsedMinutes (computed by the server,
//     no live counter) and RetryAfter.
//   - Bypass (maintenanceBypass): signed-in admin and editor users see the site as normal.
//   - Never affected: /login, /auth/*, /admin*, /api/admin*, /install*, /uploads/*.
//   - /api/* always answers JSON (503 with an error body) while enabled.
//   - Middleware is installed by RegisterRoutes ahead of the public routes; it passes everything through
//     until the contract is implemented.

// maintenanceRetryAfter is the Retry-After value in seconds when maintenance mode is on.
const maintenanceRetryAfter = 600

// maintenanceMiddleware returns the maintenance-mode gate for public routes. Not implemented yet: it
// passes every request to the next handler.
func maintenanceMiddleware(tm *theme.Manager) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return next
	}
}

// maintenanceBypass reports whether a request for path, made by user (nil when anonymous), skips the
// maintenance gate. Not implemented yet: always returns true, so no request is gated.
func maintenanceBypass(path string, user *core.User) bool {
	return true
}

// setMaintenance turns maintenance mode on or off and stores message. Turning it on writes
// maintenance_started_at; turning it off clears it. Not implemented yet: returns nil without writing.
func setMaintenance(db *gorm.DB, enabled bool, message string) error {
	return nil
}
