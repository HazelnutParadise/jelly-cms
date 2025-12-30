package auth

import "github.com/labstack/echo/v4"

func RegisterRoutes(e *echo.Echo) {
	// Local login
	e.POST("/auth/login", HandleLocalLogin)
	
	// OAuth routes
	e.GET("/auth/:provider", HandleAuth)
	e.GET("/auth/:provider/callback", HandleCallback)
	
	// Logout
	e.POST("/auth/logout", HandleLogout)
	
	// Get current user
	e.GET("/api/auth/me", RequireAuth(HandleMe))
}

// HandleMe returns the current authenticated user
func HandleMe(c echo.Context) error {
	user, ok := GetUserFromContext(c)
	if !ok {
		return c.JSON(500, map[string]string{"error": "Failed to get user from context"})
	}
	return c.JSON(200, user)
}
