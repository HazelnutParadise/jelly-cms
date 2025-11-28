package server

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

func RegisterAdminRoutes(e *echo.Echo, tm *theme.Manager) {
	postService := core.NewPostService(data.DB)

	// TODO: Add middleware to check if user is admin/authenticated

	// API Routes
	api := e.Group("/api/admin")

	// Posts
	api.GET("/posts", func(c echo.Context) error {
		postType := c.QueryParam("type")
		if postType == "" {
			postType = "post"
		}

		var posts []core.Post
		if err := data.DB.Where("type = ?", postType).Find(&posts).Error; err != nil {
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

	// Media
	api.POST("/media", func(c echo.Context) error {
		file, err := c.FormFile("file")
		if err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		defer src.Close()

		// Ensure uploads directory exists
		os.MkdirAll("data/uploads", 0755)
		dstPath := filepath.Join("data/uploads", file.Filename)
		dst, err := os.Create(dstPath)
		if err != nil {
			return err
		}
		defer dst.Close()

		if _, err = io.Copy(dst, src); err != nil {
			return err
		}

		return c.JSON(http.StatusOK, map[string]string{"url": "/uploads/" + file.Filename})
	})

	api.GET("/media", func(c echo.Context) error {
		files, err := os.ReadDir("data/uploads")
		if err != nil {
			return c.JSON(http.StatusOK, []string{})
		}
		var fileList []string
		for _, f := range files {
			if !f.IsDir() {
				fileList = append(fileList, "/uploads/"+f.Name())
			}
		}
		return c.JSON(http.StatusOK, fileList)
	})

	// Themes
	api.GET("/themes", func(c echo.Context) error {
		entries, err := os.ReadDir("web/themes")
		if err != nil {
			return err
		}
		var themes []string
		for _, e := range entries {
			if e.IsDir() {
				themes = append(themes, e.Name())
			}
		}
		return c.JSON(http.StatusOK, themes)
	})

	api.POST("/themes/activate", func(c echo.Context) error {
		var req struct {
			Name string `json:"name"`
		}
		if err := c.Bind(&req); err != nil {
			return err
		}
		if err := tm.Activate(req.Name); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "Theme activated"})
	})

	// Plugins
	api.GET("/plugins", func(c echo.Context) error {
		entries, err := os.ReadDir("data/plugins")
		if err != nil {
			return c.JSON(http.StatusOK, []string{})
		}
		var plugins []string
		for _, e := range entries {
			if e.IsDir() {
				plugins = append(plugins, e.Name())
			}
		}
		return c.JSON(http.StatusOK, plugins)
	})

	// Settings
	api.GET("/settings", func(c echo.Context) error {
		var options []core.Option
		data.DB.Find(&options)
		return c.JSON(http.StatusOK, options)
	})

	api.POST("/settings", func(c echo.Context) error {
		var req map[string]string
		if err := c.Bind(&req); err != nil {
			return err
		}
		for k, v := range req {
			var opt core.Option
			data.DB.FirstOrCreate(&opt, core.Option{Key: k})
			opt.Value = v
			data.DB.Save(&opt)
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "Settings saved"})
	})
}
