package server

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

		// Save active theme to database
		var opt core.Option
		result := data.DB.Where("key = ?", "active_theme").First(&opt)
		if result.Error != nil {
			opt = core.Option{Key: "active_theme", Value: req.Name}
		} else {
			opt.Value = req.Name
		}
		data.DB.Save(&opt)

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

	// Upload plugin
	api.POST("/plugins/upload", func(c echo.Context) error {
		file, err := c.FormFile("plugin")
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "No file uploaded"})
		}

		// Check file extension
		if !strings.HasSuffix(strings.ToLower(file.Filename), ".zip") {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Only ZIP files are supported"})
		}

		// Open uploaded file
		src, err := file.Open()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to open uploaded file"})
		}
		defer src.Close()

		// Create temporary file
		tmpPath := filepath.Join(os.TempDir(), file.Filename)
		dst, err := os.Create(tmpPath)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to create temporary file"})
		}
		defer dst.Close()
		defer os.Remove(tmpPath) // Clean up temp file

		// Copy uploaded file to temp
		if _, err = io.Copy(dst, src); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to save uploaded file"})
		}
		dst.Close()

		// Extract ZIP file
		pluginsDir := "data/plugins"
		os.MkdirAll(pluginsDir, 0755)

		// Use archive/zip to extract
		r, err := zip.OpenReader(tmpPath)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid ZIP file: " + err.Error()})
		}
		defer r.Close()

		// Find plugin.json to determine plugin directory name
		var pluginID string
		var pluginJSONFound bool
		for _, f := range r.File {
			if f.Name == "plugin.json" || strings.HasSuffix(f.Name, "/plugin.json") {
				// Read plugin.json to get plugin ID
				rc, err := f.Open()
				if err != nil {
					continue
				}
				var meta plugin.PluginMetadata
				if json.NewDecoder(rc).Decode(&meta) == nil {
					pluginID = meta.ID
					pluginJSONFound = true
				}
				rc.Close()
				break
			}
		}

		if !pluginJSONFound {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "plugin.json not found in ZIP file"})
		}

		if pluginID == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid plugin.json: ID field is required"})
		}

		// Extract to plugin directory
		pluginDir := filepath.Join(pluginsDir, pluginID)
		os.MkdirAll(pluginDir, 0755)

		for _, f := range r.File {
			// Skip directories and hidden files
			if f.FileInfo().IsDir() || strings.HasPrefix(f.Name, ".") || strings.Contains(f.Name, "..") {
				continue
			}

			// Get relative path from ZIP root
			relPath := f.Name
			if idx := strings.Index(relPath, "/"); idx >= 0 {
				relPath = relPath[idx+1:]
			}
			if relPath == "" {
				continue
			}

			destPath := filepath.Join(pluginDir, relPath)
			os.MkdirAll(filepath.Dir(destPath), 0755)

			rc, err := f.Open()
			if err != nil {
				continue
			}

			dstFile, err := os.Create(destPath)
			if err != nil {
				rc.Close()
				continue
			}

			io.Copy(dstFile, rc)
			rc.Close()
			dstFile.Close()
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Plugin uploaded and extracted successfully", "plugin_id": pluginID})
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

	// OAuth Provider Settings
	api.GET("/oauth/providers", func(c echo.Context) error {
		result := make(map[string]interface{})

		// Google OAuth config
		var opt core.Option
		googleEnabled := false
		if err := data.DB.Where("key = ?", "oauth_google_enabled").First(&opt).Error; err == nil {
			googleEnabled = opt.Value == "true"
		}

		googleConfig := make(map[string]string)
		if googleEnabled {
			keys := []string{"oauth_google_client_id", "oauth_google_client_secret"}
			for _, key := range keys {
				if err := data.DB.Where("key = ?", key).First(&opt).Error; err == nil {
					googleConfig[key] = opt.Value
				}
			}
		}
		result["google"] = map[string]interface{}{
			"enabled": googleEnabled,
			"config":  googleConfig,
		}

		// GitHub OAuth config
		githubEnabled := false
		if err := data.DB.Where("key = ?", "oauth_github_enabled").First(&opt).Error; err == nil {
			githubEnabled = opt.Value == "true"
		}

		githubConfig := make(map[string]string)
		if githubEnabled {
			keys := []string{"oauth_github_client_id", "oauth_github_client_secret"}
			for _, key := range keys {
				if err := data.DB.Where("key = ?", key).First(&opt).Error; err == nil {
					githubConfig[key] = opt.Value
				}
			}
		}
		result["github"] = map[string]interface{}{
			"enabled": githubEnabled,
			"config":  githubConfig,
		}

		return c.JSON(http.StatusOK, result)
	})

	api.POST("/oauth/providers/google", func(c echo.Context) error {
		var req struct {
			Enabled      bool   `json:"enabled"`
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		settings := map[string]string{
			"oauth_google_enabled":       fmt.Sprintf("%v", req.Enabled),
			"oauth_google_client_id":     req.ClientID,
			"oauth_google_client_secret": req.ClientSecret,
		}

		for k, v := range settings {
			var opt core.Option
			result := data.DB.Where("key = ?", k).First(&opt)
			if result.Error != nil {
				opt = core.Option{Key: k, Value: v}
			} else {
				opt.Value = v
			}
			if err := data.DB.Save(&opt).Error; err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			}
		}

		// Reload OAuth providers
		auth.ReloadOAuthProviders()

		return c.JSON(http.StatusOK, map[string]string{"message": "Google OAuth configuration saved"})
	})

	api.POST("/oauth/providers/github", func(c echo.Context) error {
		var req struct {
			Enabled      bool   `json:"enabled"`
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		settings := map[string]string{
			"oauth_github_enabled":       fmt.Sprintf("%v", req.Enabled),
			"oauth_github_client_id":     req.ClientID,
			"oauth_github_client_secret": req.ClientSecret,
		}

		for k, v := range settings {
			var opt core.Option
			result := data.DB.Where("key = ?", k).First(&opt)
			if result.Error != nil {
				opt = core.Option{Key: k, Value: v}
			} else {
				opt.Value = v
			}
			if err := data.DB.Save(&opt).Error; err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			}
		}

		// Reload OAuth providers
		auth.ReloadOAuthProviders()

		return c.JSON(http.StatusOK, map[string]string{"message": "GitHub OAuth configuration saved"})
	})
}
