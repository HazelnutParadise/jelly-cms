> 網址與名稱已由工單 [24](24-api-v1-rename.md) 取代：機器 API 改為 `/api/v1`，「AI agent」改稱「API 串接」，「agent 金鑰」改稱「存取金鑰」。

# 14 — 站長可以發給 AI agent 專用金鑰：限定權限、可輪替與撤銷、agent 自己找得到 API 說明、每個動作都有紀錄

Epic：帳號與存取
User Story：站長在後台建立 AI agent 專用身分，勾選它能做的事，拿到一把只顯示一次的金鑰交給 agent；需要時立即或給一段過渡期輪替金鑰、提前撤銷舊金鑰、停用或刪除 agent。agent 拿著金鑰打 `/api/agent/v1`，不需要人教就能從說明端點與 OpenAPI 找到可以做什麼；寫入時帶上讀到的版本，不會蓋掉別人的修改。站長事後能依 agent 查出它做過的每一件事，包含來源 IP 與所用金鑰，跨越輪替也查得到。
來源：GitHub issue HazelnutParadise/jelly-cms#6。
Blocked by：無。PR #31 已整合 main `2e1236b`，帳號、系統、商店、編輯器與 WebMCP 的相依整合見「與既有 main 的調和」。
Status：已實作並完成本機功能驗收；必要 CI、線上畫布同步及合併清理結果見 STATUS.md 與 PR #31。
設計稿：`design/project/Agents.dc.html`（清單、建立、金鑰一次性顯示與關閉後的狀態）、`design/project/AgentDetail.dc.html`（詳情、金鑰與過渡倒數、輪替歷史、活動紀錄、輪替對話框的兩種模式、過渡時長錯誤與新金鑰一次性顯示、提前撤銷確認、停用與刪除確認）。兩張板由 `design/gen/gen_agents.py` 產生（`PYTHONDONTWRITEBYTECODE=1 python3 gen_agents.py`，在 `design/gen` 下執行），放在 `canvas.json`「後台 · 帳號與系統」那一排 x = 28880、30400（讓出 06 的 Login2FA、InviteAccept、PasswordReset 位置）。側欄的「AI agent」入口只畫在這兩張板；其他產生板的側欄在下次改 `gen.py` 的 `NAV` 時一起補上。

## 流程與驗收

- [x] 管理員在 `/admin/agents` 看到所有 agent：名稱與說明、權限、擁有者、目前生效金鑰的前綴、過渡中的舊金鑰與剩餘時間、最後使用時間、啟用／停用狀態；擁有者角色不足以使用某些權限時標出「有效權限 N/M」。非管理員打開這頁得到 403。
- [x] 管理員能建立 agent：名稱（1–80 字）、說明（≤ 500 字）、權限勾選；建立後畫面一次性顯示完整金鑰並提供複製按鈕；關閉後清單與詳情只看得到前綴（`jla_` 加 8 個字元），重新整理、重開詳情、打 API 都拿不到明文，資料庫也查不到明文。
- [x] 管理員在 `/admin/agents/:id` 輪替金鑰：選「立即失效」（預設）時舊金鑰下一次請求就 401；選「過渡期」並填 1–1440 分鐘時新舊金鑰都能用，期滿舊金鑰 401；填 0 或超過 1440 被拒，畫面與 API 都說明「過渡期必須是 1 到 1440 分鐘（最長 24 小時）」。新金鑰一次性顯示。輪替後 agent 的 ID、名稱、說明、權限、擁有者、活動紀錄都不變。
- [x] 管理員能對過渡中的舊金鑰「提前撤銷」，撤銷後那把金鑰下一次請求 401；詳情頁顯示過渡中舊金鑰的到期倒數。
- [x] 管理員能停用、重新啟用、刪除 agent：停用後該 agent 所有金鑰（含過渡中的舊金鑰）下一次請求 401；重新啟用後未撤銷、未過期的金鑰恢復可用；刪除後金鑰全部失效、清單與詳情不再出現，但活動紀錄保留並可依 agent 查詢。
- [x] 輪替、撤銷、建立、修改、停用、啟用、刪除都寫進活動紀錄：誰（管理員）、何時、哪個 agent、輪替選了哪種模式與多長過渡期、新舊金鑰前綴。詳情頁有「輪替歷史」。
- [x] agent 不帶金鑰 `GET /api/agent/v1` 拿到完整說明：驗證方式、每個權限對應的端點、錯誤格式與錯誤碼、範例；`GET /api/agent/v1/openapi.json` 是 OpenAPI 3.1.0，每個 path 都真的存在，宣告 bearer 驗證。兩者由同一份路由表產生。
- [x] agent 帶金鑰 `GET /api/agent/v1/me` 看到自己的 id、名稱、權限、目前有效權限（和擁有者角色交集後）、所用金鑰的前綴與到期時間。
- [x] agent 有 `posts.write` 能建立文章；只有 `posts.read` 時建立文章得到 403，錯誤的 `details.missing_permissions` 是 `["posts.write"]`。每個 agent 端點恰好宣告一個所需權限，由 middleware 檢查。
- [x] 擁有者被降級後，agent 下一次請求就只能做擁有者新角色也能做的事（其餘 403 `owner_role_insufficient`）；擁有者被刪除後所有金鑰 401 `owner_unavailable`。
- [x] agent 更新文章、頁面、商品時必須帶 `base_updated_at`（讀取時拿到的 `updated_at`）；缺少回 400 `version_required`；兩個請求帶同一個舊版本更新同一篇，只有一個成功，另一個 409 `version_conflict`。`/api/admin` 的行為不變。
- [x] agent 金鑰打 `/api/admin/*`（含 `/api/admin/agents/*`）一律 401；agent 沒有任何管理 agent 或輪替自己金鑰的端點；人的登入憑證（JWT、cookie）打 `/api/agent/v1` 也是 401。
- [x] agent 有 `logs.read` 時能以 `GET /api/agent/v1/logs` 查自己的請求紀錄（cursor 分頁、`total`、依狀態、模組、時間篩選）；管理員在詳情頁與 `GET /api/admin/agents/events` 看全部紀錄，可依 agent 篩選並跨越輪替，看得到遮罩後的來源 IP 與金鑰前綴。紀錄不存完整請求本文與回應內容。
- [x] agent API 的所有錯誤都是 `{"code","message","details","correlation_id"}`，回應帶 `X-Request-ID`，與 `correlation_id` 相同。
- [x] repo 附 `skills/jelly-cms/SKILL.md`：說明怎麼連線（金鑰放哪）、怎麼探索（`/api/agent/v1`、`openapi.json`、`/me`）、常見操作（讀文章、建立草稿、帶 `base_updated_at` 更新、處理 409、查自己的紀錄）。
- [x] 側欄「系統」區有「AI agent」入口（僅 admin），在「使用者」與「設定」之間。

