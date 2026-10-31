# 06 — 站長與成員可以管理帳號：使用者與角色、個人帳號、API 權杖、通知中心、登入裝置、兩步驟驗證

Epic：帳號與存取
User Story：站長能邀請與管理成員、停用帳號、重設密碼、刪除時把內容轉給別人；每個成員能改自己的資料、密碼、語言，管理 API 權杖與登入裝置，開啟兩步驟驗證，在通知中心看到待審留言與安全事件。
Blocked by：03 的角色與 OAuth 身分（已合併）、04 的留言待審掛鉤 `OnCommentPending`（已合併）。
Status：implemented（本機實作、PostgreSQL 競態測試與 357 項瀏覽器檢查通過；交付與 CI 結果見 PR #24，線上帳號板已同步第 37 版，主代理已核對線上版本）
設計稿：`design/project/Users、Profile、Tokens、Notifications、Login.dc.html`；本工作單先補：登入的驗證碼步驟（`Login2FA`）、接受邀請（`InviteAccept`）、重設密碼（`PasswordReset`）、個人帳號的兩步驟驗證啟用區與備用碼、使用者刪除時的內容轉移面板；並把 `Profile` 板的分頁改成和 `Tokens`、`Notifications` 相同的六個分頁（基本資料、通知、API 權杖、密碼與安全、登入裝置、第三方帳號），語言只留繁中與 English。

驗收依據：主代理完整差異審查與競態全套、獨立複審、真實伺服器 D7 完整流程；詳細紀錄見 [STATUS.md](../../STATUS.md)。

## 流程與驗收

- [x] 站長在 `/admin/users` 看到全部／管理員／編輯／作者／顧客分段與計數、搜尋、角色 chip、最後登入（時間與裝置）、狀態（啟用／邀請中 · N 天後過期／停用）；能邀請（Email、角色、備註、7 天連結，寄不出去時可複製連結）、重寄或撤銷邀請、改角色、停用／啟用、重設密碼（1 小時連結）、登出該帳號所有裝置、批次處理、刪除（有文章、頁面、留言時必須指定轉移對象）。
- [x] 系統永遠保留至少一位啟用中的管理員：不能降級、停用、刪除最後一位；不能改自己的角色或刪除自己。被停用或邀請中的帳號不能登入，舊的 JWT 與 API 權杖立即失效。
- [x] 受邀者打開 `/invite/<token>` 設定帳號與密碼後成為啟用成員，連結只能用一次、過期無效；`/password-reset/<token>` 同樣一次性、1 小時內有效，完成後該帳號其他登入全部登出。
- [x] 成員在 `/admin/account` 改顯示名稱（前台署名）、簡介、頭像（png／jpeg／webp ≤ 2 MB）、介面語言（繁中／English）、外觀（跟系統／淺／深）、Email（要輸入密碼，新信箱以連結確認，寄不出去時站長可在使用者頁看到待確認狀態）、密碼（改完其他裝置全部登出）；只用 OAuth 登入的帳號可以直接設定密碼。
- [x] 成員能建立 API 權杖：名稱、用途、到期（7／30／90 天、1 年、永不）、角色範本多選（作者、內容唯讀、訂單唯讀、管理員）；權杖只顯示一次，之後只看得到末四碼、最後使用時間與遮罩 IP；能編輯名稱、撤銷、恢復、刪除（只能刪已撤銷的）。權杖的權限不會超過擁有者目前的角色；權杖不能管理權杖、密碼、兩步驟驗證、登入裝置或第三方帳號。
  - 已由工單 22 取代：個人權杖併入存取金鑰，`/api/account/tokens*` 已下線。
