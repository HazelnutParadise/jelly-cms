# 27 — 機器 API 有自己的契約：清單統一分頁信封、拿掉重複端點、每個串接限流、`/api` 總索引

Epic：帳號與存取
User Story：程式與 AI agent 用存取金鑰打 `/api/v1` 時，拿到的回應形狀由機器 API 自己決定，不再跟著後台畫面的 handler 走。每個清單端點都用同一種分頁信封；只有一條路做同一件事；一個串接每分鐘有上限，猜金鑰的來源會被擋；`GET /api` 一眼看到這個站有哪幾套 API、各自給誰用、文件在哪。
交付紀錄：已實作並完成本機驗收，結果見 工單的驗收紀錄。
Blocked by：24（`/api/v1` 改名，已合併）。專案沒有任何已部署站台或版本標籤，所以拿掉的端點直接回 404，不留別名。

## 背景與決定

- 現況 `/api/v1` 的路由表（`internal/server/integration_routes.go`）直接掛後台 handler：`GET /posts`、`GET /pages` 用 `listPosts`，回整包陣列、不分頁、每篇都帶 `content` 與 `source`，而且沒 preload `Author`，`author` 是一個全空的使用者物件；`GET /products` 回 `{items,total,counts,page,per_page}`，`GET /orders` 回 `{items,total,counts,kpi}`，`counts` 與 `kpi` 是後台列表畫面的統計，訂單 item 還有 `qty`／`quantity`、`total`／`total_amount` 兩組給舊畫面的重複鍵；`/me/events` 用 `next_cursor`。同一個 v1 裡有多種清單形狀，而且改後台畫面會悄悄改到外部契約。
- 現況 v1 同時放出 `GET /logs` 與 `GET /me/events`（同一個 handler）、舊版檔名式媒體 `/media*` 與新版 `/media/files*` 兩套、`POST /settings` 與 `PUT /settings` 兩種方法。
- 現況 `/api/v1` 沒有任何限流；登入、留言、購物車都有。
- 決定：v1 的回應形狀由 v1 自己的 handler 與明確列出欄位的 struct 決定，OpenAPI 從同一份欄位清單產生。清單一律是物件，不再回裸陣列。後台 `/api/admin/*` 的回應與參數處理完全不變。
- 決定：拿掉重複端點，只留 `/me/events`、`/media/files*`、`PUT /settings`。
- 決定：限流以串接（integration）為單位計數，另對金鑰驗證失敗的來源 IP 計數、對公開說明端點的來源 IP 計數。計數器先用程序內記憶體（`internal/cache` 的 memory store），之後接 Redis 共用計數另案。
- 決定：新增 `GET /api` 總索引，只列目前四套有說明端點的 API 與後台私有 API 的用途，不改任何既有端點。
- 不改：金鑰格式、權限字串、既有錯誤碼、稽核（`integration_events`）、`/api/admin/*`、`/api/public/*`、`/api/shop/*`、`/api/auth/*`、`/api/account/*` 的行為與錯誤格式。

## 名詞

| 名詞 | 定義 |
|---|---|
| 分頁信封 | `{"items": [...], "total": N, "page": P, "per_page": S}`，`total` 是符合條件的總筆數，`items` 是本頁；`items` 空時輸出 `[]` 不輸出 `null` |
| 集合信封 | `{"items": [...], "total": N}`，用在不分頁的小集合，`N` 等於 `items` 長度 |
| 事件信封 | `{"items": [...], "next_cursor": "..."|null, "total": N}`，只用在 `/me/events`（保留游標分頁，因為是追加式紀錄） |
| 內容摘要 | 文章／頁面在清單裡的形狀，欄位固定為：`id`、`title`、`slug`、`type`、`status`、`visibility`、`author_id`、`excerpt`、`cover`、`pinned`、`format`、`template`、`published_at`、`created_at`、`updated_at`、`allow_comments`、`seo_index`、`seo_ai`、`meta`、`categories`、`tags`。沒有 `content`、`source`、`author` |
| 訂單摘要 | 訂單在清單裡的形狀：`id`、`number`、`email`、`status`、`total_amount`、`currency`、`payment_provider`、`payment_method`、`shipping`、`items`（每筆 `title`、`variant_name`、`quantity`）、`created_at`。沒有 `qty`、`total` 這兩個重複鍵 |
| 有效角色 | 擁有者角色套用 `role_cap` 之後的角色，即 `integrationKeyMiddleware` 放進 `c.Get("user")` 的那個 |

