# 32 — 機器 API 補到能當無頭後台：留言審核、訂單處理、分類標籤寫入、折扣碼

Epic：帳號與存取
User Story：站長給自動化程式或 AI agent 一把金鑰，就能審留言、處理訂單（出貨、退款、標記已付、加備註）、整理分類與標籤、管理折扣碼，不必再開後台網頁。每個動作沿用後台同一套規則、同一批外掛事件與通知。
Blocked by：27（v1 契約，已合併）、28（外掛事件，已合併）、34（訂單詳情 struct 與 `integrationSchemaOf`）。

## 背景與決定

現況（行號以 main `0dc56d9` 為準）：

- `/api/v1` 路由表有 37 條（`integration_routes.go:40-76`），`/api/admin` 有 216 條。`GET /api/v1` 的 `not_exposed` 列了留言、選單、分類寫入、標籤寫入、回收桶、版本、金流憑證、OAuth 憑證、外掛上傳、串接管理、檔名式媒體、系統日誌共 12 項（`integrations_discovery.go:77`）；折扣碼與訂單寫入連列都沒列。
- 權限字串：常數與 `AllPermissions()` 在 `internal/integration/integration.go:30-77`（16 個）；中文說明 `permissionDescription` 在 `internal/server/integrations_discovery.go:50`；角色 `permissionRoles` 在 `integrations_admin.go:54`，由路由表的 `RequiredRoles` 推導，不用手改。資料庫以 `integrations_permissions_check`（`internal/data/migrations/0025_agents.sql:18-26`，0027 改名）限制為同一份清單；遷移檔交付後不可變（ENG.md），目前最新是 0027。
- 後台的權限勾選格由 `GET /api/v1` 的 `permissions` 動態產生（`web/admin/assets/integrations.js:295-312,1217-1223`），但資源的中英文標籤寫死在 `:53-63`（posts、pages、media、products、orders、themes、plugins、settings、logs），沒有的資源會直接顯示原始鍵名。
- 後台 handler 現況：留言（`handlers_comments.go:76-89`，角色 admin／editor）、分類標籤（`handlers_taxonomy.go:43-53`，寫入 admin／editor，建立標籤連 author 都可以）、訂單（`shop_admin_routes.go:135-140`，admin）、折扣碼（`shop_admin_routes.go:142-148`，admin）。都用 `currentUser(c)` 取身分，沒有依賴 CSRF 或 Session，可以直接掛到 v1。
- 事件與通知都在 handler 內：留言審核發 `OnCommentUpdate`（`handlers_comments.go:700-712`）、回覆發 `OnCommentCreate`、刪除發 `OnCommentDelete`；訂單轉換經 `core.OrderService.Transition` 寫 `OrderEvent` 並發 `OnOrderUpdate`，出貨寄信（`shop_orders_admin.go:428-436`）、退款發 `OnOrderRefund` 並通知（`:785-797`）、標記已付通知（`:835-845`）。沿用 handler 就不會漏事件。
- 後台回應形狀：留言清單 `{items,total,page,per_page,counts}`，item 是 `adminCommentRow`（`handlers_comments.go:491`）；分類、標籤清單裸陣列；訂單狀態寫入回 `{"status":"ok"}`、不合法轉換回 409 `{"allowed":[...]}`；折扣碼建立回 200 單筆（不是 201）、PATCH 回 `{coupon, targets}`、欄位錯誤回 `{"error","fields"}`。
- 留言 handler 的回收桶規則（`handlers_comments.go:664-668`）：對回收桶內的留言做非 `restore` 的動作回 404；對不在回收桶的留言做 `restore` 回 409。
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

新遷移檔 `internal/data/migrations/0028_integration_permissions_v2.sql`（若屆時 0028 已被用掉就接續下一號）：同一交易內 `ALTER TABLE integrations DROP CONSTRAINT IF EXISTS integrations_permissions_check` 再 `ADD CONSTRAINT` 為 22 項清單；不動既有列。`integration.go` 的常數與 `AllPermissions()`（順序：既有 16 項後接上表六項）、`integrations_discovery.go` 的 `permissionDescription` 同步；`permissionRoles` 由路由表自動推導。

## 規則

### R1 路由

