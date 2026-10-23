package auth

import (
	"strings"

	"github.com/labstack/echo/v4"
)

type authDocumentedOperation struct {
	method  string
	path    string
	summary string
	openAPI map[string]any
}

func authJSONResponse(description string, schema any, example any) map[string]any {
	media := map[string]any{"schema": schema}
	if example != nil {
		media["example"] = example
	}
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": media,
		},
	}
}

func authErrorResponse(description string) map[string]any {
	return authJSONResponse(description, map[string]any{"$ref": "#/components/schemas/Error"}, map[string]any{"error": description})
}

func authTextErrorResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"text/plain": map[string]any{"schema": map[string]any{"type": "string"}},
		},
	}
}

func authMixedErrorResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema":  map[string]any{"$ref": "#/components/schemas/Error"},
				"example": map[string]any{"error": description},
			},
			"text/plain": map[string]any{"schema": map[string]any{"type": "string"}},
		},
	}
}

func authObjectSchema(required []string, properties map[string]any) map[string]any {
	return map[string]any{"type": "object", "required": required, "properties": properties}
}

func authStringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func authBody(required bool, schema any, jsonExample, formExample any) map[string]any {
	content := map[string]any{
		"application/json":                  map[string]any{"schema": schema},
		"application/x-www-form-urlencoded": map[string]any{"schema": schema},
	}
	if jsonExample != nil {
		content["application/json"].(map[string]any)["example"] = jsonExample
	}
	if formExample != nil {
		content["application/x-www-form-urlencoded"].(map[string]any)["example"] = formExample
	}
	return map[string]any{"required": required, "content": content}
}

func authProviderParameter() map[string]any {
	return map[string]any{
		"name": "provider", "in": "path", "required": true,
		"description": "OAuth 提供者，只接受 google 或 github。",
		"schema":      map[string]any{"type": "string", "enum": []string{"google", "github"}},
		"example":     "google",
	}
}

func authUserSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "公開使用者欄位；不包含密碼雜湊或第三方固定身分值。",
		"properties": map[string]any{
			"id":         map[string]any{"type": "integer", "format": "int64"},
			"username":   map[string]any{"type": "string"},
			"email":      map[string]any{"type": "string", "format": "email"},
			"avatar_url": map[string]any{"type": "string"},
			"role":       map[string]any{"type": "string", "enum": []string{"admin", "editor", "author"}},
			"provider":   map[string]any{"type": "string", "enum": []string{"local", "google", "github"}},
			"created_at": map[string]any{"type": "string", "format": "date-time"},
			"updated_at": map[string]any{"type": "string", "format": "date-time"},
		},
	}
}