- [x] 成員在通知中心看到全部／未讀／留言／訂單／系統／安全分段與計數、同一群組的通知合併（「2 則新留言待審」）、就地動作（去審核、全部核准）、全部標為已讀；通知保留 90 天；能設定 8 種事件在站內／Email／Webhook 的開關、Webhook 網址（只接受 https，拒絕內網）、勿擾時段。
- [x] 待審留言會通知管理員與編輯；新裝置登入會發安全通知給本人；邀請被接受會通知管理員。
- [x] 成員看到自己的登入裝置（瀏覽器／系統、遮罩 IP、建立與最近活動時間、「這台」標記、新裝置標記），能登出單一裝置或其他所有裝置；登出會把該登入標記為已撤銷。
- [x] 成員能啟用兩步驟驗證（顯示密鑰與 QR、輸入一次驗證碼後啟用、一次性顯示 10 組備用碼）、重新產生備用碼、關閉（要密碼加驗證碼或備用碼）。啟用後本機登入分兩步：密碼正確後拿到 5 分鐘的 `mfa_token`，再送驗證碼或備用碼；錯 5 次鎖 15 分鐘；同一個時間片的驗證碼不能重複使用。OAuth 登入信任提供者，不再要求驗證碼。
- [x] 只有 admin、editor、author 能進 `/admin/*` 頁面與 `/api/admin/*`；其他角色（例如商店工作單將建立的 `customer`）即使登入也拿到 403，頁面導向首頁。OAuth 第一次登入建立的帳號角色取自選項 `oauth_default_role`（預設 `author`），不再寫死。
- [x] 後台頁面照設計稿：使用者與角色（含邀請面板、權限矩陣、刪除轉移面板）、個人帳號六個分頁、通知中心（列表與偏好）、登入頁的驗證碼步驟、接受邀請與重設密碼頁；側欄「使用者」入口（系統區、僅 admin）與 ⌘K「邀請使用者」指令。

## 錯誤與邊界

未登入 401、角色不足 403、權杖打到互動限定的端點 403、找不到或不是自己的資源 404、最後一位管理員或自己 409、Email 已存在 409、刪除有內容的使用者沒給轉移對象 409（回傳 posts／pages／comments 數量）、驗證碼錯 401、鎖定中 429、邀請或重設連結過期或已用 410。所有令牌只存 SHA-256 雜湊；TOTP 密鑰以 AES-GCM 加密，金鑰存在 `data/auth-secrets.json` 的新欄位 `totp_key`，不隨 JWT 密鑰輪替而失效。IP 一律遮罩後儲存（IPv4 去最後一段、IPv6 保留前 48 位元）。沒有 SMTP 寄件設定時（寄件設定在工作單 08），邀請、重設、Email 確認都回傳可複製的連結，Email／Webhook 投遞先進待送表。

## 契約（骨架與測試共同依據）

### 資料表（`internal/data/migrations/0002` 到 `0007`，嵌入 SQL，和 `migrateOAuthIdentities` 同一種跑法；全部可重跑）

| 表 | 主要欄位 | 備註 |
|---|---|---|
| users（ALTER） | `display_name varchar(120)`、`bio text`、`locale varchar(10)` 預設 `zh-TW`、`ui_theme` system／light／dark、`status` active／invited／disabled 預設 active、`disabled_at`、`last_login_at`、`password_changed_at`、`email_verified_at`、`pending_email`、`pending_email_hash`、`pending_email_expires_at` | `role` 預設改為 `author`；Go 端 `core.User` 加對應欄位，pending 欄位 `json:"-"` |
| login_sessions | `id char(64)` = `hashLoginID(jti)`（與 `revoked_logins.id` 同值）、`user_id`、`method`、`device_label`、`ua_hash`、`ip_masked`、`created_at`、`last_seen_at`、`expires_at`、`revoked_at` | 不存原始 jti |
| api_tokens | `id`、`user_id`、`name`、`note`、`token_hash char(64)` unique、`last4`、`scopes text[]`、`expires_at`、`revoked_at`、`last_used_at`、`last_used_ip_masked`、`created_at` | 權杖 = `jly_` + base64url(32 bytes) |
| notifications | `id`、`user_id`、`kind`、`category`（comments／orders／system／security）、`title_key`、`params jsonb`、`actions jsonb`、`group_key`、`count`、`read_at`、`created_at`、`expires_at`（＋90 天） | 未讀且同 `group_key` 唯一，用來合併 |
| notification_settings | `user_id` PK、`webhook_url`、`quiet_start`、`quiet_end`、`quiet_tz` | |
| notification_prefs | `(user_id, event)` PK、`in_app`、`email`、`webhook` | 沒有列代表預設（站內開、其他關） |
| notification_deliveries | `id`、`notification_id`、`channel`（email／webhook）、`status`（pending／sent／failed／held）、`attempts`、`next_attempt_at`、`error` | 待送表；寄件器是介面，預設只記錄 |
| user_totp | `user_id` PK、`secret_enc bytea`、`enabled_at`、`last_step bigint`、`failed_count`、`locked_until` | `enabled_at` 為空代表啟用中尚未驗證 |
| user_recovery_codes | `id`、`user_id`、`code_hash`、`used_at` | 每組 10 個 |
| user_invites | `id`、`user_id`、`kind`（invite／reset／email）、`token_hash`、`role`、`note`、`invited_by`、`expires_at`、`accepted_at`、`revoked_at` | 每個使用者每種 kind 只有一列有效 |

