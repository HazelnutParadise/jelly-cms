# 31 — 機器 API 補到能當無頭後台：留言審核、訂單處理、分類標籤寫入、折扣碼

Epic：帳號與存取
User Story：站長給自動化程式或 AI agent 一把金鑰，就能審留言、處理訂單（出貨、退款、標記已付、加備註）、整理分類與標籤、管理折扣碼，不必再開後台網頁。每個動作沿用後台同一套規則、同一批外掛事件與通知。
Status：待實作。
Blocked by：27（v1 契約，已合併）、28（外掛事件，已合併）。

## 背景與決定

現況（行號以 main `0dc56d9` 為準）：

- `/api/v1` 有 45 條，`/api/admin` 有 216 條。`GET /api/v1` 的 `not_exposed` 列了留言、選單、分類標籤寫入、回收桶、版本、金流憑證、OAuth 憑證、外掛上傳、串接管理、檔名式媒體、系統日誌共 12 項；折扣碼與訂單寫入連列都沒列。
- 權限字串定義在 `internal/integration/integration.go:30-77`（16 個），資料庫以 `integrations_permissions_check`（`internal/data/migrations/0025_agents.sql:18-26`，0027 改名）限制為同一份清單；遷移檔交付後不可變（ENG.md），新增權限字串要新的遷移檔重建約束。
- 後台的權限勾選格由 `GET /api/v1` 的 `permissions` 動態產生（`web/admin/assets/integrations.js:295-312,1217-1223`），新權限不用改畫面。
- 後台 handler 現況：留言（`handlers_comments.go:76-89`，角色 admin／editor）、分類標籤（`handlers_taxonomy.go:43-53`，寫入 admin／editor，建立標籤連 author 都可以）、訂單（`shop_admin_routes.go:135-140`，admin）、折扣碼（`shop_admin_routes.go:142-148`，admin）。都用 `currentUser(c)` 取身分，沒有依賴 CSRF 或 Session，可以直接掛到 v1。
- 事件與通知都在 handler 內：留言審核發 `OnCommentUpdate`（`handlers_comments.go:700-712`）、回覆發 `OnCommentCreate`、刪除發 `OnCommentDelete`；訂單轉換經 `core.OrderService.Transition` 寫 `OrderEvent` 並發 `OnOrderUpdate`，出貨寄信（`shop_orders_admin.go:428-436`）、退款發 `OnOrderRefund` 並通知（`:785-797`）、標記已付通知（`:835-845`）。沿用 handler 就不會漏事件。
- 後台回應形狀：留言清單 `{items,total,page,per_page,counts}`；分類、標籤清單裸陣列；訂單狀態寫入回 `{"status":"ok"}`、不合法轉換回 409 `{"allowed":[...]}`；折扣碼建立回 200 單筆（不是 201）、欄位錯誤回 `{"error","fields"}`。
- 決定：新增六個權限字串，一次遷移。路由沿用後台 handler 做業務與事件，回應由 v1 自己的薄包裝決定形狀（清單用工單 27 的信封，寫入回更新後的資源而不是 `{"status":"ok"}`）。
- 決定：作者角色不能透過 v1 寫分類標籤（後台讓作者建標籤是畫面便利，v1 以 `editorRoles` 為準）。
- 不改：後台 `/api/admin/*` 的行為與回應；事件、通知、訂單狀態機、退款規則；既有 16 個權限的語意。

## 權限

| 權限 | 角色 | 涵蓋 |
|---|---|---|
| `comments.read` | admin、editor | 列留言、讀單筆 |
| `comments.write` | admin、editor | 審核動作、批次、回覆、改內文、永久刪除 |
| `taxonomy.write` | admin、editor | 分類與標籤的建、改、刪、分類排序（讀取沿用 `posts.read`） |
| `orders.write` | admin | 改狀態、備註、改地址、退款、標記已付（讀取沿用 `orders.read`） |
| `coupons.read` | admin | 列、讀單筆、預覽 |
| `coupons.write` | admin | 建、改、刪、批次產生 |

新遷移檔 `internal/data/migrations/0029_integration_permissions_v2.sql`（編號依當時最新接續）：同一交易內 `ALTER TABLE integrations DROP CONSTRAINT integrations_permissions_check` 再 `ADD CONSTRAINT` 為 22 項清單；冪等（`IF EXISTS`）；不動既有列。`integration.go` 的常數、`AllPermissions()`（順序：既有 16 項後接上表六項）、`permissionDescription`、`permissionRoles` 同步。

## 規則

### R1 路由