## 錯誤與邊界

所有金鑰只存 SHA-256 雜湊與前綴；明文只出現在建立與輪替的那一次回應。IP 一律遮罩後儲存（IPv4 去最後一段、IPv6 保留前 48 位元）。過渡期 1–1440 分鐘；一個 agent 同時最多兩把有效金鑰（生效中 1 把＋過渡中 1 把），再輪替時更早的過渡金鑰立即撤銷。不做排程自動輪替、MCP server、第三方 OAuth 授權流程、agent 呼叫外部 LLM。

## 契約與驗收依據

### 名詞

- **agent**：`agents` 一列。擁有者 = 建立者（必為建立當下的 admin）。所有 admin 都能管理所有 agent。
- **金鑰**：`jla_` + base64url（無補位，RFC 4648 §5）編碼的 32 個隨機位元組，共 47 字元，符合 `^jla_[A-Za-z0-9_-]{43}$`。**前綴** = 明文前 12 個字元（`jla_` + 8 個字元）。**雜湊** = 明文（含 `jla_`）的 SHA-256 小寫十六進位，64 字元。
- **金鑰狀態**（計算值）：`revoked`（`revoked_at` 非空）→ `expired`（`expires_at` ≤ 現在）→ `grace`（`expires_at` 非空）→ `active`，依此順序判斷。
- **有效權限**：agent 持有的權限中，擁有者目前角色出現在該權限 `required_roles` 裡的那些。擁有者不存在時為空。

### 資料表（`internal/data/migrations/0025_agents.sql`，嵌入 SQL，可重跑；由 `MigrateDB` 在 `migrateOAuthIdentities` 之後套用，鎖 `pg_advisory_xact_lock(731984201, 25)`）

| 表 | 欄位 | 約束與索引 |
|---|---|---|
| `agents` | `id bigint identity pk`、`name varchar(80) not null`、`description text not null default ''`、`permissions text[] not null default '{}'`、`owner_id bigint null`、`created_by bigint null`、`status varchar(16) not null default 'active'`、`disabled_at timestamptz`、`created_at`、`updated_at timestamptz not null default now()`、`deleted_at timestamptz` | `owner_id`、`created_by` → `users(id) ON DELETE SET NULL`；`status IN ('active','disabled')`；唯一索引 `lower(name) WHERE deleted_at IS NULL` |
| `agent_keys` | `id bigint identity pk`、`agent_id bigint not null`、`key_hash char(64) not null`、`prefix varchar(12) not null`、`created_at timestamptz not null default now()`、`created_by bigint null`、`last_used_at timestamptz`、`last_used_ip_masked varchar(64) not null default ''`、`expires_at timestamptz`、`revoked_at timestamptz`、`revoked_by bigint null` | `agent_id` → `agents(id) ON DELETE RESTRICT`；`created_by`、`revoked_by` → `users(id) ON DELETE SET NULL`；唯一 `key_hash`；索引 `(agent_id)` |
| `agent_events` | `id bigint identity pk`、`at timestamptz not null default now()`、`actor_type varchar(8) not null`、`actor_user_id bigint null`、`agent_id bigint not null`、`agent_name varchar(80) not null`、`key_id bigint null`、`key_prefix varchar(12) not null default ''`、`action varchar(32) not null`、`module varchar(16) not null`、`method varchar(8) not null default ''`、`route varchar(255) not null default ''`、`status int not null default 0`、`ip_masked varchar(64) not null default ''`、`summary jsonb not null default '{}'`、`request_id varchar(64) not null default ''` | `actor_type IN ('user','agent')`；`actor_user_id` → `users(id) ON DELETE SET NULL`；`agent_id` → `agents(id) ON DELETE RESTRICT`；索引 `(agent_id, id DESC)`、`(at DESC)` |

- 刪除 agent 是軟刪除（設 `deleted_at`、同一交易撤銷所有金鑰），`agents` 列與 `agent_events` 永遠保留；`agent_name` 是事件當下名稱的快照。
- 第一版不清除 `agent_events`；`summary` 序列化後 ≤ 2 KiB。
- Go 模型放 `internal/core/agent.go`（`Agent`、`AgentKey`、`AgentEvent`），`key_hash` 欄位 `json:"-"`。

### `internal/agent` 套件（純函式）

```go
const KeyPrefix = "jla_"                    // 金鑰開頭
const MinGraceMinutes, MaxGraceMinutes = 1, 1440
type Permission string                       // "posts.read" 等
func AllPermissions() []Permission           // 權限表的 16 個，依權限表順序
func ValidPermission(name string) bool
func (p Permission) Resource() string        // "posts.write" → "posts"
func GenerateKey() (string, error)           // crypto/rand 32 bytes → jla_ + base64url
func HashKey(plaintext string) string        // SHA-256 小寫 hex
func PrefixOf(plaintext string) string       // 前 12 字元；短於 12 回空字串
func MaskIP(ip string) string                // "198.51.100.77" → "198.51.100.0"；"2001:db8:abcd:12::5" → "2001:db8:abcd::"；IPv4 對映的 IPv6（::ffff:a.b.c.d）當 IPv4；無法解析回 ""
func ValidGraceMinutes(n int) bool           // 1 ≤ n ≤ 1440
```

### 權限 → agent 端點 → 重用的 handler → 原本的角色要求

全部端點在 `/api/agent/v1` 之下。「重用的 handler」是 `/api/admin` 上同一個 Go 函式（目前寫成匿名函式的要抽成具名函式，兩處掛同一個）；「角色」是那個 handler 在 `/api/admin` 註冊時所在群組的 `RequireRole` 角色，agent 路由表必須引用同一個角色變數，不得另寫一份字面值。C = `admin, editor, author`（`content` 群組），A = `admin`（`api` 群組），E = `admin, editor`：handler 內部另外排除作者時（`createPost` 對 `type=page` 拒絕作者），角色取群組角色與 handler 內部限制的交集，讓 `effective_permissions` 只列出擁有者真的做得到的權限；E 定義成具名變數，handler 內部的限制也改讀它。

