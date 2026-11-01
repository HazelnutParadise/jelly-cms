package server

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/integration"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

var integrationPathParam = regexp.MustCompile(`:([A-Za-z0-9_]+)`)

func openAPIPath(path string) string { return integrationPathParam.ReplaceAllString(path, "{$1}") }
func integrationFullPath(path string) string {
	if path == "" {
		return machineAPIBasePath
	}
	return machineAPIBasePath + path
}
func integrationDiscovery(tm *theme.Manager, rt *plugin.Runtime) echo.HandlerFunc {
	return func(c echo.Context) error {
		rows := integrationRouteTable(tm, rt)
		perms := integration.AllPermissions()
		permEndpoints := map[string][]map[string]any{}
		permRoles := map[string][]string{}
		endpoints := make([]map[string]any, 0, len(rows))
		for _, r := range rows {
			ep := map[string]any{"method": r.Method, "path": integrationFullPath(openAPIPath(r.Path)), "permission": string(r.Permission), "summary": r.Summary, "required_roles": r.RequiredRoles, "version_guard": r.VersionGuard, "auth": string(r.Auth)}
			endpoints = append(endpoints, ep)
			if r.Permission != "" {
				p := string(r.Permission)
				permEndpoints[p] = append(permEndpoints[p], ep)
				permRoles[p] = r.RequiredRoles
			}
		}
		permissions := make([]map[string]any, 0, len(perms))
		for _, p := range perms {
			roles := permRoles[string(p)]
			if roles == nil {
				roles = permissionRoles(string(p))
			}
			permissions = append(permissions, map[string]any{"name": string(p), "description": permissionDescription(string(p)), "required_roles": roles, "endpoints": permEndpoints[string(p)]})
		}
		return c.JSON(200, map[string]any{"name": "Jelly CMS API", "version": "v1", "base_path": machineAPIBasePath, "openapi_url": machineAPIBasePath + "/openapi.json", "skill_path": "skills/jelly-cms/SKILL.md", "authentication": map[string]any{"type": "bearer", "header": "Authorization", "scheme": "Bearer", "key_prefix": "jly_", "how_to_get": "由管理員在 Jelly CMS 後台建立", "description": "Send the access key in the Authorization header as a Bearer token."}, "permissions": permissions, "endpoints": endpoints, "concurrency": integrationConcurrency(rows), "errors": map[string]any{"format": map[string]any{"code": "…", "message": "…", "details": map[string]any{}, "correlation_id": "req_…"}, "codes": integrationErrorCodes()}, "not_exposed": integrationNotExposed(), "examples": []map[string]any{{"title": "確認金鑰與有效權限", "request": "GET " + machineAPIBasePath + "/me", "response": map[string]any{"id": 1, "effective_permissions": []string{"posts.read"}}}, {"title": "以版本更新文章", "request": "PUT " + machineAPIBasePath + "/posts/1 with base_updated_at", "response": map[string]any{"updated_at": "RFC3339 timestamp"}}}})
	}
}
func permissionDescription(p string) string {
	return map[string]string{"posts.read": "讀取文章與內容分類。", "posts.write": "建立、更新與刪除文章。", "pages.read": "讀取頁面。", "pages.write": "更新頁面。", "media.read": "列出媒體附件。", "media.write": "上傳、更新與刪除媒體。", "products.read": "讀取商品。", "products.write": "建立、更新與刪除商品。", "orders.read": "讀取訂單。", "themes.read": "檢視主題與設定。", "themes.write": "啟用主題及更新主題設定。", "plugins.read": "列出外掛。", "plugins.write": "重新載入外掛。", "settings.read": "讀取已遮罩的系統設定。", "settings.write": "更新系統設定。", "logs.read": "讀取此串接自己的活動紀錄。"}[p]
}
func integrationErrorCodes() []map[string]any {
	codes := []struct {
		c string
		s int
	}{{"key_missing", 401}, {"key_invalid", 401}, {"key_revoked", 401}, {"key_expired", 401}, {"agent_disabled", 401}, {"owner_unavailable", 401}, {"permission_missing", 403}, {"owner_role_insufficient", 403}, {"forbidden", 403}, {"version_required", 400}, {"version_invalid", 400}, {"type_mismatch", 400}, {"logs_filter_invalid", 400}, {"bad_request", 400}, {"name_invalid", 400}, {"description_too_long", 400}, {"permissions_required", 400}, {"permission_unknown", 400}, {"rotation_mode_invalid", 400}, {"grace_out_of_range", 400}, {"not_found", 404}, {"agent_not_found", 404}, {"key_not_found", 404}, {"method_not_allowed", 405}, {"version_conflict", 409}, {"agent_name_taken", 409}, {"conflict", 409}, {"payload_too_large", 413}, {"internal_error", 500}, {"service_unavailable", 503}}
	out := make([]map[string]any, 0, len(codes))
	for _, v := range codes {
		out = append(out, map[string]any{"code": v.c, "status": v.s, "description": integrationErrorDescription(v.c)})
	}
	return out
}
func integrationConcurrency(rows []integrationRoute) map[string]any {
	paths := []string{}
	for _, r := range rows {
		if r.VersionGuard {
			paths = append(paths, r.Method+" "+integrationFullPath(openAPIPath(r.Path)))
		}
	}
	return map[string]any{"field": "base_updated_at", "applies_to": paths, "conflict_code": "version_conflict"}
}
func integrationErrorDescription(code string) string {
	return map[string]string{"key_missing": "Bearer access key is required", "key_invalid": "Access key is invalid", "key_revoked": "Access key was revoked", "key_expired": "Access key expired", "agent_disabled": "Integration is disabled", "owner_unavailable": "Integration owner is unavailable", "permission_missing": "Access key lacks a required permission", "owner_role_insufficient": "Owner role cannot use this permission", "forbidden": "Access is forbidden", "version_required": "base_updated_at is required", "version_invalid": "base_updated_at is invalid", "type_mismatch": "Resource type does not match this endpoint", "logs_filter_invalid": "Event filter is invalid", "bad_request": "Request is invalid", "name_invalid": "Integration name is invalid", "description_too_long": "Integration description is too long", "permissions_required": "At least one permission is required", "permission_unknown": "Permission is unknown", "rotation_mode_invalid": "Key rotation mode is invalid", "grace_out_of_range": "Grace period is out of range", "not_found": "Endpoint was not found", "agent_not_found": "Integration was not found", "key_not_found": "Access key was not found", "method_not_allowed": "HTTP method is not allowed", "version_conflict": "Resource changed since it was read", "agent_name_taken": "Integration name is already in use", "conflict": "Request conflicts with current state", "payload_too_large": "Request body is too large", "internal_error": "Internal server error", "service_unavailable": "Service is unavailable"}[code]
}
func integrationNotExposed() []map[string]string {
	return []map[string]string{{"path": "/api/admin/comments*", "reason": "留言沒有對應的串接權限"}, {"path": "/api/admin/menus*", "reason": "選單沒有對應的串接權限"}, {"path": "/api/admin/categories* write", "reason": "分類寫入沒有對應的串接權限"}, {"path": "/api/admin/tags* write", "reason": "標籤寫入沒有對應的串接權限"}, {"path": "/api/admin/trash*", "reason": "回收桶沒有對應的串接權限"}, {"path": "/api/admin/posts/:id/revisions*", "reason": "文章版本沒有對應的串接權限"}, {"path": "/api/admin/payment/gateways*", "reason": "金流憑證不開放給串接"}, {"path": "/api/admin/oauth/providers*", "reason": "第三方憑證不開放給串接"}, {"path": "/api/admin/plugins/upload", "reason": "安裝外掛等於執行程式碼"}, {"path": "/api/admin/agents*", "reason": "串接不能管理串接"}}
}
func integrationOpenAPI(tm *theme.Manager, rt *plugin.Runtime) echo.HandlerFunc {
	return func(c echo.Context) error {
		paths := map[string]any{}
		for _, r := range integrationRouteTable(tm, rt) {
			p := integrationFullPath(openAPIPath(r.Path))
			item, ok := paths[p].(map[string]any)
			if !ok {
				item = map[string]any{}
				for _, m := range integrationPathParam.FindAllStringSubmatch(r.Path, -1) {
					typeName := "string"
					if m[1] == "id" || m[1] == "key_id" {
						typeName = "integer"
					}
					item["parameters"] = append(anySlice(item["parameters"]), map[string]any{"name": m[1], "in": "path", "required": true, "schema": map[string]any{"type": typeName}})
				}
				paths[p] = item
			}
			op := map[string]any{"operationId": operationID(r.Method, r.Path), "summary": r.Summary, "responses": integrationOperationResponses(r)}
			if parameters := integrationQueryParameters(r); len(parameters) > 0 {
				op["parameters"] = parameters
			}
			if body := integrationRequestBody(r); body != nil {
				op["requestBody"] = body
			}
			if r.Auth == integrationRoutePublic {
				op["security"] = []any{}
			} else {
				op["security"] = []any{map[string]any{"AccessKey": []string{}}}
				if r.Permission != "" {
					op["x-jelly-permission"] = string(r.Permission)
					op["x-jelly-required-roles"] = r.RequiredRoles
				}
			}
			item[strings.ToLower(r.Method)] = op
		}
		errorSchema := map[string]any{
			"type":     "object",
			"required": []string{"code", "message", "details", "correlation_id"},
			"properties": map[string]any{
				"code":           map[string]any{"type": "string"},
				"message":        map[string]any{"type": "string"},
				"details":        map[string]any{"type": "object", "additionalProperties": true},
				"correlation_id": map[string]any{"type": "string"},
			},
		}
		doc := map[string]any{
			"openapi":  "3.1.0",
			"info":     map[string]any{"title": "Jelly CMS API", "version": "1.0.0"},
			"paths":    paths,
			"security": []any{map[string]any{"AccessKey": []string{}}},
			"components": map[string]any{
				"securitySchemes": map[string]any{"AccessKey": map[string]any{"type": "http", "scheme": "bearer", "description": "Jelly CMS access key (jly_…)"}},
				"schemas":         map[string]any{"Error": errorSchema},
			},
		}
		return c.JSON(http.StatusOK, doc)
	}
}
func anySlice(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return []any{}
}
func operationID(method, path string) string {
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == ':' || r == '-' })
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.ToLower(method) + strings.Join(parts, "")
}

