# 30 — 匿名 API 只有一個前綴、一份文件、一種錯誤格式

Epic：外觀與擴充
User Story：做主題、前台 SPA 或 WebMCP 工具的人，只要看 `GET /api/public` 與 `/api/public/openapi.json` 就能找到所有不用登入的端點；每個錯誤都長一樣，用 `code` 分流、用 `correlation_id` 回報；搬家後舊路徑一個版本內還能用。
Status：待實作。
Blocked by：27（`/api/v1` 契約與 `GET /api` 總索引，已合併）。與 31 同時改 `internal/auth/api_documentation.go`，見「跨工單」。

## 背景與決定

現況（行號以 main `0dc56d9` 為準）：

- 不用登入的 JSON 端點散在七個前綴：`/api/public`（5 條：索引、OpenAPI、`posts`、`content/:slug`、`site`）、`/api/search`、`/api/comments`（2 條）、`/api/cart*`（6 條）、`/api/checkout*`（3 條）、`/api/orders/*`（3 條）、`/api/shop/*-methods`（2 條），另有 `/api/oauth/status`、`/api/invites/accept`、`/api/password-reset` 三條屬於帳號流程。
- 錯誤格式有三種：`/api/public` 的 `publicAPIError` 回 `{code, message, correlation_id}`（`public_api.go:25-37`，沒有 `details`）；商店的 `shopJSONError` 回 `{code, error}`（`shop_cart.go:794-796`）；留言、搜尋與所有 `echo.NewHTTPError` 經全域錯誤處理回 `{error, code: 數字}`（`errors.go:85-87`），500 的訊息被換成 `Internal Server Error`（`errors.go:51-57`）。還有不經任何 helper 的：`shopRateLimited` 回 `{"error": "rate_limit"}` 沒有 `code`（`shop_lookup.go:139-141`）；sandbox 模擬付款用 `c.NoContent(404/400/500)` 與 `{"error": "Invalid payment status"}`（`shop_payment.go:385-444`）；訂單查詢來源檢查用 `echo.NewHTTPError(403, "invalid origin")`（`shop_lookup.go:56-57,258-259`）。
- 說明文件有兩份：`/api/public/openapi.json`（3 條）與 `/api/shop/openapi.json`（`shop_api_documentation.go` 共 56 個操作：14 條匿名、15 條 `/api/account/*` 與金流回呼、27 條 `/api/admin/*`）。`/api/search`、`/api/comments`、`/api/oauth/status` 不在任何一份裡。
- 清單形狀不一：`/api/shop/shipping-methods` 與 `payment-methods` 回裸陣列（`shop_options.go:195,203`），其他回物件。
- 路徑被寫死在兩個業務邏輯裡：sandbox 付款的授權 Cookie `Path` 是 `/api/checkout/sandbox/<trade_no>`（`shop_payment.go:363`），語言偏好只對 `/api/cart`、`/api/shop`、`/api/checkout` 生效（`middleware_i18n.go:85-86` 的 `isLanguagePreferencePath`）。
- 呼叫端只有三種：預設主題的 `web/themes/default/assets/shop.js`（只讀 `data.code` 與 HTTP 狀態碼，不讀訊息文字；`messageFor` 在 `:273-287`）、`web/themes/default/_comments.html:174`（只看狀態碼）、WebMCP 內建四個工具（`internal/server/webmcp.go:256-259`，指向 `/api/search` 與 `/api/public/*`，`webmcp-tools.json` 由伺服器動態產生）。
- 決定：所有不用登入的內容與商店端點搬到 `/api/public/` 底下，一份 OpenAPI，一種錯誤格式（與 `/api/v1` 相同的信封）。`/api/oauth/status`、`/api/invites/accept`、`/api/password-reset` 屬於帳號流程，路徑不動，但 `/api/oauth/status` 補進 `/api/auth/openapi.json`。
- 決定：舊路徑回 308 轉到新路徑，保留到下一個次版本，再由新工單移除。308 保留方法與 body，`fetch` 預設會跟隨；WebMCP 的 `webmcp.js` 用 `redirect: "error"`，但它的工具清單由伺服器產生，直接改成新路徑就不會碰到轉址。
- 決定：錯誤 `code` 字串除下列兩類外沿用現有值：(a) `rate_limit` 改 `rate_limited`；(b) 結帳以欄位名當 `code` 的那組（`email`、`phone`、`accept_terms`、`website`、`idempotency_key`、`shipping`、`shipping.method`、`payment.method`、`amount` 與發票欄位，`shop_checkout.go:151-177,548-556`）改成 `code = "input"` 加 `details.fields`。主題 JS 讀的 `stock`、`coupon`、`cart_unavailable`、`origin`、`idempotency_conflict` 等不變。
- 決定：`shop_api_documentation.go` 裡 27 條 `/api/admin/*` 的文件刪除不搬：後台 API 只給網頁用（工單 23），公開 OpenAPI 裡不該有它；`shop_api_documentation_test.go:62-66` 鎖住這些操作的斷言一併刪除。
- 不改：各端點的業務規則、來源檢查、限流次數、購物車 Cookie、維護模式下匿名 API 回 503 的行為、未安裝時非 `/api/public` 路徑 302 到 `/install` 的行為（搬進 `/api/public` 的端點未安裝時從 302 變成 503，與該子樹現況一致）、`/api/v1`、`/api/admin/*`、`/api/account/*`、`/api/auth/*`、金流回呼 `/api/payment/:gateway/*`。

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
| `GET /api/shop`、`GET /api/shop/openapi.json` | 308 到 `GET /api/public` 與 `/api/public/openapi.json` |