### 自助 API（`auth.RequireAuth`，所有角色；標 I 的為互動限定，拒絕 API 權杖）

已由工單 23 取代：互動限定統一由 `auth.RequireInteractive()` 判斷，Bearer 登入也會被拒絕（403）（見 [工單 23](23-admin-api-browser-only.md)）。

| # | 方法與路徑 | 請求 | 回應與規則 |
|---|---|---|---|
| S1 | GET `/api/account/profile` | | `{id, username, display_name, email, email_verified, pending_email, avatar_url, bio, locale, ui_theme, role, created_at, post_count, author_url}` |
| S2 | PATCH `/api/account/profile` | `display_name` 1–120、`bio` ≤ 500、`locale` zh-TW／en、`ui_theme` | 200 同 S1；`id`、`role`、`status`、`email`、`password` 一律忽略 |
| S3 | POST／DELETE `/api/account/avatar`（I） | multipart png／jpeg／webp ≤ 2 MB | 200 `{avatar_url}` |
| S4 | POST `/api/account/email`（I） | `{new_email, current_password}` | 202 `{confirm_url?}`；已被使用 409；`GET /account/email/confirm?token=` 完成 |
| S5 | POST `/api/account/password`（I） | `{current_password?, new_password ≥ 8}` | 200；只用 OAuth 的帳號可省略 `current_password`；撤銷其他登入；發 `security.password_changed` |
| S6 | GET `/api/account/2fa` | | `{enabled, enabled_at, recovery_remaining}` |
| S7 | POST `/api/account/2fa/enroll`（I） | | `{secret, otpauth_uri, qr_svg}`，覆蓋未驗證的舊密鑰 |
| S8 | POST `/api/account/2fa/verify`（I） | `{code}` | 啟用並回 `{recovery_codes:[10]}`（唯一一次） |
| S9 | POST `/api/account/2fa/disable`（I） | `{current_password, code 或 recovery_code}` | 200；刪除密鑰與備用碼 |
| S10 | POST `/api/account/2fa/recovery-codes`（I） | `{code}` | 新 10 組，舊的全失效 |
| S11 | GET `/api/account/sessions` | | `[{id（雜湊前 16 碼）, device_label, ip_masked, created_at, last_seen_at, current, new_device}]` 只列有效 |
| S12 | DELETE `/api/account/sessions/:id`（I） | | 撤銷；別人的 404 |
| S13 | POST `/api/account/sessions/revoke-others`（I） | | 撤銷目前以外全部 |
| S14 | GET `/api/account/tokens` | | `[{id, name, note, last4, scopes, expires_at, last_used_at, last_used_ip_masked, status}]`（active／expired／revoked） |
| S15 | POST `/api/account/tokens`（I） | `{name 1–80, note, expires_in_days: 7／30／90／365／null, scopes[]}` | 201 含 `token`（唯一一次）；scopes 不能超過擁有者角色 |
| S16 | PATCH `/api/account/tokens/:id`（I） | `{name, note}` | scopes 與到期不可改 |
| S17 | POST `/api/account/tokens/:id/revoke`、`/restore`（I） | | 恢復只清 `revoked_at`，過期的仍是過期 |
| S18 | DELETE `/api/account/tokens/:id`（I） | | 只有已撤銷可刪，否則 409 |
| S19 | GET `/api/account/notifications` | `category, unread=1, before, limit ≤ 50` | `{items, unread_count, counts:{all,unread,comments,orders,system,security}}` |
| S20 | POST `/api/account/notifications/:id/read`、`/read-all?category=` | | 只動自己的；別人的 404 |
| S21 | GET／PUT `/api/account/notification-preferences` | `{events:{<event>:{in_app,email,webhook}}, webhook_url, quiet:{enabled,start,end,tz}}` | webhook 必須 https 且非內網／回送位址 |
| S22 | POST `/auth/login/2fa`（公開，套 `ProtectBrowserWrites` 與 `ProtectLoginAttempts`） | `{mfa_token, code 或 recovery_code}` | 成功發登入；錯 401；鎖定 429 |

### 管理 API（`auth.RequireRole("admin")`）

