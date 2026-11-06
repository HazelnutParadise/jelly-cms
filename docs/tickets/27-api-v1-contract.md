# 27 — 機器 API 有自己的契約：清單統一分頁信封、拿掉重複端點、每把金鑰限流、`/api` 總索引

Epic：帳號與存取
User Story：程式與 AI agent 用存取金鑰打 `/api/v1` 時，拿到的回應形狀由機器 API 自己決定，不再跟著後台畫面的 handler 走。每個清單端點都用同一種分頁信封；只有一條路做同一件事；一把金鑰每分鐘有上限，猜金鑰的來源會被擋；`GET /api` 一眼看到這個站有哪幾套 API、各自給誰用、文件在哪。
Status：待實作。
Blocked by：24（`/api/v1` 改名，已合併）。專案沒有任何已部署站台或版本標籤，所以拿掉的端點直接回 404，不留別名。

## 背景與決定

- 現況 `/api/v1` 的路由表（`internal/server/integration_routes.go`）直接掛後台 handler：`GET /posts`、`GET /pages` 用 `listPosts`，回整包陣列、不分頁、每篇都帶 `content` 與 `source`；`GET /products` 回 `{items,total,counts,page,per_page}`，`GET /orders` 回 `{items,total,counts,kpi}`，`counts` 與 `kpi` 是後台列表畫面的統計；`/me/events` 用 `next_cursor`。同一個 v1 裡有三種清單形狀，而且改後台畫面會悄悄改到外部契約。
- 現況 v1 同時放出 `GET /logs` 與 `GET /me/events`（同一個 handler）、舊版檔名式媒體 `/media*` 與新版 `/media/files*` 兩套、`POST /settings` 與 `PUT /settings` 兩種方法。
- 現況 `/api/v1` 沒有任何限流；登入、留言、購物車都有。
- 決定：v1 的回應形狀由 v1 自己的 handler 決定。清單一律是物件，不再回裸陣列。後台 `/api/admin/*` 的回應完全不變。
- 決定：拿掉重複端點，只留 `/me/events`、`/media/files*`、`PUT /settings`。
- 決定：限流以金鑰為單位計數，另對驗證失敗的來源 IP 計數。計數器先用程序內記憶體（`internal/cache` 的 memory store），之後接 Redis 共用計數另案。
- 決定：新增 `GET /api` 總索引，只列目前四套有說明端點的 API 與後台私有 API 的用途，不改任何既有端點。
- 不改：金鑰格式、權限字串、既有錯誤碼、稽核（`integration_events`）、`/api/admin/*`、`/api/public/*`、`/api/shop/*`、`/api/auth/*`、`/api/account/*` 的行為與錯誤格式。

## 名詞

| 名詞 | 定義 |
|---|---|
| 分頁信封 | `{"items": [...], "total": N, "page": P, "per_page": S}`，`total` 是符合條件的總筆數，`items` 是本頁 |
| 集合信封 | `{"items": [...], "total": N}`，用在不分頁的小集合 |
| 事件信封 | `{"items": [...], "next_cursor": "..."|null, "total": N}`，只用在 `/me/events`（保留游標分頁，因為是追加式紀錄） |
| 摘要 | 文章／頁面資源去掉 `content` 與 `source` 兩個欄位後的物件；其餘欄位與單筆端點相同 |

## 規則

### R1 清單信封

| 端點 | 之前 | 之後 |
|---|---|---|
| `GET /posts`、`GET /pages` | 裸陣列、不分頁、含全文 | 分頁信封，`items` 為摘要 |
| `GET /products` | `{items,total,counts,page,per_page}` | 分頁信封（移除 `counts`） |
| `GET /orders` | `{items,total,counts,kpi}` | 分頁信封（移除 `counts`、`kpi`；加 `page`、`per_page`） |
| `GET /media/files` | 分頁信封 | 不變 |
| `GET /categories`、`GET /tags`、`GET /themes`、`GET /plugins`、`GET /settings` | 裸陣列 | 集合信封，`items` 內每個元素的形狀不變（`/themes` 仍是字串） |
| `GET /me/events` | 事件信封 | 不變 |

