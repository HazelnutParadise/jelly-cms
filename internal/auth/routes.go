package auth

import "github.com/labstack/echo/v4"

func RegisterRoutes(e *echo.Echo) {
	// Public auth API documentation
	e.GET("/api/auth", HandleAuthAPIIndex)
	e.GET("/api/auth/openapi.json", HandleAuthOpenAPI)

	// Local login
	e.POST("/auth/login", ProtectBrowserWrites(ProtectLoginAttempts(HandleLocalLogin)))

	// OAuth routes
	e.GET("/auth/:provider", ProtectLoginAttempts(HandleAuth))
	e.GET("/auth/:provider/callback", HandleCallback)
	e.POST("/auth/recover", auditOAuthRecoveryAction(ProtectBrowserWrites(ProtectLoginAttempts(HandleOAuthRecovery))))

	// Logout
	e.POST("/auth/logout", ProtectLogoutWrites(HandleLogout))

	// Get current user
	e.GET("/api/auth/me", RequireAuth(HandleMe))
	e.GET("/api/auth/identities", RequireAuth(HandleIdentityList))
	e.POST("/api/auth/identities/:provider/start", auditOAuthIdentityAction("link_start", RequireAuth(ProtectLoginAttempts(HandleIdentityLinkStart))))
	e.DELETE("/api/auth/identities/:provider", auditOAuthIdentityAction("unlink", RequireAuth(HandleIdentityUnlink)))
}

// HandleMe returns the current authenticated user
func HandleMe(c echo.Context) error {
	user, ok := GetUserFromContext(c)
	if !ok {
		return c.JSON(500, map[string]string{"error": "Failed to get user from context"})
	}
	return c.JSON(200, user)
}