## 規則

### R1 清單信封

| 端點 | 之前 | 之後 |
|---|---|---|
| `GET /posts`、`GET /pages` | 裸陣列、不分頁、含全文 | 分頁信封，`items` 為內容摘要 |
| `GET /products` | `{items,total,counts,page,per_page}` | 分頁信封（移除 `counts`；item 形狀不變） |
| `GET /orders` | `{items,total,counts,kpi}` | 分頁信封（移除 `counts`、`kpi`；加 `page`、`per_page`；item 為訂單摘要） |
| `GET /media/files` | 分頁信封 | 回應不變；參數檢查依 R2 |
| `GET /categories`、`GET /tags`、`GET /themes`、`GET /plugins`、`GET /settings` | 裸陣列（`/themes`、`/plugins` 空時回 `null`） | 集合信封，`items` 內每個元素的形狀不變（`/themes` 仍是字串），空時 `[]` |
| `GET /me/events` | 事件信封 | 不變 |

`GET /posts/:id`、`GET /pages/:id` 與寫入端點的回應不變，仍含 `content` 與 `source`。`GET /api/v1` 索引新增 `pagination` 區塊，說明頁碼式（哪些端點、`page`／`per_page`）與游標式（`/me/events`、`cursor`／`limit`）兩種樣式。

### R2 分頁與篩選參數

`GET /posts`、`GET /pages`、`GET /products`、`GET /orders`、`GET /media/files` 共用：`page` 預設 1、最大 1,000,000；`per_page` 預設 20、最大 100。帶了值但不是整數、小於 1 或超過上限，一律 400 `bad_request`，`message` 為 `Invalid page` 或 `Invalid per_page`，`details` 為 `{"param": "page"}` 或 `{"param": "per_page"}`。空字串與沒帶視為沒帶。`page` 超過最後一頁回 200、`items` 為 `[]`、`total` 照算。未知的查詢參數一律忽略。

`/products`、`/orders`、`/media/files` 的後台 handler 對這兩個參數維持原本的處理（`/products` 與 `/media/files` 夾值，`/orders` 用 `parsePositiveQueryInt` 回 400 `{"error": ...}`），v1 在呼叫查詢前先依本規則檢查。

`GET /posts`、`GET /pages` 另支援：

- `status`：非空時精確比對 `status` 欄位，不設白名單（文章狀態目前只有 `draft` 與 `published`，寫入端不驗證），空字串等於不篩選。
- `q`：標題不分大小寫包含，`%` 與 `_` 當字面值（沿用 `likeEscaper`）。

排序固定 `created_at DESC, id DESC`。回收桶內的文章不在 `items`，也不計入 `total`。有效角色為 `author` 時，`GET /posts` 只回 `author_id` 等於擁有者的文章且 `total` 同樣只算自己的；`GET /pages` 回空的分頁信封（`total` 0），與 `GET /pages/:id` 對作者一律 404 的現況一致。

### R3 拿掉的端點

`GET /logs`、`GET /media`、`POST /media`、`PUT /media/:filename`、`DELETE /media/:filename`、`POST /media/rename`、`POST /settings` 從路由表移除。打這些路徑走既有的 `/*` 兜底與 `Match` 規則：帶有效金鑰時，路徑不在表裡回 404 `not_found`，路徑還在表裡但方法不在（例如 `POST /settings`）回 405 `method_not_allowed`；不帶金鑰一律 401 `key_missing`。`GET /api/v1` 的 `endpoints` 與 `/openapi.json` 的 `paths` 不再出現這些路徑；`not_exposed` 新增兩列：`/api/admin/media`（檔名式媒體）指向 `/api/v1/media/files`，`/api/admin/logs*`（系統日誌）說明串接只能讀自己的 `/me/events`。`integration_routes.go` 上傳 body 上限條件中引用 `/media` POST 與 `/media/:filename` PUT 的部分，以及 `integrations_discovery.go` 內 `/media`、`/media/:filename`、`/media/rename`、`/logs` 的 schema、request body 與 query 分支一併刪除。