`GET /posts/:id`、`GET /pages/:id` 與寫入端點的回應不變，仍含 `content` 與 `source`。

### R2 分頁參數

`GET /posts`、`GET /pages`、`GET /products`、`GET /orders`、`GET /media/files` 共用：`page` 預設 1、最大 1,000,000；`per_page` 預設 20、最大 100。非整數、小於 1 或超過上限一律 400 `bad_request`，`details` 為 `{"param": "page"}` 或 `{"param": "per_page"}`。`/products`、`/orders` 原本把超出範圍的 `per_page` 夾到 100、把空白或小於 1 的 `page` 當 1，改為與本規則一致（空白仍用預設值）。

`GET /posts`、`GET /pages` 另支援 `status`（精確比對；文章狀態目前只有 `draft` 與 `published`，其他值回 400 `bad_request`，`details.param = "status"`）與 `q`（標題不分大小寫包含）。排序固定 `created_at DESC, id DESC`。擁有者有效角色為 `author` 時，文章與頁面都只回自己寫的（`author_id` 等於擁有者），`total` 同樣只算自己的。

### R3 拿掉的端點

`GET /logs`、`GET /media`、`POST /media`、`PUT /media/:filename`、`DELETE /media/:filename`、`POST /media/rename`、`POST /settings` 從路由表移除。打這些路徑一律走既有的 `/*` 兜底：帶有效金鑰回 404 `not_found`，不帶金鑰回 401。`GET /api/v1` 的 `endpoints` 與 `/openapi.json` 的 `paths` 不再出現這些路徑；`not_exposed` 新增兩列：`/api/admin/media`（檔名式媒體）指向 `/api/v1/media/files`，`/api/admin/logs*`（系統日誌）說明串接只能讀自己的 `/me/events`。

### R4 限流

三個計數器，都是固定 60 秒視窗，窗內超過上限回 429：

| 計數器 | 範圍 | 預設上限 | 環境變數 |
|---|---|---|---|
| 每把金鑰 | 所有需要金鑰的路由，驗證通過後計數 | 600 | `JELLY_API_RATE_LIMIT_PER_KEY` |
| 驗證失敗來源 | 每個來源 IP，金鑰驗證回 401 時計數；達上限後該 IP 在窗內所有需要金鑰的請求都先回 429，不查資料庫 | 60 | `JELLY_API_RATE_LIMIT_FAILED_AUTH_PER_IP` |
| 公開說明端點 | `GET /api/v1`、`GET /api/v1/openapi.json`，每個來源 IP | 120 | `JELLY_API_RATE_LIMIT_PUBLIC_PER_IP` |

環境變數是每分鐘次數的整數，`0` 關閉該計數器，無法解析時用預設值。計數器存在 `internal/cache` 的 memory store，鍵為 `ratelimit` 命名空間下的 `key:<key_id>:<window_start_unix>`、`ip:<ip>:<window_start_unix>`、`public:<ip>:<window_start_unix>`，TTL 為該視窗剩餘秒數。

429 的回應：錯誤信封 `code = "rate_limited"`、`message = "Rate limit exceeded"`、`details = {"scope": "key"|"ip", "limit": N, "window_seconds": 60, "retry_after": 秒}`；標頭 `Retry-After`（秒，整數）、`RateLimit-Limit`、`RateLimit-Remaining`（0）、`RateLimit-Reset`（距視窗結束的秒數）。每把金鑰計數器的路由在 2xx 與 4xx 回應也帶 `RateLimit-Limit`、`RateLimit-Remaining`、`RateLimit-Reset`。每把金鑰的 429 照常寫一筆稽核（`status = 429`）；驗證失敗來源與公開端點的 429 不寫稽核（前者沒有金鑰身分，後者本來就不稽核）。`rate_limited` 加入 `GET /api/v1` 的 `errors.codes`（status 429）與 OpenAPI 每個操作的 `429` 回應。

### R5 `GET /api` 總索引

不需驗證，回 200：

