# 23 — 後台 API 只給網頁用：不再接受 Authorization 標頭，統一「互動限定」規則

Epic：帳號與存取
User Story：後台 `/api/admin/*` 是網頁介面的私有 API，只接受瀏覽器的登入狀態（Cookie 或 Session）。程式與 AI agent 一律使用存取金鑰打 `/api/agent/v1`（工單 14、22）。敏感操作（改密碼、2FA、使用者管理、備份還原、更新、外掛安裝、程式碼注入等）的「互動限定」只有一條規則，不再有三份各自的寫法。
Status：本工單為第二階段；第一階段是工單 22（個人權杖併入存取金鑰，已合併）。後續階段：對外改名（網址、「串接」用語）。
Blocked by：22。

## 背景

- 工單 22 之後，沒有任何個人權杖可用在後台 API，但登入 JWT 仍可放在 `Authorization: Bearer` 打 `/api/admin/*`（README 曾寫為正式用法，工單 22 已改為指向存取金鑰）。
- 目前有兩套「互動限定」：`auth.RequireInteractive()`（只擋 API 權杖，工單 22 後等於只做認證）與 server 內三份重複寫法（`browserSessionOnly`／`isBrowserSession`、`interactiveOnly`、`isInteractiveRequest`），要求不得帶 `Authorization`、有登入 cookie、寫入要有 `X-Jelly-CSRF`。
- 決定：後台 API 只收瀏覽器登入；`/api/auth/*`、`/api/account/*` 的 Bearer 行為不在本工單改變（登入、登出、OAuth 連結等）。

## 規則

1. 總閘門：`enforceStaffAdminAPIs`（`internal/server/admin_render.go`）對路徑為 `/api/admin` 或 `/api/admin/*` 的請求，只要帶有任何 `Authorization` 標頭，在任何認證之前直接回 401，body `{"error":"Browser login required"}`。有 Cookie 或 Session 而沒有 `Authorization` 的請求行為不變（含既有的來源檢查 `ProtectCookieWrites`）。
2. `auth.RequireInteractive()`（`internal/auth/middleware.go`）成為唯一的互動限定規則：解析身分失敗回 401；身分來源是 Bearer（`principalBearer`）回 403 `{"error":"Interactive login required"}`；方法不是 GET／HEAD／OPTIONS 時，必須帶非空的 `X-Jelly-CSRF` 標頭，否則 403（同一訊息）；通過後 `c.Set("user")`、`c.Set("auth.principal")` 與現況相同。
3. 刪除三份重複寫法（`browserSessionOnly`、`isBrowserSession`、`interactiveOnly`、`isInteractiveRequest`），所有使用處改用 `auth.RequireInteractive()`；程式碼注入 PUT 的「目前密碼」驗證保留。
4. 不改：`/api/agent/v1/*` 的金鑰驗證、`/api/auth/*`、`/api/account/*` 的認證來源、公開 API、付款通知。

## 呼叫端與測試

- 測試輔助函式：目前許多整合測試用 `Authorization: Bearer <JWT>` 打 `/api/admin/*`（主要在 `access_control_integration_test.go` 的 `accessHarness.token()`／`request`，另有 `tokTestReq`、`acctCall`、`agtAdmin` 等各檔自有輔助函式）。全部改成以 `jwt_token` Cookie 登入（寫入同時帶 `X-Jelly-CSRF` 以及測試環境下能通過來源檢查的標頭），行為與斷言不變。這一步在規則實作之前就必須全綠（現況程式接受 Cookie）。
- 仍須保留 Bearer 的測試：驗證 `/api/auth/*`、`/api/account/*` 的 Bearer 行為者，不改。
- `scripts/acceptance/mermaid.spec.mjs` 改為用登入回應的 Cookie 呼叫後台 API。
- 文件：README「管理後台 API」改成：後台 API 僅供網頁使用，程式請用存取金鑰；`docs/tickets/03-access-control.md`、`06-accounts.md`、`08-system.md` 中描述舊互動限定規則處加一行指向本工單；`STATUS.md` 記錄。

## 驗收（測試契約）

新測試檔 `internal/server/admin_api_browser_only_integration_test.go`：

- 路由表：列舉 `e.Routes()` 中所有 `/api/admin/*` 路由，對每一個以「合法登入 JWT 放在 Authorization: Bearer」呼叫，一律 401 且 body 為 `Browser login required`；以 `jla_`／`jly_` 形狀的 Bearer 同樣 401；改用 Cookie 登入呼叫，不被這條規則擋（不要求成功，只要求不是 `Browser login required`）。
- 互動限定：對一組代表性端點（`POST /api/account/password`、`POST /api/admin/users/invite`、`POST /api/admin/backups`、`PUT /api/admin/settings/injection`、`POST /api/admin/store/install`），Bearer 回 403；Cookie 無 `X-Jelly-CSRF` 的寫入回 403；Cookie 加 `X-Jelly-CSRF` 通過這條規則。GET 下載（例如 `GET /api/admin/logs/export`）以 Cookie 不需要 CSRF 標頭。
- 未登入：打任一互動端點回 401（不是 403）。
- 不變量：`GET /api/auth/me` 以登入 JWT Bearer 仍回 200；`GET /api/agent/v1/me` 以存取金鑰仍回 200。
- 既有測試（改為 Cookie 登入的版本）全數維持通過。

## 不在本工單

- 對外改名（`/api/v1`、「串接」用語、頁面與設計板文字）。
- 資料表、Go 套件的內部改名；刪除 `api_tokens` 資料表。
- 讓 `/api/account/*`、`/api/auth/*` 只收瀏覽器登入。