func authDocumentationOperations() []authDocumentedOperation {
	userRef := map[string]any{"$ref": "#/components/schemas/User"}
	messageRef := map[string]any{"$ref": "#/components/schemas/Message"}
	loginRequestRef := map[string]any{"$ref": "#/components/schemas/LocalLoginRequest"}
	loginResponseRef := map[string]any{"$ref": "#/components/schemas/LoginResponse"}
	authURLRef := map[string]any{"$ref": "#/components/schemas/AuthURL"}
	identityListRef := map[string]any{"$ref": "#/components/schemas/IdentityList"}
	recoveryRequestRef := map[string]any{"$ref": "#/components/schemas/RecoveryRequest"}

	return []authDocumentedOperation{
		{
			method: "POST", path: "/auth/login", summary: "以帳號密碼登入",
			openAPI: map[string]any{
				"summary":     "以帳號密碼登入",
				"description": "接受 JSON 或一般表單本文。Accept 恰為 application/json 時回 JSON；其他情況以 302 導向安全的站內 redirect。成功時設定登入 Cookie。瀏覽器寫入須提供通過同來源檢查的來源證明。",
				"parameters":  []any{map[string]any{"name": "redirect", "in": "query", "required": false, "description": "一般表單登入成功後的站內返回路徑；站外值不會被採用。", "schema": map[string]any{"type": "string"}, "example": "/admin"}},
				"requestBody": authBody(true, loginRequestRef, map[string]any{"username": "editor@example.test", "password": "example-password"}, map[string]any{"username": "editor@example.test", "password": "example-password"}),
				"responses": map[string]any{
					"200": authJSONResponse("登入成功；回傳使用者及登入 JWT。", loginResponseRef, nil),
					"302": map[string]any{"description": "一般瀏覽器登入成功，導向站內 redirect；未指定時導向管理頁。", "headers": map[string]any{"Location": map[string]any{"schema": map[string]any{"type": "string"}, "description": "站內返回路徑。"}, "Set-Cookie": map[string]any{"schema": map[string]any{"type": "string"}, "description": "設定 HttpOnly 登入 Cookie。"}}},
					"400": authErrorResponse("本文格式錯誤或缺少 username/password。"),
					"401": authErrorResponse("帳號或密碼無效，或帳號使用 OAuth 登入。"),
					"403": authErrorResponse("瀏覽器請求未通過同來源保護。"),
					"429": authErrorResponse("登入次數過多；可參考 Retry-After。"),
					"500": authErrorResponse("無法簽發登入憑證。"),
					"503": authErrorResponse("請求保護或登入服務暫時無法使用。"),
				},
			},
		},
		{
			method: "POST", path: "/auth/logout", summary: "登出並撤銷目前登入",
			openAPI: map[string]any{
				"summary":     "登出並撤銷目前登入",
				"description": "可使用 Bearer JWT、jwt_token Cookie 或 Session Cookie。瀏覽器 Cookie 寫入須通過同來源保護；沒有有效登入時仍回登出成功。",
				"security":    []any{map[string]any{"BearerAuth": []any{}}, map[string]any{"JWTTokenCookie": []any{}}, map[string]any{"SessionCookie": []any{}}},
				"responses": map[string]any{
					"200": authJSONResponse("登出成功並清除登入 Cookie。", messageRef, map[string]any{"message": "Logged out successfully"}),
					"403": authErrorResponse("瀏覽器 Cookie 請求未通過同來源保護。"),
					"500": authErrorResponse("無法清除 Session。"),
					"503": authErrorResponse("無法撤銷登入憑證。"),
				},
			},
		},
		{
			method: "GET", path: "/auth/{provider}", summary: "開始 OAuth 登入",
			openAPI: map[string]any{
				"summary":     "開始 OAuth 登入",
				"description": "以 Google 或 GitHub 開始授權。redirect 僅接受安全的站內返回位置。成功回 307 至第三方授權網址，並設定有效 600 秒的簽章 state Cookie。入口有來源限流。",
				"parameters":  []any{authProviderParameter(), map[string]any{"name": "redirect", "in": "query", "required": false, "description": "登入完成後的站內返回路徑；站外值不會被採用。", "schema": map[string]any{"type": "string"}, "example": "/admin"}},
				"responses": map[string]any{
					"307": map[string]any{"description": "導向第三方授權頁並設定短效 state Cookie。", "headers": map[string]any{"Location": map[string]any{"schema": map[string]any{"type": "string", "format": "uri"}}, "Set-Cookie": map[string]any{"schema": map[string]any{"type": "string"}, "description": "HttpOnly、SameSite=Lax 的短效授權 Cookie。"}}},
					"400": authTextErrorResponse("提供者、state 或 OAuth 請求無效。"),
					"429": authErrorResponse("OAuth 入口次數過多；可參考 Retry-After。"),
					"502": authTextErrorResponse("第三方提供者無法建立授權網址。"),
					"503": authTextErrorResponse("OAuth 服務或短效 Cookie 儲存暫時無法使用。"),
				},
			},
		},
		{
			method: "GET", path: "/auth/{provider}/callback", summary: "完成 OAuth 登入回呼",
			openAPI: map[string]any{
				"summary":     "完成 OAuth 登入回呼",
				"description": "需提供 state，並且恰有一個非空 code 或 error。link 與 recover 意圖由入口簽署在 state Cookie 中，不能由 callback query 自行選擇。正常 OAuth 登入或帳號復原的 JSON 成功回 message/user/token；purpose=link 的 JSON 僅回 message，且不重簽登入 Cookie。一般瀏覽器成功時，正常登入導向站內返回位置；purpose=link 與 recover 固定導向 /account。link 回呼在上游授權完成後、寫入關聯前會再次驗證原登入；失效或撤銷時回 401。state 一次性使用。",
				"parameters": []any{
					authProviderParameter(),
					map[string]any{"name": "state", "in": "query", "required": true, "schema": map[string]any{"type": "string", "minLength": 1}, "example": "state-from-provider"},
					map[string]any{"name": "code", "in": "query", "required": false, "description": "提供者成功授權時必填；不得與 error 同時提供。", "schema": map[string]any{"type": "string", "minLength": 1}},
					map[string]any{"name": "error", "in": "query", "required": false, "description": "提供者拒絕授權時使用；不得與 code 同時提供。", "schema": map[string]any{"type": "string", "minLength": 1}},
				},
				"responses": map[string]any{
					"200": authJSONResponse("Accept 恰為 application/json 時成功。正常登入或復原回 message/user/token；purpose=link 回 message 且不重簽登入 Cookie。", map[string]any{"anyOf": []any{loginResponseRef, messageRef}}, nil),
					"302": map[string]any{"description": "一般瀏覽器成功時以 302 導向站內位置。正常登入使用站內返回位置並設定登入 Cookie；purpose=link 或 recover 固定導向 /account。link 僅清除短效授權 Cookie，不重簽登入 Cookie。", "headers": map[string]any{"Location": map[string]any{"schema": map[string]any{"type": "string"}}, "Set-Cookie": map[string]any{"schema": map[string]any{"type": "string"}, "description": "正常登入或復原設定登入 Cookie；link 僅清除短效授權 Cookie。"}}},
					"400": authTextErrorResponse("state、回呼參數或提供者身分無效。"),
					"401": authTextErrorResponse("purpose=link 的原登入在授權回呼時已過期或撤銷。"),
					"409": authTextErrorResponse("第三方固定身分與既有帳號衝突。"),
					"502": authTextErrorResponse("第三方身分驗證失敗。"),
					"503": authTextErrorResponse("state 消耗或使用者資料存取暫時無法使用。"),
					"500": authTextErrorResponse("無法簽發登入憑證。"),
				},
			},
		},
		{
			method: "GET", path: "/api/auth/me", summary: "取得目前使用者",
			openAPI: map[string]any{
				"summary":     "取得目前使用者",
				"description": "需要有效 Bearer JWT、JWT Cookie 或 Session Cookie；回傳資料不含密碼欄位。",
				"security":    []any{map[string]any{"BearerAuth": []any{}}, map[string]any{"JWTTokenCookie": []any{}}, map[string]any{"SessionCookie": []any{}}},
				"responses": map[string]any{
					"200": authJSONResponse("目前使用者。", userRef, nil),
					"401": authErrorResponse("缺少、過期、撤銷或無效的登入憑證。"),
					"500": authErrorResponse("無法取得目前使用者。"),
				},
			},
		},
		{
			method: "POST", path: "/auth/recover", summary: "以一次性復原授權開始 OAuth 復原",
			openAPI: map[string]any{
				"summary":     "以一次性復原授權開始 OAuth 復原",
				"description": "復原授權只接受 JSON 或表單本文的 grant，不接受 query。無需既有登入。Accept 為 application/json 回 200 auth_url；一般表單回 303 至第三方授權頁。復原授權十分鐘內有效且只能使用一次，成功後沿用一般 OAuth 登入流程。",
				"requestBody": authBody(true, recoveryRequestRef, map[string]any{"grant": "<one-time recovery grant>"}, map[string]any{"grant": "<one-time recovery grant>"}),
				"responses": map[string]any{
					"200": authJSONResponse("JSON 用戶端取得第三方授權網址。", authURLRef, map[string]any{"auth_url": "https://provider.example/authorize"}),
					"303": map[string]any{"description": "一般表單成功，導向第三方授權頁。", "headers": map[string]any{"Location": map[string]any{"schema": map[string]any{"type": "string", "format": "uri"}}}},
					"400": authTextErrorResponse("復原授權缺少、無效、過期或用途錯誤。"),
					"403": authErrorResponse("瀏覽器請求未通過同來源保護。"),
					"409": authTextErrorResponse("復原授權已使用或目標帳號不符合復原條件。"),
					"429": authErrorResponse("復原入口次數過多；可參考 Retry-After。"),
					"502": authTextErrorResponse("第三方提供者無法建立授權網址。"),
					"503": authMixedErrorResponse("復原或 OAuth 服務暫時無法使用；回應可能是 JSON 或純文字。"),
				},
			},
		},
		{
			method: "GET", path: "/api/auth/identities", summary: "列出目前使用者可用的登入方式",
			openAPI: map[string]any{
				"summary":     "列出目前使用者可用的登入方式",
				"description": "所有已登入角色均可使用。固定回傳 Google、GitHub 的啟用與連結狀態，不回傳第三方固定身分值或其他使用者資料。",
				"security":    []any{map[string]any{"BearerAuth": []any{}}, map[string]any{"JWTTokenCookie": []any{}}, map[string]any{"SessionCookie": []any{}}},
				"responses": map[string]any{
					"200": authJSONResponse("本人登入方式。", identityListRef, map[string]any{"local_login": true, "providers": []any{map[string]any{"provider": "google", "enabled": true, "linked": false}, map[string]any{"provider": "github", "enabled": false, "linked": false}}}),
					"401": authErrorResponse("缺少、過期、撤銷或無效的登入憑證。"),
					"503": authErrorResponse("登入方式資料暫時無法取得。"),
				},
			},
		},
		{
			method: "POST", path: "/api/auth/identities/{provider}/start", summary: "開始連結 OAuth 登入方式",
			openAPI: map[string]any{
				"summary":     "開始連結 OAuth 登入方式",
				"description": "所有已登入角色均可使用。僅接受有效 JWT Cookie 或 Session Cookie；此互動流程禁止 Authorization 標頭。需提供同來源請求證明（Origin／Referer／Sec-Fetch-Site 或 X-Jelly-CSRF: 1）。開始授權不新增關聯，也不重發登入憑證。",
				"parameters":  []any{authProviderParameter()},
				"security":    []any{map[string]any{"JWTTokenCookie": []any{}}, map[string]any{"SessionCookie": []any{}}},
				"responses": map[string]any{
					"200": authJSONResponse("建立連結授權網址。", authURLRef, map[string]any{"auth_url": "https://provider.example/authorize"}),
					"400": authErrorResponse("提供者無效、請求格式錯誤，或帶有此流程不接受的 Authorization 標頭。"),
					"401": authErrorResponse("缺少、過期、撤銷或無效的 Cookie 登入。"),
					"403": authErrorResponse("瀏覽器請求未通過同來源保護。"),
					"429": authErrorResponse("OAuth 入口次數過多；可參考 Retry-After。"),
					"502": authErrorResponse("第三方提供者無法建立授權網址。"),
					"503": authErrorResponse("登入或 OAuth 服務暫時無法使用。"),
				},
			},
		},
		{
			method: "DELETE", path: "/api/auth/identities/{provider}", summary: "解除本人 OAuth 登入方式",
			openAPI: map[string]any{
				"summary":     "解除本人 OAuth 登入方式",
				"description": "所有已登入角色均可使用。可用 Bearer JWT、JWT Cookie 或 Session Cookie；Cookie 寫入須提供通過同來源檢查的來源證明。未連結時仍成功；不能解除最後一種可用登入方式。",
				"parameters":  []any{authProviderParameter()},
				"security":    []any{map[string]any{"BearerAuth": []any{}}, map[string]any{"JWTTokenCookie": []any{}}, map[string]any{"SessionCookie": []any{}}},
				"responses": map[string]any{
					"200": authJSONResponse("解除成功或該提供者原本未連結。", messageRef, map[string]any{"message": "Login method removed"}),
					"400": authErrorResponse("提供者無效。"),
					"401": authErrorResponse("缺少、過期、撤銷或無效的登入憑證。"),
					"403": authErrorResponse("瀏覽器 Cookie 請求未通過同來源保護。"),
					"409": authErrorResponse("不能解除最後一種可用登入方式，或身分狀態衝突。"),
					"503": authErrorResponse("登入方式資料暫時無法更新。"),
				},
			},
		},
	}
}

