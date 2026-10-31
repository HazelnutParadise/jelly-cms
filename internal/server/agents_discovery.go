package server

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/agent"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

var agentPathParam = regexp.MustCompile(`:([A-Za-z0-9_]+)`)

func openAPIPath(path string) string { return agentPathParam.ReplaceAllString(path, "{$1}") }
func agentFullPath(path string) string {
	if path == "" {
		return "/api/agent/v1"
	}
	return "/api/agent/v1" + path
}
func agentDiscovery(tm *theme.Manager, rt *plugin.Runtime) echo.HandlerFunc {
	return func(c echo.Context) error {
		rows := agentRouteTable(tm, rt)
		perms := agent.AllPermissions()
		permEndpoints := map[string][]map[string]any{}
		permRoles := map[string][]string{}
		endpoints := make([]map[string]any, 0, len(rows))
		for _, r := range rows {
			ep := map[string]any{"method": r.Method, "path": agentFullPath(openAPIPath(r.Path)), "permission": string(r.Permission), "summary": r.Summary, "required_roles": r.RequiredRoles, "version_guard": r.VersionGuard, "auth": string(r.Auth)}
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
		return c.JSON(200, map[string]any{"name": "Jelly CMS Agent API", "version": "v1", "base_path": "/api/agent/v1", "openapi_url": "/api/agent/v1/openapi.json", "skill_path": "skills/jelly-cms/SKILL.md", "authentication": map[string]any{"type": "bearer", "header": "Authorization", "scheme": "Bearer", "key_prefix": "jly_", "how_to_get": "由管理員在 Jelly CMS 後台建立"}, "permissions": permissions, "endpoints": endpoints, "concurrency": agentConcurrency(rows), "errors": map[string]any{"format": map[string]any{"code": "…", "message": "…", "details": map[string]any{}, "correlation_id": "req_…"}, "codes": agentErrorCodes()}, "not_exposed": agentNotExposed(), "examples": []map[string]any{{"title": "確認金鑰與有效權限", "request": "GET /api/agent/v1/me", "response": map[string]any{"id": 1, "effective_permissions": []string{"posts.read"}}}, {"title": "以版本更新文章", "request": "PUT /api/agent/v1/posts/1 with base_updated_at", "response": map[string]any{"updated_at": "RFC3339 timestamp"}}}})
	}
}
func permissionDescription(p string) string {
	return map[string]string{"posts.read": "讀取文章與內容分類。", "posts.write": "建立、更新與刪除文章。", "pages.read": "讀取頁面。", "pages.write": "更新頁面。", "media.read": "列出媒體附件。", "media.write": "上傳、更新與刪除媒體。", "products.read": "讀取商品。", "products.write": "建立、更新與刪除商品。", "orders.read": "讀取訂單。", "themes.read": "檢視主題與設定。", "themes.write": "啟用主題及更新主題設定。", "plugins.read": "列出外掛。", "plugins.write": "重新載入外掛。", "settings.read": "讀取已遮罩的系統設定。", "settings.write": "更新系統設定。", "logs.read": "讀取此 agent 自己的活動紀錄。"}[p]
}
func agentErrorCodes() []map[string]any {
	codes := []struct {
		c string
		s int
	}{{"key_missing", 401}, {"key_invalid", 401}, {"key_revoked", 401}, {"key_expired", 401}, {"agent_disabled", 401}, {"owner_unavailable", 401}, {"permission_missing", 403}, {"owner_role_insufficient", 403}, {"forbidden", 403}, {"version_required", 400}, {"version_invalid", 400}, {"type_mismatch", 400}, {"logs_filter_invalid", 400}, {"bad_request", 400}, {"name_invalid", 400}, {"description_too_long", 400}, {"permissions_required", 400}, {"permission_unknown", 400}, {"rotation_mode_invalid", 400}, {"grace_out_of_range", 400}, {"not_found", 404}, {"agent_not_found", 404}, {"key_not_found", 404}, {"method_not_allowed", 405}, {"version_conflict", 409}, {"agent_name_taken", 409}, {"conflict", 409}, {"payload_too_large", 413}, {"internal_error", 500}, {"service_unavailable", 503}}
	out := make([]map[string]any, 0, len(codes))
	for _, v := range codes {
		out = append(out, map[string]any{"code": v.c, "status": v.s, "description": agentErrorDescription(v.c)})
	}
	return out
}
func agentConcurrency(rows []agentRoute) map[string]any {
	paths := []string{}
	for _, r := range rows {
		if r.VersionGuard {
			paths = append(paths, r.Method+" "+agentFullPath(openAPIPath(r.Path)))
		}
	}
	return map[string]any{"field": "base_updated_at", "applies_to": paths, "conflict_code": "version_conflict"}
}
func agentErrorDescription(code string) string {
	return map[string]string{"key_missing": "Bearer agent key is required", "key_invalid": "Agent key is invalid", "key_revoked": "Agent key was revoked", "key_expired": "Agent key expired", "agent_disabled": "Agent is disabled", "owner_unavailable": "Agent owner is unavailable", "permission_missing": "Agent key lacks a required permission", "owner_role_insufficient": "Owner role cannot use this permission", "forbidden": "Access is forbidden", "version_required": "base_updated_at is required", "version_invalid": "base_updated_at is invalid", "type_mismatch": "Resource type does not match this endpoint", "logs_filter_invalid": "Event filter is invalid", "bad_request": "Request is invalid", "name_invalid": "Agent name is invalid", "description_too_long": "Agent description is too long", "permissions_required": "At least one permission is required", "permission_unknown": "Permission is unknown", "rotation_mode_invalid": "Key rotation mode is invalid", "grace_out_of_range": "Grace period is out of range", "not_found": "Endpoint was not found", "agent_not_found": "Agent was not found", "key_not_found": "Agent key was not found", "method_not_allowed": "HTTP method is not allowed", "version_conflict": "Resource changed since it was read", "agent_name_taken": "Agent name is already in use", "conflict": "Request conflicts with current state", "payload_too_large": "Request body is too large", "internal_error": "Internal server error", "service_unavailable": "Service is unavailable"}[code]
}
func agentNotExposed() []map[string]string {
	return []map[string]string{{"path": "/api/admin/comments*", "reason": "留言沒有對應的 agent 權限"}, {"path": "/api/admin/menus*", "reason": "選單沒有對應的 agent 權限"}, {"path": "/api/admin/categories* write", "reason": "分類寫入沒有對應的 agent 權限"}, {"path": "/api/admin/tags* write", "reason": "標籤寫入沒有對應的 agent 權限"}, {"path": "/api/admin/trash*", "reason": "回收桶沒有對應的 agent 權限"}, {"path": "/api/admin/posts/:id/revisions*", "reason": "文章版本沒有對應的 agent 權限"}, {"path": "/api/admin/payment/gateways*", "reason": "金流憑證不開放給 agent"}, {"path": "/api/admin/oauth/providers*", "reason": "第三方憑證不開放給 agent"}, {"path": "/api/admin/plugins/upload", "reason": "安裝外掛等於執行程式碼"}, {"path": "/api/admin/agents*", "reason": "agent 不能管理 agent"}}
}
func agentOpenAPI(tm *theme.Manager, rt *plugin.Runtime) echo.HandlerFunc {
	return func(c echo.Context) error {
		paths := map[string]any{}
		for _, r := range agentRouteTable(tm, rt) {
			p := agentFullPath(openAPIPath(r.Path))
			item, ok := paths[p].(map[string]any)
			if !ok {
				item = map[string]any{}
				for _, m := range agentPathParam.FindAllStringSubmatch(r.Path, -1) {
					typeName := "string"
					if m[1] == "id" || m[1] == "key_id" {
						typeName = "integer"
					}
					item["parameters"] = append(anySlice(item["parameters"]), map[string]any{"name": m[1], "in": "path", "required": true, "schema": map[string]any{"type": typeName}})
				}
				paths[p] = item
			}
			op := map[string]any{"operationId": operationID(r.Method, r.Path), "summary": r.Summary, "responses": agentOperationResponses(r)}
			if parameters := agentQueryParameters(r); len(parameters) > 0 {
				op["parameters"] = parameters
			}
			if body := agentRequestBody(r); body != nil {
				op["requestBody"] = body
			}
			if r.Auth == agentRoutePublic {
				op["security"] = []any{}
			} else if r.Permission != "" {
				op["x-jelly-permission"] = string(r.Permission)
				op["x-jelly-required-roles"] = r.RequiredRoles
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
			"info":     map[string]any{"title": "Jelly CMS Agent API", "version": "1.0.0"},
			"paths":    paths,
			"security": []any{map[string]any{"AgentKey": []string{}}},
			"components": map[string]any{
				"securitySchemes": map[string]any{"AgentKey": map[string]any{"type": "http", "scheme": "bearer", "description": "Jelly CMS agent key (jly_…)"}},
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

func agentSchemaObject(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func agentArraySchema(item any) map[string]any { return map[string]any{"type": "array", "items": item} }
func agentStringSchema() map[string]any        { return map[string]any{"type": "string"} }
func agentIntSchema() map[string]any           { return map[string]any{"type": "integer"} }

func agentResourceSchema(kind string) map[string]any {
	props := map[string]any{}
	if kind == "post" || kind == "page" {
		props = map[string]any{
			"id": agentIntSchema(), "title": agentStringSchema(), "slug": agentStringSchema(), "content": agentStringSchema(), "type": agentStringSchema(), "status": agentStringSchema(), "author_id": agentIntSchema(), "author": map[string]any{"type": "object"}, "template": agentStringSchema(), "meta": map[string]any{}, "published_at": map[string]any{"type": []string{"string", "null"}, "format": "date-time"}, "created_at": map[string]any{"type": "string", "format": "date-time"}, "updated_at": map[string]any{"type": "string", "format": "date-time"}, "excerpt": agentStringSchema(), "cover": agentStringSchema(), "allow_comments": map[string]any{"type": []string{"boolean", "null"}}, "pinned": map[string]any{"type": "boolean"}, "visibility": agentStringSchema(), "categories": map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "object"}}, "tags": map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "object"}}, "source": agentStringSchema(), "format": agentStringSchema(), "seo_index": agentStringSchema(), "seo_ai": agentStringSchema(), "diagnostics": agentArraySchema(map[string]any{"type": "object"}),
		}
		return agentSchemaObject(props)
	}
	if kind == "product" {
		props = map[string]any{
			"id": agentIntSchema(), "title": agentStringSchema(), "slug": agentStringSchema(), "description": agentStringSchema(), "price": agentIntSchema(), "sku": agentStringSchema(), "stock": agentIntSchema(), "images": map[string]any{}, "status": agentStringSchema(), "created_at": map[string]any{"type": "string", "format": "date-time"}, "updated_at": map[string]any{"type": "string", "format": "date-time"}, "deleted_by_id": agentIntSchema(), "deleted_at": map[string]any{"type": []string{"string", "null"}, "format": "date-time"}, "compare_at_price": agentIntSchema(), "low_stock_threshold": agentIntSchema(), "requires_shipping": map[string]any{"type": "boolean"}, "cold_chain": map[string]any{"type": "boolean"}, "attributes": map[string]any{}, "related_ids": map[string]any{}, "sold_count": agentIntSchema(), "seo_title": agentStringSchema(), "seo_description": agentStringSchema(), "seo_index": agentStringSchema(), "seo_ai": agentStringSchema(), "kind": agentStringSchema(), "variants": agentArraySchema(map[string]any{"type": "object", "additionalProperties": true}), "categories": agentArraySchema(agentIntSchema()),
		}
		return agentSchemaObject(props)
	}
	return agentSchemaObject(map[string]any{})
}

func agentMediaFileSchema() map[string]any {
	return agentSchemaObject(map[string]any{
		"id": agentIntSchema(), "policy_id": agentIntSchema(), "group_id": map[string]any{"type": []string{"integer", "null"}}, "filename": agentStringSchema(), "path": agentStringSchema(), "url": agentStringSchema(), "mime": agentStringSchema(), "size": agentIntSchema(), "width": map[string]any{"type": []string{"integer", "null"}}, "height": map[string]any{"type": []string{"integer", "null"}}, "alt": agentStringSchema(), "sha256": agentStringSchema(), "source": agentStringSchema(), "exif_stripped": map[string]any{"type": "boolean"}, "trashed": map[string]any{"type": "boolean"}, "created_at": map[string]any{"type": "string", "format": "date-time"},
	})
}

func agentProductInputSchema(includeVersion bool) map[string]any {
	variant := agentSchemaObject(map[string]any{"id": agentIntSchema(), "name": agentStringSchema(), "sku": agentStringSchema(), "price": agentStringSchema(), "compare_at_price": agentStringSchema(), "stock": agentIntSchema(), "position": agentIntSchema(), "status": agentStringSchema()})
	props := map[string]any{"title": agentStringSchema(), "slug": agentStringSchema(), "description": agentStringSchema(), "price": map[string]any{"oneOf": []any{agentStringSchema(), agentIntSchema()}}, "sku": map[string]any{"type": []string{"string", "null"}}, "stock": map[string]any{"type": []string{"integer", "null"}}, "kind": agentStringSchema(), "status": agentStringSchema(), "compare_at_price": agentStringSchema(), "low_stock_threshold": map[string]any{"type": []string{"integer", "null"}}, "requires_shipping": map[string]any{"type": []string{"boolean", "null"}}, "cold_chain": map[string]any{"type": "boolean"}, "images": agentArraySchema(agentStringSchema()), "categories": agentArraySchema(agentIntSchema()), "variants": agentArraySchema(variant), "attributes": map[string]any{"type": "object", "additionalProperties": agentStringSchema()}, "related_ids": agentArraySchema(agentIntSchema()), "seo_title": agentStringSchema(), "seo_description": agentStringSchema(), "seo_index": map[string]any{"type": []string{"string", "null"}}, "seo_ai": map[string]any{"type": []string{"string", "null"}}}
	if includeVersion {
		props["base_updated_at"] = map[string]any{"type": "string", "format": "date-time"}
	}
	if includeVersion {
		return agentSchemaObject(props, "base_updated_at")
	}
	return agentSchemaObject(props, "title")
}

func agentSuccessSchema(r agentRoute) map[string]any {
	path := r.Path
	if path == "" {
		return agentSchemaObject(map[string]any{"name": agentStringSchema(), "version": agentStringSchema(), "base_path": agentStringSchema(), "openapi_url": agentStringSchema(), "skill_path": agentStringSchema(), "authentication": map[string]any{"type": "object"}, "permissions": agentArraySchema(map[string]any{"type": "object"}), "endpoints": agentArraySchema(map[string]any{"type": "object"}), "concurrency": map[string]any{"type": "object"}, "errors": map[string]any{"type": "object"}, "not_exposed": agentArraySchema(map[string]any{"type": "object"}), "examples": agentArraySchema(map[string]any{"type": "object"})})
	}
	if path == "/openapi.json" {
		return map[string]any{"type": "object", "additionalProperties": true}
	}
	if path == "/me" {
		return agentSchemaObject(map[string]any{
			"id": agentIntSchema(), "name": agentStringSchema(), "description": agentStringSchema(),
			"permissions": agentArraySchema(agentStringSchema()), "effective_permissions": agentArraySchema(agentStringSchema()),
			"owner_role": agentStringSchema(), "key": agentSchemaObject(map[string]any{
				"id": agentIntSchema(), "prefix": agentStringSchema(),
				"expires_at": map[string]any{"type": []string{"string", "null"}, "format": "date-time"},
			}),
		}, "id", "name", "permissions", "effective_permissions", "key")
	}
	if path == "/logs" {
		item := agentSchemaObject(map[string]any{
			"id": agentIntSchema(), "at": map[string]any{"type": "string", "format": "date-time"},
			"actor_type": agentStringSchema(), "actor_user_id": map[string]any{"type": []string{"integer", "null"}},
			"agent_id": agentIntSchema(), "agent_name": agentStringSchema(), "key_id": map[string]any{"type": []string{"integer", "null"}},
			"key_prefix": agentStringSchema(), "action": agentStringSchema(), "module": agentStringSchema(),
			"method": agentStringSchema(), "route": agentStringSchema(), "status": agentIntSchema(),
			"ip_masked": agentStringSchema(), "summary": map[string]any{"type": "object", "additionalProperties": true},
			"correlation_id": agentStringSchema(),
		}, "id", "at", "actor_type", "agent_id", "agent_name", "action", "module", "method", "route", "status", "summary", "correlation_id")
		return agentSchemaObject(map[string]any{"items": agentArraySchema(item), "next_cursor": map[string]any{"type": []string{"string", "null"}}, "total": agentIntSchema()}, "items", "next_cursor", "total")
	}
	if path == "/posts" || path == "/pages" {
		kind := strings.TrimSuffix(strings.TrimPrefix(path, "/"), "s")
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			return agentResourceSchema(kind)
		}
		return map[string]any{"type": []string{"array", "null"}, "items": agentResourceSchema(kind)}
	}
	if path == "/posts/:id" || path == "/pages/:id" {
		kind := "post"
		if strings.HasPrefix(path, "/pages") {
			kind = "page"
		}
		if r.Method == http.MethodDelete {
			return agentSchemaObject(map[string]any{"message": agentStringSchema()})
		}
		return agentResourceSchema(kind)
	}
	if path == "/categories" {
		return map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "object", "additionalProperties": true}}
	}
	if path == "/tags" {
		return agentArraySchema(map[string]any{"type": "object", "additionalProperties": true})
	}
	if path == "/media" {
		if r.Method == http.MethodGet {
			return map[string]any{"type": []string{"array", "null"}, "items": agentStringSchema()}
		}
		if r.Method == http.MethodPost {
			return agentSchemaObject(map[string]any{"url": agentStringSchema()})
		}
		return agentSchemaObject(map[string]any{"message": agentStringSchema()})
	}
	if strings.HasPrefix(path, "/media/files") {
		if path == "/media/files" {
			if r.Method == http.MethodPost {
				return agentMediaFileSchema()
			}
			return agentSchemaObject(map[string]any{"items": agentArraySchema(agentMediaFileSchema()), "total": agentIntSchema(), "page": agentIntSchema(), "per_page": agentIntSchema()})
		}
		if r.Method == http.MethodDelete {
			return agentSchemaObject(map[string]any{"trashed": map[string]any{"type": "boolean"}})
		}
		return agentMediaFileSchema()
	}
	if path == "/products" {
		if r.Method == http.MethodPost {
			return agentResourceSchema("product")
		}
		return agentSchemaObject(map[string]any{"items": agentArraySchema(agentResourceSchema("product")), "total": agentIntSchema(), "counts": map[string]any{"type": "object", "additionalProperties": agentIntSchema()}, "page": agentIntSchema(), "per_page": agentIntSchema()})
	}
	if path == "/products/:id" {
		if r.Method == http.MethodDelete {
			return agentSchemaObject(map[string]any{"message": agentStringSchema()})
		}
		return agentResourceSchema("product")
	}
	if path == "/orders" {
		return map[string]any{"type": "object", "properties": map[string]any{"items": agentArraySchema(map[string]any{"type": "object", "additionalProperties": true}), "total": agentIntSchema(), "counts": map[string]any{"type": "object", "additionalProperties": agentIntSchema()}, "kpi": map[string]any{"type": "object", "additionalProperties": agentIntSchema()}}}
	}
	if path == "/orders/:id" {
		return map[string]any{"type": "object", "additionalProperties": true}
	}
	if path == "/themes" {
		return map[string]any{"type": []string{"array", "null"}, "items": agentStringSchema()}
	}
	if path == "/themes/:name/config" {
		return agentSchemaObject(map[string]any{"name": agentStringSchema(), "version": agentStringSchema(), "description": agentStringSchema(), "author": agentStringSchema(), "parent": agentStringSchema(), "colors": map[string]any{"type": "object"}, "layout": map[string]any{"type": "object"}, "custom_fields": agentArraySchema(map[string]any{"type": "object"}), "webmcp_tools": map[string]any{}})
	}
	if path == "/themes/:name/settings" {
		if r.Method == http.MethodPost {
			return agentSchemaObject(map[string]any{"message": agentStringSchema()})
		}
		return agentSchemaObject(map[string]any{"theme_name": agentStringSchema(), "colors": map[string]any{}, "layout": map[string]any{}, "custom": map[string]any{}})
	}
	if path == "/themes/activate" || path == "/plugins/:id/reload" || path == "/media/rename" || (path == "/media/:filename" && r.Method != http.MethodGet) || (path == "/products/:id" && r.Method == http.MethodDelete) {
		return agentSchemaObject(map[string]any{"message": agentStringSchema()})
	}
	if path == "/plugins" {
		return map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "object", "properties": map[string]any{"id": agentStringSchema(), "name": agentStringSchema(), "version": agentStringSchema(), "description": agentStringSchema(), "author": agentStringSchema()}}}
	}
	if path == "/settings" {
		if r.Method == http.MethodGet {
			return agentArraySchema(agentSchemaObject(map[string]any{"key": agentStringSchema(), "value": agentStringSchema()}))
		}
		return agentSchemaObject(map[string]any{"message": agentStringSchema()})
	}
	return map[string]any{"type": "object", "additionalProperties": true}
}

