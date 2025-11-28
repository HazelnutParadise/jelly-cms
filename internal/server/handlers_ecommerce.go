package server

import (
	"net/http"
	"strconv"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
)

func RegisterEcommerceRoutes(e *echo.Echo) {
	productService := core.NewProductService(data.DB)
	api := e.Group("/api/admin")

	// Products
	api.GET("/products", func(c echo.Context) error {
		var products []core.Product
		if err := data.DB.Find(&products).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, products)
	})

	api.POST("/products", func(c echo.Context) error {
		var product core.Product
		if err := c.Bind(&product); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}
		if err := productService.Create(&product); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, product)
	})

	api.GET("/products/:id", func(c echo.Context) error {
		id, _ := strconv.Atoi(c.Param("id"))
		product, err := productService.GetByID(uint(id))
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Product not found"})
		}
		return c.JSON(http.StatusOK, product)
	})

	api.PUT("/products/:id", func(c echo.Context) error {
		id, _ := strconv.Atoi(c.Param("id"))
		product, err := productService.GetByID(uint(id))
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Product not found"})
		}
		if err := c.Bind(product); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}
		if err := productService.Update(product); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, product)
	})

	api.DELETE("/products/:id", func(c echo.Context) error {
		id, _ := strconv.Atoi(c.Param("id"))
		if err := productService.Delete(uint(id)); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "Deleted"})
	})

	// Orders (Basic View)
	api.GET("/orders", func(c echo.Context) error {
		var orders []core.Order
		if err := data.DB.Preload("User").Preload("Items").Find(&orders).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, orders)
	})
}
