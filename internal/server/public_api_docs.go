package server

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

func publicAPIOverview(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{
		"openapi_url": "/api/public/openapi.json",
		"operations": []map[string]string{
			{"method": "GET", "path": "/api/public/posts", "summary": "列出公開文章", "documentation_url": "#/paths/~1api~1public~1posts/get"},
			{"method": "GET", "path": "/api/public/content/{slug}", "summary": "讀取公開文章或頁面", "documentation_url": "#/paths/~1api~1public~1content~1{slug}/get"},
			{"method": "GET", "path": "/api/public/site", "summary": "讀取網站資訊", "documentation_url": "#/paths/~1api~1public~1site/get"},
		},
	})
}

func publicAPIOpenAPI(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title": "Jelly CMS Public API", "version": "1.0.0",
			"description": "公開唯讀內容 API；所有端點均不需登入，且不會依登入身分放寬公開範圍。",
		},
		"servers": []map[string]string{{"url": "/", "description": "目前站台"}},
		"paths": map[string]any{
			"/api/public/posts": map[string]any{"get": map[string]any{
				"summary": "列出公開文章", "description": "依置頂、有效發布時間新到舊及 ID 排序。超過最後一頁回傳空 items。",
				"parameters": []any{
					publicAPIQueryParam("page", "頁碼；缺少、非數字或小於 1 時使用 1。", map[string]any{"type": "integer", "minimum": 1}),
					publicAPIQueryParam("per_page", "每頁筆數；缺少、非數字或小於 1 時使用 10，最大 50。", map[string]any{"type": "integer", "minimum": 1, "maximum": 50}),
				},
				"responses": map[string]any{
					"200": publicAPIResponse("公開文章頁面。", map[string]any{"$ref": "#/components/schemas/PublicPostList"}),
					"500": publicAPIErrorResponse("伺服器無法完成查詢。"), "503": publicAPIErrorResponse("資料庫尚未可用。"),
				},
			}},
			"/api/public/content/{slug}": map[string]any{"get": map[string]any{
				"summary": "讀取公開文章或頁面", "description": "只回傳已發布、公開且發布時間已到的文章或頁面。不存在與非公開 slug 回相同 404。",
				"parameters": []any{map[string]any{"name": "slug", "in": "path", "required": true, "schema": map[string]any{"type": "string"}}},
				"responses": map[string]any{
					"200": publicAPIResponse("公開內容純文字。", map[string]any{"$ref": "#/components/schemas/PublicContent"}),
					"404": publicAPIErrorResponse("找不到公開內容。"), "500": publicAPIErrorResponse("伺服器無法完成查詢。"),
					"503": publicAPIErrorResponse("資料庫尚未可用。"),
				},
			}},
			"/api/public/site": map[string]any{"get": map[string]any{
				"summary": "讀取網站資訊", "description": "回傳目前請求所使用的 scheme 與 Host 組成的網站根網址。",
				"responses": map[string]any{
					"200": publicAPIResponse("網站名稱、描述、語言與網址。", map[string]any{"$ref": "#/components/schemas/PublicSite"}),
					"500": publicAPIErrorResponse("伺服器無法完成查詢。"), "503": publicAPIErrorResponse("資料庫尚未可用。"),
				},
			}},
		},
		"components": map[string]any{"schemas": map[string]any{
			"PublicPostList": map[string]any{"type": "object", "required": []string{"items", "page", "per_page", "total"}, "properties": map[string]any{
				"items": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/PublicPostSummary"}},
				"page":  map[string]any{"type": "integer"}, "per_page": map[string]any{"type": "integer"}, "total": map[string]any{"type": "integer", "format": "int64"},
			}},
			"PublicPostSummary": map[string]any{"type": "object", "required": []string{"title", "excerpt", "url", "published_at"}, "properties": map[string]any{
				"title": publicAPIString(), "excerpt": publicAPIString(), "url": publicAPIString(), "published_at": map[string]any{"type": "string", "format": "date-time"},
			}},
			"PublicContent": map[string]any{"type": "object", "required": []string{"type", "title", "url", "published_at", "excerpt", "text"}, "properties": map[string]any{
				"type": map[string]any{"type": "string", "enum": []string{"post", "page"}}, "title": publicAPIString(), "url": publicAPIString(),
				"published_at": map[string]any{"type": "string", "format": "date-time"}, "excerpt": publicAPIString(), "text": publicAPIString(),
			}},
			"PublicSite": map[string]any{"type": "object", "required": []string{"name", "description", "language", "url"}, "properties": map[string]any{
				"name": publicAPIString(), "description": publicAPIString(), "language": publicAPIString(), "url": map[string]any{"type": "string", "format": "uri"},
			}},
			"PublicAPIError": map[string]any{"type": "object", "required": []string{"code", "message", "correlation_id"}, "properties": map[string]any{
				"code":    map[string]any{"type": "string", "enum": []string{"not_found", "service_unavailable", "internal_error"}},
				"message": publicAPIString(), "correlation_id": map[string]any{"type": "string", "description": "與 X-Request-ID 相同的請求識別值。"},
			}},
		}},
	})
}

func publicAPIQueryParam(name, description string, schema map[string]any) map[string]any {
	return map[string]any{"name": name, "in": "query", "required": false, "description": description, "schema": schema}
}

func publicAPIString() map[string]any { return map[string]any{"type": "string"} }

func publicAPIResponse(description string, schema map[string]any) map[string]any {
	return map[string]any{"description": description, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
}

func publicAPIErrorResponse(description string) map[string]any {
	return publicAPIResponse(description, map[string]any{"$ref": "#/components/schemas/PublicAPIError"})
}
