package server

import (
	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/labstack/echo/v4"
)

func RegisterEcommerceRoutes(e *echo.Echo) {
	api := e.Group("/api/admin", auth.RequireRole(adminRoles...))

	// Products are owned by the typed catalog handlers. Orders remain below.
	api.GET("/products", adminProductsList)
	api.POST("/products", adminProductsCreate)
	api.GET("/products/:id", adminProductsGet)
	api.PUT("/products/:id", adminProductsUpdate)
	api.DELETE("/products/:id", adminProductsDelete)

	// Orders (Basic View)
	api.GET("/orders", adminOrdersList)
}
