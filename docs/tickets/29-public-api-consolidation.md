# 29 — 匿名 API 只有一個前綴、一份文件、一種錯誤格式

Epic：外觀與擴充
User Story：做主題、前台 SPA 或 WebMCP 工具的人，只要看 `GET /api/public` 與 `/api/public/openapi.json` 就能找到所有不用登入的端點；每個錯誤都長一樣，用 `code` 分流、用 `correlation_id` 回報；搬家後舊路徑一個版本內還能用。
Status：待實作。
Blocked by：27（`/api/v1` 契約與 `GET /api` 總索引，已合併）。

## 背景與決定

現況（行號以 main `0dc56d9` 為準）：

- 不用登入的 JSON 端點散在七個前綴：`/api/public`（3 條）、`/api/search`、`/api/comments`（2 條）、`/api/cart*`（6 條）、`/api/checkout*`（3 條）、`/api/orders/*`（3 條）、`/api/shop/*-methods`（2 條），另有 `/api/oauth/status`、`/api/invites/accept`、`/api/password-reset` 三條屬於帳號流程。
- 錯誤格式有三種：`/api/public` 的 `publicAPIError` 回 `{code, message, correlation_id}`（`public_api.go:25-37`）；商店的 `shopJSONError` 回 `{code, error}`（`shop_cart.go:794-796`）；留言、搜尋與所有 `echo.NewHTTPError` 經全域錯誤處理回 `{error, code: 數字}`（`errors.go:85-87`），其中 500 的訊息一律被換成 `Internal Server Error`（`errors.go:51-57`）。
- 說明文件有兩份：`/api/public/openapi.json`（3 條）與 `/api/shop/openapi.json`（29 條，混了匿名、`/api/account/*` 與金流回呼）。`/api/search`、`/api/comments`、`/api/oauth/status` 不在任何一份裡。
- 清單形狀不一：`/api/shop/shipping-methods` 與 `payment-methods` 回裸陣列（`shop_options.go:195,203`），其他回物件。
- 呼叫端只有三種：預設主題的 `web/themes/default/assets/shop.js`（只讀 `data.code` 與 HTTP 狀態碼，不讀訊息文字；`messageFor` 在 `:273-287`）、`web/themes/default/_comments.html:174`（只看狀態碼）、WebMCP 內建四個工具（`internal/server/webmcp.go:256-259`，指向 `/api/search` 與 `/api/public/*`，`webmcp-tools.json` 由伺服器動態產生）。
- 決定：所有不用登入的內容與商店端點搬到 `/api/public/` 底下，一份 OpenAPI，一種錯誤格式（與 `/api/v1` 相同的信封）。`/api/oauth/status`、`/api/invites/accept`、`/api/password-reset` 屬於帳號流程，路徑不動，但 `/api/oauth/status` 補進 `/api/auth/openapi.json`。
- 決定：舊路徑回 308 轉到新路徑，保留到下一個次版本，再由新工單移除。308 保留方法與 body，`fetch` 預設會跟隨；WebMCP 的 `webmcp.js` 用 `redirect: "error"`，但它的工具清單由伺服器產生，直接改成新路徑就不會碰到轉址。
- 決定：錯誤 `code` 字串沿用現有值（`input`、`stock`、`coupon`、`cart_unavailable`、`origin`、`idempotency_conflict`、`not_found`、`query_too_long` 等），只把 `rate_limit` 改成 `rate_limited`（與 `/api/v1` 一致；主題 JS 對 429 只看狀態碼，沒有讀這個值）。
- 不改：各端點的業務規則、來源檢查、限流次數、購物車 Cookie、維護模式下匿名 API 回 503 的行為、`/api/v1`、`/api/admin/*`、`/api/account/*`、`/api/auth/*`、金流回呼 `/api/payment/:gateway/*`。

## 名詞

| 名詞 | 定義 |
|---|---|
| 匿名 API | 不需登入、不需存取金鑰就能呼叫的 JSON 端點 |
| 公開錯誤信封 | `{"code": "...", "message": "...", "details": {...}, "correlation_id": "req_..."}`，與工單 27 的 `writeAPIError` 相同；`details` 永遠是物件，欄位錯誤放 `details.fields`（鍵為欄位名、值為訊息） |
| 集合信封 | `{"items": [...], "total": N}` |

## 規則

### R1 路徑

