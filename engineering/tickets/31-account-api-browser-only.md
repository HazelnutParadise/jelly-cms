# 31 — 登入 JWT 只能當 Cookie 用：`Authorization: Bearer <JWT>` 不再是任何端點的身分

Epic：帳號與存取
User Story：站上只有兩種憑證：瀏覽器的登入 Cookie（給網頁）與 `jly_` 存取金鑰（給程式）。登入 JWT 放在 `Authorization: Bearer` 不再被解析成身分；`/api/auth/*`、`/api/account/*` 與付款 API 帶這個標頭會得到明確的 401，個人中心與商店會員 API 的寫入都受到和後台一樣的來源與 CSRF 保護。
Blocked by：23（後台 API 只收網頁登入，已合併）。與 30 同時改 `internal/auth/api_documentation.go`，見「跨工單」。

## 背景與決定

現況（行號以 main `0dc56d9` 為準）：

- `internal/auth/principal.go:32-62` 的 `resolvePrincipal` 先看 `Authorization: Bearer <JWT>`（來源 `principalBearer`，`:42-52`），再看 `jwt_token` Cookie，再看 Session。`RequireAuth`（`middleware.go:14-38`）、`RequireRole`（`:46-67`）、`RequirePermission`（`:104-121`）都讓 Bearer 直接通過；只有 `RequireInteractive`（`:74-92`）對 Bearer 回 403。
- Bearer JWT 會走到的地方不只 API：`/account/*` 頁面（`shop_account.go:39-44`）與 `/admin/account`（`account_routes.go:78`）經 `RequireAuth`；維護模式用 `GetCurrentUser` 判斷放行（`maintenance.go:57`）；OAuth 回呼的 `revalidatePrincipal`（`oauth_accounts.go:353`）與連結意圖的來源判斷（`:325`）。`HandleLogout` 不經 `resolvePrincipal`，自己解析標頭撤銷 jti（`service.go:791-797`）。
- 工單 23 把 `/api/admin/*` 改成「有 `Authorization` 標頭就 401 `Browser login required`」（`admin_render.go:236-241`），同一函式接著做工作人員角色檢查（`:243-252`），但明文保留 `/api/auth/*`、`/api/account/*` 的 Bearer 行為。
- 現在仍收 Bearer JWT 的 API：`GET /api/auth/me`、`GET /api/auth/identities`、`DELETE /api/auth/identities/:provider`；`/api/account` 的讀取五條；`/api/account` 的寫入四條沒有掛 `RequireInteractive`（`PATCH profile`、`POST notifications/read-all`、`POST notifications/:id/read`、`PUT notification-preferences`，`account_routes.go:53,70-73`）；`shop_account.go:45-56` 的會員商店 API 12 條（含取消訂單、再次購買、地址與發票資料的建改刪）；`handlers_payment.go:39-41` 的 `/api/payment/create`、`/api/payment/order/:id/status`。
- 用 Bearer 寫入時沒有 Cookie，`ProtectCookieWrites` 不會做來源檢查（`request_protection.go:24-27`），也沒有 CSRF 標頭要求。一把外洩的 JWT 可以改個人資料、取消訂單、改收件地址。
- 前端（`web/admin/*.html`、`web/themes/default/assets/shop.js`、WebMCP）沒有任何地方用 `Authorization` 標頭送登入 JWT；`scripts/acceptance/` 也沒有。`auth.GetUserFromJWT`（`service.go:272-278`）沒有呼叫端。
- 登入成功的 JSON 回應仍回 `token`（`internal/auth/service.go:481-485`、`:673-677`、`:755-759`、`internal/server/account_totp.go:146`），約 41 處測試用它登入後放 Cookie 或 Bearer。
- 決定：`resolvePrincipal` 不再解析 Bearer JWT（刪掉 `principal.go:42-52` 的 JWT 分支，保留對 `jla_`／`jly_` 的拒絕），所以頁面、維護模式、OAuth 回呼全部一致地不認 Bearer。`HandleLogout` 維持自己解析並撤銷 Bearer（撤銷外洩的 JWT 是降風險，不是能力）。
- 決定：在認證之前多一道明確的閘門：`/api/auth`、`/api/account`、`/api/payment/create`、`/api/payment/order` 子樹帶任何 `Authorization` 標頭回 401 `{"error": "Browser login required"}`，與 `/api/admin` 同一段程式，讓客戶端拿到一致、可讀的原因，而不是「像沒登入一樣」的 401。
- 決定：`/api/account/*` 的四條非互動寫入與商店會員 API 的所有寫入改掛 `auth.RequireInteractive()`（寫入要 `X-Jelly-CSRF`），讀取維持 `RequireAuth`。
- 決定：登入 JSON 回應的 `token` 欄位本工單不拿掉（測試大量依賴，且值與 Cookie 相同），文件改為標示「與 Cookie 相同，只供寫入 Cookie 的客戶端使用；放進 `Authorization` 不會被接受」。拿掉另案。
- 不改：`/api/v1` 的金鑰驗證、`/auth/login`、`/auth/:provider`、OAuth 回呼流程本身、`/auth/login/2fa`、`/api/invites/accept`、`/api/password-reset`、金流回呼 `/api/payment/:gateway/*`、`/api/admin` 既有的閘門與角色檢查。