### R4 限流

三個計數器，都是對齊整分鐘的固定 60 秒視窗，窗內超過上限回 429：

| 計數器 | 鍵 | 什麼時候計數 | 超限時 | 預設上限 | 環境變數 |
|---|---|---|---|---|---|
| 串接 | `integration:<integration_id>` | 需要金鑰的路由（含 `/*` 兜底與 `Match("")`），金鑰驗證通過後、稽核之後、權限檢查之前 | 429，仍寫稽核（`status = 429`），不更新 `last_used_at` | 600 | `JELLY_API_RATE_LIMIT_PER_INTEGRATION` |
| 驗證失敗來源 | `auth_failures:<ip bucket>` | 金鑰驗證回 401 `key_invalid`（格式不對或雜湊查不到）時計數；`key_missing`、`key_revoked`、`key_expired`、`integration_disabled`、`owner_unavailable` 不計 | 該 IP 窗內的請求仍照常驗證：格式不對或雜湊查不到的改回 429（取代 401），查得到的金鑰完全不受影響 | 60 | `JELLY_API_RATE_LIMIT_AUTH_FAILURES_PER_IP` |
| 公開說明端點 | `public:<ip bucket>` | `GET /api/v1`、`GET /api/v1/openapi.json` 每次請求 | 429 | 120 | `JELLY_API_RATE_LIMIT_PUBLIC_PER_IP` |

- IP 分桶：來源用 `c.RealIP()`（沿用 `JELLY_TRUSTED_PROXIES` 的信任規則）；IPv4 整個位址一桶，IPv6 以 /64 一桶。
- 環境變數是每分鐘次數的整數；`0` 關閉該計數器（不計數、不回 429、不送 `RateLimit-*`）；無法解析或負數用預設值。
- 計數器存在 `internal/cache` memory store 的 `ratelimit` 命名空間，鍵不帶視窗戳；每次 `Incr` 的 TTL 是「到本分鐘結束的剩餘秒數」，最少 1 秒（無條件進位）。視窗到期項目自然消失，不需要清掃。
- 429 的回應：錯誤信封 `code = "rate_limited"`、`message = "Rate limit exceeded"`、`details = {"scope": "integration"|"auth_failures"|"public", "limit": N, "window_seconds": 60, "retry_after": 秒}`；標頭 `Retry-After`（距視窗結束的整數秒，最少 1）、`RateLimit-Limit`、`RateLimit-Remaining`（`0`）、`RateLimit-Reset`（同 `Retry-After`）。
- 串接計數器開啟時，金鑰驗證通過後的所有回應（2xx、4xx、5xx）都帶 `RateLimit-Limit`、`RateLimit-Remaining`、`RateLimit-Reset`；標頭在呼叫下一層之前就設好。只有 429 帶 `Retry-After`，403 等其他回應不帶。計數器關閉（`0`）時不送這三個標頭。
- `withIntegrationKeyTouch` 只在 2xx 更新 `last_used_at`，所以 429 不會更新；中介層順序維持「金鑰驗證 → 稽核 → 更新最後使用 → 串接限流 → 權限」。
- 這三個標頭沿用業界常見的三欄位寫法，`Reset` 是距視窗結束的秒數；文件不宣稱對應 IETF 草案（現行草案是 `RateLimit` 與 `RateLimit-Policy` 兩個結構化欄位，之後要加是純增量）。
- 限流一律用 `writeAPIError` 回應；`integrationErrorEnvelope` 的狀態碼對映表與 `errors.go` 對 `/api/v1` 的對映表都補 `429 → rate_limited`，避免外層冒出的 429 變成 `http_429` 或 `internal_error`。
- 金鑰驗證中介層以 `c.Set("integration.auth_failure", code)` 告訴外層失敗原因，失敗來源計數器讀這個旗標而不是嗅狀態碼。
- `rate_limited`（429）加入 `GET /api/v1` 的 `errors.codes` 與 OpenAPI 每個操作的 `429` 回應，429 回應宣告 `Retry-After`、`RateLimit-Limit`、`RateLimit-Remaining`、`RateLimit-Reset` 四個 header（寫法沿用 `internal/auth/api_documentation.go` 既有的 headers 區塊）。