```json
{
  "name": "Jelly CMS",
  "apis": [
    {"name": "v1", "audience": "machine", "auth": "access_key", "base_path": "/api/v1", "index_url": "/api/v1", "openapi_url": "/api/v1/openapi.json"},
    {"name": "public", "audience": "anonymous", "auth": "none", "base_path": "/api/public", "index_url": "/api/public", "openapi_url": "/api/public/openapi.json"},
    {"name": "shop", "audience": "anonymous", "auth": "none", "base_path": "/api/shop", "index_url": "/api/shop", "openapi_url": "/api/shop/openapi.json"},
    {"name": "auth", "audience": "browser", "auth": "session", "base_path": "/api/auth", "index_url": "/api/auth", "openapi_url": "/api/auth/openapi.json"}
  ],
  "admin_api": {"base_path": "/api/admin", "audience": "admin_ui", "auth": "browser_session", "note": "Private API of the admin screens; use /api/v1 with an access key instead."}
}
```

回應帶 `Cache-Control: no-store`。維護模式下照常回 200（加入 `maintenance.go` 的略過清單）。

## 程式（格子）

互不相依的格子放在不同檔案。既有檔案只改列出的位置。

1. `internal/server/integration_list.go`（新檔）：
   - `type integrationPage struct { Page, PerPage int }`；`func integrationPageParams(c echo.Context) (integrationPage, error)`，錯誤型別帶 `param` 名稱，呼叫端用 `writeAPIError(c, 400, "bad_request", "Invalid "+param, map[string]any{"param": param})` 回應。
   - `func integrationPaged(items any, total int64, p integrationPage) map[string]any` 與 `func integrationCollection(items any, total int) map[string]any` 組信封；`items` 為 nil 時輸出 `[]` 不輸出 `null`。
   - `func integrationListContent(kind string) echo.HandlerFunc`：R2 的查詢、作者範圍、摘要轉換（`core.Post` 轉成 `map[string]any` 後刪 `content`、`source`，或用只含摘要欄位的 struct，兩者擇一但 OpenAPI 的摘要 schema 必須與實際輸出欄位一致）。
   - `func integrationListCategories`、`integrationListTags`、`integrationListThemes(tm)`、`integrationListPlugins`、`integrationListSettings`：各自呼叫既有 handler 的查詢邏輯後套集合信封。既有的 `listCategories`、`listTags`、`listThemes`、`listPlugins`、`handleSettingsList` 若把查詢與回應寫在一起，抽出回傳 `([]T, error)` 的查詢函式，後台 handler 行為不變。
2. `internal/server/shop_products_admin.go`、`internal/server/shop_orders_admin.go`：把 `adminProductsList`、`adminOrdersList` 拆成查詢函式（輸入 `echo.Context`，輸出 `items`、`total`、`integrationPage`、錯誤）與後台 handler；後台 handler 繼續加 `counts`、`kpi` 與原本的錯誤格式。新檔 `internal/server/integration_catalog.go` 放 `integrationListProducts`、`integrationListOrders`，用同一個查詢函式套分頁信封，錯誤用 v1 信封。後台 handler 的參數處理維持原行為（夾值），v1 的依 R2 嚴格檢查。
3. `internal/server/integration_ratelimit.go`（新檔）：`type integrationRateLimiter`（持有 `cache.Store`、三個上限、時間函式可注入）、`newIntegrationRateLimiterFromEnv()`、三個 middleware：`limitFailedAuth`（包在 `integrationKeyMiddleware` 外層）、`limitPerKey`（放在 `integrationRequestAudit` 之後、權限檢查之前）、`limitPublic`（公開兩條路由）。標頭與 429 格式依 R4。
4. `internal/server/integration_routes.go`：路由表依 R1、R3 換 handler 與刪列；`registerIntegrationRoutes` 建一個 limiter 實例並依格子 3 掛中介層；`/*` 兜底與 `""` 的 Match 也套 `limitFailedAuth`。
5. `internal/server/integrations_discovery.go`：`integrationSuccessSchema` 依 R1 改（分頁信封、集合信封、摘要 schema）；`integrationQueryParameters` 為 `/posts`、`/pages` 加 `page`、`per_page`、`status`、`q`；`integrationOperationResponses` 每個操作加 `429`；`integrationErrorCodes` 加 `rate_limited`；`integrationErrorDescription` 加對應文字；`integrationNotExposed` 加 R3 的兩列。
6. `internal/server/public_api.go`：`GET /api` 依 R5；`internal/server/maintenance.go` 略過清單加 `/api`。
7. 文件：`skills/jelly-cms/SKILL.md`（`/logs` 改 `/me/events`、說明清單信封、分頁與 429 的處理方式）；`README.md` 的機器 API 段落補分頁、限流與環境變數、拿掉的端點；`docs/tickets/14-agent-api.md` 與 `24-api-v1-rename.md` 開頭各加一行指向本工單；`STATUS.md` 記錄。不改設計板（沒有畫面變更）。