## 規則

### R1 總閘門

`enforceStaffAdminAPIs`（`internal/server/admin_render.go`）拆成兩步：

1. 「帶 `Authorization` 標頭就 401」這一步改用 `browserOnlyAPIPath(path) bool` 判斷，涵蓋 `/api/admin`、`/api/auth`、`/api/account`、`/api/payment/create`、`/api/payment/order` 五個子樹加精確路徑 `/api/docs`（`/api/payment/:gateway/notify|callback|return` 不在內：前綴 `/api/payment/` 的下一段若不是 `create` 或 `order` 就不是）。`GET /api/auth`、`/api/auth/openapi.json`、`/api/docs` 三條公開說明端點帶標頭同樣 401，不帶標頭仍公開。
2. 工作人員角色檢查（非 admin／editor／author 回 403、未登入回 401，`:243-252`）**只限** `/api/admin` 子樹，範圍不變。一般會員打 `/api/account` 不會被這一步擋。

### R2 互動限定

下列寫入改掛 `auth.RequireInteractive()`，非 GET／HEAD／OPTIONS 必須帶非空 `X-Jelly-CSRF`，否則 403 `{"error": "Interactive login required"}`：

`RequireInteractive` 不做來源檢查，所以所有掛法都是「既有的 `RequireAuth`（或群組的 `RequireAuth`）在外、`RequireInteractive()` 在內」，與 `account_routes.go:54-67` 現有互動路由相同。

- `PATCH /api/account/profile`、`POST /api/account/notifications/read-all`、`POST /api/account/notifications/:id/read`、`PUT /api/account/notification-preferences`
- `POST /api/account/orders/:number/cancel`、`POST /api/account/orders/:number/reorder`
- `POST|PUT|DELETE /api/account/addresses*`、`POST|PUT|DELETE /api/account/invoice-profiles*`
- `DELETE /api/auth/identities/:provider`、`POST /api/auth/identities/:provider/start`（`internal/auth/routes.go:25-26`）：`RequireAuth` 保留（它負責 `ProtectCookieWrites` 的來源檢查，`middleware.go:36`；`RequireInteractive` 本身不檢查來源），在它裡面再套 `RequireInteractive()`，寫成
  `e.POST("/api/auth/identities/:provider/start", auditOAuthIdentityAction("link_start", RequireAuth(RequireInteractive()(ProtectLoginAttempts(HandleIdentityLinkStart)))))`
  `e.DELETE("/api/auth/identities/:provider", auditOAuthIdentityAction("unlink", RequireAuth(RequireInteractive()(HandleIdentityUnlink))))`
  （稽核在最外層，失敗也留紀錄；`oauth_link_integration_test.go:768-795` 的跨站 `Origin` 測試必須維持通過）；`start` handler 內原本的「有 `Authorization` 就 400 `Cookie login required`」檢查（`oauth_accounts.go:175-177`）刪除，由閘門（401）與 `RequireInteractive`（403）接手。
- `POST /api/payment/create`（`ProtectBrowserWrites` 維持，`RequireInteractive` 加在它外層）

讀取（`GET`）維持 `RequireAuth`。

### R3 `resolvePrincipal` 與殘留的 Bearer 入口

- `principal.go:42-52`：刪除 Bearer JWT 的解析；`Authorization` 標頭只要存在（不論值）就回未認證的錯誤，不退回 Cookie 或 Session（沿用 `:43-44` 現有的「不退回」原則，`auth_revocation_test.go:530-539` 的子測試語意因此不變）；訊息對 `jla_`／`jly_` 開頭維持「存取金鑰不能用在這裡」（`:47-51`），其他值為「Authorization header is not accepted; use the login cookie」。
- `principalBearer` 常數、`middleware.go:81-83` 的 `p.Source == principalBearer` 分支、`oauth_accounts.go:325` 允許 `principalBearer` 來源的判斷一併刪除。`auth.GetUserFromJWT` 刪除。
- 刪除後仍會讀 `Authorization` 的只有 `HandleLogout`（`service.go:794-797`），行為不變。

