# 22 — 程式與 AI agent 共用一套機器 API：個人權杖併入存取金鑰，後台 API 不再收 jly_

Epic：帳號與存取
User Story：站長與開發者要讓程式（含 AI agent）呼叫網站時，只需要學一套 API 和一種憑證：在後台建立一個「存取金鑰」身分，勾選權限，拿到金鑰，打機器 API。後台 `/api/admin/*` 是給網頁用的，不再接受個人 API 權杖。
Status：第一階段（本工單）= 把個人 API 權杖（`jly_`）併入存取金鑰；後續階段 = 後台 API 只收網頁登入（含改寫測試輔助函式）、對外改名（網址、頁面、文字）。三階段的先後順序不可顛倒：先有地方去，再關門。
Blocked by：06（帳號、權杖）、14（agent API）。

## 背景與決定

- 目前有三種憑證：登入 JWT（Bearer）、個人 API 權杖 `jly_`（`api_tokens`，打 `/api/admin/*`）、agent 金鑰 `jla_`（`agent_keys`，打 `/api/agent/v1`）。`jly_` 與 `jla_` 都是 47 字元（前綴 4 + base64url 32 位元組），雜湊都是 SHA-256 十六進位。
- 決定：機器 API 只有 `/api/agent/v1` 一套（本階段不改網址與名稱，對外改名在後一階段）。個人權杖併入它。
- 決定：新發的金鑰一律 `jly_` 前綴；既有 `jla_` 金鑰繼續有效。
- 決定：只有管理員能建立存取金鑰身分（維持工單 14 現況）。原本非管理員自助建立個人權杖的能力會消失，這是已知且被接受的縮小。
- 決定：併入用資料遷移，不做雙軌解析。遷移後 `/api/admin/*` 不再解析 `jly_`。

## 資料：遷移 0026（不改 0025）

`0026_tokens_into_keys.sql`，冪等，同一筆交易：

0. 新增 `agents.role_cap VARCHAR(16)`，可為空，只允許 `admin`、`editor`、`author`。語意：這個身分執行時的角色不得高於 `role_cap`（取擁有者角色與 `role_cap` 中較低者）。空值代表不限制。
1. 放寬 `agent_keys_prefix_check`：接受 `^jla_[A-Za-z0-9_-]{8}$` 或 `^jly_[A-Za-z0-9_-]{4,8}$`。
2. 對每筆 `api_tokens`（無論已撤銷或過期）建立一個 `agents` 列與一個 `agent_keys` 列。已存在相同 `key_hash` 於 `agent_keys` 的 token 跳過（重跑不重複）。
3. `agents` 欄位：`name` = token 的 `name`；若與目前未刪除的 agent 名稱（不分大小寫）衝突，改為 `name || ' (' || username || ')'`，仍衝突再加 ` #2`、` #3`…；截斷到 80 字元且保持唯一。`description` = token 的 `note`。`owner_id`、`created_by` = token 的 `user_id`。`status` = `active`。`permissions` 依下表對應（去重、只含 0025 允許的值）。
3a. `role_cap`：token 的 scopes 含 `admin`、`editor`、`author` 時取其中最高者；只有 `content_read`／`orders_read` 時為空。這保住舊行為：舊個人權杖用 `author` 範圍時，登入身分會被降成作者（`projectedUser`），例如管理員擁有的 `author` 權杖仍不能改別人的文章；機器 API 執行時用擁有者的真實角色，若不加上限，併入後權限會被悄悄放大。
4. `agent_keys` 欄位：`key_hash` = `token_hash`；`prefix` = `'jly_' || last4`；`created_at`、`expires_at`、`revoked_at`、`last_used_at`、`last_used_ip_masked` 照抄；`created_by` = `user_id`；撤銷者 `revoked_by` 為空。已撤銷的 token 其金鑰同樣帶 `revoked_at`，agent 本身保持 `active`。
5. 不刪除 `api_tokens`（程式停止讀取，之後的遷移再移除）。

scope 對應：

| 個人權杖 scope | 存取金鑰權限 |
|---|---|
| `content_read` | `posts.read`、`pages.read` |
| `orders_read` | `orders.read` |
| `author` | `posts.read`、`posts.write`、`pages.read`、`media.read`、`media.write` |
| `editor` | `author` 的全部加 `pages.write` |
| `admin` | 0025 允許的全部 16 項 |
| 其他字串 | 忽略 |

權限最後有效範圍仍由擁有者角色決定（執行時交集，見 `agentPermissionMiddleware`、`agentMe`），遷移時不依角色裁剪。

## 程式