每條舊路徑回 `308 Permanent Redirect`，`Location` 為對應新路徑並保留查詢字串與路徑參數；body 為公開錯誤信封 `code = "moved"`、`details.location` 同 `Location`。舊路徑的原 handler 刪除，只剩 308。維護模式下打舊路徑會在 308 之前就被 `maintenanceMiddleware` 以現況格式（`{"error": "Service unavailable", "code": 503}`）擋下，這是預期的，不另處理。頁面路由 `GET /checkout/sandbox/:trade_no`（`shop_payment.go:38`）不是 API，不動。`webmcp.go` 的 `webMCPPrivilegedPath` 不變。

跟著路徑一起改的業務細節：

- `shop_payment.go:363` 的 sandbox Cookie `Path` 改為 `/api/public/checkout/sandbox/<trade_no>`。
- `shop_lookup.go:200` 訂單查詢連結發的 `jelly_order_view` Cookie `Path` 從 `/api/orders` 改為 `/api/public/orders`（讀取在 `shop_access.go:48`），否則查狀態與補寄會因為瀏覽器不送 Cookie 而 404。
- `middleware_i18n.go:85-86` 的 `isLanguagePreferencePath` 把 `/api/cart`、`/api/shop`、`/api/checkout` 改成 `/api/public/cart`、`/api/public/shop`、`/api/public/checkout`；舊前綴在 308 期間一併保留（轉址前的請求也套語言偏好，行為與現況相同）。

### R2 錯誤信封

`/api/public/**` 的所有錯誤一律公開錯誤信封，包括：handler 自己回的 `c.JSON`、`c.NoContent(4xx/5xx)`、`echo.NewHTTPError` 經全域錯誤處理的、維護模式的 503、安裝閘門在資料庫不可用時的 503。