func authAPIComponents() map[string]any {
	stringProperty := func(description string) map[string]any { return authStringSchema(description) }
	return map[string]any{
		"securitySchemes": map[string]any{
			"BearerAuth":     map[string]any{"type": "http", "scheme": "bearer", "bearerFormat": "JWT"},
			"JWTTokenCookie": map[string]any{"type": "apiKey", "in": "cookie", "name": "jwt_token"},
			"SessionCookie":  map[string]any{"type": "apiKey", "in": "cookie", "name": "jelly-cms-session"},
		},
		"schemas": map[string]any{
			"Error":   authErrorSchema(),
			"Message": authObjectSchema([]string{"message"}, map[string]any{"message": stringProperty("結果訊息。")}),
			"User":    authUserSchema(),
			"LocalLoginRequest": authObjectSchema([]string{"username", "password"}, map[string]any{
				"username": stringProperty("使用者名稱或電子郵件。"),
				"password": map[string]any{"type": "string", "format": "password", "writeOnly": true},
			}),
			"LoginResponse": authObjectSchema([]string{"message", "user", "token"}, map[string]any{
				"message": stringProperty("登入結果。"), "user": map[string]any{"$ref": "#/components/schemas/User"},
				"token": map[string]any{"type": "string", "description": "新簽發的登入 JWT。"},
			}),
			"AuthURL": authObjectSchema([]string{"auth_url"}, map[string]any{"auth_url": map[string]any{"type": "string", "format": "uri", "description": "第三方提供者的授權網址。"}}),
			"ProviderIdentityStatus": authObjectSchema([]string{"provider", "enabled", "linked"}, map[string]any{
				"provider": map[string]any{"type": "string", "enum": []string{"google", "github"}},
				"enabled":  map[string]any{"type": "boolean", "description": "此程序已載入且可開始 OAuth。"},
				"linked":   map[string]any{"type": "boolean", "description": "目前使用者已連結此登入方式。"},
			}),
			"IdentityList": authObjectSchema([]string{"local_login", "providers"}, map[string]any{
				"local_login": map[string]any{"type": "boolean", "description": "目前帳號有可用的本機密碼登入。"},
				"providers":   map[string]any{"type": "array", "minItems": 2, "maxItems": 2, "items": map[string]any{"$ref": "#/components/schemas/ProviderIdentityStatus"}},
			}),
			"RecoveryRequest": authObjectSchema([]string{"grant"}, map[string]any{"grant": map[string]any{"type": "string", "writeOnly": true, "description": "由網站擁有者核實本人後產生的一次性 OAuth 復原授權。只接受本文，不接受 query。"}}),
		},
	}
}