func integrationSchemaObject(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func integrationArraySchema(item any) map[string]any {
	return map[string]any{"type": "array", "items": item}
}
func integrationStringSchema() map[string]any { return map[string]any{"type": "string"} }
func integrationIntSchema() map[string]any    { return map[string]any{"type": "integer"} }

func integrationResourceSchema(kind string) map[string]any {
	props := map[string]any{}
	if kind == "post" || kind == "page" {
		props = map[string]any{
			"id": integrationIntSchema(), "title": integrationStringSchema(), "slug": integrationStringSchema(), "content": integrationStringSchema(), "type": integrationStringSchema(), "status": integrationStringSchema(), "author_id": integrationIntSchema(), "author": map[string]any{"type": "object"}, "template": integrationStringSchema(), "meta": map[string]any{}, "published_at": map[string]any{"type": []string{"string", "null"}, "format": "date-time"}, "created_at": map[string]any{"type": "string", "format": "date-time"}, "updated_at": map[string]any{"type": "string", "format": "date-time"}, "excerpt": integrationStringSchema(), "cover": integrationStringSchema(), "allow_comments": map[string]any{"type": []string{"boolean", "null"}}, "pinned": map[string]any{"type": "boolean"}, "visibility": integrationStringSchema(), "categories": map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "object"}}, "tags": map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "object"}}, "source": integrationStringSchema(), "format": integrationStringSchema(), "seo_index": integrationStringSchema(), "seo_ai": integrationStringSchema(), "diagnostics": integrationArraySchema(map[string]any{"type": "object"}),
		}
		return integrationSchemaObject(props)
	}
	if kind == "product" {
		props = map[string]any{
			"id": integrationIntSchema(), "title": integrationStringSchema(), "slug": integrationStringSchema(), "description": integrationStringSchema(), "price": integrationIntSchema(), "sku": integrationStringSchema(), "stock": integrationIntSchema(), "images": map[string]any{}, "status": integrationStringSchema(), "created_at": map[string]any{"type": "string", "format": "date-time"}, "updated_at": map[string]any{"type": "string", "format": "date-time"}, "deleted_by_id": integrationIntSchema(), "deleted_at": map[string]any{"type": []string{"string", "null"}, "format": "date-time"}, "compare_at_price": integrationIntSchema(), "low_stock_threshold": integrationIntSchema(), "requires_shipping": map[string]any{"type": "boolean"}, "cold_chain": map[string]any{"type": "boolean"}, "attributes": map[string]any{}, "related_ids": map[string]any{}, "sold_count": integrationIntSchema(), "seo_title": integrationStringSchema(), "seo_description": integrationStringSchema(), "seo_index": integrationStringSchema(), "seo_ai": integrationStringSchema(), "kind": integrationStringSchema(), "variants": integrationArraySchema(map[string]any{"type": "object", "additionalProperties": true}), "categories": integrationArraySchema(integrationIntSchema()),
		}
		return integrationSchemaObject(props)
	}
	return integrationSchemaObject(map[string]any{})
}