| 之前 | 之後 |
|---|---|
| `GET /api/public`、`/api/public/openapi.json`、`/api/public/posts`、`/api/public/content/:slug`、`/api/public/site` | 不變 |
| `GET /api/search` | `GET /api/public/search` |
| `GET /api/comments`、`POST /api/comments` | `GET /api/public/comments`、`POST /api/public/comments` |
| `GET /api/cart`、`POST /api/cart/items`、`PATCH|DELETE /api/cart/items/:variant_id`、`POST|DELETE /api/cart/coupon` | `/api/public/cart`、`/api/public/cart/items`、`/api/public/cart/items/:variant_id`、`/api/public/cart/coupon` |
| `POST /api/checkout`、`POST /api/checkout/retry`、`POST /api/checkout/sandbox/:trade_no` | `/api/public/checkout`、`/api/public/checkout/retry`、`/api/public/checkout/sandbox/:trade_no` |
| `POST /api/orders/lookup`、`GET /api/orders/:number/status`、`POST /api/orders/:number/resend` | `/api/public/orders/lookup`、`/api/public/orders/:number/status`、`/api/public/orders/:number/resend` |
| `GET /api/shop/shipping-methods`、`GET /api/shop/payment-methods` | `GET /api/public/shop/shipping-methods`、`GET /api/public/shop/payment-methods` |
| `GET /api/shop`、`GET /api/shop/openapi.json` | 併入 `GET /api/public` 與 `/api/public/openapi.json` |

每條舊路徑（含 `/api/shop` 兩條說明端點）回 `308 Permanent Redirect`，`Location` 為對應新路徑並保留查詢字串與路徑參數；body 為公開錯誤信封 `code = "moved"`、`details.location` 同 `Location`。`webmcp.go` 的 `webMCPPrivilegedPath` 不變。

### R2 錯誤信封

`/api/public/**` 的所有錯誤（handler 自己回的、`echo.NewHTTPError` 經全域錯誤處理的、維護模式的 503、安裝閘門的 503）一律公開錯誤信封。`errors.go` 的 `/api/v1` 分支擴成「`/api/v1` 或 `/api/public` 子樹」；`maintenance.go` 對 `/api/public` 子樹的 503 也用它（`code = "maintenance"`，保留 `Retry-After`）。500 的 `message` 仍為固定文字 `Internal server error`，不洩漏內部訊息。

商店 handler 的 `shopJSONError`、留言的 `{"error": ...}`、搜尋的 `{"error": ...}` 與 `{"code": "query_too_long", "error": ...}` 全部改用 `writeAPIError`；`code` 對照：

| 現況 | 之後 |
|---|---|
| `shopJSONError(c, status, code, msg)` | `writeAPIError(c, status, code, msg, nil)`；`code` 字串不變，`rate_limit` 改 `rate_limited` |
| 結帳 400 的欄位錯誤（`input` 加各欄位） | `code = "input"`，`details.fields = {"email": "...", ...}`；`shop.js` 的 `serverFieldErrors` 改讀 `details.fields` |
| 留言 `{"error": "Too many comments, try again later"}` 429 | `code = "rate_limited"`，加 `Retry-After` |
| 留言其餘 400/401/403/404/500 | `code` 依序 `input`、`login_required`、`comments_disabled`／`comments_closed`、`not_found`、`internal_error` |
| 搜尋 `q` 缺少 | `code = "input"`，`details.param = "q"` |
| 搜尋 `query_too_long` | 不變 |
| `echo.NewHTTPError(404)` | `not_found`；`echo.NewHTTPError(500, ...)` → `internal_error` |

### R3 清單與文件

- `GET /api/public/shop/shipping-methods`、`payment-methods` 改回集合信封；`shop.js:975`、`:1131` 跟著改讀 `items`。
- `GET /api/public` 的 `operations` 列出全部匿名端點；`GET /api/public/openapi.json` 是 OpenAPI 3.1.0（與 `/api/v1` 一致，從 3.0.3 升），涵蓋 R1 之後的所有 `/api/public/**` 路徑，每個操作宣告 `security: []`、各自的 4xx 與 `429`（有限流者）、`503`，錯誤 schema 引用公開錯誤信封；`components.schemas` 的購物車、結帳、訂單狀態、留言樹、搜尋結果形狀從 handler 實際輸出的 struct 產生（沿用工單 27 的做法：struct 的 json tag 就是 schema 的鍵）。
- `/api/shop/openapi.json` 原本涵蓋的 `/api/account/*` 商店端點改收進 `/api/auth/openapi.json`（它已經涵蓋 `/api/account/*` 其他端點）；金流回呼 `/api/payment/:gateway/notify|callback|return` 放進 `/api/public/openapi.json` 的「金流回呼」標籤，標明「由金流服務呼叫」。
- `GET /api` 的 `apis` 從四筆變三筆（`v1`、`public`、`auth`）；`shop` 這筆拿掉。
- 文件與程式不一致的三處一起修正：商店說明對購物車四條端點寫「需同來源請求」但程式沒檢查（`shop_api_documentation.go:54-58`）→ 改文件；`shop.js` 的 `serverFieldErrors` 讀 `data.field`／`data.fields` 但伺服器從沒送過 → 依 R2 改成真的會送；`shop.js:279` 比對從未出現的 `cart_changed` → 刪掉這個分支。