### R5 `GET /api` 總索引

`GET` 與 `HEAD` 都接，不需驗證，回 200：

```json
{
  "name": "Jelly CMS",
  "apis": [
    {"name": "v1", "audience": "machine", "auth": "access_key", "base_path": "/api/v1", "index_url": "/api/v1", "openapi_url": "/api/v1/openapi.json"},
    {"name": "public", "audience": "anonymous", "auth": "none", "base_path": "/api/public", "index_url": "/api/public", "openapi_url": "/api/public/openapi.json"},
    {"name": "shop", "audience": "anonymous", "auth": "none", "base_path": "/api/shop", "index_url": "/api/shop", "openapi_url": "/api/shop/openapi.json"},
    {"name": "auth", "audience": "browser", "auth": "browser_session", "base_path": "/api/auth", "index_url": "/api/auth", "openapi_url": "/api/auth/openapi.json"}
  ],
  "admin_api": {"base_path": "/api/admin", "audience": "admin_ui", "auth": "browser_session", "note": "Private API of the admin screens; use /api/v1 with an access key instead."}
}
```

`auth` 只有 `none`、`access_key`、`browser_session` 三個值；`audience` 只有 `machine`、`anonymous`、`browser`、`admin_ui`。回應帶 `Cache-Control: no-store`。維護模式下照常回 200（`maintenanceBypass` 加 `path == "/api"`）。

## 程式（格子）

互不相依的格子放在不同檔案。既有檔案只改列出的位置。

1. `internal/server/integration_list.go`（新檔）：
   - `type integrationPage struct { Page, PerPage int }`；`func integrationPageParams(c echo.Context) (integrationPage, *integrationParamError)`，錯誤帶 `Param` 名稱；`func (e *integrationParamError) write(c echo.Context) error` 用 `writeAPIError(c, 400, "bad_request", "Invalid "+param, map[string]any{"param": param})`。
   - `func integrationPaged(items any, total int64, p integrationPage) map[string]any`、`func integrationCollection(items any) map[string]any`；nil 切片輸出 `[]`。
   - `type integrationContentSummary struct`（內容摘要的欄位，json tag 與 `core.Post` 相同）、`func integrationContentSummaryFields() []string`（給 OpenAPI 用，順序與 struct 相同）、`func integrationListContent(kind string) echo.HandlerFunc`（R2 的查詢、作者範圍、摘要轉換）。
   - `integrationListCategories`、`integrationListTags`、`integrationListThemes(tm)`、`integrationListPlugins`、`integrationListSettings`：各自呼叫查詢後套集合信封。既有 `listCategories`、`listTags`、`listThemes`、`listPlugins`、`handleSettingsList` 把查詢與回應寫在一起，抽出回傳 `([]T, error)` 的查詢函式，後台 handler 行為不變。
   - `integrationListMediaFiles`：先 `integrationPageParams` 檢查，通過後呼叫既有 `handleMediaFileList`（它會再解析同一組已經合法的值），回應形狀不變。
