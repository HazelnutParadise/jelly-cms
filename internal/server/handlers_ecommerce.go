package server

import (
	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/labstack/echo/v4"
)

func RegisterEcommerceRoutes(e *echo.Echo) {
	api := e.Group("/api/admin", auth.RequireRole(adminRoles...))
	orderReaders := e.Group("/api/admin", auth.RequirePermission("orders_read"))

	// Products are owned by the typed catalog handlers. Orders remain below.
	api.GET("/products", adminProductsList)
	api.POST("/products", adminProductsCreate)
	api.GET("/products/:id", adminProductsGet)
	api.PUT("/products/:id", adminProductsUpdate)
	api.DELETE("/products/:id", adminProductsDelete)

	// Orders (Basic View)
	orderReaders.GET("/orders", adminOrdersList)
}