| 權限 | agent 端點 | 重用的 handler（`/api/admin` 原路由） | 角色 | 備註 |
|---|---|---|---|---|
| posts.read | `GET /posts` | `listPosts`（`GET /posts`） | C | 固定 `type=post` |
| posts.read | `GET /posts/:id` | `getPost`（`GET /posts/:id`） | C | 不是 `type=post` → 404 |
| posts.read | `GET /categories` | `listCategories`（`GET /categories`） | C | 指定分類用 |
| posts.read | `GET /tags` | `listTags`（`GET /tags`） | C | 指定標籤用 |
| posts.write | `POST /posts` | `createPost`（`POST /posts`） | C | `type` 省略即 `post`，其他值 400 `type_mismatch` |
| posts.write | `PUT /posts/:id` | `updatePost`（`PUT /posts/:id`） | C | 版本防覆蓋；改 `type` → 400 `type_mismatch` |
| posts.write | `DELETE /posts/:id` | `deletePost`（`DELETE /posts/:id`） | C | 進回收桶 |
| pages.read | `GET /pages` | `listPosts` | C | 固定 `type=page` |
| pages.read | `GET /pages/:id` | `getPost` | C | 不是 `type=page` → 404 |
| pages.write | `POST /pages` | `createPost` | E | `type` 省略即 `page`；作者擁有者 403 `owner_role_insufficient` |
| pages.write | `PUT /pages/:id` | `updatePost` | E | 版本防覆蓋 |
| pages.write | `DELETE /pages/:id` | `deletePost` | E | |
| media.read | `GET /media` | `GET /media`（`handlers_admin.go` 匿名函式） | C | |
| media.write | `POST /media` | `POST /media` | C | multipart 欄位 `file` |
| media.write | `PUT /media/:filename` | `PUT /media/:filename` | C | |
| media.write | `DELETE /media/:filename` | `DELETE /media/:filename` | C | |
| media.write | `POST /media/rename` | `POST /media/rename` | C | |
| products.read | `GET /products` | `GET /products`（`handlers_ecommerce.go`） | A | |
| products.read | `GET /products/:id` | `GET /products/:id` | A | |
| products.write | `POST /products` | `POST /products` | A | |
| products.write | `PUT /products/:id` | `PUT /products/:id` | A | 版本防覆蓋 |
| products.write | `DELETE /products/:id` | `softDeleteProduct` | A | |
| orders.read | `GET /orders` | `GET /orders` | A | |
| themes.read | `GET /themes` | `GET /themes` | A | |
| themes.read | `GET /themes/:name/config` | `GET /themes/:name/config` | A | |
| themes.read | `GET /themes/:name/settings` | `GET /themes/:name/settings` | A | |
| themes.write | `POST /themes/activate` | `POST /themes/activate` | A | |
| themes.write | `POST /themes/:name/settings` | `POST /themes/:name/settings` | A | |
| plugins.read | `GET /plugins` | `GET /plugins` | A | |
| plugins.write | `POST /plugins/:id/reload` | `POST /plugins/:id/reload` | A | 上傳不開放，見下 |
| settings.read | `GET /settings` | `GET /settings` | A | **必須遮罩秘密**，見下 |
| settings.write | `POST /settings` | `POST /settings` | A | 與 admin 同規則 |
| logs.read | `GET /logs` | 無（agent 專用） | A | 只看自己的請求紀錄 |
| （不需權限，要金鑰） | `GET /me` | 無 | 任何角色 | 擁有者存在且有效即可 |
| （公開） | `GET /`（即 `/api/agent/v1`）、`GET /openapi.json` | 無 | — | 不需金鑰、不記錄 |

**第一版不開放給 agent 的 `/api/admin` 路由**（理由一律寫進說明端點的 `not_exposed`）：`/comments*`、`/menus*`、分類與標籤的寫入（`POST|PUT|DELETE /categories*`、`POST|PUT|DELETE /tags*`、`POST /categories/reorder`）、`/trash*`、`/posts/:id/revisions*`、`/palette`、`/payment/gateways*` 與 `/oauth/providers*`（含第三方憑證）、`POST /plugins/upload`（等於安裝程式碼）、`/agents*`（agent 不能管理 agent）。這些沒有對應權限，加開需另立工作單。

**`settings.read` 遮罩**：工作單 08 已合入 main，agent 與 admin 共用 `handleSettingsList`，由 `settings.DisplayValue` 依設定登記表與既有密封值規則遮罩，兩邊回傳相同結果。一般秘密值以 rune 計算，空字串維持空字串、至多四字元回 `****`、超過四字元回 `****` 加最後四字元；密封的結構值依共用遮罩器處理。agent 不另建後綴遮罩表，也不變更既有 admin 行為。

### agent API 的驗證、授權與記錄（`/api/agent/v1` 前綴上依序執行）

1. **Request ID**：每個 `/api/agent/v1` 回應（含成功）都帶 `X-Request-ID`，值 `req_` 開頭、符合 `^req_[0-9A-Za-z_-]{8,64}$`，由伺服器產生（不採用請求帶來的值）。錯誤本文的 `correlation_id` 與事件的 `request_id` 都是同一個值。
2. **金鑰**：只認 `Authorization: Bearer jla_…`。沒有 `Authorization` → 401 `key_missing`；其他格式、JWT、找不到雜湊 → 401 `key_invalid`；cookie 與 session 一律不看。找到金鑰後依序檢查：金鑰 `revoked_at` → 401 `key_revoked`；`expires_at` ≤ 現在 → 401 `key_expired`；agent `deleted_at` 非空 → 401 `key_revoked`；agent `status=disabled` → 401 `agent_disabled`；擁有者不存在 → 401 `owner_unavailable`。每次請求重查，不快取。
3. **權限**（每條路由一個 `Permission`，由 middleware 檢查，handler 不判斷）：agent 沒有該權限 → 403 `permission_missing`，`details = {"missing_permissions": ["posts.write"], "required_permission": "posts.write"}`；擁有者目前角色不在該權限 `required_roles` → 403 `owner_role_insufficient`，`details = {"permission", "required_roles", "owner_role"}`。通過後 `c.Set("user", 擁有者)`，被重用的 handler 照原邏輯做擁有權判斷（例如作者只能動自己的文章）。
4. **版本防覆蓋**（`PUT /posts/:id`、`PUT /pages/:id`、`PUT /products/:id`）：本文必須有 `base_updated_at`（RFC3339Nano 字串）。缺少 → 400 `version_required`；無法解析 → 400 `version_invalid`；與資料庫目前 `updated_at` 不符 → 409 `version_conflict`，`details = {"current_updated_at": "..."}`。比對與寫入在同一個條件式 `UPDATE … WHERE id = ? AND updated_at = ?` 完成（以影響列數判斷），不得先讀再寫。`base_updated_at` 本身不寫進資料。回應裡的 `updated_at` 必須是資料庫實際存的值（微秒精度），這樣下一次拿它當 `base_updated_at` 才會相符。`/api/admin` 的同名 handler 沒帶這個值時行為完全不變。
5. **記錄**：金鑰能對應到 `agent_keys` 列的請求（含被拒的 401／403）都寫一筆 `agent_events`：`actor_type=agent`、`action=request`、`module` = 權限的資源名（`/me` 為 `me`）、`method`、`route` = Echo 路由樣板（例如 `/api/agent/v1/posts/:id`；未匹配路由為空字串）、`status`、`ip_masked = agent.MaskIP(c.RealIP())`、`key_id`、`key_prefix`、`request_id`、`summary = {"params": {路徑參數}, "query_keys": [排序後的查詢鍵名，不含值], "body_bytes": 請求本文位元組數}`。不存請求本文與回應內容。有 Content-Length 時記宣告長度，chunked 本文記實際讀取量，拒絕前未讀取的串流記 0，不額外排空本文。超過摘要容量時保留可容納的參數與排序後鍵名，附 `truncated:true`、`params_total` 與 `query_keys_total`（原查詢鍵出現次數），序列化後仍小於 2 KiB；未匹配路由的 params 為空。找不到雜湊的請求不記錄。成功的請求同時更新金鑰 `last_used_at` 與 `last_used_ip_masked`（每把每分鐘最多寫一次）。
6. **維護模式**：`/api/agent/v1` 與其子路徑加入 `maintenanceBypass` 清單，維護中 agent 照常運作（與 `/api/admin` 相同）。

