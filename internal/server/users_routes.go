package server

import (
	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/labstack/echo/v4"
)

// User administration and invitations (docs/tickets/06-accounts.md).
func registerUserRoutes(e *echo.Echo, renderAdmin func(c echo.Context, file, active string) error) {
	admins := e.Group("/api/admin/users", auth.RequireRole("admin"))
	interactive := auth.RequireInteractive()

	admins.GET("", listAdminUsers)
	admins.POST("/invite", createAdminInvite, interactive)
	admins.POST("/:id/invite/resend", resendAdminInvite, interactive)
	admins.POST("/:id/invite/revoke", revokeAdminInvite, interactive)
	admins.PATCH("/:id", patchAdminUser, interactive)
	admins.POST("/:id/reset-password", createAdminPasswordReset, interactive)
	admins.POST("/:id/revoke-sessions", revokeAdminUserSessions, interactive)
	admins.DELETE("/:id", deleteAdminUser, interactive)
	admins.POST("/bulk", bulkAdminUsers, interactive)
	e.GET("/api/admin/roles", listAdminRoles, auth.RequireRole("admin"))

	e.GET("/invite/:token", noStoreUserLink(showInvitePage))
	e.POST("/api/invites/accept", noStoreUserLink(auth.ProtectBrowserWrites(auth.ProtectLoginAttempts(acceptInvite))))
	e.GET("/password-reset/:token", noStoreUserLink(showPasswordResetPage))
	e.POST("/api/password-reset", noStoreUserLink(auth.ProtectBrowserWrites(auth.ProtectLoginAttempts(acceptPasswordReset))))

	e.GET("/admin/users", func(c echo.Context) error {
		return renderAdmin(c, "users.html", "users")
	}, auth.RequireAuth, auth.RequireRole("admin"))
}

func init() {
	adminOnly := []string{"admin"}
	RegisterNav(
		NavItem{Key: "users", Section: "system", Label: "users", Icon: "i-users", Href: "/admin/users", Order: 80, Roles: adminOnly},
	)
	RegisterPaletteCommands(
		PaletteCommand{Key: "invite-user", Label: "inviteUser", Keywords: []string{"invite", "user", "邀請", "使用者"}, Icon: "i-user", Href: "/admin/users#invite", Roles: adminOnly},
	)
}