1. `agentCredential`（`internal/server/agents_auth.go`）：接受長度 47 且前綴 `jla_` 或 `jly_`。
1a. 角色上限：`agentKeyMiddleware` 設定 `c.Set("user")` 前，若 `agents.role_cap` 非空且比擁有者角色低，使用者副本的 `Role` 改為 `role_cap`；`agentPermissionMiddleware` 的 `owner_role_insufficient` 判斷與 `agentMe` 的 `effective_permissions` 都以這個『有效角色』計算。`agentMe` 回應新增 `role_cap`（可為 null）與 `effective_role`。
2. 建立與輪替金鑰（`internal/agent/agent.go` 的 `KeyPrefix` 與產生函式、`agents_admin.go`）：新金鑰前綴 `jly_`，顯示前綴 `jly_` 加 8 個字元；`jla_` 仍可驗證。
3. 後台 API 不再解析 `jly_`：`internal/auth/principal.go` 的 `jly_` 分支（`resolveTokenPrincipal`）改為拒絕（等同無效憑證，回 401）；移除 `middleware.go` 中 `content_read`／`orders_read` 的 GET 特例與 `RequirePermission` 內的權杖分支；`admin_render.go` 的 `enforceStaffAdminAPIs` 移除 api_token 分支；`handlers_ecommerce.go`、`handlers_admin.go` 中僅為權杖而設的讀取群組併回一般角色群組。登入 JWT（Bearer 與 cookie）行為不變。
4. 移除個人權杖功能：`/api/account/tokens*`（`account_routes.go`、`account_tokens.go`）端點下線（路由不存在，回 404）；個人資料頁的「API 權杖」分頁改為說明文字：管理員附連結到存取金鑰管理頁（`/admin/agents`），其他角色說明「請洽管理員建立存取金鑰」。`web/locales` 同步，兩個語系。
5. 文件：README 的後台 API 驗證說明改為：程式請使用存取金鑰打 `/api/agent/v1`（附 curl 範例）；`api_documentation.go` 與 `shop_api_documentation.go` 的 `ApiTokenAuth` 說明移除或改指向存取金鑰；`docs/tickets/06-accounts.md` 加一行註記本工單取代權杖段落。
6. 稽核：遷移後的金鑰與新金鑰的每次請求都寫 `agent_events`（既有行為，不變）。

## 驗收（測試契約）

新測試檔 `internal/server/api_keys_consolidation_integration_test.go` 與 `internal/data/tokens_into_keys_migration_test.go`：

- 遷移：多筆 token（含重名、含已撤銷、含已過期、含不同擁有者）對應出正確的 agents／agent_keys；名稱唯一且 ≤ 80；prefix 為 `jly_` 加 last4；權限依對應表；重跑冪等（列數不變）。
- 認證：遷移後以原本的明文權杖打 `GET /api/agent/v1/me` 回 200，`effective_permissions` 依擁有者角色裁剪；已撤銷回 401 `key_revoked`、已過期回 401 `key_expired`。
- 角色上限：管理員擁有、scope 為 `author` 的舊權杖，遷移後打 `/api/agent/v1/posts/:id`（PUT、DELETE）操作『別的作者的文章』回 403 或 404 且內容不變；操作自己的文章成功；`/me` 的 `effective_role` 為 `author`、`role_cap` 為 `author`；只有 `content_read` 的權杖 `role_cap` 為 null。原 `accounts_security_regression_test.go` 的權杖越權測試改寫成此行為，不得放寬斷言。
- 範圍：以遷移後的 `jly_` 權杖打 `/api/admin/posts`、`/api/admin/orders` 回 401（不再被當成後台憑證）。
- 新金鑰：建立 agent 後顯示的金鑰以 `jly_` 開頭、長度 47、顯示前綴 `jly_` 加 8 字元；`jla_` 舊金鑰仍可驗證。
- 下線：`GET|POST /api/account/tokens` 回 404；個人資料頁不再渲染權杖分頁的建立表單。
- 稽核：以遷移後金鑰打機器 API 後，`agent_events` 有一筆對應紀錄。
- 既有測試：`api_tokens_integration_test.go` 整個檔案改寫或刪除（該功能已移除）；`account_profile_integration_test.go`、`agent_*_test.go` 中對 `jla_` 前綴、權杖建立、權杖打後台的斷言同步調整，其餘斷言不得放寬。

## 不在本工單

- `/api/admin/*` 只收網頁登入、統一互動限定規則（下一階段；本機已有一份尚未提交的 WIP 在分支 `claude/unify-interactive-rule`）。
- 對外改名（網址 `/api/v1`、「串接」用語、頁面路徑、設計板文字）。
- 資料表、Go 套件的內部改名。
- 刪除 `api_tokens` 資料表。