- `errors.go` 的 `/api/v1` 分支擴成「`/api/v1` 或 `/api/public` 子樹」，公開子樹用自己的對照表 `publicAPIErrorCode(status)`：400 `bad_request`、401 `login_required`、403 `forbidden`、404 `not_found`、405 `method_not_allowed`、409 `conflict`、413 `payload_too_large`、415 `unsupported_media_type`、422 `unprocessable`、429 `rate_limited`、500 `internal_error`、503 `service_unavailable`，其他 `http_<status>`。
- 500 的 `message` 固定為 `Internal server error`（與 `/api/v1` 的 `integrationErrorEnvelope` 同一字串，不是全域處理器的 `Internal Server Error`），不洩漏內部訊息。
- `maintenance.go`：`/api/public` 子樹的 503 用信封，`code = "maintenance"`，保留 `Retry-After`。
- `installation_http.go:88-92` 改以 `pathOrSubtree(path, "/api/public")` 判斷，所有方法都適用：資料庫為 nil 時回信封 503 `service_unavailable`（多出 `details: {}` 與 `correlation_id`），判斷位置與現況相同，在 `install.IsInstalled()` 之前，所以未安裝時這個子樹也是 503 而不是 302（沿用現況；`webmcp_edges_integration_test.go:204-229` 的斷言只改 body 形狀）。其他路徑未安裝時仍 302 到 `/install`。
- `publicAPIError`（`public_api.go:25-37`）改為呼叫 `writeAPIError` 的薄包裝，多出 `details: {}`。
- 新增 `publicJSONError(c, status, code, message)`（呼叫 `writeAPIError`）與 `publicFieldError(c, status, field, message)`（`code = "input"`、`details.fields = {field: message}`，欄位鍵就是原本當 `code` 用的字串，例如 `shipping.method`）；五個商店檔案中**公開路由**的 `shopJSONError` 呼叫全部改成這兩個。`shopJSONError` 本身不動，`shop_account.go:347,352,418`（會員的 reorder）繼續用它，`/api/account/*` 的錯誤格式不變。
- `shopRateLimited`（`shop_lookup.go:139-141`）與其他商店 429（`shop_cart.go:644`、`shop_checkout.go:144`、`shop_payment.go:142`）、留言 429（`handlers_comments.go:384`）：`code = "rate_limited"`，一律帶 `Retry-After`。秒數：`shopRateLimit` 的視窗記錄在 `shopActionRateWindow.expires`，回距到期的整數秒（最少 1），回傳值改成「是否超限、距到期秒數」；`shopLookupGlobalRateLimit`（`shop_lookup.go:74,113-130,280`）的視窗是 `start + window`，同樣回 `start + window - now` 的整數秒（最少 1），回傳值同樣改成兩個；`shop_lookup.go:280` 以 `||` 串接兩個限流器時取較大的秒數；`shopRateLimited` 多收一個秒數參數；留言的 `commentRateLimiter` 是 60 秒滾動視窗，回最舊一筆到期的秒數（最少 1）。
- `shop_payment.go:157` 的 `payment_method` 與 `:209` 的 `amount`（結帳重試）也是欄位名當 `code`，同樣改成 `input` 加 `details.fields`。
- `/api/public/checkout/retry` 與 `/api/public/checkout/sandbox/:trade_no` 外層的 `auth.ProtectBrowserWrites`（`request_protection.go:41-47`）自己回 `{"error": ...}` 的 403／503，不經全域錯誤處理。改法：`internal/auth` 匯出 `RequestOriginAllowed(c) (ok bool, reason string)` 與 `ProtectBrowserWrites` 共用判斷；`internal/server` 新增 `publicBrowserWrites` 中介層，用它判斷後以信封回 403 `origin` 或 503 `service_unavailable`；這兩條路由改掛 `publicBrowserWrites`。
- sandbox 的 `c.NoContent(404)` 改 404 `not_found` 信封、`c.NoContent(400)` 改 400 `bad_request`、`{"error": "Invalid payment status"}` 改 400 `input` 加 `details.param = "status"`；訂單查詢與補寄的 `echo.NewHTTPError(403, "invalid origin")` 改 403 `origin`（與結帳一致）。
- 留言：`GET /api/public/comments` 缺 `post_id`（`handlers_comments.go:326`）→ 400 `input` 加 `details.param = "post_id"`；`:333` → 404 `not_found`；`:337` → 500 `internal_error`。`POST`（`:375-467`）對照：JSON 解析失敗（`:378`）→ 400 `input` 無 details；honeypot（`:381`）→ 400 `rejected` 無 details（不透露原因）；缺 `post_id`（`:390`）→ 400 `input` 加 `details.param = "post_id"`；內文（`:406`）、暱稱（`:415`）、Email（`:419`）、回覆目標（`:428`）→ 400 `input` 加 `details.fields` 的 `body`、`name`、`email`、`parent_id`；`:387` → 403 `comments_disabled`；`:397` → 403 `comments_closed`；`:401` → 401 `login_required`；404 → `not_found`；500 → `internal_error`。
- 搜尋：缺 `q` → 400 `input` 加 `details.param = "q"`；`query_too_long` 不變。