### 錯誤格式（agent API 全部錯誤；issue #7 WebMCP 共用）

```json
{"code": "permission_missing", "message": "這把金鑰沒有 posts.write 權限", "details": {"missing_permissions": ["posts.write"]}, "correlation_id": "req_8f3k2l1m0n9p"}
```

- `details` 一律是物件（沒有就是 `{}`）。共用函式放 `internal/server/api_errors.go`：`writeAPIError(c echo.Context, status int, code, message string, details map[string]any) error`、`requestIDFor(c echo.Context) string`。
- 未匹配的 `/api/agent/v1/*` 路由 → 404 `not_found`、方法不符 → 405 `method_not_allowed`（在 `newHTTPErrorHandler` 依前綴分流）。
- 被重用的 handler 回的 `{"error": "..."}` 錯誤改寫成本格式：`message` = 原字串，`code` 依狀態碼：400 `bad_request`、403 `forbidden`、404 `not_found`、409 `conflict`、413 `payload_too_large`、500 `internal_error`、503 `service_unavailable`，其他 `http_<狀態碼>`。
- `/api/admin/agents/*` 由本單新增的錯誤也用本格式；`RequireRole` 擋下的 401／403 維持原本的 `{"error": ...}`。

| 狀態 | code | 何時 |
|---|---|---|
| 400 | `version_required`、`version_invalid`、`type_mismatch`、`logs_filter_invalid`、`bad_request` | 見上 |
| 400 | `name_invalid`、`description_too_long`、`permissions_required`、`permission_unknown`（`details.unknown`）、`rotation_mode_invalid`、`grace_out_of_range`（`details = {"min":1,"max":1440}`） | 管理 API |
| 401 | `key_missing`、`key_invalid`、`key_revoked`、`key_expired`、`agent_disabled`、`owner_unavailable` | 見上 |
| 403 | `permission_missing`、`owner_role_insufficient`、`forbidden` | 見上 |
| 404 | `not_found`、`agent_not_found`、`key_not_found` | |
| 405 | `method_not_allowed` | |
| 409 | `version_conflict`、`agent_name_taken`、`conflict` | |

`grace_out_of_range` 的 `message` 固定說明原因：「過渡期必須是 1 到 1440 分鐘（最長 24 小時）」。

### 管理 API（`/api/admin/agents`，`auth.RequireRole("admin")`，只接受登入的人）

agent 物件（清單、詳情、建立、修改、停用、啟用回應都是這個形狀）：

```json
{"id": 3, "name": "內容小幫手", "description": "…", "permissions": ["posts.read", "posts.write"],
 "effective_permissions": ["posts.read", "posts.write"], "status": "active",
 "owner": {"id": 1, "username": "hazel", "role": "admin"},
 "created_at": "…", "updated_at": "…", "disabled_at": null, "last_used_at": "…",
 "keys": [{"id": 9, "prefix": "jla_Qm3d8sKe", "status": "active", "created_at": "…", "last_used_at": "…", "expires_at": null, "revoked_at": null}]}
```

`owner` 在擁有者被刪除後為 `null`；`keys` 新的在前，含已撤銷與已過期；`last_used_at` 取各金鑰最大值。任何回應都不含 `key_hash` 與明文（建立、輪替的 `api_key` 除外）。

| # | 方法與路徑 | 請求 | 回應與規則 |
|---|---|---|---|
| M1 | `GET /api/admin/agents` | | `{items: [agent], total}`，不含已刪除，依建立時間新到舊 |
| M2 | `POST /api/admin/agents` | `{name, description, permissions[]}` | 201 `{agent, key, api_key}`；`api_key` 是明文（唯一一次）；`key` 是 `keys[0]`；名稱去頭尾空白後 1–80 個字元（rune），否則 400 `name_invalid`；說明 > 500 個字元 400 `description_too_long`；權限空 400 `permissions_required`；未知權限（含任何 `agents.*`）400 `permission_unknown`；同名（不分大小寫、未刪除）409 `agent_name_taken`；擁有者與 `created_by` = 操作者；寫 `agent.create` 事件 |
| M3 | `GET /api/admin/agents/:id` | | agent；不存在或已刪除 404 `agent_not_found` |
| M4 | `PATCH /api/admin/agents/:id` | `{name?, description?, permissions?}` | agent；驗證同 M2；不能改擁有者；寫 `agent.update`（`summary` 記前後權限） |
| M5 | `POST /api/admin/agents/:id/rotate` | `{mode?: "immediate"\|"grace", grace_minutes?}` | 200 `{agent, key, api_key, previous_key}`；`mode` 省略或空本文視為 `immediate`，其他值 400 `rotation_mode_invalid`；`immediate` 時舊金鑰 `revoked_at = now`、忽略 `grace_minutes`；`grace` 時 `grace_minutes` 必填且 1–1440，否則 400 `grace_out_of_range`，舊金鑰 `expires_at = now + grace_minutes`；輪替當下仍在過渡中的更早金鑰一律立即撤銷（已過期的不動）；`previous_key` 是舊的生效金鑰（沒有生效金鑰時為 `null`）；停用中的 agent 也能輪替；寫 `key.rotate`，`summary = {mode, grace_minutes, old_key_id, old_key_prefix, new_key_id, new_key_prefix, old_key_expires_at, revoked_key_prefixes}`（`grace_minutes` 在立即失效時為 `null`；`revoked_key_prefixes` 是因這次輪替被撤銷的過渡金鑰前綴） |
| M6 | `POST /api/admin/agents/:id/keys/:key_id/revoke` | | 200 該金鑰（`{id, prefix, status, …}`）；金鑰不屬於此 agent 404 `key_not_found`；已撤銷的再撤銷回 200 不變；可撤銷生效中的金鑰（之後只能輪替取得新金鑰）；寫 `key.revoke` |
| M7 | `POST /api/admin/agents/:id/disable`、`/enable` | | agent；停用設 `disabled_at`，所有金鑰下一次請求 401 `agent_disabled`；啟用清掉 `disabled_at`，未撤銷未過期的金鑰恢復；寫 `agent.disable`／`agent.enable` |
| M8 | `DELETE /api/admin/agents/:id` | | 204；軟刪除、撤銷所有金鑰、寫 `agent.delete`；之後 M3 404、M1 不列，事件保留 |
| M9 | `GET /api/admin/agents/events` | `agent_id, actor_type, action, module, status, key_id, since, until, cursor, limit` | `{items, next_cursor, total}`，全部 agent（含已刪除）的事件，篩選規則同 A4 |