## 驗收（測試契約）

新測試檔 `internal/server/api_v1_contract_integration_test.go`，沿用 `newAccessHarness`、`agtSetup`、`agtCreate`、`agtDo`、`agtExpectOK`、`agtExpectError`、`agtDecode`。限流上限用 `t.Setenv` 在 `newAccessHarness` 之前設定。

- 拿掉的端點：帶有 `media.read`、`media.write`、`settings.write`、`logs.read` 的金鑰打 R3 的七條路徑，一律 404 `not_found`；不帶金鑰一律 401 `key_missing`。`GET /api/v1` 的 `endpoints` 與 `/openapi.json` 的 `paths` 不含 `/api/v1/logs`、`/api/v1/media`、`/api/v1/media/{filename}`、`/api/v1/media/rename`；`/api/v1/settings` 只有 `get` 與 `put`。
- 文章清單信封：建立 3 篇文章與 1 個頁面；`GET /posts?per_page=2` 回 200，`items` 長度 2、`total` 3、`page` 1、`per_page` 2，每個 item 沒有 `content` 與 `source` 鍵但有 `id`、`title`、`slug`、`status`、`updated_at`；`?page=2&per_page=2` 回 1 筆；不帶參數 `per_page` 為 20；`GET /posts/:id` 的回應仍有 `content`。`page=0`、`page=abc`、`per_page=0`、`per_page=101` 各回 400 `bad_request` 且 `details.param` 正確；`status=bogus` 回 400 `details.param = "status"`；`status=draft` 只回草稿；`q=` 以標題關鍵字篩選。`GET /pages` 同樣信封且只含頁面。
- 作者範圍：擁有者角色 `author` 的金鑰，`GET /posts` 的 `items` 與 `total` 只含自己的文章。
- 商品與訂單：`GET /products` 回分頁信封且沒有 `counts` 鍵；`GET /orders` 回分頁信封且沒有 `counts`、`kpi` 鍵，有 `page`、`per_page`；`per_page=101` 回 400 `bad_request`。守門：以管理員 Cookie 打 `GET /api/admin/products` 仍有 `counts`，`GET /api/admin/orders` 仍有 `counts` 與 `kpi`，且 `per_page=500` 仍被夾成 100 而不是 400。
- 集合信封：`GET /categories`、`/tags`、`/themes`、`/plugins`、`/settings` 回物件，`items` 為陣列、`total` 等於長度；`/settings` 的 `items` 內仍沒有明文密鑰（沿用既有遮罩斷言）。
- OpenAPI：五個集合端點與五個分頁端點的 `get` 200 schema 都是 `type: object` 且 `properties.items` 存在；`/posts`、`/pages` 的 `get` 有 `page`、`per_page`、`status`、`q` 四個 query 參數，200 的 `items.items.properties` 沒有 `content`、`source`，而 `/posts/{id}` 的 `get` 200 有 `content`；所有操作都有 `responses.429`；`GET /api/v1` 的 `errors.codes` 含 `rate_limited`／429。
- 每把金鑰限流：`JELLY_API_RATE_LIMIT_PER_KEY=5`，同一把金鑰連打 `GET /me` 5 次都 200 且 `RateLimit-Remaining` 從 4 降到 0，第 6 次 429 `rate_limited`、`details.scope = "key"`、`Retry-After` 為 1 到 60 的整數、`RateLimit-Remaining` 為 0；另一把金鑰同一時間打仍 200；`integration_events` 有一筆該金鑰 `status = 429` 的紀錄。
- 驗證失敗來源限流：`JELLY_API_RATE_LIMIT_FAILED_AUTH_PER_IP=3`，同一個 IP 用錯誤金鑰打 `GET /me` 3 次都 401，第 4 次 429 `details.scope = "ip"`；同一個 IP 改用有效金鑰也 429；不同 IP 用有效金鑰 200。`JELLY_API_RATE_LIMIT_FAILED_AUTH_PER_IP=0` 時打 10 次錯誤金鑰都 401。
- 公開端點限流：`JELLY_API_RATE_LIMIT_PUBLIC_PER_IP=2`，同一 IP 第 3 次 `GET /api/v1` 回 429，`GET /api/v1/openapi.json` 與它共用計數。
- `GET /api`：回 200、`Cache-Control: no-store`，`apis` 恰好四筆且 `name` 依序為 `v1`、`public`、`shop`、`auth`，每筆的 `index_url` 與 `openapi_url` 都真的存在（各打一次回 200）；維護模式開啟時仍 200。
- 既有測試：`integrations_api_integration_test.go` 的 `mustList["logs.read"]` 改為 `GET /api/v1/me/events`，所有 `/api/v1/logs` 改 `/api/v1/me/events`；`integrations_regression_test.go` 同理；`agtDecode` 解析 `/api/v1/settings` 的地方改為先解析 `{items: []core.Option}`；`integrations_regression_test.go` 的 `responseSchema("/api/v1/themes", "get")` 斷言改為物件信封且 `properties.items.items.type == "string"`；其餘斷言不得放寬。`integrations_api_integration_test.go` 中以 `strings.Contains(list.body, 標題)` 檢查清單的斷言維持不變（摘要仍含標題）。

