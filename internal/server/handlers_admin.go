package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/payment"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// uploadBodyLimit is the request size cap for routes that accept files. Every
// other route is limited to the global 2 MiB in RegisterRoutes.
const uploadBodyLimit = "64M"

func RegisterAdminRoutes(e *echo.Echo, tm *theme.Manager) {
	registerAdminRoutes(e, tm, nil)
}

func registerAdminRoutes(e *echo.Echo, tm *theme.Manager, pluginRuntime *plugin.Runtime) {
	// Content and media are available to known content roles. All other admin
	// endpoints are registered on the admin-only group below.
	content := e.Group("/api/admin", auth.RequireRole("admin", "editor", "author"))
	registerPostRoutes(content)
	api := e.Group("/api/admin", auth.RequireRole("admin"))

	// Media
	content.POST("/media", func(c echo.Context) error {
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
	}, middleware.BodyLimit(uploadBodyLimit))

	content.DELETE("/media/:filename", func(c echo.Context) error {
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

	content.PUT("/media/:filename", func(c echo.Context) error {
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
	}, middleware.BodyLimit(uploadBodyLimit))

	content.POST("/media/rename", func(c echo.Context) error {
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

	content.GET("/media", func(c echo.Context) error {
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

	// Upload plugin. The archive is validated and extracted by plugin.InstallArchive.
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

		// Copy the upload to a temporary file with a generated name.
		tmp, err := os.CreateTemp("", "jelly-plugin-*.zip")
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to create temporary file"})
		}
		tmpPath := tmp.Name()
		defer os.Remove(tmpPath)
		if _, err = io.Copy(tmp, src); err != nil {
			tmp.Close()
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to save uploaded file"})
		}
		if err := tmp.Close(); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to save uploaded file"})
		}

		pluginID, err := plugin.InstallArchive(tmpPath, filepath.Join("data", "plugins"))
		if err != nil {
			if plugin.IsArchiveError(err) {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Plugin installation failed"})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Plugin uploaded and extracted successfully", "plugin_id": pluginID})
	}, middleware.BodyLimit(uploadBodyLimit))

	// Reload plugin
	api.POST("/plugins/:id/reload", func(c echo.Context) error {
		if pluginRuntime == nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Plugin runtime is unavailable"})
		}

		meta, err := pluginRuntime.GetPlugin(c.Param("id"))
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Plugin not found"})
		}

		dir := filepath.Join("data", "plugins", meta.ID)
		if err := pluginRuntime.ReloadForID(c.Request().Context(), meta.ID, dir); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Plugin reload failed"})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Plugin reloaded"})
	})

	// Settings
	// GET masks secret values; POST accepts only registered keys and audits the write.
	api.GET("/settings", func(c echo.Context) error {
		var options []core.Option
		if err := data.DB.WithContext(c.Request().Context()).Find(&options).Error; err != nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
		}
		for i := range options {
			options[i].Value = settings.Masked(options[i].Key, options[i].Value)
		}
		return c.JSON(http.StatusOK, options)
	})

	api.POST("/settings", func(c echo.Context) error {
		var req map[string]string
		if err := c.Bind(&req); err != nil {
			return err
		}
		// The three maintenance keys keep ticket 05's validation and the
		// started_at stamping; everything else goes through the registry.
		maintenance := make(map[string]string, 3)
		for _, key := range []string{maintenanceEnabledKey, maintenanceMessageKey, maintenanceStartedKey} {
			if value, ok := req[key]; ok {
				maintenance[key] = value
				delete(req, key)
			}
		}
		var unknown []string
		for k := range req {
			if _, ok := settings.Lookup(k); !ok {
				unknown = append(unknown, k)
			}
		}
		if len(unknown) > 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Unknown setting keys: " + strings.Join(unknown, ", ")})
		}
		if len(maintenance) > 0 {
			if data.DB == nil {
				return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
			}
			if err := saveMaintenanceOptions(data.DB.WithContext(c.Request().Context()), maintenance); err != nil {
				status := http.StatusServiceUnavailable
				if errors.Is(err, errInvalidMaintenanceSetting) {
					status = http.StatusBadRequest
				}
				return c.JSON(status, map[string]string{"error": "Unable to save settings"})
			}
		}
		actor, _ := auth.GetUserFromContext(c)
		var actorID uint
		if actor != nil {
			actorID = actor.ID
		}
		if err := settings.Set(c.Request().Context(), actorID, req); err != nil {
			switch {
			case errors.Is(err, settings.ErrTooLarge):
				return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "Setting value is too large"})
			case errors.Is(err, settings.ErrUnknownKey), errors.Is(err, settings.ErrInvalidValue):
				return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Settings save failed"})
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
		if data.DB == nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
		}

		keys := []string{
			"oauth_google_enabled", "oauth_google_client_id", "oauth_google_client_secret",
			"oauth_github_enabled", "oauth_github_client_id", "oauth_github_client_secret",
		}
		var options []core.Option
		if err := data.DB.WithContext(c.Request().Context()).Where("key IN ?", keys).Find(&options).Error; err != nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
		}
		values := make(map[string]string, len(options))
		for _, option := range options {
			values[option.Key] = option.Value
		}

		googleConfig := make(map[string]string)
		for _, key := range []string{"oauth_google_client_id", "oauth_google_client_secret"} {
			if value, ok := values[key]; ok {
				googleConfig[key] = value
			}
		}
		githubConfig := make(map[string]string)
		for _, key := range []string{"oauth_github_client_id", "oauth_github_client_secret"} {
			if value, ok := values[key]; ok {
				githubConfig[key] = value
			}
		}

		return c.JSON(http.StatusOK, map[string]interface{}{
			"google": map[string]interface{}{
				"enabled": values["oauth_google_enabled"] == "true",
				"config":  googleConfig,
			},
			"github": map[string]interface{}{
				"enabled": values["oauth_github_enabled"] == "true",
				"config":  githubConfig,
			},
		})
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
