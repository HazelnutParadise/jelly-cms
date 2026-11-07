# 30 — 登入 JWT 只能當 Cookie 用：`/api/auth/*`、`/api/account/*`、`/api/payment/*` 不再收 `Authorization` 標頭

Epic：帳號與存取
User Story：站上只有兩種憑證：瀏覽器的登入 Cookie（給網頁）與 `jly_` 存取金鑰（給程式）。登入 JWT 放在 `Authorization: Bearer` 不再是任何端點的有效身分，個人中心與商店會員 API 的寫入都受到和後台一樣的來源與 CSRF 保護。
Status：待實作。
Blocked by：23（後台 API 只收網頁登入，已合併）。

## 背景與決定

現況（行號以 main `0dc56d9` 為準）：

- `internal/auth/principal.go:32-62` 的 `resolvePrincipal` 先看 `Authorization: Bearer <JWT>`（來源 `principalBearer`），再看 `jwt_token` Cookie，再看 Session。`RequireAuth`（`middleware.go:14-38`）、`RequireRole`（`:46-67`）、`RequirePermission`（`:104-121`）都讓 Bearer 直接通過；只有 `RequireInteractive`（`:74-92`）對 Bearer 回 403。
- 工單 23 把 `/api/admin/*` 改成「有 `Authorization` 標頭就 401 `Browser login required`」（`admin_render.go:236-241`），但明文保留 `/api/auth/*`、`/api/account/*` 的 Bearer 行為。
- 現在仍收 Bearer JWT 的端點：`GET /api/auth/me`、`GET /api/auth/identities`、`DELETE /api/auth/identities/:provider`；`/api/account` 的讀取五條（`profile`、`2fa`、`sessions`、`notifications`、`notification-preferences`）；`/api/account` 的寫入四條沒有掛 `RequireInteractive`（`PATCH profile`、`POST notifications/read-all`、`POST notifications/:id/read`、`PUT notification-preferences`，`account_routes.go:53,70-73`）；`shop_account.go:45-56` 的會員商店 API 全部 15 條（含取消訂單、再次購買、地址與發票資料的建改刪）；`handlers_payment.go:39-41` 的 `/api/payment/create`、`/api/payment/order/:id/status`。
- 用 Bearer 寫入時沒有 Cookie，`ProtectCookieWrites` 不會做來源檢查（`request_protection.go:24-27`），也沒有 CSRF 標頭要求。也就是說一把外洩的 JWT 可以改個人資料、取消訂單、改收件地址。
- 前端（`web/admin/*.html`、`web/themes/default/assets/shop.js`、WebMCP）沒有任何地方用 `Authorization` 標頭送登入 JWT；`scripts/acceptance/` 也沒有。`auth.GetUserFromJWT`（`service.go:272-278`）沒有呼叫端。
- 登入成功的 JSON 回應仍回 `token`（`service.go:481-485`、`:673-677`、`:755-759`、`account_totp.go:146`），約 41 處測試用它登入後改放 Cookie 或 Bearer。
- 決定：`/api/auth/*`、`/api/account/*`（含商店會員 API）、`/api/payment/create`、`/api/payment/order/:id/status` 一律只收 Cookie 或 Session；帶任何 `Authorization` 標頭的請求在認證之前回 401 `{"error": "Browser login required"}`，與 `/api/admin` 同一條規則、同一個實作。
- 決定：`/api/account/*` 的四條非互動寫入與商店會員 API 的所有寫入改掛 `auth.RequireInteractive()`（寫入要 `X-Jelly-CSRF`），讀取維持 `RequireAuth`。
- 決定：`POST /auth/logout` 維持接受 Bearer 撤銷（`service.go:794-797`），撤銷一把已外洩的 JWT 是好事，不是能力。
- 決定：登入 JSON 回應的 `token` 欄位本工單不拿掉（測試大量依賴，且值與 Cookie 相同），文件改為標示「與 Cookie 相同，只供寫入 Cookie 的客戶端使用；不能放進 `Authorization`」。拿掉另案。
- 不改：`/api/v1` 的金鑰驗證、`/auth/login`、`/auth/:provider`、OAuth 回呼、`/auth/login/2fa`、`/api/invites/accept`、`/api/password-reset`、金流回呼 `/api/payment/:gateway/*`。