func authErrorSchema() map[string]any {
	return authObjectSchema([]string{"error"}, map[string]any{"error": map[string]any{"type": "string"}})
}

func authOpenAPIDocument(operations []authDocumentedOperation) map[string]any {
	paths := make(map[string]any)
	for _, operation := range operations {
		pathItem, ok := paths[operation.path].(map[string]any)
		if !ok {
			pathItem = make(map[string]any)
			paths[operation.path] = pathItem
		}
		pathItem[strings.ToLower(operation.method)] = operation.openAPI
	}
	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "Jelly CMS Auth API",
			"version":     "1.0.0",
			"description": "認證、OAuth 登入、本人登入方式管理與一次性帳號復原的 HTTP 介面契約。文件不包含站台設定或任何帳號／提供者秘密。",
		},
		"servers":    []any{map[string]any{"url": "/", "description": "目前站台"}},
		"paths":      paths,
		"components": authAPIComponents(),
	}
}

// HandleAuthAPIIndex returns the stable index of the documented auth operations.
func HandleAuthAPIIndex(c echo.Context) error {
	operations := authDocumentationOperations()
	index := make([]map[string]string, 0, len(operations))
	for _, operation := range operations {
		index = append(index, map[string]string{
			"method":  operation.method,
			"path":    operation.path,
			"summary": operation.summary,
		})
	}
	return c.JSON(200, map[string]any{
		"openapi_url": "/api/auth/openapi.json",
		"operations":  index,
	})
}

// HandleAuthOpenAPI returns the OpenAPI document for the auth HTTP contract.
func HandleAuthOpenAPI(c echo.Context) error {
	return c.JSON(200, authOpenAPIDocument(authDocumentationOperations()))
}