| 方法 路徑 | 權限 | 沿用 handler | v1 回應 |
|---|---|---|---|
| `GET /comments` | comments.read | `listAdminComments` 的查詢（抽成查詢函式） | 分頁信封，item 為 `adminCommentRow` 形狀；query `status`（`pending|approved|spam|trash`，空或 `all` 皆為全部）、`post_id`、`q`、`page`、`per_page`（工單 27 規則） |
| `GET /comments/:id` | comments.read | 新：查一筆 | 單筆 `adminCommentRow` |
| `POST /comments/:id/{approve,unapprove,spam,trash,restore}` | comments.write | `commentAction` | 200 更新後單筆 |
| `POST /comments/bulk` | comments.write | `bulkCommentAction` | 200 `{updated}` |
| `POST /comments/:id/reply` | comments.write | `replyComment` | 201 新留言 |
| `PUT /comments/:id` | comments.write | `updateComment` | 200 單筆 |
| `DELETE /comments/:id` | comments.write | `deleteCommentPermanently` | 200 `{message}`（與 v1 其他 DELETE 一致） |
| `POST /categories`、`PUT /categories/:id`、`DELETE /categories/:id`、`POST /categories/reorder` | taxonomy.write | `createCategory`、`updateCategory`、`deleteCategory`、`reorderCategories` | 201 單筆、200 單筆、200 `{message}`、200 集合信封 |
| `POST /tags`、`PUT /tags/:id`、`DELETE /tags/:id` | taxonomy.write | `createTag`、`updateTag`、`deleteTag` | 201、200、200 `{message}` |
| `PUT /orders/:id/status` | orders.write | `adminOrderUpdateStatus` | 200 訂單詳情（工單 34 的 `integrationOrderDetail`）；不合法轉換回 409 `invalid_transition`，`details.allowed` 為可轉換清單 |
| `POST /orders/:id/notes` | orders.write | `adminOrderAddNote` | 200 訂單詳情 |
| `PATCH /orders/:id/address` | orders.write | `adminOrderUpdateAddress` | 200 訂單詳情 |
| `POST /orders/:id/refund` | orders.write | `adminOrderRefund` | 200 訂單詳情；`Idempotency-Key` 標頭與 body 的 `idempotency_key` 沿用後台規則 |
| `POST /orders/:id/mark-paid` | orders.write | `adminOrderMarkPaid` | 200 訂單詳情 |
| `GET /coupons`、`GET /coupons/:id`、`POST /coupons/preview` | coupons.read | `adminCouponsList`（抽查詢）、`adminCouponGet`、`adminCouponPreview` | 分頁信封（`page`／`per_page` 依工單 27 R2，去掉後台的 `counts` 與 `impact`），item 為「折扣碼列」：後台清單 item 的攤平形狀（`core.Coupon` 欄位加 `targets`，`shop_coupons_admin.go:578-591`），定成 `integrationCouponRow` struct；單筆與預覽：`GET /coupons/:id` 也回折扣碼列（不是後台的 `{coupon, targets}`），預覽回後台的預覽物件（定 struct） |
| `POST /coupons`、`PATCH /coupons/:id`、`DELETE /coupons/:id`、`POST /coupons/generate` | coupons.write | `adminCouponCreate`、`adminCouponPatch`、`adminCouponDelete`、`adminCouponGenerate` | 201 折扣碼列（後台回 200 單筆）、200 折扣碼列、200 `{message}`、201 `{batch_id,count,codes}` |

後台 `PUT /api/admin/orders/:id` 的路徑在 v1 改成 `/orders/:id/status`，避免和「整筆更新」混淆。

「寫入回更新後的資源」的做法：v1 包裝用與 `integrationErrorEnvelope` 相同的 recorder 技巧呼叫後台 handler，2xx 時丟掉後台寫出的 body（`{"status":"ok"}` 或後台單筆），重新讀取資源後以 v1 形狀寫出；非 2xx 原樣交給信封。

### R2 錯誤對照

後台 `{"error": msg}` 經 `integrationErrorEnvelope` 已會轉成信封；本工單補明確 `code`：