### R4 文件

- `internal/auth/api_documentation.go`：`securitySchemes` 的 `BearerAuth`（`:602`）刪除，保留既有的 `JWTTokenCookie` 與 `SessionCookie`；`:332` 依 `access` 加安全方案的邏輯改成 `self`、`admin` → `[JWTTokenCookie, SessionCookie]`，`interactive`、`admin-interactive` → 同上並在描述加「寫入需 `X-Jelly-CSRF`」；`:146`、`:198`、`:229`、`:261` 四處直接寫的 `security` 同樣改掉 `BearerAuth`；R2 改成互動限定的四條 account 寫入（`PATCH profile`、`read-all`、`:id/read`、`PUT notification-preferences`），`access` 由 `self` 改 `interactive`（`:284,299,300,302`）；若工單 30 已把 12 條會員商店端點注入這份文件，其中的寫入同樣改 `interactive`；`:241` 的「此互動流程禁止 Authorization 標頭」升級為所有端點共通的一句。登入回應的 `token` 描述依「背景與決定」改寫。工單 30 未做時，`internal/server/shop_api_documentation.go:309-310,334` 宣告的 `BearerAuth` 一併拿掉（改 `JWTTokenCookie`／`SessionCookie`）。
- README：`:107` 的「無 Cookie 的 Bearer API 保留原有認證方式」改寫為「登入 JWT 只能放 Cookie；程式請用存取金鑰」；`docs/tickets/03-access-control.md`、`06-accounts.md`、`07-shop.md`、`23-admin-api-browser-only.md` 開頭各加一行指向本工單；`STATUS.md`。

## 程式（格子）

1. `internal/auth/principal.go`：R3；`internal/auth/middleware.go:81-83`：刪 `principalBearer` 分支；`internal/auth/service.go`：刪 `GetUserFromJWT`；`internal/auth/oauth_accounts.go`：`:325` 與 `:175-177`。
2. `internal/server/admin_render.go`：R1 的 `browserOnlyAPIPath` 與兩步拆分。
3. `internal/server/account_routes.go`、`shop_account.go`、`internal/auth/routes.go`、`internal/server/handlers_payment.go`：依 R2 掛 `auth.RequireInteractive()`。
4. `internal/auth/api_documentation.go`、（30 未做時）`internal/server/shop_api_documentation.go`：R4。
5. `web/admin/profile.html`、`notifications.html`、`account.html`、`users.html`、`web/themes/default/assets/shop.js`：確認打 R2 端點的寫入請求都帶 `X-Jelly-CSRF: 1`（後台已有 `api()` 包裝；`shop.js:239-266` 的 `api()` 對非 GET 已加此標頭，確認會員 API 走同一個包裝）。沒有畫面變更。
6. 測試輔助：`access_control_integration_test.go:212-233` 的 `setAccessToken` 對 `/api/account`、`/api/auth`、`/api/payment` 一律改用 Cookie 加 CSRF 加同來源 `Origin`；`account_profile_integration_test.go:318-320,360,534-540`、`accounts_security_regression_test.go:107-110`、`login_attempts_integration_test.go:167,239`、`auth_revocation_test.go:113,235` 等以 Bearer 打這些端點的測試改成 Cookie，**斷言不變**（例如「舊 JWT 回 401」改成舊 JWT 放 Cookie 仍回 401）。`auth_revocation_test.go:530-539`「無效 Authorization 加有效 Session 不退回」的子測試**不改**：R3 之後它仍成立（有標頭就拒絕）。這一步在規則實作前就要全綠。

## 驗收（測試契約）

新測試檔 `internal/server/account_api_browser_only_integration_test.go`，沿用 `admin_api_browser_only_integration_test.go` 的 `broDo` 與路由掃描寫法；寫入請求一律帶同來源 `Origin`，讓 403 的原因只剩 CSRF：