func integrationMediaFileSchema() map[string]any {
	return integrationSchemaObject(map[string]any{
		"id": integrationIntSchema(), "policy_id": integrationIntSchema(), "group_id": map[string]any{"type": []string{"integer", "null"}}, "filename": integrationStringSchema(), "path": integrationStringSchema(), "url": integrationStringSchema(), "mime": integrationStringSchema(), "size": integrationIntSchema(), "width": map[string]any{"type": []string{"integer", "null"}}, "height": map[string]any{"type": []string{"integer", "null"}}, "alt": integrationStringSchema(), "sha256": integrationStringSchema(), "source": integrationStringSchema(), "exif_stripped": map[string]any{"type": "boolean"}, "trashed": map[string]any{"type": "boolean"}, "created_at": map[string]any{"type": "string", "format": "date-time"},
	})
}

func integrationProductInputSchema(includeVersion bool) map[string]any {
	variant := integrationSchemaObject(map[string]any{"id": integrationIntSchema(), "name": integrationStringSchema(), "sku": integrationStringSchema(), "price": integrationStringSchema(), "compare_at_price": integrationStringSchema(), "stock": integrationIntSchema(), "position": integrationIntSchema(), "status": integrationStringSchema()})
	props := map[string]any{"title": integrationStringSchema(), "slug": integrationStringSchema(), "description": integrationStringSchema(), "price": map[string]any{"oneOf": []any{integrationStringSchema(), integrationIntSchema()}}, "sku": map[string]any{"type": []string{"string", "null"}}, "stock": map[string]any{"type": []string{"integer", "null"}}, "kind": integrationStringSchema(), "status": integrationStringSchema(), "compare_at_price": integrationStringSchema(), "low_stock_threshold": map[string]any{"type": []string{"integer", "null"}}, "requires_shipping": map[string]any{"type": []string{"boolean", "null"}}, "cold_chain": map[string]any{"type": "boolean"}, "images": integrationArraySchema(integrationStringSchema()), "categories": integrationArraySchema(integrationIntSchema()), "variants": integrationArraySchema(variant), "attributes": map[string]any{"type": "object", "additionalProperties": integrationStringSchema()}, "related_ids": integrationArraySchema(integrationIntSchema()), "seo_title": integrationStringSchema(), "seo_description": integrationStringSchema(), "seo_index": map[string]any{"type": []string{"string", "null"}}, "seo_ai": map[string]any{"type": []string{"string", "null"}}}
	if includeVersion {
		props["base_updated_at"] = map[string]any{"type": "string", "format": "date-time"}
	}
	if includeVersion {
		return integrationSchemaObject(props, "base_updated_at")
	}
	return integrationSchemaObject(props, "title")
}