2. `internal/server/shop_products_admin.go`、`internal/server/shop_orders_admin.go`：把 `adminProductsList`、`adminOrdersList` 拆成查詢函式（輸入篩選條件與 `integrationPage`，輸出 items、total、錯誤）與後台 handler；後台 handler 的參數處理、`counts`、`kpi` 與錯誤格式維持原樣。新檔 `internal/server/integration_catalog.go` 放 `integrationListProducts`、`integrationListOrders`（含 `type integrationOrderSummary struct` 與 `integrationOrderSummaryFields()`），用同一個查詢函式套分頁信封，錯誤用 v1 信封。
3. `internal/server/integration_ratelimit.go`（新檔）：`type integrationRateLimiter`（持有 `cache.Store`、三個上限、`now func() time.Time`）、`newIntegrationRateLimiterFromEnv()`、`func integrationIPBucket(ip string) string`、三個 middleware：`limitAuthFailures`（包在 `integrationKeyMiddleware` 外層，讀 `integration.auth_failure` 旗標）、`limitPerIntegration`（放在 `integrationRequestAudit` 與 `withIntegrationKeyTouch` 之後、`integrationPermissionMiddleware` 之前）、`limitPublic`。套件層變數 `integrationRateLimitNow = time.Now` 供測試注入時鐘。
4. `internal/server/integration_routes.go`：路由表依 R1、R3 換 handler 與刪列；`registerIntegrationRoutes` 建一個 limiter 實例並依格子 3 掛中介層；`/*` 兜底與 `Match("")` 套 `limitAuthFailures` 與 `limitPerIntegration`。`internal/server/integrations_auth.go`：`integrationKeyMiddleware` 在回 401 前 `c.Set("integration.auth_failure", code)`；`integrationErrorEnvelope` 的對映表加 `429: "rate_limited"`。`internal/server/errors.go` 對 `/api/v1` 的對映加 429。
5. `internal/server/integrations_discovery.go`：`integrationSuccessSchema` 依 R1 改（分頁信封、集合信封、摘要與訂單摘要 schema 由格子 1、2 的欄位清單產生）；`integrationQueryParameters` 為 `/posts`、`/pages` 加 `page`、`per_page`、`status`、`q`；`integrationOperationResponses` 每個操作加 `429`（含 headers）；`integrationErrorCodes` 與 `integrationErrorDescription` 加 `rate_limited`；`integrationNotExposed` 加 R3 的兩列；索引加 `pagination` 區塊；刪 R3 列出的死分支。
6. `internal/server/public_api.go`：`GET`／`HEAD /api` 依 R5；`internal/server/maintenance.go` 的 `maintenanceBypass` 加 `path == "/api"`。
7. 文件：`skills/jelly-cms/SKILL.md`（`/logs` 改 `/me/events`；清單是摘要、全文要打單筆；分頁；429 時依 `Retry-After` 等待再重試）；`README.md` 的機器 API 段落補分頁、限流與三個環境變數、拿掉的端點；工單 14 與 `24-api-v1-rename.md` 開頭各加一行指向本工單；工單的驗收紀錄 記錄。不改設計板（沒有畫面變更）。

## 驗收（測試契約）

新測試檔 `internal/server/api_v1_contract_integration_test.go`，沿用 `newAccessHarness`、`agtSetup`、`agtCreate`、`agtDo`、`agtExpectOK`、`agtExpectError`、`agtDecode`、`agtAdmin`。限流上限用 `t.Setenv` 在 `newAccessHarness` 之前設定；時鐘用 `integrationRateLimitNow` 注入並在 `t.Cleanup` 還原。