### R4 WebMCP 與主題

- `webmcp.go:256-259` 四個內建工具的 `endpoint` 改成新路徑；`webMCPToolRegistry()` 產生的 `webmcp-tools.json` 自然跟著變。
- 預設主題：`shop.js` 的所有 `/api/cart`、`/api/checkout`、`/api/orders`、`/api/shop` 字串改成 `/api/public/...`；`_comments.html:174` 改 `/api/public/comments`。主題不靠 308，直接打新路徑。
- `scripts/acceptance/webmcp-edges.spec.mjs` 用到的 `/api/public/*` 路徑不變。

## 程式（格子）

1. `internal/server/public_api.go`：新增 `registerPublicAPIRoutes` 內的全部 R1 新路徑（把 `shop_routes.go:100-112`、`shop_lookup.go:41-43`、`shop_payment.go:38-40`、`handlers_comments.go:76-78`、`handlers_public.go:341` 的註冊搬過來，handler 不搬），與舊路徑的 308 handler（一個 `publicAPIMoved(newPath string)` 產生器，保留 `:param` 與查詢字串）。
2. `internal/server/errors.go`：`/api/public` 子樹走 `writeAPIError`；新增 `publicAPIErrorCode(status)` 對映表（404 `not_found`、405 `method_not_allowed`、413 `payload_too_large`、429 `rate_limited`、500 `internal_error`、503 `service_unavailable`）。`internal/server/maintenance.go`：`/api/public` 子樹的 503 用信封。`installation_http.go:88-92` 的 503 改用同一個 helper。
3. `internal/server/shop_cart.go`、`shop_checkout.go`、`shop_lookup.go`、`shop_payment.go`、`shop_options.go`：`shopJSONError` 改成呼叫 `writeAPIError` 的薄包裝（簽名不變，內部轉信封），欄位錯誤改放 `details.fields`，`rate_limit` 改 `rate_limited`，兩個 `-methods` 改集合信封。
4. `internal/server/handlers_comments.go`（公開兩條）、`handlers_public.go`（搜尋）：錯誤改 `writeAPIError`，`code` 依 R2 表。
5. `internal/server/public_api_docs.go`：`publicAPIOverview` 與 `publicAPIOpenAPI` 改為從一張匿名路由表產生（仿 `integrationRouteTable`：方法、路徑、摘要、是否限流、query 參數、request body、回應 struct），`shop_api_documentation.go` 的匿名部分併入、帳號部分搬到 `internal/auth/api_documentation.go`、金流回呼部分併入並加標籤；`shop_api_documentation.go` 與 `/api/shop*` 兩條路由刪除。
6. `internal/server/webmcp.go:256-259`：端點改新路徑。
7. `web/themes/default/assets/shop.js`、`web/themes/default/_comments.html`：路徑改新、`serverFieldErrors` 改讀 `details.fields`、刪 `cart_changed` 分支、`-methods` 改讀 `items`。依 AGENTS.md，畫面行為沒變但 JS 有改，瀏覽器驗收要跑購物車、結帳、留言三條流程；設計板不用動。
8. `internal/server/public_api.go`：`apiIndex` 的 `apis` 拿掉 `shop`。
9. 文件：README 的「支付 API」與商店段落的路徑；`docs/webmcp.md` 若列出端點路徑；`docs/tickets/05-public-theme.md`、`07-shop.md`、`13-webmcp.md` 開頭各加一行指向本工單；`STATUS.md`。

## 驗收（測試契約）

新測試檔 `internal/server/public_api_consolidation_integration_test.go`：