- 路由表：列舉 `e.Routes()` 中所有 `/api/auth*`、`/api/account*`、`/api/payment/create`、`/api/payment/order/*` 路由，對每一條以「合法登入 JWT 放 `Authorization: Bearer`」呼叫，一律 401 且 body 為 `Browser login required`；以 `jly_` 形狀的 Bearer 同樣 401；改用 Cookie 呼叫不被這條規則擋。`POST /auth/logout` 以 Bearer 仍可撤銷（回 2xx，且該 jti 之後放 Cookie 回 401）。`POST /api/payment/sandbox/notify` 帶 `Authorization` 不受影響（回它原本的狀態碼）。
- Bearer 在非 API 入口也無效：`GET /account/orders` 與 `GET /admin/account` 帶 Bearer 且無 Cookie 回未登入的結果（302 到登入頁或 401，依現況對未登入的處理）；維護模式開啟時管理員 JWT 放 Bearer 打 `GET /` 得到 503（不放行），放 Cookie 得到 200。
- 互動限定：對 R2 每一條以 Cookie 加 `Origin`、無 `X-Jelly-CSRF` 的寫入回 403 `Interactive login required`；加標頭通過這條規則（不要求業務成功）。`GET /api/account/profile`、`GET /api/account/orders` 以 Cookie 不需要 CSRF。
- 未登入：R2 各條與需登入的讀取（`/api/auth/me`、`/api/account/profile`、`/api/account/orders`）回 401（不是 403）；`GET /api/auth` 不帶標頭仍 200。
- 不變量：`GET /api/v1/me` 以存取金鑰仍 200；`GET /api/admin/posts` 以 Bearer 仍 401 `Browser login required`；一般會員以 Cookie 打 `GET /api/account/profile` 仍 200（角色檢查沒有擴到 `/api/account`）。
- 既有測試改動：`admin_api_browser_only_integration_test.go:292-301` 的 `TestAdminAPIBrowserOnlyInvariantAuthMeAcceptsBearer` 改名並反轉斷言（Bearer 打 `/api/auth/me` 回 401 `Browser login required`）；同檔 `broInteractiveEndpoints` 中 `POST /api/account/password` 那列的 `bearerCode` 由 403 改 401（`:209`）；`oauth_link_integration_test.go:178-200` 的「只帶 Bearer 回 4xx」維持通過。
- OpenAPI：`/api/auth/openapi.json` 的 `components.securitySchemes` 沒有 `BearerAuth`、有 `JWTTokenCookie` 與 `SessionCookie`；沒有任何操作引用 `BearerAuth`；R2 四條 account 寫入的描述含 `X-Jelly-CSRF`。
- 靜態守門：`web/` 下沒有任何 `fetch` 對 `/api/account`、`/api/auth` 送 `Authorization`（`strings.Contains` 掃描 `.html`、`.js`，排除 `integrations.js` 顯示給使用者複製的文字）。
- 瀏覽器驗收：個人中心改顯示名稱、關閉一則通知、會員中心新增地址、取消一筆待付款訂單，四個動作在瀏覽器成功；同一動作用 curl 帶 Bearer 回 401。

## 不在本工單

- 登入 JSON 回應拿掉 `token`（要重寫約 41 處測試的登入流程）。
- `/auth/logout` 停收 Bearer。
- `/api/public/*` 匿名 API（工單 30）。

## 跨工單

- 與 30：兩張都改 `internal/auth/api_documentation.go`；30 搬進去的操作用 `JWTTokenCookie`／`SessionCookie`，本工單拿掉 `BearerAuth` 時不會碰到它們；30 負責刪 auth 文件裡的 10 條 `/api/admin/*`（`:304-313`），本工單不動它們；30 未做時本工單要順手拿掉 `shop_api_documentation.go` 的 `BearerAuth`。先後皆可。

## 決策紀錄

- 刪掉 `resolvePrincipal` 的 Bearer 解析而不是只在 API 前加閘門：只加閘門會讓頁面、維護模式、OAuth 回呼仍認 Bearer，「JWT 只能當 Cookie 用」就是一句假話；刪分支讓所有入口一致，閘門只負責給 API 客戶端一個可讀的 401 原因。
- 有 `Authorization` 標頭就拒絕、不退回 Cookie：沿用現況「不退回」的原則，避免壞掉的標頭被 Cookie 悄悄救回來造成客戶端誤判，也讓既有的無退回測試原樣保留。
- 用 401「有標頭就擋」而不是只對 Bearer JWT 回 403：和 `/api/admin` 同一條規則、同一段程式，客戶端只要記一件事；且 401 在認證前就回，不會先查資料庫。
- 寫入改掛 `RequireInteractive` 而不是只擋 Bearer：擋了 Bearer 之後寫入一定帶 Cookie，`ProtectCookieWrites` 會做來源檢查，但 CSRF 標頭仍是後台的既有規則，會員 API 沒理由比後台鬆。
- 保留 logout 的 Bearer：撤銷是降低風險的動作，而且它本來就不經 `resolvePrincipal`。
- 不拿掉登入 JSON 的 `token`：拿掉是對的，但要動 41 處測試，與本工單「關門」的目標分開做風險較低。
