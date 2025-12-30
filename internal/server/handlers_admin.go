package server

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/payment"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

func RegisterAdminRoutes(e *echo.Echo, tm *theme.Manager) {
	postService := core.NewPostService(data.DB)

	// API Routes with authentication
	api := e.Group("/api/admin", auth.RequireAuth)

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

	api.DELETE("/media/:filename", func(c echo.Context) error {
		filename := c.Param("filename")
		// Prevent directory traversal
		if filepath.Base(filename) != filename {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid filename"})
		}

		path := filepath.Join("data/uploads", filename)
		if err := os.Remove(path); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "Deleted"})
	})

	api.PUT("/media/:filename", func(c echo.Context) error {
		filename := c.Param("filename")
		// Prevent directory traversal
		if filepath.Base(filename) != filename {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid filename"})
		}

		file, err := c.FormFile("file")
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "No file uploaded"})
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		defer src.Close()

		dstPath := filepath.Join("data/uploads", filename)
		dst, err := os.Create(dstPath)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		defer dst.Close()

		if _, err = io.Copy(dst, src); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Replaced"})
	})

	api.POST("/media/rename", func(c echo.Context) error {
		var req struct {
			OldName string `json:"old_name"`
			NewName string `json:"new_name"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		// Validation
		if filepath.Base(req.OldName) != req.OldName || filepath.Base(req.NewName) != req.NewName {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid filename"})
		}

		oldPath := filepath.Join("data/uploads", req.OldName)
		newPath := filepath.Join("data/uploads", req.NewName)

		if err := os.Rename(oldPath, newPath); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Renamed"})
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

	// Get theme config
	api.GET("/themes/:name/config", func(c echo.Context) error {
		themeName := c.Param("name")
		config, err := tm.Load(themeName)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Theme not found"})
		}
		return c.JSON(http.StatusOK, config)
	})

	// Get theme settings
	api.GET("/themes/:name/settings", func(c echo.Context) error {
		themeName := c.Param("name")
		settings, err := tm.GetSettings(themeName, data.DB)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, settings)
	})

	// Save theme settings
	api.POST("/themes/:name/settings", func(c echo.Context) error {
		themeName := c.Param("name")
		var req struct {
			Colors interface{} `json:"colors"`
			Layout interface{} `json:"layout"`
			Custom interface{} `json:"custom"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		// Convert to json.RawMessage
		var colorsJSON, layoutJSON, customJSON json.RawMessage
		var err error

		if req.Colors != nil {
			if str, ok := req.Colors.(string); ok {
				colorsJSON = json.RawMessage(str)
			} else {
				colorsJSON, err = json.Marshal(req.Colors)
				if err != nil {
					return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid colors format"})
				}
			}
		}

		if req.Layout != nil {
			if str, ok := req.Layout.(string); ok {
				layoutJSON = json.RawMessage(str)
			} else {
				layoutJSON, err = json.Marshal(req.Layout)
				if err != nil {
					return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid layout format"})
				}
			}
		}

		if req.Custom != nil {
			if str, ok := req.Custom.(string); ok {
				customJSON = json.RawMessage(str)
			} else {
				customJSON, err = json.Marshal(req.Custom)
				if err != nil {
					return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid custom format"})
				}
			}
		}

		settings := &core.ThemeSettings{
			ThemeName: themeName,
			Colors:    colorsJSON,
			Layout:    layoutJSON,
			Custom:    customJSON,
		}

		if err := tm.SaveSettings(settings, data.DB); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		// Clear theme cache to apply new settings
		tm.Activate(tm.GetActive())

		return c.JSON(http.StatusOK, map[string]string{"message": "Settings saved"})
	})

	// Plugins
	api.GET("/plugins", func(c echo.Context) error {
		entries, err := os.ReadDir("data/plugins")
		if err != nil {
			return c.JSON(http.StatusOK, []interface{}{})
		}
		var plugins []map[string]interface{}
		for _, e := range entries {
			if e.IsDir() {
				pluginDir := filepath.Join("data/plugins", e.Name())
				configPath := filepath.Join(pluginDir, "plugin.json")
				if data, err := os.ReadFile(configPath); err == nil {
					var meta plugin.PluginMetadata
					if json.Unmarshal(data, &meta) == nil {
						plugins = append(plugins, map[string]interface{}{
							"id":          meta.ID,
							"name":        meta.Name,
							"version":     meta.Version,
							"description": meta.Description,
							"author":      meta.Author,
						})
					}
				}
			}
		}
		return c.JSON(http.StatusOK, plugins)
	})

	// Reload plugin
	api.POST("/plugins/:id/reload", func(c echo.Context) error {
		_ = c.Param("id")
		// Get plugin runtime from context or global
		// For now, we'll need to pass it through context or make it global
		// This is a simplified version
		return c.JSON(http.StatusOK, map[string]string{"message": "Plugin reload not fully implemented"})
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

	// Payment Gateway Settings
	api.GET("/payment/gateways", func(c echo.Context) error {
		// Get payment service from global or context
		// For now, we'll create a temporary service to get configs
		service := payment.NewService(data.DB)
		configs := service.GetPaymentConfigs()
		return c.JSON(http.StatusOK, configs)
	})

	api.POST("/payment/gateways/ecpay", func(c echo.Context) error {
		var req struct {
			Enabled    bool   `json:"enabled"`
			MerchantID string `json:"merchant_id"`
			HashKey    string `json:"hash_key"`
			HashIV     string `json:"hash_iv"`
			TestMode   bool   `json:"test_mode"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		service := payment.NewService(data.DB)
		config := &payment.ECPayConfig{
			MerchantID: req.MerchantID,
			HashKey:    req.HashKey,
			HashIV:     req.HashIV,
			IsTestMode: req.TestMode,
		}

		if err := service.SaveECPayConfig(config, req.Enabled); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		// Payment service will reload from DB on next use
		// The service is initialized in main.go and will reload configs when needed

		return c.JSON(http.StatusOK, map[string]string{"message": "ECPay configuration saved"})
	})

	api.POST("/payment/gateways/newebpay", func(c echo.Context) error {
		var req struct {
			Enabled    bool   `json:"enabled"`
			MerchantID string `json:"merchant_id"`
			HashKey    string `json:"hash_key"`
			HashIV     string `json:"hash_iv"`
			TestMode   bool   `json:"test_mode"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		service := payment.NewService(data.DB)
		config := &payment.NewebPayConfig{
			MerchantID: req.MerchantID,
			HashKey:    req.HashKey,
			HashIV:     req.HashIV,
			IsTestMode: req.TestMode,
		}

		if err := service.SaveNewebPayConfig(config, req.Enabled); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		// Payment service will reload from DB on next use
		// The service is initialized in main.go and will reload configs when needed

		return c.JSON(http.StatusOK, map[string]string{"message": "NewebPay configuration saved"})
	})
}