## 規則

### R1 總閘門

`enforceStaffAdminAPIs`（`internal/server/admin_render.go`）的規則擴成：路徑為 `/api/admin`、`/api/auth`、`/api/account`、`/api/payment/create`、`/api/payment/order/*` 或其子樹（`/api/payment/:gateway/notify|callback|return` 除外），只要帶有任何 `Authorization` 標頭，在任何認證之前回 401，body `{"error": "Browser login required"}`。有 Cookie 或 Session 而沒有 `Authorization` 的請求行為不變。`GET /api/auth`、`/api/auth/openapi.json`、`/api/docs` 三條公開說明端點也套這條（帶標頭就 401），避免「有時收有時不收」。

### R2 互動限定

下列寫入改掛 `auth.RequireInteractive()`，非 GET／HEAD／OPTIONS 必須帶非空 `X-Jelly-CSRF`，否則 403 `{"error": "Interactive login required"}`：

- `PATCH /api/account/profile`、`POST /api/account/notifications/read-all`、`POST /api/account/notifications/:id/read`、`PUT /api/account/notification-preferences`
- `POST /api/account/orders/:number/cancel`、`POST /api/account/orders/:number/reorder`
- `POST|PUT|DELETE /api/account/addresses*`、`POST|PUT|DELETE /api/account/invoice-profiles*`
- `DELETE /api/auth/identities/:provider`（連結的 `start` 已經在 handler 內要求 Cookie，改成一致的中介層）
- `POST /api/payment/create`

讀取（`GET`）維持 `RequireAuth`。

### R3 `resolvePrincipal` 的 Bearer JWT 分支

`principal.go:52` 的 `resolveJWTPrincipal(db, bearer, principalBearer)` 保留，但只剩 `POST /auth/logout` 會走到（它不經 R1 閘門）。`principalBearer` 常數保留給 logout 與 OAuth 連結意圖（`oauth_accounts.go:325`）判斷。`auth.GetUserFromJWT` 刪除。

### R4 文件

- `internal/auth/api_documentation.go`：`securitySchemes` 的 `BearerAuth` 刪除（`:602`），所有 `access` 為 `self`、`interactive`、`admin`、`admin-interactive` 的操作改宣告 `CookieAuth`（`apiKey in cookie`，名稱 `jwt_token`），描述統一為「需要瀏覽器登入 Cookie；帶 `Authorization` 標頭一律 401」；`:241` 的「此互動流程禁止 Authorization 標頭」說明升級為所有端點共通。登入回應的 `token` 描述依「背景與決定」改寫。
- README：`:107` 的「無 Cookie 的 Bearer API 保留原有認證方式」改寫為「登入 JWT 只能放 Cookie；程式請用存取金鑰」；`docs/tickets/03-access-control.md`、`06-accounts.md`、`07-shop.md`、`23-admin-api-browser-only.md` 開頭各加一行指向本工單；`STATUS.md`。

## 程式（格子）

1. `internal/server/admin_render.go`：`enforceStaffAdminAPIs` 的路徑判斷抽成 `browserOnlyAPIPath(path string) bool`，涵蓋 R1 的清單；`/api/payment/:gateway/*` 用前綴 `/api/payment/` 加「下一段不是 `create` 或 `order`」排除。
2. `internal/server/account_routes.go`、`shop_account.go`、`internal/auth/routes.go`、`internal/server/handlers_payment.go`：依 R2 掛 `auth.RequireInteractive()`。
3. `internal/auth/service.go`：刪 `GetUserFromJWT`。`internal/auth/api_documentation.go`：依 R4。
4. `web/admin/profile.html`、`notifications.html`、`account.html`、`users.html`、`web/themes/default/assets/shop.js`：所有打 R2 端點的寫入請求確認帶 `X-Jelly-CSRF: 1`（後台已有 `api()` 包裝；`shop.js:239-266` 的 `api()` 對非 GET 已加此標頭，確認會員 API 走同一個包裝）。沒有畫面變更。
5. 測試輔助：`access_control_integration_test.go:212-233` 的 `setAccessToken` 對 `/api/account`、`/api/auth`、`/api/payment` 一律改用 Cookie 加 CSRF；`account_profile_integration_test.go:318-320,360,534-540`、`accounts_security_regression_test.go:107-110`、`login_attempts_integration_test.go:167,239`、`auth_revocation_test.go:113,235,530-539` 等以 Bearer 打這些端點的測試改成 Cookie，**斷言不變**（例如「舊 JWT 回 401」改成舊 JWT 放 Cookie 仍回 401）。這一步在規則實作前就要全綠。