| # | 方法與路徑 | 規則 |
|---|---|---|
| A1 | GET `/api/admin/users` | `q, role, status, page, per_page ≤ 100` → `{items:[{id, username, display_name, email, avatar_url, role, status, last_login_at, last_login_device, invite_expires_at, pending_email}], total, counts:{all, role:{...}, status:{...}}}`；`q` ILIKE username／display_name／email |
| A2 | POST `/api/admin/users/invite` | `{email, role, note}` → 201 `{id, invite_url, expires_at, emailed}`；建立 `status=invited`、無密碼、username 由 Email 推導且唯一；Email 重複 409；角色未知 400 |
| A3 | POST `/api/admin/users/:id/invite/resend`、`/invite/revoke` | 重寄換新 token；撤銷刪除邀請中的列 |
| A4 | PATCH `/api/admin/users/:id` | 只接受 `{role, status: active／disabled, display_name}`；最後一位啟用管理員不能降級或停用；不能改自己 → 409 |
| A5 | POST `/api/admin/users/:id/reset-password` | 1 小時連結 `{reset_url, emailed}`；使用時撤銷該帳號全部登入 |
| A6 | POST `/api/admin/users/:id/revoke-sessions` | 撤銷全部 |
| A7 | DELETE `/api/admin/users/:id?reassign_to=` | 有文章／頁面／留言而沒給對象 → 409 `{posts, pages, comments}`；對象必須啟用且可寫作；同一交易內轉移、刪 OAuth 身分（帶操作者寫稽核）、撤銷登入；保留歷史版本內容並將已刪除的版本製作者關聯設為 NULL；有訂單的帳號回 409，保留訂單與買家關聯並提示先停用；不能刪自己或最後一位管理員 |
| A8 | POST `/api/admin/users/bulk` | `{ids ≤ 100, action: disable／enable／set_role／delete}`，逐列套同樣規則，回 `{updated, skipped:[{id, reason}]}` |
| A9 | GET `/api/admin/roles` | 權限矩陣（唯讀，給設計板的表格） |
| A10 | 公開 `GET /invite/:token`、`POST /api/invites/accept {token, username?, password, display_name?}`、`GET /password-reset/:token`、`POST /api/password-reset {token, password}` | token 單次；過期或已用 410；接受後 `status=active`、`email_verified_at` 設定 |

### 通知（新套件 `internal/notify`）

`notify.Emit(ctx, Event{Kind, Params, GroupKey, Recipients})`：解析收件人（角色或使用者 ID）、套偏好、寫站內列（未讀同 `group_key` 合併並累加 `count`）、寫 Email／Webhook 待送列（勿擾時段標 `held`）。`Kind` 對應分類與 8 個偏好事件：`comment.pending`、`comment.reply`、`order.new`、`order.payment_failed`、`security.new_device`、`security.password_changed`、`system.update_available`、`system.plugin_failed`。本工作單接上 `server.OnCommentPending`（收件人 admin＋editor，`group_key` = `comment.pending:<post_id>`）、`security.new_device`（`issueLogin` 內 90 天沒看過的 `device_label`）、`user.invite_accepted`（歸 system）；`server.UnreadNotifications` 計算未讀未過期列。`notify.Sender` 介面有 `Send(Delivery) error`，預設實作只記錄並標 `held`。

### `internal/auth` 的變更

1. `principal.go`：Bearer 以 `jly_` 開頭走 `resolveTokenPrincipal`（雜湊查表、未撤銷、未過期、擁有者 `status=active`），principal 加 `Scopes`、`TokenID`；權杖 principal 在 `principalMatchesIntent`、`revalidatePrincipal` 與 OAuth 連結／解除一律失敗。
2. `loadPrincipal`：`status != active` 一律拒絕；`last_seen_at` 每 5 分鐘最多寫一次；權杖 `last_used_at` 每分鐘最多一次。
3. `middleware.go`：`RequireRole` 改比對 `effectiveRoles(principal)`（session／JWT 為 `{user.Role}`；權杖為範本聯集且不超過擁有者角色）；新增 `RequirePermission(perm...)` 給 `content_read`、`orders_read`（先套在 GET `/api/admin/posts`、`/api/admin/orders`）；新增 `RequireInteractive` 套在 S3–S5、S7–S18、A2–A8。
4. `service.go`：`issueLogin` 寫 `login_sessions`、更新 `last_login_at`、新裝置發通知；`HandleLocalLogin` 只要有密碼就可本機登入（OAuth 帳號設了密碼後可用）；密碼正確後才檢查 `status`（停用／邀請中 → 403 `account_disabled`）；啟用 TOTP 的帳號回 `200 {mfa_required:true, mfa_token}` 不發 Cookie；OAuth 回呼與復原路徑也檢查 `status`，不要求 TOTP。
5. 新 `totp.go`：HMAC-SHA1、30 秒、±1 步、常數時間比對、`last_step` 防重放、5 次失敗鎖 15 分鐘；匯出 `TOTPCodeAt(secret, t)` 給測試；`mfa_token` 是 5 分鐘、用途綁定的短 JWT，`jti` 用 `revoked_logins` 的型別前綴雜湊消費。
6. `secrets.go`：`auth-secrets.json` 加 `totp_key`，缺少時產生，版本相容。
7. `revocation.go`：新增 `revokeLoginHashes(hashes, expiresAt)`、`RevokeUserLogins(userID, exceptHash)`；`HandleLogout` 同時寫 `login_sessions.revoked_at`。
8. `login_attempts.go`：`/auth/login/2fa`、接受邀請、重設密碼都套 `ProtectLoginAttempts`。
9. `api_documentation.go`：補 S／A 端點與 `ApiTokenAuth` bearer scheme，`/api/docs` 作為別名。
10. 新路由檔 `internal/server/account_routes.go`、`users_routes.go`，在 `/:slug` 之前註冊；側欄 `RegisterNav{Key:"users", Section:"system", Roles:["admin"]}`，⌘K `invite-user`。
11. `internal/server/routes.go` 的 `/admin` 群組與 `renderAdmin` 加 `auth.RequireRole("admin","editor","author")`（頁面：非員工 302 到 `/`；API：403）；`internal/auth` 的 `createOAuthUser` 角色改讀選項 `oauth_default_role`，預設 `author`，值必須是已知角色。