| 方法 路徑 | 權限 | 沿用 handler | v1 回應 |
|---|---|---|---|
| `GET /comments` | comments.read | `listAdminComments` 的查詢（抽成查詢函式） | 分頁信封，item 為後台的 `adminCommentRow` 形狀；query `status`（`pending|approved|spam|trash`，空＝全部）、`post_id`、`q`、`page`、`per_page`（工單 27 規則） |
| `GET /comments/:id` | comments.read | 新：查一筆 | 單筆 |
| `POST /comments/:id/{approve,unapprove,spam,trash,restore}` | comments.write | `commentAction` | 200 更新後單筆 |
| `POST /comments/bulk` | comments.write | `bulkCommentAction` | 200 `{updated}` |
| `POST /comments/:id/reply` | comments.write | `replyComment` | 201 新留言 |
| `PUT /comments/:id` | comments.write | `updateComment` | 200 單筆 |
| `DELETE /comments/:id` | comments.write | `deleteCommentPermanently` | 200 `{message}`（與 v1 其他 DELETE 一致） |
| `POST /categories`、`PUT /categories/:id`、`DELETE /categories/:id`、`POST /categories/reorder` | taxonomy.write | `createCategory`、`updateCategory`、`deleteCategory`、`reorderCategories` | 201 單筆、200 單筆、200 `{message}`、200 集合信封 |
| `POST /tags`、`PUT /tags/:id`、`DELETE /tags/:id` | taxonomy.write | `createTag`、`updateTag`、`deleteTag` | 201、200、200 `{message}` |
| `PUT /orders/:id/status` | orders.write | `adminOrderUpdateStatus` | 200 訂單（同 `GET /orders/:id` 形狀，見工單 33）；不合法轉換回 409 `invalid_transition`，`details.allowed` 為可轉換清單 |
| `POST /orders/:id/notes` | orders.write | `adminOrderAddNote` | 200 訂單 |
| `PATCH /orders/:id/address` | orders.write | `adminOrderUpdateAddress` | 200 訂單 |
| `POST /orders/:id/refund` | orders.write | `adminOrderRefund` | 200 訂單；`Idempotency-Key` 標頭與 body 的 `idempotency_key` 沿用後台規則 |
| `POST /orders/:id/mark-paid` | orders.write | `adminOrderMarkPaid` | 200 訂單 |
| `GET /coupons`、`GET /coupons/:id`、`POST /coupons/preview` | coupons.read | `adminCouponsList`（抽查詢）、`adminCouponGet`、`adminCouponPreview` | 分頁信封（去掉 `counts`）、單筆 `{coupon, targets}`、預覽物件 |
| `POST /coupons`、`PATCH /coupons/:id`、`DELETE /coupons/:id`、`POST /coupons/generate` | coupons.write | `adminCouponCreate`、`adminCouponPatch`、`adminCouponDelete`、`adminCouponGenerate` | 201 單筆（後台回 200，v1 改 201）、200 單筆、200 `{message}`、201 `{batch_id,count,codes}` |

後台 `PUT /api/admin/orders/:id` 的路徑在 v1 改成 `/orders/:id/status`，避免和「整筆更新」混淆。

### R2 錯誤對照

後台 `{"error": msg}` 經 `integrationErrorEnvelope` 已會轉成信封；本工單補明確 `code`：留言 404 `not_found`、409（回收桶內只能還原）`conflict`；訂單 409 `{"allowed"}` → `invalid_transition` 加 `details.allowed`；訂單 400「出貨要物流資訊」→ `bad_request` 加 `details.fields`；退款 422 → `refund_unsupported`；折扣碼 409 `duplicate code` → `coupon_code_taken`、`used coupons can only be disabled` → `coupon_in_use`、欄位錯誤 `{"error","fields"}` → `bad_request` 加 `details.fields`。這些 `code` 加進 `integrationErrorCodes` 與說明。

### R3 文件與說明

- `integrationNotExposed` 拿掉留言與分類標籤寫入兩列，剩下的保留。
- `permissionDescription` 補六項中文說明；`GET /api/v1` 的 `permissions` 自然多六筆，後台勾選格跟著出現。
- OpenAPI：每條新路由的 request body 與回應 schema 由 struct 產生（留言、分類、標籤、折扣碼的 struct 已在 `core`；訂單用工單 33 的訂單詳情 struct）。
- `skills/jelly-cms/SKILL.md` 加「審留言」「處理訂單」兩段短流程；README 權限表；`STATUS.md`；工單 14 開頭加一行。

## 程式（格子）