func integrationSuccessSchema(r integrationRoute) map[string]any {
	path := r.Path
	if path == "" {
		return integrationSchemaObject(map[string]any{"name": integrationStringSchema(), "version": integrationStringSchema(), "base_path": integrationStringSchema(), "openapi_url": integrationStringSchema(), "skill_path": integrationStringSchema(), "authentication": map[string]any{"type": "object"}, "permissions": integrationArraySchema(map[string]any{"type": "object"}), "endpoints": integrationArraySchema(map[string]any{"type": "object"}), "concurrency": map[string]any{"type": "object"}, "errors": map[string]any{"type": "object"}, "not_exposed": integrationArraySchema(map[string]any{"type": "object"}), "examples": integrationArraySchema(map[string]any{"type": "object"})})
	}
	if path == "/openapi.json" {
		return map[string]any{"type": "object", "additionalProperties": true}
	}
	if path == "/me" {
		return integrationSchemaObject(map[string]any{
			"id": integrationIntSchema(), "name": integrationStringSchema(), "description": integrationStringSchema(),
			"permissions": integrationArraySchema(integrationStringSchema()), "effective_permissions": integrationArraySchema(integrationStringSchema()),
			"owner_role": integrationStringSchema(), "key": integrationSchemaObject(map[string]any{
				"id": integrationIntSchema(), "prefix": integrationStringSchema(),
				"expires_at": map[string]any{"type": []string{"string", "null"}, "format": "date-time"},
			}),
		}, "id", "name", "permissions", "effective_permissions", "key")
	}
	if path == "/logs" {
		item := integrationSchemaObject(map[string]any{
			"id": integrationIntSchema(), "at": map[string]any{"type": "string", "format": "date-time"},
			"actor_type": integrationStringSchema(), "actor_user_id": map[string]any{"type": []string{"integer", "null"}},
			"agent_id": integrationIntSchema(), "agent_name": integrationStringSchema(), "key_id": map[string]any{"type": []string{"integer", "null"}},
			"key_prefix": integrationStringSchema(), "action": integrationStringSchema(), "module": integrationStringSchema(),
			"method": integrationStringSchema(), "route": integrationStringSchema(), "status": integrationIntSchema(),
			"ip_masked": integrationStringSchema(), "summary": map[string]any{"type": "object", "additionalProperties": true},
			"correlation_id": integrationStringSchema(),
		}, "id", "at", "actor_type", "agent_id", "agent_name", "action", "module", "method", "route", "status", "summary", "correlation_id")
		return integrationSchemaObject(map[string]any{"items": integrationArraySchema(item), "next_cursor": map[string]any{"type": []string{"string", "null"}}, "total": integrationIntSchema()}, "items", "next_cursor", "total")
	}
	if path == "/posts" || path == "/pages" {
		kind := strings.TrimSuffix(strings.TrimPrefix(path, "/"), "s")
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			return integrationResourceSchema(kind)
		}
		return map[string]any{"type": []string{"array", "null"}, "items": integrationResourceSchema(kind)}
	}
	if path == "/posts/:id" || path == "/pages/:id" {
		kind := "post"
		if strings.HasPrefix(path, "/pages") {
			kind = "page"
		}
		if r.Method == http.MethodDelete {
			return integrationSchemaObject(map[string]any{"message": integrationStringSchema()})
		}
		return integrationResourceSchema(kind)
	}
	if path == "/categories" {
		return map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "object", "additionalProperties": true}}
	}
	if path == "/tags" {
		return integrationArraySchema(map[string]any{"type": "object", "additionalProperties": true})
	}
	if path == "/media" {
		if r.Method == http.MethodGet {
			return map[string]any{"type": []string{"array", "null"}, "items": integrationStringSchema()}
		}
		if r.Method == http.MethodPost {
			return integrationSchemaObject(map[string]any{"url": integrationStringSchema()})
		}
		return integrationSchemaObject(map[string]any{"message": integrationStringSchema()})
	}
	if strings.HasPrefix(path, "/media/files") {
		if path == "/media/files" {
			if r.Method == http.MethodPost {
				return integrationMediaFileSchema()
			}
			return integrationSchemaObject(map[string]any{"items": integrationArraySchema(integrationMediaFileSchema()), "total": integrationIntSchema(), "page": integrationIntSchema(), "per_page": integrationIntSchema()})
		}
		if r.Method == http.MethodDelete {
			return integrationSchemaObject(map[string]any{"trashed": map[string]any{"type": "boolean"}})
		}
		return integrationMediaFileSchema()
	}
	if path == "/products" {
		if r.Method == http.MethodPost {
			return integrationResourceSchema("product")
		}
		return integrationSchemaObject(map[string]any{"items": integrationArraySchema(integrationResourceSchema("product")), "total": integrationIntSchema(), "counts": map[string]any{"type": "object", "additionalProperties": integrationIntSchema()}, "page": integrationIntSchema(), "per_page": integrationIntSchema()})
	}
	if path == "/products/:id" {
		if r.Method == http.MethodDelete {
			return integrationSchemaObject(map[string]any{"message": integrationStringSchema()})
		}
		return integrationResourceSchema("product")
	}
	if path == "/orders" {
		return map[string]any{"type": "object", "properties": map[string]any{"items": integrationArraySchema(map[string]any{"type": "object", "additionalProperties": true}), "total": integrationIntSchema(), "counts": map[string]any{"type": "object", "additionalProperties": integrationIntSchema()}, "kpi": map[string]any{"type": "object", "additionalProperties": integrationIntSchema()}}}
	}
	if path == "/orders/:id" {
		return map[string]any{"type": "object", "additionalProperties": true}
	}
	if path == "/themes" {
		return map[string]any{"type": []string{"array", "null"}, "items": integrationStringSchema()}
	}
	if path == "/themes/:name/config" {
		return integrationSchemaObject(map[string]any{"name": integrationStringSchema(), "version": integrationStringSchema(), "description": integrationStringSchema(), "author": integrationStringSchema(), "parent": integrationStringSchema(), "colors": map[string]any{"type": "object"}, "layout": map[string]any{"type": "object"}, "custom_fields": integrationArraySchema(map[string]any{"type": "object"}), "webmcp_tools": map[string]any{}})
	}
	if path == "/themes/:name/settings" {
		if r.Method == http.MethodPost {
			return integrationSchemaObject(map[string]any{"message": integrationStringSchema()})
		}
		return integrationSchemaObject(map[string]any{"theme_name": integrationStringSchema(), "colors": map[string]any{}, "layout": map[string]any{}, "custom": map[string]any{}})
	}
	if path == "/themes/activate" || path == "/plugins/:id/reload" || path == "/media/rename" || (path == "/media/:filename" && r.Method != http.MethodGet) || (path == "/products/:id" && r.Method == http.MethodDelete) {
		return integrationSchemaObject(map[string]any{"message": integrationStringSchema()})
	}
	if path == "/plugins" {
		return map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "object", "properties": map[string]any{"id": integrationStringSchema(), "name": integrationStringSchema(), "version": integrationStringSchema(), "description": integrationStringSchema(), "author": integrationStringSchema()}}}
	}
	if path == "/settings" {
		if r.Method == http.MethodGet {
			return integrationArraySchema(integrationSchemaObject(map[string]any{"key": integrationStringSchema(), "value": integrationStringSchema()}))
		}
		return integrationSchemaObject(map[string]any{"message": integrationStringSchema()})
	}
	return map[string]any{"type": "object", "additionalProperties": true}
}

