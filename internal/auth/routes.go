package auth

import "github.com/labstack/echo/v4"

func RegisterRoutes(e *echo.Echo) {
	e.GET("/auth/:provider", HandleAuth)
	e.GET("/auth/:provider/callback", HandleCallback)
}