| 後台現況 | v1 |
|---|---|
| 留言：404 `Comment not found`（含回收桶內做非還原動作） | 404 `not_found` |
| 留言：409 `Comment is not in trash`（對不在回收桶的留言做 `restore`） | 409 `conflict` |
| 留言：400 `Unknown status`（`:602`）、內文長度（`:862,952`） | 400 `bad_request`，前者 `details.param = "status"`，後者 `details.fields = {"body": "..."}` |
| 訂單：409 `{"allowed": [...]}` | 409 `invalid_transition` 加 `details.allowed` |
| 訂單：400 `shipping details are required` | 400 `bad_request` 加 `details.fields = {"carrier": "...", "tracking_no": "..."}`（缺哪個列哪個） |
| 訂單：422 `provider refund is not supported` | 422 `refund_unsupported` |
| 訂單：409 `refund request must be resolved before shipping` | 409 `refund_pending` |
| 折扣碼：409 `duplicate code`、批次產生 `coupon conflict` | 409 `coupon_code_taken` |
| 折扣碼：409 `used coupons can only be disabled`、`used coupon cannot be deleted` | 409 `coupon_in_use` |
| 折扣碼：400／422 `{"error","fields"}` | 400 `bad_request` 加 `details.fields` |

這些 `code` 加進 `integrationErrorCodes` 與 `integrationErrorDescription`。

### R3 文件與畫面

- `integrationNotExposed` 拿掉留言、分類寫入、標籤寫入三列，剩下的保留。
- `permissionDescription` 補六項中文說明；`GET /api/v1` 的 `permissions` 自然多六筆。
- `web/admin/assets/integrations.js:53-63` 的資源標籤表補 `comments`（留言／Comments）、`taxonomy`（分類與標籤／Taxonomy）、`coupons`（折扣碼／Coupons）。依 AGENTS.md，權限勾選格多了三列，`design/gen/gen_integrations.py` 產生的 `design/project/Integrations.dc.html`、`IntegrationDetail.dc.html` 的權限區塊同一次提交更新並重新產生，線上畫布同步。
- OpenAPI：每條新路由的 request body 與回應 schema 由工單 34 的 `integrationSchemaOf` 從 struct 產生：留言用 `adminCommentRow`、分類標籤用 `core.Category`／`core.Tag`、訂單用 `integrationOrderDetail`、折扣碼用 `core.Coupon` 加 `targets` 的包裝 struct。工單 34 的「每條路由實打比對」測試要涵蓋這些新路由。
- `skills/jelly-cms/SKILL.md` 加「審留言」「處理訂單」兩段短流程；README 權限表；`STATUS.md`；工單 14 開頭加一行。

## 程式（格子）

1. `internal/integration/integration.go`：六個常數與 `AllPermissions`；`internal/data/migrations/0028_integration_permissions_v2.sql`；`internal/data/integration_permissions_v2_migration_test.go`（遷移測試放 `internal/data`）。
2. `internal/server/integration_comments.go`（新檔）：`GET /comments`、`GET /comments/:id` 與寫入的 v1 包裝；`handlers_comments.go` 的 `listAdminComments` 抽出查詢函式（後台 handler 不變）。
3. `internal/server/integration_taxonomy.go`（新檔）：分類標籤寫入的 v1 包裝（主要是回應狀態碼與信封）。
4. `internal/server/integration_orders.go`（新檔）：訂單五條寫入的 v1 包裝，成功後以工單 34 的 `loadOrderDetail` 重讀並回 `integrationOrderDetail`。
5. `internal/server/integration_coupons.go`（新檔）：折扣碼七條，v1 一律回 `integrationCouponRow`（攤平），不用後台的 `couponAdminResponse`（`{coupon, targets}`，後台繼續用）；`shop_coupons_admin.go` 的 `adminCouponsList` 抽查詢函式。
6. `internal/server/integration_routes.go`：路由表加列（`RequiredRoles` 依權限表）；`integrations_discovery.go`：`permissionDescription`、`integrationErrorCodes`、`integrationErrorDescription`、`integrationNotExposed`、schema 與 request body 分支；`integrations_regression_test.go` 的實打比對（工單 34）把新路由納入。
7. `web/admin/assets/integrations.js`、`design/gen/gen_integrations.py` 與它產生的 `design/project/Integrations.dc.html`、`IntegrationDetail.dc.html`：R3。
8. 文件：R3。