M9 的 `action` 可重複傳入，例如 `action=key.rotate&action=key.revoke`，代表任一指定動作；相同值去重，空值忽略。`key_id` 篩選指定金鑰。這些條件先用於 SQL 計數與分頁，`total` 不受 cursor 影響。請求事件的 `status=0` 表示已保存請求，但最終結果尚未確認，不能視為成功；中斷後仍保留供追查。

管理動作的事件：`actor_type=user`、`actor_user_id` = 操作者、`module=agents`、`method`／`route` = 管理 API 的方法與路由樣板、`status` = 回應狀態、`ip_masked` = 操作者遮罩 IP、`key_id`／`key_prefix` = 受影響的金鑰（建立、輪替為新金鑰，撤銷為被撤銷的金鑰，其他為空）。讀取（M1、M3、M9）不記錄。`action` 只有：`agent.create`、`agent.update`、`agent.disable`、`agent.enable`、`agent.delete`、`key.rotate`、`key.revoke`、`request`。

### agent API（`/api/agent/v1`）

| # | 方法與路徑 | 權限 | 回應 |
|---|---|---|---|
| A1 | `GET /api/agent/v1` | 公開 | 說明文件，見下 |
| A2 | `GET /api/agent/v1/openapi.json` | 公開 | OpenAPI 3.1.0，見下 |
| A3 | `GET /api/agent/v1/me` | 要金鑰 | `{id, name, description, permissions, effective_permissions, owner_role, key: {id, prefix, expires_at}}`；`expires_at` 是所用金鑰的到期時間（生效中為 `null`，過渡中為到期時刻） |
| A4 | `GET /api/agent/v1/logs` | logs.read | `?cursor=&limit=&status=&module=&since=&until=` → `{items, next_cursor, total}`；只含自己 `actor_type=agent` 的事件；新到舊（`id` 遞減）；`limit` 1–100，預設 50；`status` 是三位數狀態碼或 `2xx`／`3xx`／`4xx`／`5xx`；`module` 是資源名；`since`、`until` 是 RFC3339（含 since、不含 until）；`total` 是符合篩選的總數（不受 cursor 影響）；`next_cursor` 是不透明字串，沒有下一頁時為 `null`；任何參數不合法 400 `logs_filter_invalid` |
| — | 權限表其餘端點 | 見權限表 | 被重用 handler 的原本回應 |

事件項目（A4 與 M9 相同）：`{id, at, actor_type, actor_user_id, agent_id, agent_name, key_id, key_prefix, action, module, method, route, status, ip_masked, summary, correlation_id}`；`correlation_id` = 事件的 `request_id`。

**A1 說明文件**（不需金鑰；從路由表產生）：

```json
{"name": "Jelly CMS Agent API", "version": "v1", "base_path": "/api/agent/v1",
 "openapi_url": "/api/agent/v1/openapi.json", "skill_path": "skills/jelly-cms/SKILL.md",
 "authentication": {"type": "bearer", "header": "Authorization", "scheme": "Bearer", "key_prefix": "jla_", "how_to_get": "…"},
 "permissions": [{"name": "posts.write", "description": "…", "required_roles": ["admin","editor","author"],
                  "endpoints": [{"method": "POST", "path": "/api/agent/v1/posts", "summary": "…", "version_guard": false}]}],
 "endpoints": [{"method", "path", "permission", "summary", "required_roles", "version_guard", "auth": "public"|"key"}],
 "concurrency": {"field": "base_updated_at", "applies_to": ["PUT /api/agent/v1/posts/{id}", "…"], "conflict_code": "version_conflict"},
 "errors": {"format": {"code": "…", "message": "…", "details": {}, "correlation_id": "req_…"}, "codes": [{"code", "status", "description"}]},
 "not_exposed": [{"path": "/api/admin/comments", "reason": "…"}],
 "examples": [{"title", "request", "response"}]}
```

- `permissions` 依 `AllPermissions()` 的順序列出全部 16 個，每個至少一個端點；同一權限的端點 `required_roles` 相同。
- `endpoints` 含 A1–A4 與權限表全部端點；路徑參數一律寫成 `{id}` 形式（與 OpenAPI 相同）。
- `errors.codes` 列出上面錯誤碼表的每一個。

**A2 OpenAPI**：`openapi: "3.1.0"`；`info.title`、`info.version`；不設 `servers`，`paths` 的鍵寫完整路徑 `/api/agent/v1/...`；`components.securitySchemes.AgentKey = {"type": "http", "scheme": "bearer", "description": "…"}`；全域 `security: [{"AgentKey": []}]`，A1、A2 的操作自己設 `security: []`；每個需要權限的操作帶 `x-jelly-permission`（字串）與 `x-jelly-required-roles`；版本防覆蓋的操作 `requestBody` 宣告必填 `base_updated_at`；每個操作有 `operationId`（唯一）與至少一個回應；路徑參數都在 `parameters` 宣告（`in: path`、`required: true`）；錯誤回應 `$ref: "#/components/schemas/Error"`，所有 `$ref` 都能在同一文件解析。不新增 Go 模組相依。