### R3 清單與文件

- `GET /api/public/shop/shipping-methods`、`payment-methods` 改回集合信封；`shop.js:975`、`:1131` 跟著改讀 `items`。
- `GET /api/public` 的 `operations` 列出全部匿名端點；`GET /api/public/openapi.json` 是 OpenAPI 3.1.0（從 3.0.3 升），涵蓋 R1 之後所有 `/api/public/**` 路徑（不含索引與 OpenAPI 本身），每個操作宣告 `security: []`、各自的 4xx、有限流者的 `429`、`503`，錯誤 schema 引用公開錯誤信封；`components.schemas` 從 struct 產生（用工單 34 的 `integrationSchemaOf`；若本工單先做，先把該函式建在 `internal/server/integration_schemas.go`，33 沿用）。現有已是 struct 的沿用（`shopCartJSON`、`shopCheckoutResponse`、`shopShippingMethod`、`shopPaymentMethod`）；訂單狀態（`shop_lookup.go:253` 的 map）與搜尋結果（`handlers_public.go:360` 的區域型別）在 `internal/server/public_api_schemas.go` 新定 `publicOrderStatus`、`publicSearchResult` 兩個 struct，handler 改回它們（鍵不變）。
- 金流回呼 `/api/payment/:gateway/notify|callback|return` 放進 `/api/public/openapi.json` 的「金流回呼」標籤，標明「由金流服務呼叫」。
- `/api/shop/openapi.json` 原本涵蓋的 12 條 `/api/account/*` 商店端點改收進 `/api/auth/openapi.json`。`internal/auth` 不能 import `internal/server`（會循環），所以 `internal/auth/api_documentation.go` 新增匯出型別 `DocumentedOperation`（現有未匯出的 `authDocumentedOperation` 改名匯出）與 `RegisterAPIDocumentation(ops []DocumentedOperation, schemas map[string]any)` 註冊點（覆寫語意：重複呼叫以最後一次為準，因為測試會多次 `RegisterRoutes`），`internal/server` 在 `RegisterRoutes` 時把這 12 條操作與 `shopAPISchemas()` 裡它們用到的 components（`MemberOrders`、`Address`、`InvoiceProfile` 等，`shop_api_documentation.go:71-82,334`）注入；搬過去的操作不帶 `BearerAuth`，改用該文件既有的 `JWTTokenCookie` 與 `SessionCookie` 方案（工單 31 會把 `BearerAuth` 整個拿掉）。27 條 `/api/admin/*` 的商店文件刪除；`/api/auth/openapi.json` 裡既有的 10 條 `/api/admin/users*`、`/api/admin/roles` 文件（`internal/auth/api_documentation.go:304-313`；`:314` 是 `GET /invite/{token}`，保留）同樣刪除，理由相同（後台 API 只給網頁用）。
- `GET /api` 的 `apis` 從四筆變三筆（`v1`、`public`、`auth`）。
- 文件與程式不一致的三處一起修正：商店說明對購物車四條端點寫「需同來源請求」但程式沒檢查（`shop_api_documentation.go:54-58`）→ 改文件；`shop.js` 的 `serverFieldErrors` 讀 `data.field`／`data.fields` 但伺服器從沒送過 → 依 R2 改成讀 `details.fields`；`shop.js:279` 比對從未出現的 `cart_changed` → 刪掉這個分支。

