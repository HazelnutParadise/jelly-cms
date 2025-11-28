package server

import (
	"net/http"
	"strconv"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
)

func RegisterAdminRoutes(e *echo.Echo) {
	postService := core.NewPostService(data.DB)

	// TODO: Add middleware to check if user is admin/authenticated

	// API Routes
	api := e.Group("/api/admin")

	// Posts
	api.GET("/posts", func(c echo.Context) error {
		var posts []core.Post
		if err := data.DB.Find(&posts).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, posts)
	})

	api.POST("/posts", func(c echo.Context) error {
		var post core.Post
		if err := c.Bind(&post); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		// Set author from context (mocked for now)
		post.AuthorID = 1

		if err := postService.Create(&post); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, post)
	})

	api.GET("/posts/:id", func(c echo.Context) error {
		id, _ := strconv.Atoi(c.Param("id"))
		post, err := postService.GetByID(uint(id))
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
		}
		return c.JSON(http.StatusOK, post)
	})

	api.PUT("/posts/:id", func(c echo.Context) error {
		id, _ := strconv.Atoi(c.Param("id"))
		post, err := postService.GetByID(uint(id))
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
		}

		if err := c.Bind(post); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		if err := postService.Update(post); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, post)
	})

	api.DELETE("/posts/:id", func(c echo.Context) error {
		id, _ := strconv.Atoi(c.Param("id"))
		if err := postService.Delete(uint(id)); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "Deleted"})
	})
}