## 驗收（測試契約）

新測試檔 `internal/server/api_v1_coverage_integration_test.go`；遷移測試在 `internal/data`：

- 遷移：對空庫與含既有串接的庫各跑一次 `MigrateDB`，`integrations_permissions_check` 恰有一個且接受 22 項、拒絕 `comments.admin` 這種未知字串；`agents_permissions_check` 不存在；舊列不變；重跑冪等。
- 權限：建立串接時勾六個新權限都成功；`GET /api/v1` 的 `permissions` 有這六筆且 `required_roles` 正確；`GET /me` 的 `effective_permissions` 依擁有者角色裁剪（editor 擁有者的 `orders.write` 不在有效清單）。
- 留言：植入 pending 留言，`POST /comments/:id/approve` 回 200、`status = approved`、`integration_events` 有紀錄、外掛事件 `OnCommentUpdate` 被發出（沿用 `plugin_events_harness_test.go` 的接收方式）；沒有 `comments.write` 的金鑰回 403 `permission_missing`；`GET /comments?status=pending` 分頁信封且沒有 `counts` 鍵，`status=all` 與不帶 `status` 的 `total` 相同；回收桶內的留言 `approve` 回 404 `not_found`；對不在回收桶的留言 `restore` 回 409 `conflict`。
- 分類標籤：editor 擁有者可建改刪；author 擁有者的串接（建立串接時不驗擁有者角色，執行時才裁剪，與工單 27 的 `owner_role_insufficient` 相同）對 `POST /tags` 回 403 `owner_role_insufficient`；後台 `POST /api/admin/tags` 以 author Cookie 仍 201（守門）。
- 訂單：`PUT /orders/:id/status` 從 pending 轉 paid 回 200 且回應鍵集合等於 `integrationOrderDetail`、`OnOrderUpdate` 與 `OnOrderPaid` 發出；轉不合法狀態回 409 `invalid_transition` 且 `details.allowed` 等於 `core.AllowedTransitions`；轉 shipped 缺物流資訊回 400 且 `details.fields` 有 `carrier`、`tracking_no`；有待處理退款申請時轉 shipped 回 409 `refund_pending`；退款帶 `Idempotency-Key` 重送同內容回同結果、不同內容回 409；`mark-paid` 對非銀行轉帳訂單回 400。後台 `PUT /api/admin/orders/:id` 仍回 `{"status":"ok"}`（守門）。
- 折扣碼：`POST /coupons` 回 201 且鍵集合等於 `integrationCouponRow`；`GET /coupons/:id` 同形狀；重複代碼回 409 `coupon_code_taken`；欄位錯誤回 400 且 `details.fields` 有鍵；已使用的折扣碼 `DELETE` 與 `PATCH`（改成非停用）各回 409 `coupon_in_use`；`GET /coupons?per_page=101` 回 400；`GET /coupons` 分頁信封無 `counts`、`impact`。後台 `POST /api/admin/coupons` 仍回 200、`GET /api/admin/coupons` 仍有 `counts` 與 `impact`（守門）。
- OpenAPI：新路由都在 `paths`，request body 與 200／201 schema 由 struct 產生且有 `properties`；`not_exposed` 不含留言與分類標籤寫入；工單 34 的實打比對測試對新路由通過。
- 既有測試：`internal/integration/integration_test.go:84-92` 與 `integrations_keys_integration_test.go:39-43` 的 `agtAllPermissions`（`integrations_api_integration_test.go:121` 用它做相等比對）改成 22 項；`integrations_api_integration_test.go` 的 `mustList` 與 `not_exposed` 相關斷言依本工單調整；其餘不放寬。
- 瀏覽器驗收：後台「API 串接」建立頁的權限勾選格出現「留言」「分類與標籤」「折扣碼」三列且中英文都有標籤，與設計板一致。

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
- 折扣碼建立回 201 且與單筆同形狀：後台回 200 單筆是歷史包袱；v1 的「建立回 201 加完整資源」是既有慣例。
- 依賴 34：訂單詳情的形狀由 34 決定，先做 34 才不會做出兩個訂單詳情。