### R4 WebMCP 與主題

- `webmcp.go:256-259` 四個內建工具的 `endpoint` 改成新路徑。
- 預設主題：`shop.js` 的所有 `/api/cart`、`/api/checkout`、`/api/orders`、`/api/shop` 字串改成 `/api/public/...`；`_comments.html:174` 改 `/api/public/comments`。主題不靠 308，直接打新路徑。
- `scripts/acceptance/webmcp-edges.spec.mjs` 用到的 `/api/public/*` 路徑不變。

## 程式（格子）

1. `internal/server/public_api.go`：`registerPublicAPIRoutes` 內註冊全部 R1 新路徑（把 `shop_routes.go:83-84,100-112`、`shop_lookup.go:41-43`、`shop_payment.go:39-40`、`handlers_comments.go:76-78`、`handlers_public.go:341` 的註冊搬過來，handler 不搬；`/api/shop` 兩條說明端點的 handler 刪除）與舊路徑的 308 handler（`publicAPIMoved(newPattern string)` 產生器，保留 `:param` 值與查詢字串）；`publicBrowserWrites` 中介層；`apiIndex` 的 `apis` 拿掉 `shop`。`internal/auth/request_protection.go`：匯出 `RequestOriginAllowed`。
2. `internal/server/errors.go`、`maintenance.go`、`installation_http.go`、`public_api.go` 的 `publicAPIError`：R2 的信封與對照表。
3. `internal/server/shop_cart.go`、`shop_checkout.go`、`shop_lookup.go`、`shop_payment.go`、`shop_options.go`：這五個檔中所有公開路由直接回的 `c.JSON` 錯誤、`c.NoContent(4xx/5xx)`、`echo.NewHTTPError`、`shopJSONError`、`shopRateLimited` 依 R2 改；`shopFieldError`；`Retry-After`；sandbox Cookie `Path`；兩個 `-methods` 改集合信封。`shop_validation.go` 的 `shopRateLimit` 回傳改成「是否超限、距到期秒數」。
4. `internal/server/handlers_comments.go`（公開兩條）、`handlers_public.go`（搜尋）、`middleware_i18n.go`：R2 與 R1 的語言偏好前綴。
5. `internal/server/public_api_docs.go`、`public_api_schemas.go`（新檔）：`publicAPIOverview` 與 `publicAPIOpenAPI` 改為從一張匿名路由表產生（仿 `integrationRouteTable`：方法、路徑、摘要、是否限流與視窗、query 參數、request body struct、回應 struct）；`shop_api_documentation.go` 的匿名與金流回呼部分併入、帳號部分經 `auth.RegisterAPIDocumentation` 注入 auth 文件、後台部分刪除；`shop_api_documentation.go` 刪除。`internal/auth/api_documentation.go`：新增註冊點、刪除 11 條 `/api/admin/*` 操作。
6. `internal/server/webmcp.go:256-259`：端點改新路徑。
7. `web/themes/default/assets/shop.js`、`web/themes/default/_comments.html`：R3、R4。依 AGENTS.md，畫面文字沒變但 JS 有改，瀏覽器驗收要跑購物車、結帳、留言三條流程；設計板不用動。
8. 文件：README 的「支付 API」與商店段落的路徑；`docs/webmcp.md` 若列出端點路徑；`docs/tickets/05-public-theme.md`、`07-shop.md`、`13-webmcp.md` 開頭各加一行指向本工單；`STATUS.md`。

## 驗收（測試契約）

新測試檔 `internal/server/public_api_consolidation_integration_test.go`：