### 頁面

`/admin/users`（`web/admin/users.html`）、`/admin/account?tab=profile|notifications|tokens|security|devices|identities`（`web/admin/profile.html`，六分頁；現有 `/account` 第三方帳號獨立頁改為導向 `identities` 分頁）、`/admin/notifications`（`web/admin/notifications.html`）、`/login` 的驗證碼步驟（`web/admin/login.html`）、`/invite/:token` 與 `/password-reset/:token`（`web/admin/invite.html`、`password_reset.html`，沿用 `account_layout.html`）。前台會員中心的帳號與密碼、通知分頁由工作單 07 商店在主題側套同一組 API。

## 我先決定了這些

- 一次做完使用者、個人帳號、登入裝置、權杖、通知、兩步驟驗證；自訂角色（沒有設計板）延後。
- 沒有 SMTP：全部回可複製連結，Email／Webhook 先進待送表；寄件設定與投遞工作在工作單 08。
- OAuth 登入信任提供者，不再要求 TOTP；備用碼只在產生時顯示一次，「下載」等於重新產生。
- 權杖恢復後過期的仍是過期；內容唯讀與訂單唯讀只放行 GET。
- 語言只有繁中與 English；登入裝置只顯示遮罩 IP，不做地理位置；勿擾時段每人自訂。
- 新帳號預設角色 `author`；`customer` 角色由工作單 07 建立，永遠進不了 `/admin`。
- 設計板先補齊再動程式：改 `design/gen/gen.py` 與 `gen_halo.py`，不直接改 `.dc.html`。

## 分段實作與驗收

每段先由測試代理照本文件建立失敗案例（已隨本工作單交付），實作代理依契約實作，主代理審查後以真實伺服器與 Playwright 驗收。

1. D0 設計板：Profile 六分頁與語言、Login2FA、InviteAccept、PasswordReset、Profile 的兩步驟驗證區、Users 的刪除轉移面板；`canvas.json` 加進對應排；canvas 發佈。
2. D1 資料與遷移：`0002`–`0007`、`core.User` 欄位、`internal/data` 測試（可重跑、約束、串聯刪除）。
3. D2 使用者管理：A1–A10、最後管理員保護、刪除轉移、`users.html`。測試檔 `users_admin_integration_test.go`。
4. D3 個人帳號與登入裝置：S1–S5、S11–S13、`issueLogin` 寫入、`account.html` 基本資料／密碼與安全／登入裝置分頁。測試檔 `account_profile_integration_test.go`、`login_devices_integration_test.go`。
5. D4 API 權杖：S14–S18、權杖 principal、`RequirePermission`、`RequireInteractive`、`/api/docs`、`account.html` 權杖分頁。測試檔 `api_tokens_integration_test.go`。
6. D5 通知：`internal/notify`、S19–S21、三個生產者、鈴鐺計數、`notifications.html` 與偏好分頁。測試檔 `notifications_integration_test.go`。
7. D6 兩步驟驗證：S6–S10、S22、`totp.go`、`secrets.go`、登入頁步驟。測試檔 `totp_integration_test.go`、`internal/auth/totp_test.go`。
8. D7 驗收：真實伺服器走完邀請 → 接受 → 登入 → 啟用 2FA → 登出其他裝置 → 建權杖打 API → 待審留言出現在通知中心 → 停用帳號後權杖失效。
