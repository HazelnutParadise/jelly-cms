package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/safehttp"
	"gorm.io/gorm"
)

const (
	pluginHostJSONMax       = 1 << 20
	pluginHostHTTPBodyMax   = 512 << 10
	pluginHostHTTPHeaderMax = 64 << 10
)

var (
	pluginHostCollectionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	pluginHostKeyPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`)
	pluginHostSecretHeaders     = map[string]bool{"authorization": true, "cookie": true, "proxy-authorization": true, "set-cookie": true}
	pluginHostHopHeaders        = map[string]bool{"connection": true, "keep-alive": true, "proxy-connection": true, "transfer-encoding": true, "upgrade": true, "te": true, "trailer": true, "host": true, "content-length": true, "expect": true}
)

// ConfigurePluginHost binds the parent process APIs to one runtime. It is
// safe to call before routes or before loading plugins; the closure resolves
// the active database for every request so installation can replace it.
func ConfigurePluginHost(runtime *plugin.Runtime) {
	setExtensionRuntime(runtime)
	if runtime != nil {
		runtime.SetHostHandler(handlePluginHostCall)
	}
}

func handlePluginHostCall(ctx context.Context, meta plugin.PluginMetadata, method string, raw json.RawMessage) (json.RawMessage, error) {
	args, err := decodePluginHostArgs(raw)
	if err != nil {
		return nil, err
	}
	db := data.DB
	if db == nil {
		return nil, errors.New("plugin host database is unavailable")
	}
	switch method {
	case "config.get", "settings.get":
		if err := requirePluginHostPermission(ctx, db, meta, "settings"); err != nil {
			return nil, err
		}
		key, err := hostStringArg(args, 0, 64)
		if err != nil {
			return nil, err
		}
		return readPluginHostSetting(ctx, db, meta, key)
	case "config.set", "settings.set":
		if err := requirePluginHostPermission(ctx, db, meta, "settings"); err != nil {
			return nil, err
		}
		key, err := hostStringArg(args, 0, 64)
		if err != nil {
			return nil, err
		}
		if len(args) != 2 {
			return nil, errors.New("settings.set expects a key and value")
		}
		return writePluginHostSetting(ctx, meta, key, args[1])
	case "store.get", "store.set", "store.delete":
		if err := requirePluginHostPermission(ctx, db, meta, "store"); err != nil {
			return nil, err
		}
		return handlePluginHostStore(ctx, db, meta, method, args)
	case "http.fetch":
		if err := requirePluginHostPermission(ctx, db, meta, "http.fetch"); err != nil {
			return nil, err
		}
		return handlePluginHostHTTP(ctx, meta, args)
	default:
		return nil, errors.New("unknown plugin host method")
	}
}

func decodePluginHostArgs(raw []byte) ([]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > pluginHostJSONMax || !json.Valid(raw) || serverJSONDepth(raw) > 64 {
		return nil, errors.New("plugin host arguments are invalid or too large")
	}
	var args []json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errors.New("plugin host arguments must be an array")
	}
	return args, nil
}

func requirePluginHostPermission(ctx context.Context, db *gorm.DB, meta plugin.PluginMetadata, name string) error {
	var spec *plugin.PermissionSpec
	for i := range meta.Permissions {
		if meta.Permissions[i].Name == name {
			spec = &meta.Permissions[i]
			break
		}
	}
	if spec == nil {
		return fmt.Errorf("plugin permission denied: %s", name)
	}
	var row struct {
		Enabled bool   `gorm:"column:enabled"`
		Granted string `gorm:"column:permissions_granted_hash"`
	}
	err := db.WithContext(ctx).Table("plugins").Select("enabled, permissions_granted_hash").Where("id = ?", meta.ID).Take(&row).Error
	if err != nil || (!row.Enabled && !plugin.IsHostActivation(ctx, meta.ID)) {
		return fmt.Errorf("plugin permission denied: %s", name)
	}
	expected := plugin.PermissionNamesHash(plugin.ManifestPermissionNames(meta))
	if row.Granted == "" || row.Granted != expected {
		return fmt.Errorf("plugin permission approval is stale: %s", name)
	}
	return nil
}

func hostStringArg(args []json.RawMessage, index, max int) (string, error) {
	if len(args) <= index {
		return "", errors.New("plugin host argument is missing")
	}
	var value string
	if err := json.Unmarshal(args[index], &value); err != nil || value == "" || len(value) > max {
		return "", errors.New("plugin host string argument is invalid")
	}
	return value, nil
}

func readPluginHostSetting(ctx context.Context, db *gorm.DB, meta plugin.PluginMetadata, key string) (json.RawMessage, error) {
	spec, ok := plugin.FindSetting(meta.Settings, key)
	if !ok {
		return nil, errors.New("setting is not declared by the plugin")
	}
	plain, secrets, _, err := readSettingsRow(db.WithContext(ctx), meta.ID)
	if err != nil {
		return nil, errors.New("plugin settings could not be read")
	}
	if spec.Type == plugin.SettingSecret {
		if value, exists := secrets[key]; exists {
			if err := plugin.CheckSettingValues(meta.Settings, map[string]any{key: value}); err != nil {
				return nil, errors.New("stored plugin setting is invalid")
			}
			return json.Marshal(value)
		}
	} else if value, exists := plain[key]; exists {
		if err := plugin.CheckSettingValues(meta.Settings, map[string]any{key: value}); err != nil {
			return nil, errors.New("stored plugin setting is invalid")
		}
		return json.Marshal(value)
	}
	if spec.Default != nil && spec.Type != plugin.SettingSecret {
		return json.Marshal(spec.Default)
	}
	return nil, nil
}

func writePluginHostSetting(ctx context.Context, meta plugin.PluginMetadata, key string, raw json.RawMessage) (json.RawMessage, error) {
	if _, ok := plugin.FindSetting(meta.Settings, key); !ok {
		return nil, errors.New("setting is not declared by the plugin")
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, errors.New("setting value is invalid JSON")
	}
	if err := plugin.CheckSettingValues(meta.Settings, map[string]any{key: value}); err != nil {
		return nil, err
	}
	_, err := writePluginSettings(ctx, meta.ID, func(plain map[string]any, secrets map[string]string) {
		if plugin.IsSecretSetting(meta.Settings, key) {
			if value == nil {
				delete(secrets, key)
			} else {
				secrets[key] = value.(string)
			}
		} else if value == nil {
			delete(plain, key)
		} else {
			plain[key] = value
		}
	})
	if err != nil {
		return nil, errors.New("plugin setting could not be saved")
	}
	return json.Marshal(map[string]bool{"stored": true, "persisted": true})
}

func handlePluginHostStore(ctx context.Context, db *gorm.DB, meta plugin.PluginMetadata, method string, args []json.RawMessage) (json.RawMessage, error) {
	collection, err := hostStringArg(args, 0, 64)
	if err != nil || !pluginHostCollectionPattern.MatchString(collection) {
		return nil, errors.New("plugin store collection is invalid")
	}
	key, err := hostStringArg(args, 1, 255)
	if err != nil || !pluginHostKeyPattern.MatchString(key) {
		return nil, errors.New("plugin store key is invalid")
	}
	switch method {
	case "store.get":
		if len(args) != 2 {
			return nil, errors.New("store.get expects a collection and key")
		}
		var value string
		err := db.WithContext(ctx).Raw(`SELECT value::text FROM plugin_data WHERE plugin_id = ? AND collection = ? AND key = ?`, meta.ID, collection, key).Scan(&value).Error
		if err != nil {
			return nil, errors.New("plugin store read failed")
		}
		if value == "" {
			return nil, nil
		}
		return json.RawMessage(value), nil
	case "store.set":
		if len(args) != 3 || len(args[2]) == 0 || len(args[2]) > pluginHostJSONMax || !json.Valid(args[2]) || serverJSONDepth(args[2]) > 64 {
			return nil, errors.New("store.set value is invalid or too large")
		}
		err := db.WithContext(ctx).Exec(`INSERT INTO plugin_data (plugin_id, collection, key, value, updated_at) VALUES (?, ?, ?, CAST(? AS jsonb), now()) ON CONFLICT (plugin_id, collection, key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`, meta.ID, collection, key, string(args[2])).Error
		if err != nil {
			return nil, errors.New("plugin store write failed")
		}
		return json.Marshal(map[string]bool{"stored": true, "persisted": true})
	case "store.delete":
		if len(args) != 2 {
			return nil, errors.New("store.delete expects a collection and key")
		}
		result := db.WithContext(ctx).Exec(`DELETE FROM plugin_data WHERE plugin_id = ? AND collection = ? AND key = ?`, meta.ID, collection, key)
		if result.Error != nil {
			return nil, errors.New("plugin store delete failed")
		}
		return json.Marshal(map[string]bool{"deleted": result.RowsAffected > 0})
	default:
		return nil, errors.New("unknown plugin store operation")
	}
}

type pluginHTTPOptions struct {
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

func handlePluginHostHTTP(ctx context.Context, meta plugin.PluginMetadata, args []json.RawMessage) (json.RawMessage, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, errors.New("http.fetch expects a URL and optional options")
	}
	urlText, err := hostStringArg(args, 0, 8192)
	if err != nil {
		return nil, err
	}
	permission, _ := pluginPermission(meta, "http.fetch")
	if len(permission.Hosts) == 0 {
		return nil, errors.New("http.fetch requires an explicit host allowlist")
	}
	parsed, err := url.ParseRequestURI(urlText)
	if err != nil || parsed.User != nil || parsed.Opaque != "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return nil, errors.New("http.fetch URL is invalid")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != parsed.Hostname() || !exactHostAllowed(host, permission.Hosts) {
		return nil, errors.New("http.fetch host is not allowed")
	}
	if parsed.Port() != "" {
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("http.fetch port is invalid")
		}
	}
	options := pluginHTTPOptions{Method: http.MethodGet}
	if len(args) == 2 && string(args[1]) != "null" {
		if err := json.Unmarshal(args[1], &options); err != nil {
			return nil, errors.New("http.fetch options are invalid")
		}
		if options.Method == "" {
			options.Method = http.MethodGet
		}
	}
	options.Method = strings.ToUpper(options.Method)
	switch options.Method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead:
	default:
		return nil, errors.New("http.fetch method is not allowed")
	}
	if len(options.Body) > pluginHostHTTPBodyMax || len(options.Headers) > 32 {
		return nil, errors.New("http.fetch request is too large")
	}
	request, err := http.NewRequestWithContext(ctx, options.Method, parsed.String(), bytes.NewReader([]byte(options.Body)))
	if err != nil {
		return nil, errors.New("http.fetch request is invalid")
	}
	for key, value := range options.Headers {
		canonical := http.CanonicalHeaderKey(key)
		lower := strings.ToLower(canonical)
		if !validPluginHostHeaderName(key) || pluginHostSecretHeaders[lower] || pluginHostHopHeaders[lower] || strings.ContainsAny(value, "\r\n") || len(value) > 8192 {
			return nil, errors.New("http.fetch header is not allowed")
		}
		request.Header.Set(canonical, value)
	}
	client := safehttp.Client(safehttp.Options{Timeout: time.Second, MaxBytes: pluginHostHTTPBodyMax, AllowHosts: permission.Hosts})
	client.Transport = &pluginHostHeaderLimit{base: client.Transport, maxBytes: pluginHostHTTPHeaderMax}
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= safehttp.MaxRedirects || next.URL.User != nil || next.URL.Opaque != "" || next.URL.Fragment != "" || (next.URL.Scheme != "http" && next.URL.Scheme != "https") || !exactHostAllowed(strings.ToLower(next.URL.Hostname()), permission.Hosts) {
			return errors.New("http.fetch redirect host is not allowed")
		}
		if parsed.Scheme == "https" && next.URL.Scheme != "https" {
			return errors.New("http.fetch cannot downgrade HTTPS")
		}
		for name := range next.Header {
			if pluginHostSecretHeaders[strings.ToLower(name)] || pluginHostHopHeaders[strings.ToLower(name)] {
				next.Header.Del(name)
			}
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("http.fetch request failed or reached a blocked address")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, pluginHostHTTPBodyMax+1))
	if err != nil || len(body) > pluginHostHTTPBodyMax {
		return nil, errors.New("http.fetch response body is too large")
	}
	headers := make(map[string]string)
	for name, values := range response.Header {
		lower := strings.ToLower(name)
		if pluginHostSecretHeaders[lower] || pluginHostHopHeaders[lower] {
			continue
		}
		joined := strings.Join(values, ", ")
		if len(joined) <= 8192 {
			headers[name] = joined
		}
	}
	result, err := json.Marshal(map[string]any{"status": response.StatusCode, "headers": headers, "body": string(body)})
	if err != nil || len(result) > pluginHostJSONMax {
		return nil, errors.New("http.fetch response is too large")
	}
	return result, nil
}

type pluginHostHeaderLimit struct {
	base     http.RoundTripper
	maxBytes int
}

func (t *pluginHostHeaderLimit) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	total := 0
	for key, values := range response.Header {
		total += len(key)
		for _, value := range values {
			total += len(value)
		}
		if total > t.maxBytes {
			_ = response.Body.Close()
			return nil, errors.New("http.fetch response headers are too large")
		}
	}
	return response, nil
}

func validPluginHostHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, char := range name {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			continue
		}
		switch char {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		}
		return false
	}
	return true
}

func pluginPermission(meta plugin.PluginMetadata, name string) (plugin.PermissionSpec, bool) {
	for _, permission := range meta.Permissions {
		if permission.Name == name {
			return permission, true
		}
	}
	return plugin.PermissionSpec{}, false
}

func exactHostAllowed(host string, hosts []string) bool {
	for _, allowed := range hosts {
		if host == allowed {
			return true
		}
	}
	return false
}

func serverJSONDepth(raw []byte) int {
	depth, maximum := 0, 0
	inString, escaped := false, false
	for _, b := range raw {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if b == '\\' {
				escaped = true
				continue
			}
			if b == '"' {
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > maximum {
				maximum = depth
			}
		case '}', ']':
			depth--
		}
	}
	return maximum
}