- 拿掉的端點：帶有 `media.read`、`media.write`、`settings.write`、`logs.read` 的金鑰打 `GET /logs`、`GET /media`、`POST /media`、`PUT /media/x.png`、`DELETE /media/x.png`、`POST /media/rename` 回 404 `not_found`，`POST /settings` 回 405 `method_not_allowed`；不帶金鑰七條都 401 `key_missing`。`GET /api/v1` 的 `endpoints` 與 `/openapi.json` 的 `paths` 不含 `/api/v1/logs`、`/api/v1/media`、`/api/v1/media/{filename}`、`/api/v1/media/rename`；`/api/v1/settings` 只有 `get` 與 `put`。
- 文章清單信封：建立 3 篇文章、1 篇回收桶文章與 1 個頁面；`GET /posts?per_page=2` 回 200，`items` 長度 2、`total` 3、`page` 1、`per_page` 2；每個 item 的鍵集合恰好等於內容摘要的欄位（沒有 `content`、`source`、`author`）；`?page=2&per_page=2` 回 1 筆；`?page=3&per_page=2` 回 200 且 `items` 為 `[]`；不帶參數 `per_page` 為 20；`GET /posts/:id` 仍有 `content`。`page=0`、`page=abc`、`per_page=0`、`per_page=101` 各回 400 `bad_request`、`message` 為 `Invalid page`／`Invalid per_page`、`details.param` 正確；`page=`（空）用預設；`status=draft` 只回草稿、`status=` 不篩選；`q=50%` 當字面值不當萬用字元；未知參數 `foo=1` 被忽略回 200。`GET /pages` 同樣信封且只含頁面。
- 作者範圍：擁有者角色 `author` 的金鑰與 `role_cap = author` 的管理員金鑰，`GET /posts` 的 `items` 與 `total` 只含自己的文章；`GET /pages` 回 200、`items` 為 `[]`、`total` 0。
- 商品與訂單：`GET /products` 回分頁信封且沒有 `counts` 鍵；`GET /orders` 回分頁信封、沒有 `counts`、`kpi` 鍵、有 `page`、`per_page`，每個 item 的鍵集合恰好等於訂單摘要欄位（沒有 `qty`、`total`），`items[].items[]` 有 `quantity` 沒有 `qty`；兩者 `per_page=101` 回 400 `bad_request`。守門：以管理員 Cookie 打 `GET /api/admin/products` 仍有 `counts` 且 `per_page=500` 仍被夾成 100；`GET /api/admin/orders` 仍有 `counts` 與 `kpi`，`per_page=500` 仍回 400 且 body 是 `{"error": ...}`；`GET /api/admin/orders` 的 item 仍有 `qty` 與 `total`。
- 媒體：`GET /media/files?per_page=101` 回 400 `bad_request`（v1）；`GET /api/admin/media/files?per_page=101` 以管理員 Cookie 仍回 200（夾值）。
- 集合信封：空站上 `GET /themes`、`/plugins` 的 `items` 是 `[]` 不是 `null`；`GET /categories`、`/tags`、`/themes`、`/plugins`、`/settings` 回物件，`items` 為陣列、`total` 等於長度；`/settings` 的 `items` 內仍沒有明文密鑰（沿用既有遮罩斷言）。
- OpenAPI：五個集合端點與五個分頁端點的 `get` 200 schema 都是 `type: object` 且 `properties.items` 存在；`/posts`、`/pages` 的 `get` 有 `page`、`per_page`、`status`、`q` 四個 query 參數，200 的 `properties.items.items.properties` 的鍵集合等於內容摘要欄位，而 `/posts/{id}` 的 `get` 200 有 `content`；`/orders` 的 `properties.items.items.properties` 鍵集合等於訂單摘要欄位；所有操作都有 `responses.429` 且宣告四個 header；`GET /api/v1` 的 `errors.codes` 含 `rate_limited`／429，索引有 `pagination` 區塊。
- 串接限流：`JELLY_API_RATE_LIMIT_PER_INTEGRATION=5`，同一把金鑰連打 `GET /me` 5 次都 200 且 `RateLimit-Remaining` 從 4 降到 0，第 6 次 429 `rate_limited`、`details.scope = "integration"`、`Retry-After` 與 `RateLimit-Reset` 相等且為 1 到 60 的整數、`RateLimit-Remaining` 為 0；同一串接過渡期的另一把金鑰共用計數（輪替後第一次請求就 429）；另一個串接的金鑰同一時間打仍 200；`integration_events` 有該串接 `status = 429` 的紀錄；429 後 `last_used_at` 沒有更新；沒有 `posts.read` 的金鑰超限時回 429 而不是 403；403 `permission_missing` 的回應也帶三個 `RateLimit-*` 標頭；`/*` 兜底的未知路徑計入串接計數；時鐘撥到下一分鐘後計數歸零。`=0` 時打 20 次都 200 且沒有 `RateLimit-*` 標頭；`=abc` 時用預設 600。
- 驗證失敗來源限流：`JELLY_API_RATE_LIMIT_AUTH_FAILURES_PER_IP=3`，同一 IP 用亂碼金鑰打 `GET /me` 3 次都 401 `key_invalid`，第 4 次 429 `details.scope = "auth_failures"`；同一 IP 改用有效金鑰仍 200；同一 IP 用已撤銷的金鑰打 10 次都 401 `key_revoked`（不計數、不被擋）；不帶金鑰打 10 次都 401 `key_missing`；同一 /64 的另一個 IPv6 位址共用計數；不同 IPv4 不受影響；`JELLY_TRUSTED_PROXIES` 未設時 `X-Forwarded-For` 不改變分桶（沿用 `security_fixes_integration_test.go` 的寫法）。`=0` 時打 10 次亂碼金鑰都 401。
- 公開端點限流：`JELLY_API_RATE_LIMIT_PUBLIC_PER_IP=2`，同一 IP 第 3 次 `GET /api/v1` 回 429 `details.scope = "public"`，`GET /api/v1/openapi.json` 與它共用計數；429 經過信封後 `code` 仍是 `rate_limited`。
- `GET /api`：`GET` 與 `HEAD` 都回 200、`Cache-Control: no-store`，`apis` 恰好四筆且 `name` 依序為 `v1`、`public`、`shop`、`auth`，每筆 `auth` 與 `audience` 在枚舉內，`index_url` 與 `openapi_url` 各打一次都回 200；維護模式開啟時仍 200。
- 既有測試：`integrations_api_integration_test.go` 的 `mustList["logs.read"]` 改為 `GET /api/v1/me/events`，所有 `/api/v1/logs` 改 `/api/v1/me/events`；`integrations_regression_test.go` 同理；`agtDecode` 解析 `/api/v1/settings` 成 `[]core.Option` 的地方（`integrations_api_integration_test.go` 與 `integrations_regression_test.go` 各一處）改為先解析 `{items: []core.Option}`；`integrations_regression_test.go` 對 `/posts`、`/pages` GET 200 schema 取 `items.properties` 的斷言改為 `properties.items.items.properties`，對 `/api/v1/themes` 的斷言改為物件信封且 `properties.items.items.type == "string"`；其餘斷言不得放寬。`integrations_api_integration_test.go` 中以 `strings.Contains(list.body, 標題)` 檢查清單的斷言維持不變。