- 路徑對照表每一列：新路徑以原本測試的輸入打一次得到原本的狀態碼；舊路徑回 308、`Location` 等於新路徑（含 `:variant_id`、`:number`、`:trade_no` 的實際值與查詢字串）、body 是公開錯誤信封 `code = "moved"`；`GET /api/shop` 與 `/api/shop/openapi.json` 回 308 到 `/api/public` 與 `/api/public/openapi.json`。
- sandbox：走新路徑完成一次模擬付款（授權 Cookie 的 `Path` 對得上新路徑）；訂單查詢：經查詢連結取得 `jelly_order_view` Cookie 後 `GET /api/public/orders/:number/status` 回 200、`POST /api/public/orders/:number/resend` 不因缺 Cookie 而 404；語言偏好：`GET /api/public/cart?lang=en` 的回應語言與現況 `GET /api/cart?lang=en` 相同。
- `/api/account/orders/:number/reorder` 的錯誤 body 仍是 `{"code", "error"}`（守門：`shopJSONError` 未被改動）。
- 錯誤信封：對每個端點各挑一個 4xx 案例（購物車 `input`、加入不存在的 variant `variant`、折扣碼 `coupon`、結帳缺 email → `input` 且 `details.fields.email` 存在、結帳非同來源 `origin`、訂單查詢非同來源 `origin`、訂單查詢超限 `rate_limited` 且 `Retry-After` 為 1 到 60 的整數、留言超限 `rate_limited` 且 `Retry-After` 為 1 到 60、留言缺 `post_id` 的 `input`、搜尋缺 `q` 的 `input` 且 `details.param = "q"`、搜尋過長 `query_too_long`、sandbox 不存在的 trade_no 回 404 `not_found` 且 body 非空、不存在的 `/api/public/nope` 的 `not_found`），斷言四個鍵齊全、`details` 是物件、`correlation_id` 等於 `X-Request-ID`。
- 全路由掃描：對 `e.Routes()` 中每條 `/api/public/**` 路由（排除 308 的舊路徑），用該路徑沒有註冊的方法打一次（例如 `DELETE /api/public/site`），回 405 且 body 是信封 `method_not_allowed`，`correlation_id` 非空。這證明全域錯誤處理對整個子樹都走信封。
- 維護模式：匿名 GET `/api/public/comments?post_id=1` 回 503、信封 `code = "maintenance"`、有 `Retry-After`；`maintenance_integration_test.go:187-192` 的既有斷言改成這個形狀。安裝閘門：資料庫為 nil 時 `GET /api/public/cart` 回 503 信封 `service_unavailable`。
- 清單：兩個 `-methods` 回 `{items,total}`。
- OpenAPI：`/api/public/openapi.json` 的 `openapi` 為 `3.1.0`；`paths` 的鍵集合（`{param}` 形式）等於「`e.Routes()` 中所有 `/api/public/**` 路由，扣掉 `/api/public` 與 `/api/public/openapi.json`，加上三條 `/api/payment/{gateway}/*`」；每個操作 `security` 為 `[]`；有限流的操作有 `429`；`/api/shop*` 與 `/api/admin/*` 不出現在 `/api/public/openapi.json`、`/api/auth/openapi.json`、`/api/v1/openapi.json` 任一份；`/api/auth/openapi.json` 含 12 條 `/api/account/*` 商店端點與 `/api/oauth/status`，且搬過去的操作沒有引用 `BearerAuth`。
- 來源檢查：`POST /api/public/checkout/retry` 不帶 `Origin` 回 403 信封 `origin`。
- `GET /api`：`apis` 恰好 `v1`、`public`、`auth` 三筆（`api_v1_contract_integration_test.go` 的 `TestAPIV1ContractAPIIndexGETAndHEAD` 期望值同步改成三筆，其餘不變）。
- WebMCP：`GET /_jelly/webmcp-tools.json` 四個內建工具的 `endpoint` 都以 `/api/public/` 開頭；`webmcp_integration_test.go` 內指向 `/api/search` 的斷言改成 `/api/public/search`。
- 既有測試：`cart_integration_test.go`、`checkout_integration_test.go`、`coupons_integration_test.go`、`order_lookup_integration_test.go`、`comments_integration_test.go`、`shop_*_test.go`、`theme_*search*_test.go`、`public_theme_edges_test.go` 等所有打舊路徑的改打新路徑，解析錯誤的地方改讀 `code`／`message`／`details`；斷言不得放寬。`shop_api_documentation_test.go` 整檔改寫為驗證合併後的 `/api/public` 文件，刪除對 `/api/admin/*` 文件的斷言。
- 瀏覽器驗收（Playwright，`scripts/acceptance/`）：新增 `storefront.spec.mjs`，跑加入購物車 → 改數量 → 套折扣碼 → 結帳（sandbox 模擬付款）→ 查訂單狀態 → 留言送出六步，每步斷言畫面文字且網路請求只打 `/api/public/**`（用 `page.on('request')` 蒐集，出現 `/api/cart`、`/api/checkout`、`/api/orders`、`/api/comments`、`/api/search` 即失敗）。