## 不在本工單

- 匿名前台 API（`/api/public`、`/api/shop`、`/api/cart`、`/api/checkout`、`/api/orders`、`/api/comments`、`/api/search`）收攏到同一前綴與同一錯誤信封。
- `/api/account/*` 與 `/api/auth/me` 停收 Bearer JWT。
- 機器 API 補留言、分類標籤寫入、訂單狀態、折扣碼、選單、轉址、使用者、備份端點。
- 限流計數器改用 Redis 跨程序共用；每把金鑰自訂上限。
- 刪除 `api_tokens` 資料表與內部改名。

## 決策紀錄

- 清單 items 用摘要而不是全文：全文清單在文章多時一次拉完會撐爆回應，且 AI agent 多半先列再挑；單筆端點保留全文。否決「加 `fields` 參數」：多一個維度要文件與測試，先不做。
- `/me/events` 保留游標分頁：事件是追加式紀錄，游標在新事件持續寫入時不會漏筆或重複；其他清單用頁碼，因為後台 handler 已經是頁碼，客戶端也最容易理解。
- 拿掉 `/logs` 留 `/me/events`：`/me/*` 明確表示「這把金鑰自己的」，與 `/me` 一致。
- 拿掉檔名式 `/media*`：它直接讀寫 `data/uploads` 目錄、沒有資料列與權限邊界，`/media/files*` 才是附件庫的正式介面。
- `POST /settings` 拿掉留 `PUT`：寫入整組鍵值是取代語意。
- 限流用固定視窗而不是滑動視窗：`cache.Store` 只有 `Incr`，固定視窗一個計數器就夠，邊界抖動對每分鐘 600 次的上限影響可接受。
- 驗證失敗來源達上限後連有效金鑰也擋：這是防猜金鑰的保護，正常客戶端不會在一分鐘內出現 60 次 401。
- 限流預設值：600／分是單一 agent 正常使用的數倍；需要更高的站台用環境變數調整，之後再做每把金鑰自訂。