## 不在本工單

- 匿名前台 API（`/api/public`、`/api/shop`、`/api/cart`、`/api/checkout`、`/api/orders`、`/api/comments`、`/api/search`）收攏到同一前綴與同一錯誤信封。
- `/api/account/*` 與 `/api/auth/me` 停收 Bearer JWT。
- 機器 API 補留言、分類標籤寫入、訂單狀態、折扣碼、選單、轉址、使用者、備份端點。
- 限流計數器改用 Redis 跨程序共用（要先確認 `internal/cache/redis.go` 的 `Incr` 是否每次刷新 TTL；若會，固定視窗會變成滑動視窗）；每個串接自訂上限；改送 IETF `RateLimit`／`RateLimit-Policy` 結構化標頭。
- `/me/events` 的 `logs_filter_invalid` 補 `details.param`。
- 文章清單的 `fields` 參數。
- 刪除 `api_tokens` 資料表與內部改名。

## 決策紀錄

- 清單 items 用正向列出欄位的摘要而不是「資源扣掉 content／source」：扣除法會讓 `core.Post` 之後新增的任何欄位自動漏進對外契約，而且現況的 `author` 是沒 preload 的空物件。全文清單在文章多時一次拉完會撐爆回應，AI agent 多半先列再挑；單筆端點保留全文。否決「加 `fields` 參數」：之後加是純增量，先不做。
- 訂單清單定自己的摘要：後台 item 的 `qty`／`quantity`、`total`／`total_amount` 是給舊畫面的相容欄位，放進 v1 就得背三年。
- `/me/events` 保留游標分頁：事件是追加式紀錄，游標在新事件持續寫入時不會漏筆或重複；其他清單用頁碼，因為後台查詢已經是頁碼，客戶端也最容易理解。否決把 `limit` 改名 `per_page`：會動既有 `/me/events` 契約。
- `status` 不設白名單：寫入端對 `status` 不驗證，白名單只是假保證；`private` 是 `visibility` 的值，初案列錯。
- 作者的 `GET /pages` 回空集合：`getPost` 對作者強制 `type = post`，單筆本來就 404；作者也不能建頁面，放寬沒有對象。
- 拿掉 `/logs` 留 `/me/events`：`/me/*` 明確表示「這把金鑰自己的」，與 `/me` 一致。
- 拿掉檔名式 `/media*`：它直接讀寫 `data/uploads` 目錄、沒有資料列與權限邊界，`/media/files*` 才是附件庫的正式介面。
- `POST /settings` 拿掉留 `PUT`：寫入整組鍵值是取代語意。
- 限流以串接為單位而不是每把金鑰：過渡期兩把金鑰不會變成雙倍額度，輪替也不會歸零。
- 計數鍵不帶視窗戳：帶戳的鍵在下一分鐘不再被存取，memory store 只在存取時清過期項目，會每分鐘留一筆永久殘留；改成 TTL 等於視窗剩餘秒數，項目自然到期。
- 驗證失敗來源超限時只擋查不到的金鑰、不擋查得到的：預設沒有信任代理時，反向代理後所有客戶端的 `RealIP()` 都是代理 IP，若超限連有效金鑰都擋，任何人每分鐘丟 60 個亂碼就能讓整站機器 API 停擺；金鑰是 256 位元隨機值，線上猜測不可行，這個計數器實際保護的是索引查詢與日誌量，所以多做一次雜湊查詢換零誤傷是划算的。只計 `key_invalid`：撤銷、過期、停用的 401 典型來源是輪替後還在重試舊金鑰的合法客戶端。
- IPv6 以 /64 分桶：一個 /64 有 2^64 個位址，整址分桶形同沒有上限。
- 限流用固定視窗而不是滑動視窗：`cache.Store` 只有 `Incr`，固定視窗一個計數器就夠，邊界 2 倍突發對每分鐘 600 次的上限可接受。
- `RateLimit-*` 三欄位而不是 IETF 草案的結構化欄位：客戶端函式庫對三欄位支援最廣，草案未定稿；之後要加 `RateLimit`／`RateLimit-Policy` 是純增量。文件不宣稱對應 IETF。
- 429 放在稽核之後：超限的請求仍會寫兩筆交易，600／分的量可接受，換來站長在活動紀錄看得到被擋的請求。
- 限流預設值：600／分是單一 agent 正常使用的數倍；需要更高的站台用環境變數調整，之後再做每個串接自訂。