**路由表**：`internal/server/agent_routes.go` 的 `agentRouteTable() []agentRoute` 是唯一來源；註冊 Echo 路由、A1、A2、權限 middleware 都讀它。`agentRoute` 至少有 `Method`、`Path`（Echo 形式，相對 `/api/agent/v1`）、`Permission`、`Summary`、`RequiredRoles`、`VersionGuard`、`Auth`、`Handler`。

### 頁面

- `GET /admin/agents` → `web/admin/agents.html`（`Active: "agents"`）、`GET /admin/agents/:id` → `web/admin/agent_detail.html`；兩者只有 admin，其他登入角色 403（後台錯誤畫面），未登入導向登入頁。
- 側欄：`RegisterNav(NavItem{Key: "agents", Section: "system", Label: "agents", Icon: "i-key", Href: "/admin/agents", Order: 85, Roles: []string{"admin"}})`；翻譯鍵 `agents`（繁中「AI agent」、英文「AI agents」）。
- 清單頁照 `Agents` 板：表格、建立面板（名稱、說明、依資源分組的讀／寫勾選、需要管理員的權限標「需管理員」）、建立成功後一次性顯示金鑰（深色區塊、完整金鑰、複製按鈕、「我已複製，關閉」），關閉後只剩前綴；權限清單取自 `GET /api/agent/v1`。
- 詳情頁照 `AgentDetail` 板：基本資料與擁有者、金鑰表（生效中、過渡中含倒數、已撤銷、已過期）與提前撤銷、權限與有效權限、輪替歷史（`action=key.rotate`、`key.revoke`）、活動紀錄（狀態、模組、金鑰、時間篩選，「載入更多」用 cursor，顯示總數）、輪替對話框（立即失效預設；過渡期輸入分鐘，0 或 > 1440 時在欄位下方顯示錯誤訊息並停用確認鈕；完成後一次性顯示新金鑰與舊金鑰到期時間）、停用／啟用、刪除（確認對話框說明紀錄會保留）。
- 頁面內所有腳本內嵌於模板或放 `web/admin/assets/`，不從外部網址載入。

### Agent Skill

`skills/jelly-cms/SKILL.md`：YAML front matter（`name: jelly-cms`、`description`），本文至少涵蓋：金鑰放 `Authorization: Bearer jla_…`；先 `GET /api/agent/v1` 與 `GET /api/agent/v1/openapi.json` 探索、`GET /api/agent/v1/me` 確認有效權限；讀文章、建立草稿、帶 `base_updated_at` 更新並處理 409（重新讀取、合併、再送）；錯誤格式與 `correlation_id`；`logs.read` 查自己的紀錄；不要把金鑰寫進程式碼或日誌。

## 與既有 main 的調和

- **06 帳號**：`resolvePrincipal` 直接拒絕 `jla_`，管理寫入使用 `RequireInteractive`，`jly_` 個人 API 權杖不能管理 agent。擁有者的有效狀態與目前角色每次重查。IP 遮罩共用 `agent.MaskIP`，保留帳號端原本的 host／port 解析。
- **06 導覽與文件**：系統區順序為 users（80）、agents（85）、settings（90）。`/api/docs` 的 OpenAPI 說明連到 agent 探索端點；Tokens 板分清個人權杖與 AI agent 金鑰。
- **資料遷移**：沿用 `runEmbeddedMigrations` 套用新增的 `0025_agents.sql`，不修改 main 已有的 `0024` 搜尋遷移。
- **08 系統**：Request ID 沿用現有中介層，每筆 agent event 同交易寫入 `source=audit` 的 system log。設定讀寫使用與 admin 相同的 handler、設定登記表及遮罩。保留 POST 並支援 main 的 PUT 設定寫入。
- **08 附件**：除舊式 media 路由，也涵蓋 GET／POST `/media/files`、PATCH／DELETE `/media/files/:id`、POST `/media/files/:id/replace`。agent 不開放儲存策略、分組寫入或抓取外部網址。
- **09 編輯器**：共用文章 handler 與條件式 UPDATE，`source`、`format` 等現有內容欄位沿用原有語意，agent 更新必帶 `base_updated_at`。文章／頁面的類型限制同時套用讀取與最終寫入。
- **07 商店**：商品、訂單直接使用 main 的型別化 handler，包含 GET `/orders/:id`。商品版本防覆蓋涵蓋整個交易，庫存、規格與分類不能部分成功。
- **13 WebMCP**：保留現有公開 API 與錯誤格式，agent 的錯誤封裝只作用於新增的 agent 路由。

## 決策紀錄

