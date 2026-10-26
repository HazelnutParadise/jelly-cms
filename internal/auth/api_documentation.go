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

func authRateLimitResponse(description string) map[string]any {
	response := authErrorResponse(description)
	response["headers"] = map[string]any{"Retry-After": map[string]any{"description": "再次嘗試前需等待的秒數。", "schema": map[string]any{"type": "integer", "minimum": 1}}}
	return response
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
	dateTime := map[string]any{"type": "string", "format": "date-time", "nullable": true}
	return map[string]any{
		"type":        "object",
		"description": "登入回應中的 core.User JSON 欄位；不包含密碼雜湊或待確認電子郵件 token。",
		"required":    []string{"id", "username", "email", "avatar_url", "role", "provider", "created_at", "updated_at", "display_name", "bio", "locale", "ui_theme", "status", "disabled_at", "last_login_at", "password_changed_at", "email_verified_at"},
		"properties": map[string]any{
			"id": map[string]any{"type": "integer", "format": "int64"}, "username": map[string]any{"type": "string"},
			"email": map[string]any{"type": "string", "format": "email"}, "avatar_url": map[string]any{"type": "string"},
			"role":       map[string]any{"type": "string", "enum": []string{"admin", "editor", "author", "customer"}},
			"provider":   map[string]any{"type": "string", "enum": []string{"local", "google", "github"}},
			"created_at": map[string]any{"type": "string", "format": "date-time"}, "updated_at": map[string]any{"type": "string", "format": "date-time"},
			"display_name": map[string]any{"type": "string"}, "bio": map[string]any{"type": "string"},
			"locale": map[string]any{"type": "string"}, "ui_theme": map[string]any{"type": "string"},
			"status":      map[string]any{"type": "string", "enum": []string{"active", "invited", "disabled"}},
			"disabled_at": dateTime, "last_login_at": dateTime, "password_changed_at": dateTime, "email_verified_at": dateTime,
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

	return append([]authDocumentedOperation{
		{
			method: "POST", path: "/auth/login", summary: "以帳號密碼登入",
			openAPI: map[string]any{
				"summary":     "以帳號密碼登入",
				"description": "接受 JSON 或一般表單本文。未啟用兩步驟驗證時，Accept 恰為 application/json 回 message/user/token 並設定登入 Cookie；啟用時回 mfa_required/mfa_token，尚未建立登入 Session。其他情況在密碼成功後以 302 導向安全的站內 redirect。瀏覽器寫入須提供通過同來源檢查的來源證明。",
				"parameters":  []any{map[string]any{"name": "redirect", "in": "query", "required": false, "description": "一般表單登入成功後的站內返回路徑；站外值不會被採用。", "schema": map[string]any{"type": "string"}, "example": "/admin"}},
				"requestBody": authBody(true, loginRequestRef, map[string]any{"username": "editor@example.test", "password": "example-password"}, map[string]any{"username": "editor@example.test", "password": "example-password"}),
				"responses": map[string]any{
					"200": authJSONResponse("未啟用兩步驟驗證時回登入憑證；啟用時回五分鐘用途限定 MFA token。", map[string]any{"oneOf": []any{loginResponseRef, map[string]any{"$ref": "#/components/schemas/MFAChallengeResponse"}}}, nil),
					"302": map[string]any{"description": "一般瀏覽器登入成功，導向站內 redirect；未指定時導向管理頁。", "headers": map[string]any{"Location": map[string]any{"schema": map[string]any{"type": "string"}, "description": "站內返回路徑。"}, "Set-Cookie": map[string]any{"schema": map[string]any{"type": "string"}, "description": "設定 HttpOnly 登入 Cookie。"}}},
					"400": authErrorResponse("本文格式錯誤或缺少 username/password。"),
					"401": authErrorResponse("帳號或密碼無效。"),
					"403": authErrorResponse("瀏覽器請求未通過同來源保護。"),
					"429": authRateLimitResponse("登入次數過多。"),
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
					"429": authRateLimitResponse("OAuth 入口次數過多。"),
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
					"429": authRateLimitResponse("復原入口次數過多。"),
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
					"429": authRateLimitResponse("OAuth 入口次數過多。"),
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
	}, accountDocumentationOperations()...)
}

func accountDocumentationOperations() []authDocumentedOperation {
	type operationSpec struct {
		method, path, summary, access, detail string
		body                                  bool
		success                               string
		statuses                              []string
	}
	items := []operationSpec{
		{"GET", "/api/account/profile", "讀取本人資料", "self", "所有登入角色可讀本人欄位。", false, "200", []string{"401", "500"}},
		{"PATCH", "/api/account/profile", "更新本人資料", "self", "所有登入角色與本人 API 權杖皆可更新本人資料；只接受顯示名稱、簡介、語言與主題，id、role、status、email、password 欄位會忽略。Cookie 寫入須通過同來源保護。", true, "200", []string{"400", "401", "403", "500"}},
		{"POST", "/api/account/avatar", "上傳本人頭像", "interactive", "互動式登入限定；multipart 欄位 avatar，PNG/JPEG/WebP，最多 2 MiB。", true, "200", []string{"400", "401", "403", "413", "500"}},
		{"DELETE", "/api/account/avatar", "刪除本人頭像", "interactive", "互動式登入限定。", false, "200", []string{"401", "403", "500"}},
		{"POST", "/api/account/email", "申請變更電子郵件", "interactive", "互動式登入限定；確認連結有效 24 小時，成功只回一次。", true, "202", []string{"400", "401", "403", "409", "500", "503"}},
		{"GET", "/account/email/confirm", "確認電子郵件變更", "public", "公開的一次性連結；token 放在 query，過期、重複或失效回 410。", false, "200", []string{"400", "410", "500"}},
		{"POST", "/api/account/password", "變更本人密碼", "interactive", "互動式登入限定；OAuth-only 帳號可省略 current_password，更新後撤銷其他登入。", true, "200", []string{"400", "401", "403", "500"}},
		{"GET", "/api/account/2fa", "讀取兩步驟驗證狀態", "self", "回傳啟用狀態、啟用時間與未使用備用碼數。", false, "200", []string{"401", "500"}},
		{"POST", "/api/account/2fa/enroll", "開始兩步驟驗證設定", "interactive", "互動式登入限定；不需本文；回傳 setup secret 一次，取代尚未驗證的設定。", false, "200", []string{"401", "403", "409", "500"}},
		{"POST", "/api/account/2fa/verify", "啟用兩步驟驗證", "interactive", "互動式登入限定；成功後十組備用碼只顯示一次。", true, "200", []string{"400", "401", "403", "409", "429", "500"}},
		{"POST", "/api/account/2fa/disable", "停用兩步驟驗證", "interactive", "互動式登入限定；需密碼及 TOTP 或一組未使用備用碼。", true, "200", []string{"400", "401", "403", "409", "429", "500"}},
		{"POST", "/api/account/2fa/recovery-codes", "重新產生備用碼", "interactive", "互動式登入限定；需有效 TOTP，舊備用碼全部失效，新十組只顯示一次。", true, "200", []string{"400", "401", "403", "409", "429", "500"}},
		{"GET", "/api/account/sessions", "列出本人登入裝置", "self", "只列有效登入；id 為登入雜湊前 16 個字元。", false, "200", []string{"401", "500"}},
		{"DELETE", "/api/account/sessions/{id}", "撤銷一個登入裝置", "interactive", "互動式登入限定；id 不屬於本人時回 404。", false, "200", []string{"401", "403", "404", "500"}},
		{"POST", "/api/account/sessions/revoke-others", "撤銷其他登入裝置", "interactive", "互動式登入限定；保留目前登入並撤銷其他登入。", false, "200", []string{"401", "403", "500"}},
		{"GET", "/api/account/tokens", "列出本人 API 權杖", "self", "所有登入 principal 可讀本人權杖 metadata；明文權杖不會再次回傳。", false, "200", []string{"401", "500"}},
		{"POST", "/api/account/tokens", "建立 API 權杖", "interactive", "互動式登入限定；scopes 不得超過擁有者角色，明文權杖只在建立時回傳一次。", true, "201", []string{"400", "401", "403", "500"}},
		{"PATCH", "/api/account/tokens/{id}", "更新 API 權杖名稱", "interactive", "互動式登入限定；只可改 name 與 note，無欄位可更新時回 400。", true, "200", []string{"400", "401", "403", "404", "500"}},
		{"POST", "/api/account/tokens/{id}/revoke", "撤銷 API 權杖", "interactive", "互動式登入限定；只改本人權杖，已撤銷時回 409。", false, "200", []string{"401", "403", "404", "409", "500"}},
		{"POST", "/api/account/tokens/{id}/restore", "恢復 API 權杖", "interactive", "互動式登入限定；清除撤銷狀態，不延長已過期期限；未撤銷時回 409。", false, "200", []string{"401", "403", "404", "409", "500"}},
		{"DELETE", "/api/account/tokens/{id}", "刪除已撤銷 API 權杖", "interactive", "互動式登入限定；僅能刪除已撤銷權杖，否則回 409。", false, "200", []string{"401", "403", "404", "409", "500"}},
		{"GET", "/api/account/notifications", "列出本人通知", "self", "category 可為 comments、orders、system、security、all；unread 為 0/1；before 為 ID 或 RFC3339；limit 預設 20、上限 50。", false, "200", []string{"400", "401", "500"}},
		{"POST", "/api/account/notifications/{id}/read", "標示通知已讀", "self", "所有登入角色與本人 API 權杖皆可標示本人的有效通知；不存在或非本人資料回 404。Cookie 寫入須通過同來源保護。", false, "200", []string{"400", "401", "403", "404", "500"}},
		{"POST", "/api/account/notifications/read-all", "標示通知全部已讀", "self", "所有登入角色與本人 API 權杖皆可標示本人的通知；可用 category 選擇分類，all 或未提供代表全部。Cookie 寫入須通過同來源保護。", false, "200", []string{"400", "401", "403", "500"}},
		{"GET", "/api/account/notification-preferences", "讀取通知偏好", "self", "回傳八種事件偏好、Webhook 網址及勿擾時段。", false, "200", []string{"401", "500"}},
		{"PUT", "/api/account/notification-preferences", "更新通知偏好", "self", "所有登入角色與本人 API 權杖皆可更新本人的偏好；事件只接受已知項目；Webhook 必須 HTTPS 且解析至公開 IP；勿擾時段需 HH:MM 與有效時區。Cookie 寫入須通過同來源保護。", true, "200", []string{"400", "401", "403", "500"}},
		{"POST", "/auth/login/2fa", "完成登入兩步驟驗證", "public", "公開端點，受登入限流與瀏覽器來源保護；成功建立登入 Cookie 與 JWT，五次錯誤後鎖定 15 分鐘。", true, "200", []string{"400", "401", "403", "429", "500"}},
		{"GET", "/api/admin/users", "搜尋與分頁列出使用者", "admin", "管理員限定；q 搜尋 username/display_name/email；per_page 預設 20、上限 100。", false, "200", []string{"400", "401", "403", "500"}},
		{"POST", "/api/admin/users/invite", "邀請使用者", "admin-interactive", "管理員且互動式登入限定；建立 invited 帳號，邀請連結回傳一次。", true, "201", []string{"400", "401", "403", "409", "500"}},
		{"POST", "/api/admin/users/{id}/invite/resend", "重寄使用者邀請", "admin-interactive", "管理員且互動式登入限定；舊邀請連結失效。", false, "200", []string{"400", "401", "403", "404", "409", "500"}},
		{"POST", "/api/admin/users/{id}/invite/revoke", "撤銷使用者邀請", "admin-interactive", "管理員且互動式登入限定；撤銷待接受邀請。", false, "200", []string{"400", "401", "403", "404", "409", "500"}},
		{"PATCH", "/api/admin/users/{id}", "更新使用者角色或狀態", "admin-interactive", "管理員且互動式登入限定；只接受 role、active/disabled status、display_name；不能修改自己的角色或狀態，最後一位啟用管理員不可降級或停用。", true, "200", []string{"400", "401", "403", "404", "409", "500"}},
		{"POST", "/api/admin/users/{id}/reset-password", "建立密碼重設連結", "admin-interactive", "管理員且互動式登入限定；重設連結一小時有效，成功後撤銷該使用者所有登入。", false, "200", []string{"400", "401", "403", "404", "500"}},
		{"POST", "/api/admin/users/{id}/revoke-sessions", "撤銷使用者所有登入", "admin-interactive", "管理員且互動式登入限定。", false, "200", []string{"400", "401", "403", "404", "500"}},
		{"DELETE", "/api/admin/users/{id}", "刪除使用者", "admin-interactive", "管理員且互動式登入限定；可用 reassign_to 指定內容移交對象；有訂單的帳號不可刪除，請停用以保留訂單與買家關聯；不能刪自己或最後一位管理員。", false, "200", []string{"400", "401", "403", "404", "409", "500"}},
		{"POST", "/api/admin/users/bulk", "批次管理使用者", "admin-interactive", "管理員且互動式登入限定；最多 100 個 ID，逐列套用最後管理員、不能改自己等保護。", true, "200", []string{"400", "401", "403", "500"}},
		{"GET", "/api/admin/roles", "讀取角色權限矩陣", "admin", "管理員限定，唯讀。", false, "200", []string{"401", "403", "500"}},
		{"GET", "/invite/{token}", "查看邀請頁", "public", "公開 HTML 頁面；無效或過期 token 回 410 並顯示失效狀態。", false, "200", []string{"410", "500"}},
		{"POST", "/api/invites/accept", "接受使用者邀請", "public", "公開的一次性邀請；成功啟用帳號並設定密碼。", true, "200", []string{"400", "403", "409", "410", "429", "500"}},
		{"GET", "/password-reset/{token}", "查看密碼重設頁", "public", "公開 HTML 頁面；無效或過期 token 回 410 並顯示失效狀態。", false, "200", []string{"410", "500"}},
		{"POST", "/api/password-reset", "完成密碼重設", "public", "公開的一次性重設 token；成功後撤銷此帳號所有登入。", true, "200", []string{"400", "403", "410", "429", "500"}},
	}
	operations := make([]authDocumentedOperation, 0, len(items))
	for _, item := range items {
		responses := map[string]any{item.success: authJSONResponse(item.summary+"成功。", accountResponseSchema(item.method, item.path), nil)}
		for _, status := range item.statuses {
			responses[status] = accountOperationErrorResponse(status, item.method, item.path)
		}
		if strings.HasPrefix(item.path, "/invite/") || strings.HasPrefix(item.path, "/password-reset/") {
			pageResponse := map[string]any{"description": "頁面呈現邀請或重設表單狀態。", "content": map[string]any{"text/html": map[string]any{"schema": map[string]any{"type": "string"}}}}
			responses["200"] = pageResponse
			responses["410"] = map[string]any{"description": "一次性連結失效、過期或已使用；以 HTML 顯示失效狀態。", "content": map[string]any{"text/html": map[string]any{"schema": map[string]any{"type": "string"}}}}
		}
		doc := map[string]any{"summary": item.summary, "description": item.detail, "responses": responses}
		if item.access == "self" || item.access == "interactive" || item.access == "admin" || item.access == "admin-interactive" {
			doc["security"] = []any{map[string]any{"BearerAuth": []any{}}, map[string]any{"JWTTokenCookie": []any{}}, map[string]any{"SessionCookie": []any{}}}
			if item.access == "self" || item.access == "admin" {
				doc["security"] = append(doc["security"].([]any), map[string]any{"ApiTokenAuth": []any{}})
			}
		}
		if strings.HasPrefix(item.access, "admin") {
			doc["description"] = "僅限管理員。" + item.detail
		}
		if strings.HasSuffix(item.access, "interactive") {
			doc["description"] = doc["description"].(string) + " API 權杖不允許此操作。Cookie 寫入須通過同來源保護。"
		}
		if item.body {
			doc["requestBody"] = accountRequestBody(item.method, item.path)
		}
		parameters := accountParameters(item.method, item.path)
		if len(parameters) > 0 {
			doc["parameters"] = parameters
		}
		operations = append(operations, authDocumentedOperation{method: item.method, path: item.path, summary: item.summary, openAPI: doc})
	}
	return operations
}

func accountRequestBody(method, path string) map[string]any {
	schema := accountRequestSchema(path)
	if path == "/api/account/avatar" {
		return map[string]any{"required": true, "content": map[string]any{"multipart/form-data": map[string]any{"schema": schema}}}
	}
	jsonOnly := path == "/api/account/notification-preferences" ||
		path == "/api/admin/users/invite" || path == "/api/admin/users/bulk" ||
		path == "/api/invites/accept" || path == "/api/password-reset" ||
		(path == "/api/admin/users/{id}" && method == "PATCH")
	if jsonOnly {
		return map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
	}
	return authBody(true, schema, nil, nil)
}

func accountRequestSchema(path string) map[string]any {
	str := func(description string) map[string]any { return authStringSchema(description) }
	password := map[string]any{"type": "string", "format": "password", "writeOnly": true}
	props := map[string]any{}
	required := []string{}
	needsFactor := false
	switch path {
	case "/api/account/profile":
		props = map[string]any{"display_name": str("顯示名稱，1 到 120 字。"), "bio": str("個人簡介，最多 500 字。"), "locale": map[string]any{"type": "string", "enum": []string{"zh-TW", "en"}}, "ui_theme": map[string]any{"type": "string", "enum": []string{"system", "light", "dark"}}}
	case "/api/account/avatar":
		return authObjectSchema([]string{"avatar"}, map[string]any{"avatar": map[string]any{"type": "string", "format": "binary", "description": "PNG、JPEG 或 WebP，最多 2 MiB。"}})
	case "/api/account/email":
		required = []string{"new_email", "current_password"}
		props = map[string]any{"new_email": map[string]any{"type": "string", "format": "email"}, "current_password": password}
	case "/api/account/password":
		required = []string{"new_password"}
		props = map[string]any{"current_password": password, "new_password": map[string]any{"type": "string", "minLength": 8, "format": "password", "writeOnly": true}}
	case "/api/account/2fa/enroll":
		return authObjectSchema(nil, props)
	case "/api/account/2fa/verify", "/api/account/2fa/recovery-codes":
		required = []string{"code"}
		props = map[string]any{"code": map[string]any{"type": "string", "minLength": 6, "maxLength": 6, "pattern": "^[0-9]{6}$"}}
	case "/api/account/2fa/disable":
		required = []string{"current_password"}
		props = map[string]any{"current_password": password, "code": map[string]any{"type": "string", "minLength": 6, "maxLength": 6, "pattern": "^[0-9]{6}$"}, "recovery_code": map[string]any{"type": "string", "description": "十六位一次性備用碼，可含連字號。"}}
		needsFactor = true
	case "/api/account/tokens":
		required = []string{"name", "scopes"}
		props = map[string]any{"name": map[string]any{"type": "string", "minLength": 1, "maxLength": 80}, "note": str("權杖用途備註。"), "expires_in_days": map[string]any{"type": "integer", "nullable": true, "enum": []any{7, 30, 90, 365, nil}}, "scopes": map[string]any{"type": "array", "minItems": 1, "uniqueItems": true, "items": map[string]any{"type": "string", "enum": []string{"admin", "editor", "author", "content_read", "orders_read"}}}}
	case "/api/account/tokens/{id}":
		props = map[string]any{"name": map[string]any{"type": "string", "minLength": 1, "maxLength": 80}, "note": str("權杖用途備註。")}
	case "/api/account/notifications/read-all":
		props = map[string]any{"category": map[string]any{"type": "string", "enum": []string{"comments", "orders", "security", "system"}}}
	case "/api/account/notification-preferences":
		props = map[string]any{"events": notificationPreferenceEventsSchema(false), "webhook_url": map[string]any{"type": "string", "maxLength": 2048, "description": "空字串代表未設定；有值時須為 HTTPS 且只解析到公開 IP。"}, "quiet": authObjectSchema(nil, map[string]any{"enabled": map[string]any{"type": "boolean"}, "start": str("HH:MM；enabled 時必填，停用時須留空。"), "end": str("HH:MM；enabled 時必填，停用時須留空。"), "tz": str("IANA 時區；enabled 時必填，停用時須留空。")})}
	case "/auth/login/2fa":
		required = []string{"mfa_token"}
		props = map[string]any{"mfa_token": str("密碼步驟簽發的五分鐘用途限定權杖。"), "code": map[string]any{"type": "string", "minLength": 6, "maxLength": 6, "pattern": "^[0-9]{6}$"}, "recovery_code": map[string]any{"type": "string", "description": "十六位一次性備用碼，可含連字號。"}}
		needsFactor = true
	case "/api/admin/users/invite":
		required = []string{"email", "role"}
		props = map[string]any{"email": map[string]any{"type": "string", "format": "email"}, "role": map[string]any{"type": "string", "enum": []string{"admin", "editor", "author", "customer"}}, "note": map[string]any{"type": "string", "maxLength": 500}}
	case "/api/admin/users/{id}":
		props = map[string]any{"role": map[string]any{"type": "string", "enum": []string{"admin", "editor", "author", "customer"}}, "status": map[string]any{"type": "string", "enum": []string{"active", "disabled"}}, "display_name": map[string]any{"type": "string", "minLength": 1, "maxLength": 120}}
	case "/api/admin/users/bulk":
		required = []string{"ids", "action"}
		props = map[string]any{"ids": map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "integer", "minimum": 1}}, "action": map[string]any{"type": "string", "enum": []string{"disable", "enable", "set_role", "delete"}}, "role": map[string]any{"type": "string", "enum": []string{"admin", "editor", "author", "customer"}, "description": "action=set_role 時必填。"}}
	case "/api/invites/accept":
		required = []string{"token", "password"}
		props = map[string]any{"token": map[string]any{"type": "string", "writeOnly": true}, "username": map[string]any{"type": "string", "minLength": 1, "maxLength": 50}, "password": map[string]any{"type": "string", "minLength": 8, "maxLength": 72, "format": "password", "writeOnly": true}, "display_name": map[string]any{"type": "string", "maxLength": 120}}
	case "/api/password-reset":
		required = []string{"token", "password"}
		props = map[string]any{"token": map[string]any{"type": "string", "writeOnly": true}, "password": map[string]any{"type": "string", "minLength": 8, "maxLength": 72, "format": "password", "writeOnly": true}}
	default:
		props = map[string]any{"id": str("資源識別碼。")}
	}
	schema := authObjectSchema(required, props)
	if needsFactor {
		schema = map[string]any{"allOf": []any{schema, map[string]any{"oneOf": []any{map[string]any{"required": []string{"code"}}, map[string]any{"required": []string{"recovery_code"}}}}}}
	}
	if path == "/api/account/notification-preferences" || path == "/api/admin/users/invite" || path == "/api/admin/users/bulk" || path == "/api/invites/accept" || path == "/api/password-reset" || path == "/api/admin/users/{id}" {
		schema["additionalProperties"] = false
	}
	return schema
}

func accountResponseSchema(method, path string) any {
	str := func(description string) map[string]any { return authStringSchema(description) }
	integer := map[string]any{"type": "integer", "format": "int64"}
	message := map[string]any{"$ref": "#/components/schemas/Message"}
	switch {
	case path == "/api/account/profile":
		return authObjectSchema([]string{"id", "username", "display_name", "email", "email_verified", "pending_email", "avatar_url", "bio", "locale", "ui_theme", "role", "created_at", "post_count", "author_url"}, map[string]any{
			"id": integer, "username": str("帳號名稱。"), "display_name": str("顯示名稱。"), "email": map[string]any{"type": "string", "format": "email"}, "email_verified": map[string]any{"type": "boolean"}, "pending_email": str("等待確認的新電子郵件。"), "avatar_url": str("頭像路徑。"), "bio": str("個人簡介。"), "locale": map[string]any{"type": "string", "enum": []string{"zh-TW", "en"}}, "ui_theme": map[string]any{"type": "string", "enum": []string{"system", "light", "dark"}}, "role": str("本人角色。"), "created_at": map[string]any{"type": "string", "format": "date-time"}, "post_count": integer, "author_url": str("作者頁路徑。"),
		})
	case path == "/api/account/avatar":
		return authObjectSchema([]string{"avatar_url"}, map[string]any{"avatar_url": str("站內頭像路徑；刪除時為空字串。")})
	case path == "/api/account/email":
		return authObjectSchema([]string{"confirm_url"}, map[string]any{"confirm_url": map[string]any{"type": "string", "format": "uri", "description": "24 小時有效的一次性確認連結。"}})
	case path == "/api/account/2fa":
		return authObjectSchema([]string{"enabled", "enabled_at", "recovery_remaining"}, map[string]any{"enabled": map[string]any{"type": "boolean"}, "enabled_at": map[string]any{"type": "string", "format": "date-time", "nullable": true}, "recovery_remaining": integer})
	case path == "/api/account/2fa/enroll":
		return authObjectSchema([]string{"secret", "otpauth_uri", "qr_svg"}, map[string]any{"secret": str("僅在設定時回傳的 TOTP secret。"), "otpauth_uri": str("Authenticator 設定 URI。"), "qr_svg": str("離線產生的 QR SVG。")})
	case path == "/api/account/2fa/verify" || path == "/api/account/2fa/recovery-codes":
		return authObjectSchema([]string{"recovery_codes"}, map[string]any{"recovery_codes": map[string]any{"type": "array", "minItems": 10, "maxItems": 10, "items": str("只能使用一次的備用碼。")}})
	case path == "/api/account/sessions":
		return map[string]any{"type": "array", "items": authObjectSchema([]string{"id", "device_label", "ip_masked", "created_at", "last_seen_at", "current", "new_device"}, map[string]any{"id": str("登入識別雜湊前 16 個字元。"), "device_label": str("瀏覽器與作業系統摘要。"), "ip_masked": str("遮罩 IP。"), "created_at": map[string]any{"type": "string", "format": "date-time"}, "last_seen_at": map[string]any{"type": "string", "format": "date-time"}, "current": map[string]any{"type": "boolean", "description": "是否為目前這次登入。"}, "new_device": map[string]any{"type": "boolean", "description": "最近 90 天內此 User-Agent hash 僅出現於一筆登入時為 true。"}})}
	case path == "/api/account/tokens" && method == "GET":
		return map[string]any{"type": "array", "items": accountTokenSchema()}
	case path == "/api/account/tokens" && method == "POST":
		return accountTokenCreatedSchema()
	case strings.HasPrefix(path, "/api/account/tokens/") && method == "PATCH":
		return accountTokenSchema()
	case path == "/api/account/notifications":
		return authObjectSchema([]string{"items", "unread_count", "counts"}, map[string]any{"items": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/Notification"}}, "unread_count": integer, "counts": map[string]any{"$ref": "#/components/schemas/NotificationCounts"}})
	case path == "/api/account/notification-preferences":
		if method == "PUT" {
			return authObjectSchema([]string{"ok"}, map[string]any{"ok": map[string]any{"type": "boolean"}})
		}
		return accountNotificationPreferencesSchema()
	case path == "/auth/login/2fa":
		return map[string]any{"$ref": "#/components/schemas/LoginResponse"}
	case path == "/api/account/notifications/read-all":
		return authObjectSchema([]string{"updated"}, map[string]any{"updated": integer})
	case path == "/api/account/notifications/{id}/read":
		return authObjectSchema([]string{"ok"}, map[string]any{"ok": map[string]any{"type": "boolean"}})
	case path == "/api/admin/users":
		return adminUsersResponseSchema()
	case path == "/api/admin/users/invite" || strings.HasSuffix(path, "/invite/resend"):
		return authObjectSchema([]string{"id", "invite_url", "expires_at", "emailed"}, map[string]any{"id": integer, "invite_url": str("邀請頁路徑。"), "expires_at": map[string]any{"type": "string", "format": "date-time"}, "emailed": map[string]any{"type": "boolean", "description": "目前未設定寄件服務時為 false。"}})
	case path == "/api/account/email/confirm", path == "/api/account/password", path == "/api/account/sessions/revoke-others",
		path == "/api/invites/accept", path == "/api/password-reset", path == "/api/account/avatar" && method == "DELETE",
		path == "/api/account/sessions/{id}" && method == "DELETE",
		strings.HasSuffix(path, "/invite/revoke"), strings.HasSuffix(path, "/tokens/{id}/revoke"), strings.HasSuffix(path, "/tokens/{id}/restore"),
		path == "/api/admin/users/{id}" && method == "DELETE":
		return message
	case path == "/api/admin/users/{id}" && method == "PATCH":
		return authObjectSchema([]string{"id", "username", "display_name", "email", "avatar_url", "role", "status"}, map[string]any{"id": integer, "username": str("帳號名稱。"), "display_name": str("顯示名稱。"), "email": map[string]any{"type": "string", "format": "email"}, "avatar_url": str("頭像路徑。"), "role": str("角色。"), "status": str("帳號狀態。")})
	case strings.HasSuffix(path, "/reset-password"):
		return authObjectSchema([]string{"reset_url", "emailed"}, map[string]any{"reset_url": str("一小時有效的一次性重設路徑。"), "emailed": map[string]any{"type": "boolean"}})
	case strings.HasSuffix(path, "/revoke-sessions"):
		return authObjectSchema([]string{"revoked"}, map[string]any{"revoked": map[string]any{"type": "boolean"}})
	case path == "/api/admin/users/bulk":
		return map[string]any{"$ref": "#/components/schemas/BulkUserResult"}
	case path == "/api/admin/roles":
		return authObjectSchema([]string{"roles", "permissions"}, map[string]any{"roles": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/RolePermissionMatrix"}}, "permissions": map[string]any{"type": "array", "items": authObjectSchema([]string{"key"}, map[string]any{"key": str("權限識別碼。")})}})
	default:
		return message
	}
}

func accountTokenSchema() map[string]any {
	str := func(description string) map[string]any { return authStringSchema(description) }
	properties := map[string]any{"id": map[string]any{"type": "integer"}, "name": str("權杖名稱。"), "note": str("用途備註。"), "last4": str("權杖末四碼。"), "scopes": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "expires_at": map[string]any{"type": "string", "format": "date-time", "nullable": true}, "last_used_at": map[string]any{"type": "string", "format": "date-time", "nullable": true}, "last_used_ip_masked": str("遮罩後的最後使用 IP。"), "status": map[string]any{"type": "string", "enum": []string{"active", "expired", "revoked"}}}
	required := []string{"id", "name", "note", "last4", "scopes", "expires_at", "last_used_at", "last_used_ip_masked", "status"}
	return authObjectSchema(required, properties)
}

func accountTokenCreatedSchema() map[string]any {
	str := func(description string) map[string]any { return authStringSchema(description) }
	return authObjectSchema([]string{"id", "name", "note", "last4", "scopes", "expires_at", "token"}, map[string]any{
		"id": map[string]any{"type": "integer", "format": "int64"}, "name": str("權杖名稱。"), "note": str("用途備註。"),
		"last4": str("權杖末四碼。"), "scopes": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"expires_at": map[string]any{"type": "string", "format": "date-time", "nullable": true},
		"token":      str("只在建立權杖時回傳一次的明文權杖。"),
	})
}

func notificationPreferenceEventsSchema(response bool) map[string]any {
	channels := authObjectSchema(nil, map[string]any{"in_app": map[string]any{"type": "boolean"}, "email": map[string]any{"type": "boolean"}, "webhook": map[string]any{"type": "boolean"}})
	channels["additionalProperties"] = false
	if response {
		channels["required"] = []string{"in_app", "email", "webhook"}
	}
	properties := map[string]any{
		"comment.pending": channels, "comment.reply": channels,
		"order.new": channels, "order.payment_failed": channels,
		"security.new_device": channels, "security.password_changed": channels,
		"system.update_available": channels, "system.plugin_failed": channels,
	}
	return map[string]any{"type": "object", "description": "只接受八種已登記通知事件。", "properties": properties, "additionalProperties": false}
}

func accountNotificationPreferencesSchema() map[string]any {
	str := func(description string) map[string]any { return authStringSchema(description) }
	return authObjectSchema([]string{"events", "webhook_url", "quiet"}, map[string]any{
		"events":      notificationPreferenceEventsSchema(true),
		"webhook_url": map[string]any{"type": "string", "description": "空字串代表未設定。"},
		"quiet":       authObjectSchema([]string{"enabled", "start", "end", "tz"}, map[string]any{"enabled": map[string]any{"type": "boolean"}, "start": str("HH:MM。"), "end": str("HH:MM。"), "tz": str("IANA 時區名稱。")}),
	})
}

func adminUsersResponseSchema() map[string]any {
	str := func(description string) map[string]any { return authStringSchema(description) }
	user := authObjectSchema([]string{"id", "username", "display_name", "email", "avatar_url", "role", "status", "last_login_at", "last_login_device", "invite_expires_at", "pending_email"}, map[string]any{
		"id": map[string]any{"type": "integer", "format": "int64"}, "username": str("帳號名稱。"), "display_name": str("顯示名稱。"), "email": map[string]any{"type": "string", "format": "email"}, "avatar_url": str("頭像路徑。"), "role": map[string]any{"type": "string", "enum": []string{"admin", "editor", "author", "customer"}}, "status": map[string]any{"type": "string", "enum": []string{"active", "invited", "disabled"}}, "last_login_at": map[string]any{"type": "string", "format": "date-time", "nullable": true}, "last_login_device": str("最近登入裝置。"), "invite_expires_at": map[string]any{"type": "string", "format": "date-time", "nullable": true}, "pending_email": str("等待確認的新電子郵件。"),
	})
	return authObjectSchema([]string{"items", "total", "counts"}, map[string]any{
		"items": map[string]any{"type": "array", "items": user}, "total": map[string]any{"type": "integer", "format": "int64"},
		"counts": authObjectSchema([]string{"all", "role", "status"}, map[string]any{"all": map[string]any{"type": "integer", "format": "int64"}, "role": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer"}}, "status": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer"}}}),
	})
}

func accountParameters(method, path string) []any {
	params := []any{}
	if strings.Contains(path, "{id}") {
		idSchema := map[string]any{"type": "integer", "format": "int64", "minimum": 1}
		if path == "/api/account/sessions/{id}" {
			idSchema = map[string]any{"type": "string", "minLength": 16, "maxLength": 16, "pattern": "^[0-9a-f]{16}$"}
		}
		params = append(params, map[string]any{"name": "id", "in": "path", "required": true, "schema": idSchema})
	}
	if strings.Contains(path, "{token}") {
		params = append(params, map[string]any{"name": "token", "in": "path", "required": true, "schema": map[string]any{"type": "string", "writeOnly": true}})
	}
	query := map[string]map[string]any{}
	switch path {
	case "/account/email/confirm":
		query["token"] = map[string]any{"type": "string", "writeOnly": true}
	case "/api/account/notifications":
		query = map[string]map[string]any{"category": {"type": "string", "enum": []string{"all", "comments", "orders", "system", "security"}}, "unread": {"type": "integer", "enum": []int{0, 1}}, "before": {"type": "string", "description": "通知 ID 或 RFC3339 時間。"}, "limit": {"type": "integer", "minimum": 1, "maximum": 50, "default": 20}}
	case "/api/account/notifications/read-all":
		query["category"] = map[string]any{"type": "string", "enum": []string{"all", "comments", "orders", "system", "security"}}
	case "/api/admin/users":
		query = map[string]map[string]any{"q": {"type": "string"}, "role": {"type": "string", "enum": []string{"admin", "editor", "author", "customer"}}, "status": {"type": "string", "enum": []string{"active", "invited", "disabled"}}, "page": {"type": "integer", "minimum": 1, "default": 1}, "per_page": {"type": "integer", "minimum": 1, "maximum": 100, "default": 20}}
	case "/api/admin/users/{id}":
		if method == "DELETE" {
			query["reassign_to"] = map[string]any{"type": "integer", "minimum": 1, "description": "刪除使用者時的內容移交目標 ID。"}
		}
	}
	for name, schema := range query {
		params = append(params, map[string]any{"name": name, "in": "query", "required": path == "/account/email/confirm" && name == "token", "schema": schema})
	}
	return params
}

func accountOperationErrorResponse(status, method, path string) map[string]any {
	if status == "409" && method == "DELETE" && path == "/api/admin/users/{id}" {
		return authJSONResponse("使用者仍有內容，需提供 reassign_to；或帳號仍有訂單，必須停用以保留訂單與買家關聯。", map[string]any{
			"oneOf": []any{
				map[string]any{"$ref": "#/components/schemas/UserContentReassignCounts"},
				map[string]any{"$ref": "#/components/schemas/Error"},
			},
		}, map[string]any{"posts": 1, "pages": 0, "comments": 2})
	}
	response := authErrorResponse(accountErrorDescription(status, path))
	if status == "429" {
		return authRateLimitResponse(accountErrorDescription(status, path))
	}
	return response
}

func accountErrorDescription(status, path string) string {
	switch status {
	case "400":
		return "本文、路徑或查詢參數格式無效。"
	case "401":
		return "登入憑證、密碼或驗證碼無效，或登入已撤銷。"
	case "403":
		return "管理員權限、互動式登入或瀏覽器來源檢查未通過。"
	case "404":
		return "資源不存在或不屬於目前使用者。"
	case "409":
		return "資源狀態衝突，例如電子郵件已使用、最後管理員保護或權杖尚未撤銷。"
	case "410":
		return "一次性連結無效、過期或已使用。"
	case "413":
		return "上傳檔案超過 2 MiB。"
	case "429":
		return "驗證或操作已達速率限制；兩步驟驗證鎖定時間為 15 分鐘。"
	case "500":
		return "伺服器無法完成操作。"
	case "503":
		return "登入或資料服務暫時無法使用。"
	default:
		return path + " 操作失敗。"
	}
}

func authAPIComponents() map[string]any {
	stringProperty := func(description string) map[string]any { return authStringSchema(description) }
	return map[string]any{
		"securitySchemes": map[string]any{
			"BearerAuth":     map[string]any{"type": "http", "scheme": "bearer", "bearerFormat": "JWT"},
			"ApiTokenAuth":   map[string]any{"type": "http", "scheme": "bearer", "bearerFormat": "jly_ personal API token"},
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
			"MFAChallengeResponse": authObjectSchema([]string{"mfa_required", "mfa_token"}, map[string]any{
				"mfa_required": map[string]any{"type": "boolean", "enum": []bool{true}},
				"mfa_token":    map[string]any{"type": "string", "description": "五分鐘有效、僅供完成登入兩步驟驗證的用途限定權杖。"},
			}),
			"Notification": authObjectSchema([]string{"id", "user_id", "kind", "category", "title_key", "params", "actions", "group_key", "count", "in_app_visible", "read_at", "created_at", "expires_at"}, map[string]any{
				"id": map[string]any{"type": "integer", "format": "int64"}, "user_id": map[string]any{"type": "integer", "format": "int64"},
				"kind": stringProperty("通知事件種類。"), "category": map[string]any{"type": "string", "enum": []string{"comments", "orders", "system", "security"}},
				"title_key": stringProperty("本地化標題鍵。"), "params": map[string]any{"type": "object", "additionalProperties": true},
				"actions":   map[string]any{"type": "array", "items": authObjectSchema([]string{"label_key", "url"}, map[string]any{"label_key": stringProperty("動作的本地化標籤鍵。"), "url": stringProperty("站內操作路徑。")})},
				"group_key": stringProperty("合併未讀通知的分組鍵。"), "count": map[string]any{"type": "integer", "minimum": 1},
				"in_app_visible": map[string]any{"type": "boolean"}, "read_at": map[string]any{"type": "string", "format": "date-time", "nullable": true},
				"created_at": map[string]any{"type": "string", "format": "date-time"}, "expires_at": map[string]any{"type": "string", "format": "date-time"},
			}),
			"NotificationCounts": authObjectSchema([]string{"all", "unread", "comments", "orders", "system", "security"}, map[string]any{
				"all": map[string]any{"type": "integer", "minimum": 0}, "unread": map[string]any{"type": "integer", "minimum": 0},
				"comments": map[string]any{"type": "integer", "minimum": 0}, "orders": map[string]any{"type": "integer", "minimum": 0},
				"system": map[string]any{"type": "integer", "minimum": 0}, "security": map[string]any{"type": "integer", "minimum": 0},
			}),
			"BulkUserResult": authObjectSchema([]string{"updated", "skipped"}, map[string]any{
				"updated": map[string]any{"type": "integer", "minimum": 0},
				"skipped": map[string]any{"type": "array", "items": authObjectSchema([]string{"id", "reason"}, map[string]any{"id": map[string]any{"type": "integer", "format": "int64"}, "reason": stringProperty("此使用者未套用批次操作的原因。")})},
			}),
			"RolePermissionMatrix": authObjectSchema([]string{"key", "permissions"}, map[string]any{
				"key":         map[string]any{"type": "string", "enum": []string{"admin", "editor", "author", "customer"}},
				"permissions": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "boolean"}},
			}),
			"UserContentReassignCounts": authObjectSchema([]string{"posts", "pages", "comments"}, map[string]any{
				"posts": map[string]any{"type": "integer", "minimum": 0}, "pages": map[string]any{"type": "integer", "minimum": 0}, "comments": map[string]any{"type": "integer", "minimum": 0},
			}),
		},
	}
}

func authErrorSchema() map[string]any {
	return authObjectSchema([]string{"error"}, map[string]any{"error": map[string]any{"type": "string"}, "code": map[string]any{"type": "integer", "description": "HTTP 狀態碼；部分 handler 直接回 error 時不會包含此欄位。"}})
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