## 不在本工單

- 移除 308 舊路徑（下一個次版本的工單）。
- `/api/oauth/status`、`/api/invites/accept`、`/api/password-reset` 搬路徑。
- 匿名 API 加版本號。
- 維護模式對 `/api/payment/:gateway/notify` 金流回呼回 503 的問題（`maintenance.go:119-133` 沒有放行）。已記入 AGENTS.md Follow-ups，另開工單。
- 購物車端點補同來源檢查（文件說有、程式沒有）；本工單只把文件改成符合程式，要不要加檢查另案決定。
- 後台 `/api/admin/*` 的任何文件化。

## 跨工單

- 與 31：兩張都改 `internal/auth/api_documentation.go`。先做哪張都可以：本工單搬進去的操作用 `JWTTokenCookie`／`SessionCookie`，寫入類的描述加「需 `X-Jelly-CSRF`」（30 會把它們改成互動限定）；本工單刪除 auth 文件裡的 10 條 `/api/admin/*`，若 31 先做，31 的 R4 改 `access` 時這 10 條還在，31 不必處理它們，由本工單刪。
- 與 32：兩張都改 `handlers_comments.go`（本工單改公開兩條與限流回傳，31 抽出 `listAdminComments` 的查詢），不衝突但會碰同一個檔。
- 與 34：schema 產生用同一個 `integrationSchemaOf`，先做的那張建函式，後做的沿用。

## 決策紀錄

- 前綴選 `/api/public` 而不是 `/api/v1/public`：`/api/v1` 整個子樹掛金鑰驗證與稽核，`GET /api` 剛把它定義為「機器、存取金鑰」；購物車靠 Cookie 與來源檢查，是瀏覽器的東西，混進去會讓受眾說明變假。
- 不加版本號：主題與 CMS 同版出貨，相容靠 308 過渡期，不靠第二個 v1。之後若要給第三方 SPA 長期契約，再在 `/api/public/v2` 開。
- 舊路徑用 308 不用 404：預設主題以外的自製主題會直接打舊路徑，308 讓它們多活一個版本；用 308 不用 301／302 是為了保留 POST 的方法與 body。
- 錯誤信封沿用 `/api/v1` 的四個鍵而不是另設：客戶端兩種 API 共用一套錯誤處理；`code` 值沿用現有字串，因為主題 JS 已經依賴 `stock`；結帳欄位錯誤改成 `input` 加 `details.fields` 是因為「欄位名當 code」讓 code 的集合無限大，客戶端無法窮舉。
- `-methods` 改集合信封：全站清單只剩這兩條裸陣列，趁搬路徑一起改最便宜。
- `/api/shop` 文件併入而不是保留，後台 27 條文件刪除：它一半內容是帳號與金流回呼，本來就不是「商店匿名 API」；後台 API 只給網頁用，放在公開文件裡等於邀請外部程式去打。