1. `internal/integration/integration.go`：六個常數與 `AllPermissions`；`internal/data/migrations/00NN_integration_permissions_v2.sql`；`internal/data/*_migration_test.go` 補遷移測試（舊列不變、新字串可寫入、舊約束名不存在）。
2. `internal/server/integration_comments.go`（新檔）：`GET /comments`、`GET /comments/:id` 與寫入的 v1 包裝；`handlers_comments.go` 的 `listAdminComments` 抽出查詢函式（後台 handler 不變）。
3. `internal/server/integration_taxonomy.go`（新檔）：分類標籤寫入的 v1 包裝（主要是回應狀態碼與信封）。
4. `internal/server/integration_orders.go`（新檔）：訂單五條寫入的 v1 包裝，成功後重讀訂單回詳情；`shop_orders_admin.go` 的 `adminOrderGet` 抽出「組訂單詳情」函式供 v1 與工單 33 共用。
5. `internal/server/integration_coupons.go`（新檔）：折扣碼七條；`shop_coupons_admin.go` 的 `adminCouponsList` 抽查詢函式。
6. `internal/server/integration_routes.go`：路由表加列（`RequiredRoles` 依權限表）；`integrations_discovery.go`：`permissionDescription`、`integrationErrorCodes`、`integrationNotExposed`、schema 與 request body 分支。
7. 文件：格子 R3。

## 驗收（測試契約）

新測試檔 `internal/server/api_v1_coverage_integration_test.go`：

- 遷移：對空庫與含既有串接的庫各跑一次 `MigrateDB`，`integrations_permissions_check` 接受 22 項、拒絕 `comments.admin` 這種未知字串；重跑冪等。
- 權限：建立串接時勾六個新權限都成功；`GET /api/v1` 的 `permissions` 有這六筆且 `required_roles` 正確；`GET /me` 的 `effective_permissions` 依擁有者角色裁剪（editor 擁有者的 `orders.write` 不在有效清單）。
- 留言：植入 pending 留言，`POST /comments/:id/approve` 回 200、`status = approved`、`integration_events` 有紀錄、外掛事件 `OnCommentUpdate` 被發出（沿用 `plugin_events_harness_test.go` 的接收方式）；沒有 `comments.write` 的金鑰回 403 `permission_missing`；`GET /comments?status=pending` 分頁信封且沒有 `counts` 鍵；回收桶內 `approve` 回 409 `conflict`。
- 分類標籤：editor 擁有者可建改刪；author 擁有者對 `POST /tags` 回 403 `owner_role_insufficient`；後台 `POST /api/admin/tags` 以 author Cookie 仍 201（守門）。
- 訂單：`PUT /orders/:id/status` 從 pending 轉 paid 回 200 且回應是訂單詳情、`OnOrderUpdate` 與 `OnOrderPaid` 發出；轉不合法狀態回 409 `invalid_transition` 且 `details.allowed` 等於 `core.AllowedTransitions`；轉 shipped 缺物流資訊回 400 `details.fields`；退款帶 `Idempotency-Key` 重送同內容回同結果、不同內容回 409；`mark-paid` 對非銀行轉帳訂單回 400。後台 `PUT /api/admin/orders/:id` 仍回 `{"status":"ok"}`（守門）。
- 折扣碼：`POST /coupons` 回 201；重複代碼回 409 `coupon_code_taken`；欄位錯誤回 400 且 `details.fields` 有鍵；已使用的折扣碼 `DELETE` 回 409 `coupon_in_use`；`GET /coupons` 分頁信封無 `counts`。後台 `POST /api/admin/coupons` 仍回 200（守門）。
- OpenAPI：新路由都在 `paths`，request body 與 200／201 schema 有 `properties`；`not_exposed` 不含留言與分類標籤寫入。
- 既有測試：`integrations_api_integration_test.go` 的 `mustList` 與 `not_exposed` 相關斷言依本工單調整，其餘不放寬。

## 不在本工單

- 選單、回收桶、文章版本、使用者管理、備份、轉址、通知中心的 v1 端點。
- 每個串接自訂限流上限。
- 權限的「唯讀全站」捷徑（例如 `*.read`）。

## 決策紀錄

- 新增權限字串而不是沿用 `posts.write` 之類的寬權限：站長要能給「只審留言」的金鑰；一把能改文章的金鑰不該順便能退款。
- 一次遷移加六項而不是每個端點各一次：遷移檔不可變，少開一次就少一個檔。
- 沿用後台 handler 而不是重寫：事件、通知、狀態機、冪等都在 handler 內，重寫一定漏；v1 只包回應。
- 寫入回更新後的資源而不是 `{"status":"ok"}`：客戶端少打一次 GET，也與 v1 既有的文章、商品寫入一致。
- 作者不能經 v1 寫分類標籤：`taxonomy.write` 用 `editorRoles`，後台讓作者建標籤是畫面流程的便利，不是權限模型的一部分。
- 訂單狀態路徑用 `/orders/:id/status`：後台用 `PUT /orders/:id` 但 body 只有狀態與物流欄位，對外叫「整筆更新」會誤導。