| 決定 | 被否決的選項與理由 |
|---|---|
| 獨立前綴 `/api/agent/v1`，重用 `/api/admin` 的 handler（同一個函式掛兩處）；`/api/admin/*` 不接受 agent 金鑰，一律 401 | `/api/admin/*` 直接接受金鑰：權限邊界與文件不清楚，人與 agent 的請求混在一起難以稽核 |
| 只有 admin 能建立 agent，擁有者 = 建立者；所有 admin 可管理所有 agent | 任何員工都能建 agent：作者可替自己發高權限金鑰，稽核責任不清 |
| 權限的 `required_roles` 取群組角色與 handler 內部限制的交集（`pages.write` 為 admin、editor） | 只沿用群組角色：作者擁有者的 `effective_permissions` 會列出 `pages.write`，實際呼叫卻被 handler 403，agent 無法靠 `/me` 判斷自己能做什麼 |
| `settings.read` 沿用工作單 08 已合入的設定登記表與共用遮罩 handler | agent 另建遮罩表：與 admin 結果不同，且會漏掉已登記的秘密與密封結構值 |
| `plugins.write` 只含 reload，不含外掛上傳 | 開放上傳等於讓 agent 安裝程式碼，須另立工作單評估 |
| 金鑰 `jla_` + base64url(32 bytes)，只存 SHA-256 雜湊與前綴，明文只在建立與輪替回應出現一次 | 加密保存＋「顯示金鑰」：資料庫與加密金鑰一起外洩時所有金鑰外洩；也讓「只顯示一次」形同虛設 |
| agent 與金鑰分表，一個 agent 多把金鑰紀錄，各有 `created_at`、`last_used_at`、`expires_at`、`revoked_at` | 金鑰欄位放在 agent 上：無法同時存在新舊兩把，也留不下輪替歷史 |
| 輪替預設立即失效，可選 1–1440 分鐘過渡期，可提前撤銷；同時最多兩把有效金鑰 | 不設上限的過渡期：舊金鑰可能長期有效；允許多把過渡金鑰：難以推算哪把還能用 |
| 不做排程自動輪替 | 排程輪替：新金鑰要能自動送到 agent 端，超出本單範圍 |
| 16 個 `資源.read/write` 權限；實際授權 = agent 持有該權限且擁有者目前角色通過該 handler 在 `/api/admin` 的原角色檢查（引用同一個角色變數） | 另建角色 → 權限對照表：兩份規則會漂移；只看 agent 權限不看擁有者：擁有者被降級後 agent 仍保有管理員能力 |
| 每次請求重查金鑰、agent、擁有者，不快取 | 快取驗證結果：撤銷、停用、降級不會「下一次請求」就生效 |
| agent 沒有 `agents.*` 權限、不能輪替自己的金鑰；`/api/admin/agents/*` 只接受登入的人 | 讓 agent 自我輪替：金鑰外洩者能把合法持有人踢出並延長存取 |
| 說明端點與 OpenAPI 由同一份路由表產生，不需金鑰 | 手寫兩份文件：容易和實際路由不一致；需要金鑰才看得到說明：agent 無法先探索再請求權限 |
| 寫入必帶 `base_updated_at`，比對與寫入在同一個條件式 UPDATE | 選填：agent 容易忘記帶而覆蓋人的修改；先讀再寫：兩個請求同時通過比對 |
| 刪除 agent 為軟刪除，事件永久保留並存名稱快照 | 硬刪除串聯事件：失去稽核軌跡 |
| 事件只存請求摘要（路徑參數、查詢鍵名、本文長度），不存本文與回應 | 存完整請求與回應：日誌過大且會留下內容與個資（Surtopya 的教訓） |
| 被拒的 401／403 只要能對應到金鑰也記錄；找不到雜湊的不記錄 | 全部記錄：亂猜金鑰會灌爆日誌；全部不記錄：查不到已撤銷金鑰仍在被使用 |
| agent 自己的 `/logs` 只看 `actor_type=agent`；管理員看全部 | 讓 agent 看到管理動作：會看到操作者的使用者 ID，沒有必要 |
| 錯誤格式 `{code, message, details, correlation_id}`，`req_` Request ID 由伺服器產生 | 沿用 `{"error": ...}`：agent 無法依代碼分流；採用請求帶來的 ID：可被偽造混淆紀錄 |
| 設定讀取共用 main 的遮罩結果；`/api/admin/settings` 本單不改 | 改既有 admin 遮罩門檻會超出本單範圍 |
| 不開放留言、選單、回收桶、版本、金流與 OAuth 設定、外掛上傳 | 全部開放：沒有對應權限、含第三方憑證或等於安裝程式碼 |
| 維護模式不擋 `/api/agent/v1` | 擋下：維護期間正是常需要 agent 批次處理內容的時候 |
| 過渡期滿的測試直接改 `agent_keys.expires_at` | 可注入時鐘：要在正式程式留測試掛鉤，增加複雜度 |

## 實作順序

每段先由測試代理照本文件建立失敗案例（已隨本工作單交付），實作代理依契約實作，主代理審查後以真實伺服器與 Playwright 驗收。測試名稱清單見下一節的對照表。

1. A0 設計板（已隨本單交付）：`design/gen/gen_agents.py` 產生 `Agents`、`AgentDetail`，`canvas.json` 已加入。
2. A1 資料與金鑰：`0025_agents.sql` 與套用、`internal/core/agent.go`、`internal/agent` 全部函式。測試：`internal/agent/agent_test.go`、`TestAgentSchemaMigrationIsRepeatable`。
3. A2 管理 API：M1–M8、管理事件、`api_errors.go`。測試：`agent_keys_integration_test.go` 除 `TestAgentAdminPagesAdminOnly` 與 `TestAgentAdminEventsFollowAgentAcrossRotation` 以外的測試（需要 A3 的 `/me` 才能全綠）。
4. A3 agent 驗證、路由表與授權：Request ID、金鑰 middleware、權限 middleware、handler 抽成具名函式並掛兩處、`type` 範圍、版本防覆蓋、錯誤改寫、設定遮罩、維護模式放行。測試：`agent_api_integration_test.go` 的 `Me`、`PostsWrite…`、`TypeScoped`、`UpdateRequires…`、`KeyCannotReachAdminAPI`、`ErrorsUseEnvelope…`、`SettingsReadMasksSecrets`、`APIOpenDuringMaintenance`。
5. A4 自我說明：A1、A2。測試：`TestAgentDiscoveryIndexNeedsNoKey`、`TestAgentOpenAPIDocumentIsValid31AndMatchesRoutes`。
6. A5 稽核：請求事件、A4、M9。測試：`TestAgentLogsListOwnEventsWithCursor`、`TestAgentAdminEventsFollowAgentAcrossRotation`。
7. A6 後台畫面：`agents.html`、`agent_detail.html` 依板實作，頁面限 admin。測試：`TestAgentAdminPagesAdminOnly`、`TestAdminNavRoleVisibility`；瀏覽器驗收步驟見下。
8. A7 Agent Skill：`skills/jelly-cms/SKILL.md`。測試：`TestAgentSkillFileDescribesDiscovery`。

### issue 完成條件 → 驗證

| issue #6 完成條件 | 測試或瀏覽器步驟 |
|---|---|
| 建立後離開畫面無法再看到明文、資料庫查不到明文 | `TestAgentCreateShowsKeyOnceAndStoresOnlyHash`；瀏覽器 B1 |
| 輪替後新金鑰立即可用、ID 權限日誌不變、可查到輪替紀錄 | `TestAgentRotateImmediateRevokesOldKeyAtOnce`、`TestAgentAdminEventsFollowAgentAcrossRotation`；瀏覽器 B2 |
| 立即失效後舊金鑰下一次 401 | `TestAgentRotateImmediateRevokesOldKeyAtOnce` |
| 過渡期內新舊都成功、期滿舊金鑰 401、提前撤銷後立即 401 | `TestAgentRotateGraceKeepsBothKeysUntilExpiry`、`TestAgentRevokeGraceKeyEarly`；瀏覽器 B3、B4 |
| 過渡期 > 24 小時被拒並說明原因 | `TestAgentRotateGraceBounds`；瀏覽器 B3 |
| `posts.write` 可建文章、只有 `posts.read` 得 403 並指出缺少權限 | `TestAgentPostsWriteCreatesPostAndReadOnlyGets403` |
| 停用 agent 或擁有者被降權後所有金鑰（含過渡中舊金鑰）401／403 | `TestAgentDisableBlocksEveryKeyIncludingGrace`、`TestAgentOwnerDowngradeAndDeletionLimitAccess`；瀏覽器 B5 |
| 不帶金鑰可取得完整端點與權限對照，`openapi.json` 通過 OpenAPI 3.1 驗證 | `TestAgentDiscoveryIndexNeedsNoKey`、`TestAgentOpenAPIDocumentIsValid31AndMatchesRoutes` |
| 同一舊版本號更新同一篇，第二個 409 | `TestAgentUpdateRequiresBaseUpdatedAt` |
| 日誌可篩出某 agent 全部操作（跨越輪替）並看到來源 IP 與金鑰前綴 | `TestAgentAdminEventsFollowAgentAcrossRotation`、`TestAgentLogsListOwnEventsWithCursor`；瀏覽器 B2 |
| agent 金鑰打 `/api/admin/*` 401、agent 不能管理 agent | `TestAgentKeyCannotReachAdminAPI`、`TestAgentManagementRequiresSignedInAdmin` |
| 附 Agent Skill | `TestAgentSkillFileDescribesDiscovery` |
| PostgreSQL 整合測試 | 上列 `internal/server` 測試皆以 `JELLY_TEST_POSTGRES=1` 執行 |