## 驗收（測試契約）

新測試檔 `internal/server/account_api_browser_only_integration_test.go`，沿用 `admin_api_browser_only_integration_test.go` 的 `broDo` 與路由掃描寫法：

- 路由表：列舉 `e.Routes()` 中所有 `/api/auth*`、`/api/account*`、`/api/payment/create`、`/api/payment/order/*` 路由，對每一條以「合法登入 JWT 放 `Authorization: Bearer`」呼叫，一律 401 且 body 為 `Browser login required`；以 `jly_` 形狀的 Bearer 同樣 401；改用 Cookie 呼叫不被這條規則擋。`POST /auth/logout` 以 Bearer 仍可撤銷（回 2xx 且該 jti 之後放 Cookie 也 401）。`POST /api/payment/sandbox/notify` 之類的金流回呼帶 `Authorization` 不受影響（回它原本的狀態碼）。
- 互動限定：對 R2 每一條以 Cookie 無 `X-Jelly-CSRF` 的寫入回 403 `Interactive login required`；加標頭通過這條規則（不要求業務成功）。`GET /api/account/profile`、`GET /api/account/orders` 以 Cookie 不需要 CSRF。
- 未登入：打任一條回 401（不是 403）。
- 不變量：`GET /api/v1/me` 以存取金鑰仍 200；`GET /api/admin/posts` 以 Bearer 仍 401 `Browser login required`（既有規則不變）。
- `admin_api_browser_only_integration_test.go:292-301` 的 `TestAdminAPIBrowserOnlyInvariantAuthMeAcceptsBearer` 改名並反轉斷言：Bearer 打 `/api/auth/me` 回 401。
- OpenAPI：`/api/auth/openapi.json` 的 `components.securitySchemes` 沒有 `BearerAuth`、有 `CookieAuth`；沒有任何操作引用 `BearerAuth`。
- 靜態守門：`web/` 下沒有任何 `fetch` 對 `/api/account`、`/api/auth` 送 `Authorization`（`strings.Contains` 掃描 `.html`、`.js`，排除 `integrations.js` 顯示給使用者複製的文字）。
- 瀏覽器驗收：個人中心改顯示名稱、關閉一則通知、會員中心新增地址、取消一筆待付款訂單，四個動作在瀏覽器成功；同一動作用 curl 帶 Bearer 回 401。

## 不在本工單

- 登入 JSON 回應拿掉 `token`（要重寫約 41 處測試的登入流程）。
- `/auth/logout` 停收 Bearer。
- `/api/public/*` 匿名 API（工單 29）。

## 決策紀錄

- 用 401「有標頭就擋」而不是只對 Bearer JWT 回 403：和 `/api/admin` 同一條規則、同一段程式，客戶端只要記一件事；且 401 在認證前就回，不會先查資料庫。
- 寫入改掛 `RequireInteractive` 而不是只擋 Bearer：擋了 Bearer 之後寫入一定帶 Cookie，`ProtectCookieWrites` 會做來源檢查，但 CSRF 標頭仍是後台的既有規則，會員 API 沒理由比後台鬆。
- 保留 logout 的 Bearer：撤銷是降低風險的動作，而且不經閘門的實作最簡單。
- 不拿掉登入 JSON 的 `token`：拿掉是對的，但要動 41 處測試，與本工單「關門」的目標分開做風險較低。