- 路徑對照表每一列：新路徑以原本測試的輸入打一次得到原本的狀態碼；舊路徑回 308、`Location` 等於新路徑（含 `:variant_id`、`:number`、`:trade_no` 的實際值與查詢字串）、body 是公開錯誤信封 `code = "moved"`；`GET /api/shop` 與 `/api/shop/openapi.json` 回 308 到 `/api/public` 與 `/api/public/openapi.json`。
- 錯誤信封：對每個端點各挑一個 4xx 案例（購物車 `input`、加入不存在的 variant `variant`、折扣碼 `coupon`、結帳缺 email 的 `input` 且 `details.fields.email` 存在、結帳非同來源 `origin`、訂單查詢超限 `rate_limited` 且有 `Retry-After`、留言缺 `post_id` 的 `input`、搜尋缺 `q` 的 `input` 且 `details.param = "q"`、搜尋過長 `query_too_long`、不存在的 `/api/public/nope` 的 `not_found`），斷言四個鍵齊全、`details` 是物件、`correlation_id` 等於 `X-Request-ID`。整個 `internal/server` 套件中 `/api/public` 回應不得再出現 `{"error":` 開頭的 body（用一個路由表掃描：對每條 `/api/public/**` 路由發一個必定失敗的請求，解析 body 必須有 `code` 與 `correlation_id`）。
- 維護模式：匿名 GET `/api/public/comments?post_id=1` 回 503、信封 `code = "maintenance"`、有 `Retry-After`；`maintenance_integration_test.go:187-192` 的既有斷言改成這個形狀。
- 清單：兩個 `-methods` 回 `{items,total}`。
- OpenAPI：`/api/public/openapi.json` 的 `openapi` 為 `3.1.0`；`paths` 的鍵集合恰好等於 R1 之後所有 `/api/public/**` 路由（用 `e.Routes()` 比對，排除 308 的舊路徑）；每個操作 `security` 為 `[]`；有限流的操作有 `429`；`/api/shop*` 不存在於任何 OpenAPI；`/api/auth/openapi.json` 含 `/api/account/orders*` 與 `/api/oauth/status`。
- `GET /api`：`apis` 恰好 `v1`、`public`、`auth` 三筆（`api_v1_contract_integration_test.go` 的 `TestAPIV1ContractAPIIndexGETAndHEAD` 期望值同步改成三筆，其餘不變）。
- WebMCP：`GET /_jelly/webmcp-tools.json` 四個內建工具的 `endpoint` 都以 `/api/public/` 開頭；`webmcp_integration_test.go` 內指向 `/api/search` 的斷言改成 `/api/public/search`。
- 既有測試：`cart_integration_test.go`、`checkout_integration_test.go`、`coupons_integration_test.go`、`order_lookup_integration_test.go`、`comments_integration_test.go`、`shop_*_test.go`、`theme_*search*_test.go`、`public_theme_edges_test.go` 等所有打舊路徑的改打新路徑，解析錯誤的地方改讀 `code`／`message`；斷言不得放寬。`shop_api_documentation_test.go` 整檔改寫為驗證合併後的 `/api/public` 文件。
- 瀏覽器驗收（Playwright，`scripts/acceptance/`）：新增 `storefront.spec.mjs`，跑加入購物車 → 改數量 → 套折扣碼 → 結帳（sandbox）→ 查訂單狀態 → 留言送出六步，每步斷言畫面文字且網路請求只打 `/api/public/**`（用 `page.on('request')` 蒐集，出現 `/api/cart`、`/api/checkout`、`/api/orders`、`/api/comments`、`/api/search` 即失敗）。

## 不在本工單

- 移除 308 舊路徑（下一個次版本的工單）。
- `/api/oauth/status`、`/api/invites/accept`、`/api/password-reset` 搬路徑。
- 匿名 API 加版本號。
- 維護模式對 `/api/payment/:gateway/notify` 金流回呼回 503 的問題（`maintenance.go:119-133` 沒有放行；金流服務會重試，但重試用盡會掉單）。已記入 AGENTS.md Follow-ups，另開工單。
- 購物車端點補同來源檢查（文件說有、程式沒有）；本工單只把文件改成符合程式，要不要加檢查另案決定。

## 決策紀錄

- 前綴選 `/api/public` 而不是 `/api/v1/public`：`/api/v1` 整個子樹掛金鑰驗證與稽核，`GET /api` 剛把它定義為「機器、存取金鑰」；購物車靠 Cookie 與來源檢查，是瀏覽器的東西，混進去會讓受眾說明變假。
- 不加版本號：主題與 CMS 同版出貨，相容靠 308 過渡期，不靠第二個 v1。之後若要給第三方 SPA 長期契約，再在 `/api/public/v2` 開。
- 舊路徑用 308 不用 404：預設主題以外的自製主題會直接打舊路徑，308 讓它們多活一個版本；用 308 不用 301／302 是為了保留 POST 的方法與 body。
- 錯誤信封沿用 `/api/v1` 的四個鍵而不是另設：客戶端兩種 API 共用一套錯誤處理；`code` 值沿用現有字串，因為主題 JS 已經依賴 `stock`。
- `-methods` 改集合信封：全站清單只剩這兩條裸陣列，趁搬路徑一起改最便宜。
- `/api/shop` 文件併入而不是保留：它一半內容是帳號與金流回呼，本來就不是「商店匿名 API」；併入後每個端點只在一份文件出現。