### 瀏覽器驗收步驟（主代理以真實伺服器與 Playwright 操作，1280px 與 390px、亮與暗）

- B1 建立：admin 登入 → 側欄「系統 › AI agent」→「建立 agent」→ 名稱「驗收 agent」、勾 `posts.read`、`posts.write` → 建立 → 畫面出現完整 `jla_…` 金鑰與複製按鈕 → 按複製，剪貼簿內容等於畫面金鑰 → 按「我已複製，關閉」→ 清單只顯示前綴；重新整理、打開詳情、檢視原始碼都找不到明文；用複製的金鑰 `curl /api/agent/v1/me` 得 200。
- B2 立即輪替：詳情 →「輪替金鑰」→ 保持「立即失效」→ 確認 → 新金鑰一次性顯示 → 舊金鑰打 `/me` 得 401 `key_revoked`、新金鑰 200 → 輪替歷史出現一筆「立即失效」，活動紀錄篩這個 agent 時看得到新舊兩個前綴與遮罩 IP，ID、名稱、權限沒變。
- B3 過渡輪替：「輪替金鑰」→ 選「過渡期」→ 輸入 1500，欄位下方出現「過渡期必須是 1 到 1440 分鐘（最長 24 小時）」且確認鈕停用 → 改 30 → 確認 → 金鑰表出現「過渡中 · 剩 30 分」倒數 → 新舊金鑰都 200。
- B4 提前撤銷：在過渡中的舊金鑰按「提前撤銷」→ 確認 → 舊金鑰立即 401 `key_revoked`、新金鑰 200，輪替歷史多一筆撤銷。
- B5 停用：先再做一次過渡輪替 → 按「停用」→ 新舊金鑰都 401 `agent_disabled` → 「啟用」後兩把都恢復 200。
- B6 權限：editor 登入看不到側欄入口，直接開 `/admin/agents` 得 403 畫面。

### 測試另外釘下的約定

- 檔名：`internal/server/agent_keys_integration_test.go`、`internal/server/agent_api_integration_test.go`、`internal/agent/agent_test.go`；Skill 檔 `skills/jelly-cms/SKILL.md`。
- 建立回應鍵：`agent`、`key`、`api_key`；輪替回應鍵：`agent`、`key`、`api_key`、`previous_key`；agent 物件鍵：`id`、`name`、`description`、`permissions`、`effective_permissions`、`status`（`active`／`disabled`）、`owner{id, username, role}`、`created_at`、`updated_at`、`disabled_at`、`last_used_at`、`keys[]{id, prefix, status, created_at, last_used_at, expires_at, revoked_at}`；清單 `{items, total}`。
- 輪替請求欄位 `mode`、`grace_minutes`；`grace_out_of_range` 的 `details.min = 1`、`details.max = 1440`。
- 事件 `summary` 鍵：輪替 `mode`、`grace_minutes`、`old_key_prefix`、`new_key_prefix`；請求 `params`、`query_keys`、`body_bytes`。
- `/me` 鍵：`id`、`name`、`description`、`permissions`、`effective_permissions`、`owner_role`、`key{id, prefix, expires_at}`。
- 說明文件鍵：`openapi_url`、`authentication.key_prefix`、`permissions[]{name, required_roles, endpoints[]{method, path}}`、`endpoints[]{method, path, permission, auth}`、`errors.codes[]{code, status}`；路徑參數寫 `{id}`；`auth` 值 `public`／`key`。
- OpenAPI：`components.securitySchemes.AgentKey`（`type: http`、`scheme: bearer`）、操作擴充 `x-jelly-permission`、A1／A2 的 `security: []`、`components.schemas.Error`。
- IP 遮罩字串：IPv4 `198.51.100.0`、IPv6 `2001:db8:abcd::`；設定遮罩 `****` 加最後 4 字元。
- 測試以請求的 `RemoteAddr` 提供來源 IP，不送 `X-Forwarded-For`。
- 過渡期滿以 SQL 把 `agent_keys.expires_at` 改成過去時間模擬；擁有者降級與刪除以 SQL 直接改 `users`。
- `X-Request-ID` 符合 `^req_[0-9A-Za-z_-]{8,64}$`。
- 側欄 `agents` 在 admin 的 `system` 區、`settings` 之前；editor 與 author 看不到。
- 頁面：editor 打 `/admin/agents` 得 403；admin 的版面含 `href="/admin/agents"`。

### 實作者的判斷邊界

可以自己決定：抽出的具名 handler 名稱（建議 `listMedia`、`uploadMedia`、`replaceMedia`、`deleteMedia`、`renameMedia`、`listThemes`、`activateTheme(tm)`、`getThemeConfig(tm)`、`getThemeSettings(tm)`、`saveThemeSettings(tm)`、`listPlugins`、`reloadPlugin(rt)`、`getSettings`、`saveSettings`、`listProducts`、`createProduct`、`getProduct`、`updateProduct`、`listOrders`）、cursor 的編碼方式、說明文件與 OpenAPI 的描述文字與範例內容、錯誤 `message` 的文字（`grace_out_of_range` 除外）、`last_used_at` 節流的寫法、頁面的 JavaScript 結構與 CSS 類名（不得從外部網址載入）、`agent_events` 額外的索引。

必須回報、不得自行決定：改變 `/api/admin` 任何既有端點的行為或錯誤格式；增減權限或改變權限 → 端點對照；讓 agent 碰到任何未列在權限表的 `/api/admin` 路由；放寬金鑰格式、雜湊或「只顯示一次」；改變過渡期上下限或「最多兩把有效金鑰」；記錄請求本文或回應內容；新增 Go 模組相依；需要修改本單的測試才能通過。