## 驗收紀錄

交付時記下的驗證結果與裁決，依時間由新到舊。

- 工作單 27：機器 API `/api/v1` 有自己的契約，不再直接掛後台 handler。清單一律回物件：文章、頁面、商品、訂單、`media/files` 用 `{items,total,page,per_page}`（`page` 預設 1、最大 1,000,000；`per_page` 預設 20、最大 100；不合法回 400 `bad_request` 加 `details.param`），分類、標籤、主題、外掛、設定用 `{items,total}`，`/me/events` 維持游標。文章與頁面清單是固定欄位的摘要（沒有 `content`、`source`，也沒有原本沒 preload 的空 `author`），支援 `status` 與 `q`；訂單清單是自己的摘要，沒有後台畫面的 `counts`、`kpi` 與 `qty`／`total` 重複鍵。拿掉 `GET /logs`、檔名式 `/media*` 五條與 `POST /settings`（回 404／405，不留別名）。限流三個計數器：每個串接每分鐘 600 次、金鑰驗證失敗每個來源 IP 60 次（超限只擋查不到的金鑰，有效金鑰不受影響，IPv6 以 /64 分桶）、公開說明端點每 IP 120 次；環境變數 `JELLY_API_RATE_LIMIT_PER_INTEGRATION`、`JELLY_API_RATE_LIMIT_AUTH_FAILURES_PER_IP`、`JELLY_API_RATE_LIMIT_PUBLIC_PER_IP`，`0` 關閉；429 回 `rate_limited` 並帶 `Retry-After` 與 `RateLimit-*`，計數器存程序內記憶體快取。新增 `GET /api` 總索引列出四套 API 與後台私有 API 的用途。後台 `/api/admin/*` 的回應與參數處理不變，由守門測試鎖住。驗證：主代理重跑 `gofmt`、`go vet ./...`、`JELLY_TEST_POSTGRES=1 go test ./... -count=1 -timeout 45m`：server 套件（707 秒）等全部通過，`internal/plugin` 的 `TestRuntimeCallsPluginHooksAndPreservesThemOnFailedReload` 在併入 #46 的修正前並行時偶發失敗一次（本次沒改該套件，單獨重跑 5 次與整個套件重跑都通過；併入 main 後該套件以 `-race` 重跑通過），新測試 34 個全過（含 `GET /api` 四個 `openapi_url` 各實際打過一次）；沒有畫面變更，沒有部署。設計方案先經獨立模型審查後改定（詳見工單決策紀錄）。