func integrationJSONMedia(schema any) map[string]any {
	return map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": schema}}}
}
func integrationErrorResponse(description string) map[string]any {
	return map[string]any{"description": description, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Error"}}}}
}
func integrationOperationResponses(r integrationRoute) map[string]any {
	status := "200"
	description := "Successful response"
	if r.Path == "/media/files" && r.Method == http.MethodPost {
		status, description = "201", "Media file created"
	}
	responses := map[string]any{status: map[string]any{"description": description, "content": map[string]any{"application/json": map[string]any{"schema": integrationSuccessSchema(r)}}}, "400": integrationErrorResponse("Invalid request"), "404": integrationErrorResponse("Not found"), "405": integrationErrorResponse("Method not allowed"), "413": integrationErrorResponse("Request body is too large"), "500": integrationErrorResponse("Server error")}
	if r.Auth == integrationRouteKey {
		responses["401"] = integrationErrorResponse("Invalid or missing access key")
	}
	if r.Permission != "" {
		responses["403"] = integrationErrorResponse("Permission denied")
	}
	if r.VersionGuard {
		responses["409"] = integrationErrorResponse("Resource version conflict")
	}
	return responses
}

func integrationRequestBody(r integrationRoute) map[string]any {
	if r.Method == http.MethodGet || r.Method == http.MethodDelete || r.Path == "/plugins/:id/reload" {
		return nil
	}
	jsonBody := func(schema map[string]any) map[string]any {
		return map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
	}
	if r.Path == "/posts" || r.Path == "/pages" || r.Path == "/posts/:id" || r.Path == "/pages/:id" {
		props := map[string]any{"title": integrationStringSchema(), "slug": integrationStringSchema(), "content": integrationStringSchema(), "source": integrationStringSchema(), "format": integrationStringSchema(), "status": integrationStringSchema(), "type": integrationStringSchema(), "template": integrationStringSchema(), "excerpt": integrationStringSchema(), "cover": integrationStringSchema(), "visibility": integrationStringSchema(), "allow_comments": map[string]any{"type": "boolean"}, "pinned": map[string]any{"type": "boolean"}, "seo_index": integrationStringSchema(), "seo_ai": integrationStringSchema(), "meta": map[string]any{}, "published_at": map[string]any{"type": []string{"string", "null"}, "format": "date-time"}, "category_ids": integrationArraySchema(integrationIntSchema()), "tag_ids": integrationArraySchema(integrationIntSchema()), "tags": integrationArraySchema(integrationStringSchema())}
		if r.VersionGuard {
			props["base_updated_at"] = map[string]any{"type": "string", "format": "date-time"}
			return jsonBody(integrationSchemaObject(props, "base_updated_at"))
		}
		return jsonBody(integrationSchemaObject(props, "title"))
	}
	if r.Path == "/products" || r.Path == "/products/:id" {
		return jsonBody(integrationProductInputSchema(r.VersionGuard))
	}
	if r.Path == "/settings" {
		return jsonBody(map[string]any{"type": "object", "additionalProperties": map[string]any{"oneOf": []any{integrationStringSchema(), map[string]any{"type": "boolean"}, integrationIntSchema()}}})
	}
	if r.Path == "/themes/activate" {
		return jsonBody(integrationSchemaObject(map[string]any{"name": integrationStringSchema()}, "name"))
	}
	if strings.HasPrefix(r.Path, "/themes/") && strings.HasSuffix(r.Path, "/settings") {
		return jsonBody(integrationSchemaObject(map[string]any{"colors": map[string]any{}, "layout": map[string]any{}, "custom": map[string]any{}}))
	}
	if r.Path == "/media/rename" {
		return jsonBody(integrationSchemaObject(map[string]any{"old_name": integrationStringSchema(), "new_name": integrationStringSchema()}, "old_name", "new_name"))
	}
	if r.Path == "/media/files/:id" && r.Method == http.MethodPatch {
		return jsonBody(integrationSchemaObject(map[string]any{"alt": integrationStringSchema(), "group_id": map[string]any{"type": []string{"integer", "null"}}}))
	}
	if strings.HasPrefix(r.Path, "/media") {
		props := map[string]any{"file": map[string]any{"type": "string", "format": "binary"}}
		if r.Path == "/media/files" {
			props["alt"] = integrationStringSchema()
			props["policy_id"] = integrationStringSchema()
			props["group_id"] = integrationStringSchema()
		}
		return map[string]any{"required": true, "content": map[string]any{"multipart/form-data": map[string]any{"schema": integrationSchemaObject(props, "file")}}}
	}
	return nil
}

func integrationQueryParameters(r integrationRoute) []any {
	var definitions []struct {
		name, desc string
		schema     map[string]any
		repeat     bool
	}
	add := func(name, desc string, schema map[string]any, repeat bool) {
		definitions = append(definitions, struct {
			name, desc string
			schema     map[string]any
			repeat     bool
		}{name, desc, schema, repeat})
	}
	if r.Path == "/logs" {
		add("cursor", "Opaque cursor for the next page", integrationStringSchema(), false)
		add("limit", "Number of rows from 1 to 100", map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, false)
		add("status", "Three-digit status or 2xx through 5xx", integrationStringSchema(), false)
		add("actor_type", "Event actor type", integrationStringSchema(), false)
		add("action", "Event action; may be repeated", integrationStringSchema(), true)
		add("module", "Event module", integrationStringSchema(), false)
		add("key_id", "Access key identifier", integrationIntSchema(), false)
		add("since", "Inclusive RFC3339 start time", map[string]any{"type": "string", "format": "date-time"}, false)
		add("until", "Exclusive RFC3339 end time", map[string]any{"type": "string", "format": "date-time"}, false)
	}
	if r.Path == "/tags" {
		add("q", "Tag name prefix", integrationStringSchema(), false)
	}
	if r.Path == "/media/files" {
		add("q", "Filename search", integrationStringSchema(), false)
		add("page", "Page number", map[string]any{"type": "integer", "minimum": 1}, false)
		add("per_page", "Rows per page", map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, false)
		add("group_id", "Media group identifier or none", integrationStringSchema(), false)
		add("policy_id", "Media policy identifier", integrationIntSchema(), false)
		add("trashed", "Set to 1 to list trashed files", integrationStringSchema(), false)
	}
	if r.Path == "/products" {
		add("status", "Product status filter", integrationStringSchema(), false)
		add("q", "Search product title, slug or SKU", integrationStringSchema(), false)
		add("page", "Page number", map[string]any{"type": "integer", "minimum": 1, "maximum": 1000000}, false)
		add("per_page", "Rows per page", map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, false)
	}
	if r.Path == "/orders" {
		add("status", "Order status filter", integrationStringSchema(), false)
		add("q", "Search order number, email, phone or shipping", integrationStringSchema(), false)
		add("email", "Email substring filter", integrationStringSchema(), false)
		add("from", "Inclusive start date (YYYY-MM-DD)", map[string]any{"type": "string", "format": "date"}, false)
		add("to", "Inclusive end date (YYYY-MM-DD)", map[string]any{"type": "string", "format": "date"}, false)
		add("date", "Single day filter (YYYY-MM-DD)", map[string]any{"type": "string", "format": "date"}, false)
		add("page", "Page number", map[string]any{"type": "integer", "minimum": 1, "maximum": 1000000}, false)
		add("per_page", "Rows per page", map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, false)
	}
	out := make([]any, 0, len(definitions))
	for _, d := range definitions {
		p := map[string]any{"name": d.name, "in": "query", "required": false, "description": d.desc, "schema": d.schema}
		if d.repeat {
			p["style"] = "form"
			p["explode"] = true
		}
		out = append(out, p)
	}
	return out
}