func agentJSONMedia(schema any) map[string]any {
	return map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": schema}}}
}
func agentErrorResponse(description string) map[string]any {
	return map[string]any{"description": description, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Error"}}}}
}
func agentOperationResponses(r agentRoute) map[string]any {
	status := "200"
	description := "Successful response"
	if r.Path == "/media/files" && r.Method == http.MethodPost {
		status, description = "201", "Media file created"
	}
	responses := map[string]any{status: map[string]any{"description": description, "content": map[string]any{"application/json": map[string]any{"schema": agentSuccessSchema(r)}}}, "400": agentErrorResponse("Invalid request"), "404": agentErrorResponse("Not found"), "405": agentErrorResponse("Method not allowed"), "413": agentErrorResponse("Request body is too large"), "500": agentErrorResponse("Server error")}
	if r.Auth == agentRouteKey {
		responses["401"] = agentErrorResponse("Invalid or missing agent key")
	}
	if r.Permission != "" {
		responses["403"] = agentErrorResponse("Permission denied")
	}
	if r.VersionGuard {
		responses["409"] = agentErrorResponse("Resource version conflict")
	}
	return responses
}

func agentRequestBody(r agentRoute) map[string]any {
	if r.Method == http.MethodGet || r.Method == http.MethodDelete || r.Path == "/plugins/:id/reload" {
		return nil
	}
	jsonBody := func(schema map[string]any) map[string]any {
		return map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
	}
	if r.Path == "/posts" || r.Path == "/pages" || r.Path == "/posts/:id" || r.Path == "/pages/:id" {
		props := map[string]any{"title": agentStringSchema(), "slug": agentStringSchema(), "content": agentStringSchema(), "source": agentStringSchema(), "format": agentStringSchema(), "status": agentStringSchema(), "type": agentStringSchema(), "template": agentStringSchema(), "excerpt": agentStringSchema(), "cover": agentStringSchema(), "visibility": agentStringSchema(), "allow_comments": map[string]any{"type": "boolean"}, "pinned": map[string]any{"type": "boolean"}, "seo_index": agentStringSchema(), "seo_ai": agentStringSchema(), "meta": map[string]any{}, "published_at": map[string]any{"type": []string{"string", "null"}, "format": "date-time"}, "category_ids": agentArraySchema(agentIntSchema()), "tag_ids": agentArraySchema(agentIntSchema()), "tags": agentArraySchema(agentStringSchema())}
		if r.VersionGuard {
			props["base_updated_at"] = map[string]any{"type": "string", "format": "date-time"}
			return jsonBody(agentSchemaObject(props, "base_updated_at"))
		}
		return jsonBody(agentSchemaObject(props, "title"))
	}
	if r.Path == "/products" || r.Path == "/products/:id" {
		return jsonBody(agentProductInputSchema(r.VersionGuard))
	}
	if r.Path == "/settings" {
		return jsonBody(map[string]any{"type": "object", "additionalProperties": map[string]any{"oneOf": []any{agentStringSchema(), map[string]any{"type": "boolean"}, agentIntSchema()}}})
	}
	if r.Path == "/themes/activate" {
		return jsonBody(agentSchemaObject(map[string]any{"name": agentStringSchema()}, "name"))
	}
	if strings.HasPrefix(r.Path, "/themes/") && strings.HasSuffix(r.Path, "/settings") {
		return jsonBody(agentSchemaObject(map[string]any{"colors": map[string]any{}, "layout": map[string]any{}, "custom": map[string]any{}}))
	}
	if r.Path == "/media/rename" {
		return jsonBody(agentSchemaObject(map[string]any{"old_name": agentStringSchema(), "new_name": agentStringSchema()}, "old_name", "new_name"))
	}
	if r.Path == "/media/files/:id" && r.Method == http.MethodPatch {
		return jsonBody(agentSchemaObject(map[string]any{"alt": agentStringSchema(), "group_id": map[string]any{"type": []string{"integer", "null"}}}))
	}
	if strings.HasPrefix(r.Path, "/media") {
		props := map[string]any{"file": map[string]any{"type": "string", "format": "binary"}}
		if r.Path == "/media/files" {
			props["alt"] = agentStringSchema()
			props["policy_id"] = agentStringSchema()
			props["group_id"] = agentStringSchema()
		}
		return map[string]any{"required": true, "content": map[string]any{"multipart/form-data": map[string]any{"schema": agentSchemaObject(props, "file")}}}
	}
	return nil
}

func agentQueryParameters(r agentRoute) []any {
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
		add("cursor", "Opaque cursor for the next page", agentStringSchema(), false)
		add("limit", "Number of rows from 1 to 100", map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, false)
		add("status", "Three-digit status or 2xx through 5xx", agentStringSchema(), false)
		add("actor_type", "Event actor type", agentStringSchema(), false)
		add("action", "Event action; may be repeated", agentStringSchema(), true)
		add("module", "Event module", agentStringSchema(), false)
		add("key_id", "Agent key identifier", agentIntSchema(), false)
		add("since", "Inclusive RFC3339 start time", map[string]any{"type": "string", "format": "date-time"}, false)
		add("until", "Exclusive RFC3339 end time", map[string]any{"type": "string", "format": "date-time"}, false)
	}
	if r.Path == "/tags" {
		add("q", "Tag name prefix", agentStringSchema(), false)
	}
	if r.Path == "/media/files" {
		add("q", "Filename search", agentStringSchema(), false)
		add("page", "Page number", map[string]any{"type": "integer", "minimum": 1}, false)
		add("per_page", "Rows per page", map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, false)
		add("group_id", "Media group identifier or none", agentStringSchema(), false)
		add("policy_id", "Media policy identifier", agentIntSchema(), false)
		add("trashed", "Set to 1 to list trashed files", agentStringSchema(), false)
	}
	if r.Path == "/products" {
		add("status", "Product status filter", agentStringSchema(), false)
		add("q", "Search product title, slug or SKU", agentStringSchema(), false)
		add("page", "Page number", map[string]any{"type": "integer", "minimum": 1, "maximum": 1000000}, false)
		add("per_page", "Rows per page", map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, false)
	}
	if r.Path == "/orders" {
		add("status", "Order status filter", agentStringSchema(), false)
		add("q", "Search order number, email, phone or shipping", agentStringSchema(), false)
		add("email", "Email substring filter", agentStringSchema(), false)
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
