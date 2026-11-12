# 34 — `/api/v1` 的 OpenAPI 每個回應與 request body 都有明確欄位

Epic：帳號與存取
User Story：用 OpenAPI 產 client 的開發者對 `/api/v1` 每個端點都拿到有型別的物件，不會拿到 `any`；文件說的欄位就是伺服器回的欄位，測試會抓不一致。
Blocked by：27（契約與 struct 產 schema 的做法，已合併）。工單 32 依賴本工單的 `integrationOrderDetail` 與 `integrationSchemaOf`。

## 背景與決定

現況（`internal/server/integrations_discovery.go`，行號以 main `0dc56d9` 為準）：

| 端點 | 現況 | 實際回應 |
|---|---|---|
| `GET /categories`、`GET /tags` | 集合 items 為 `additionalProperties: true`（`:306-310`） | `core.Category`（`core/content.go:14-30`，15 欄含 `post_count`）、`core.Tag`（`:33-43`，9 欄） |
| `GET /orders/:id` | 整個物件 `additionalProperties: true`（`:339-340`） | `adminOrderGet` 的 map（`shop_orders_admin.go:305-340`）：37 個頂層鍵，含 `total`／`total_amount` 重複、`snapshot` 與 `shipping`／`invoice` 重複；`items` 有 `qty`／`quantity` 重複與 `price`（舊欄位）／`unit_price`（快照）；`timeline` 有 `from`／`from_status`、`to`／`to_status`／`status` 重複；`payment` 有紀錄、零元訂單、其他三種情況形狀不同（`:258-275`） |
| `GET /me` | 缺 `role_cap`、`effective_role`（`:267-276`） | `integrationMe` 回這兩欄（`integrations_auth.go:132-143`） |
| `GET /themes/:name/config` | `colors`、`layout` 只有 `type: object`，`custom_fields` items 空，`webmcp_tools` 為 `{}`（`:345-346`） | `core.ThemeConfig`（`core/theme.go:6-20`），`colors` 是 `ThemeColors`（`:23` 起） |
| `GET /themes/:name/settings`、`POST` 的 body | `colors`、`layout`、`custom` 為 `{}`（`:348-352`、`:433`） | `core.ThemeSettings` 三個 `json.RawMessage`，可能為 `null` |
| 文章資源（`integrationResourceSchema`，`:189`） | `author` 只有 `type: object`、`meta` 為 `{}`、`categories`／`tags` items 為 `type: object`、`diagnostics` items 空 | `core.Post` 含 `Author core.User`（沒 preload 時是全空物件）、`meta` 為 RawMessage |
| 商品資源（`:194`） | `variants` items 為 `additionalProperties: true` | `core.ProductVariant` |
| `GET /me/events` | `summary` 為 `additionalProperties: true` | `json.RawMessage`，內容依 `action` 不同 |
| `PUT /settings` body | 只有 `additionalProperties: oneOf[string,boolean,integer]`（`:427`） | `handleSettingsWrite` 只接受已註冊且不在拒絕清單的鍵（`settings_api.go:87-97`：拒絕 `inject_*`、`queue_driver`、`queue_url_enc`、`cache_driver`、`cache_url_enc`、Tab 為 internal 的鍵但 `maintenance_started_at` 除外），值接受字串、布林、整數後轉成字串（`:48-78`） |
| `GET /openapi.json` | `additionalProperties: true`（`:264-265`） | OpenAPI 文件本身 |
| request body | `POST /media/files`、`/media/files/:id/replace` 是手寫 multipart（`:440-447`）、`POST /themes/activate` 是手寫 `{name}`（`:430-432`） | — |

- 工單 27 的做法已證明可行：回應用 struct 定義、`integrationJSONFields` 取 json tag 產 schema、測試比對鍵集合。
- 決定：所有 v1 回應與 request body 的 schema 都從 Go struct 產生（`integrationSchemaOf`），不再手寫 map。`additionalProperties: true` 只保留在 `GET /openapi.json` 與 `/me/events` 的 `summary`。
- 決定：`GET /orders/:id` 定自己的訂單詳情 struct（工單 32 也要用），欄位見「名詞」；後台 `GET /api/admin/orders/:id` 不變。
- 決定：文章資源的 `author` 改成固定的作者摘要 `{id, username, display_name}` 並真的 preload；不再輸出全空的 `User`。`GET /posts/:id`、`POST`、`PUT` 的回應跟著變（後台不變）。
- 決定：`PUT /settings` 的 body schema 列出 `handleSettingsWrite` 實際接受的鍵（`settings.Specs()` 扣掉拒絕清單），每個鍵的型別維持現況的 `oneOf[string, boolean, integer]`（handler 接受這三種再轉成字串，`settings_api.go:48-78`），密鑰 Kind 標 `writeOnly`，`additionalProperties: false`。
- 不改：各端點的業務行為；後台回應；工單 27 已定的清單信封與摘要欄位。

## 名詞

| struct | 欄位 |
|---|---|
| `integrationAuthorSummary` | `id`、`username`、`display_name` |
| `integrationContentResource`（單筆文章或頁面） | `id`、`title`、`slug`、`content`、`source`、`format`、`type`、`status`、`visibility`、`author_id`、`author`（`integrationAuthorSummary`）、`template`、`meta`（RawMessage）、`excerpt`、`cover`、`allow_comments`、`pinned`、`published_at`、`created_at`、`updated_at`、`seo_index`、`seo_ai`、`categories`（`[]core.Category`）、`tags`（`[]core.Tag`）。沒有 `deleted_at`、`deleted_by_id` |
| `integrationContentWriteResult`（`POST`／`PUT` 文章頁面的回應） | `integrationContentResource` 全部欄位加 `diagnostics`（`[]integrationDiagnostic`） |
| `integrationDiagnostic` | `markup.Diagnostic`（`markup/types.go:74-80`）的五個欄位加 json tag：`line`、`col`、`severity`、`code`、`message`。現況沒有 tag，鍵是 `Line`、`Col`、`Severity`、`Code`、`Message`；本工單把 v1 的 200／201 與 400 `diagnostics` 回應都改成小寫鍵（後台回應不變） |
| `integrationOrderDetail` | `id`、`number`、`status`、`email`、`phone`、`currency`、`subtotal`、`discount_total`、`shipping_total`、`tax_total`、`rounding_adjustment`、`total_amount`、`refunded_total`、`refund_requested`、`coupon_code`、`coupon_id`、`shipping_method`、`shipping`（RawMessage）、`invoice`（RawMessage）、`payment_provider`、`payment_method`、`carrier`、`tracking_no`、`internal_note`、`created_at`、`updated_at`、`paid_at`、`shipped_at`、`completed_at`、`cancelled_at`、`items`、`timeline`、`payment`、`customer`、`allowed`（`[]string`）。沒有 `total`、`snapshot` |
| `integrationOrderDetail.items[]` | `id`、`variant_id`、`product_id`、`title`、`variant_name`、`sku`、`image`、`quantity`、`unit_price`、`line_total`。沒有 `qty`、`price` |
| `integrationOrderDetail.timeline[]` | `id`、`at`、`kind`、`from`、`to`、`message`、`actor_user_id`、`meta`（RawMessage）。沒有 `status`、`from_status`、`to_status` |
| `integrationOrderDetail.payment`（指標，可為 `null`） | `id`、`provider`、`method`、`merchant_trade_no`、`amount`、`currency`、`status`、`provider_trade_no`、`paid_at`、`masked_info`。依後台現況（`shop_orders_admin.go:256-275`）：訂單零元且 `payment_provider`、`payment_method` 都是 `none` 時，不論有沒有付款紀錄，`provider`、`method`、`status` 為 `"none"`、`amount` 0、`currency` 為訂單幣別、其餘欄位 `null`；否則有付款紀錄 → 全部填值；沒有紀錄 → `payment` 為 `null` |
| `integrationOrderDetail.customer` | `email`、`phone`、`name`（可 `null`）、`user_id`（可 `null`）、`order_count`（等於後台 `history_count`，`shop_orders_admin.go:304`；後台鍵名不改） |
| `integrationMeResponse`（`integrationMe` 已是 handler 函式名） | 現有 `/me` 的欄位加 `role_cap`（可 `null`）、`effective_role` |
| `integrationThemeSettings` | `theme_name`、`colors`、`layout`、`custom`（三個 RawMessage，可 `null`） |
| `integrationMediaUploadInput`（multipart） | `file`（必填）、`alt`、`policy_id`、`group_id` |
| `integrationMediaReplaceInput`（multipart） | `file`（必填） |
| `integrationThemeActivateInput` | `name`（必填） |

其餘沿用既有 struct：`integrationContentSummary`、`integrationOrderSummary`（工單 27）、`adminProductView`（商品，`variants` 為 `[]core.ProductVariant`，`attributes` 為 `json.RawMessage`）、`core.Category`、`core.Tag`、`core.ThemeConfig`、`integrationEventResponse`、`core.Option`、`mediaFileJSON` 對應的 struct（新定 `integrationMediaFile`，欄位等於 `mediaFileJSON` 的 16 個鍵，`media_api.go:919-936`）、外掛 `{id,name,version,description,author}`、索引文件。

## 規則

### R1 schema 產生

- `integrationSchemaOf(v any) map[string]any`：struct → `{type: object, properties, required}`（`required` 為沒有 `omitempty` 的欄位；有 `omitempty` 的欄位出現在 `properties` 但不在 `required`）；`[]T` → array；`*T`、`sql.Null*`、`gorm.DeletedAt` → 對應型別加 `null`；`json.RawMessage` → `{}`（任意 JSON，可 `null`）；`map[string]T` → `{type: object, additionalProperties: schema(T)}`；`time.Time` → `string`／`date-time`；內嵌 struct 攤平；`json:"-"` 略過。
- 每個資源一個 struct（檔案 `internal/server/integration_schemas.go`），清單見「名詞」。request body：`integrationContentInput`、`integrationProductInput`（取代手寫）、`integrationThemeSettingsInput`、`integrationMediaPatchInput`、`integrationSettingsInput`（從 `settings.WritableSpecs()` 產生，每鍵 `oneOf[string, boolean, integer]`，密鑰 `writeOnly`，`additionalProperties: false`）、三個新的 multipart 與 activate input。multipart 的 schema 用 `multipart/form-data` 的 `properties`，欄位名即表單欄位名。

### R2 不一致即失敗

`integrations_regression_test.go` 的 `TestIntegrationRegressionDiscoveryAndOpenAPISchemasMatchHandlers`（`:1091-1211`）擴成：對 **每一條** v1 路由，用測試資料實際呼叫一次，把回應 JSON 的鍵集合與 OpenAPI 該操作 schema 比對：回應的鍵必須是 schema `properties` 的子集合，且 schema `required` 的鍵必須都出現在回應（`omitempty` 欄位可缺）；遞迴到第二層，但 RawMessage（schema 為 `{}`）與 `map[string]T` 的子鍵不比對。request body 用一個合法 body 打一次，body 的鍵（multipart 為表單欄位名）必須都在 schema 的 `properties` 裡。例外清單只允許 `GET /openapi.json` 與 `/me/events` 的 `summary`。工單 32 新增的路由也要納入這個比對。

## 程式（格子）

1. `internal/server/integration_schemas.go`（新檔）：`integrationSchemaOf` 與所有 struct。
2. `internal/server/integrations_discovery.go`：`integrationSuccessSchema`、`integrationRequestBody`、`integrationResourceSchema`、`integrationMediaFileSchema`、`integrationProductInputSchema`、`integrationContentSummarySchema`、`integrationOrderSummarySchema` 全部改成呼叫 `integrationSchemaOf(對應 struct)`；手寫 map 刪除。
3. `internal/server/handlers_posts.go` 的 `getPost`、`createPost`、`updatePost`：v1 路徑（`c.Get("integration.identity") != nil`）回 `integrationContentResource`／`integrationContentWriteResult`（含 preload 的作者摘要）；後台路徑不變。做法：在 handler 回應前依 context 分流，後台測試不得受影響。
4. `internal/server/shop_orders_admin.go`：`adminOrderGet` 抽出 `loadOrderDetail(ctx, id) (integrationOrderDetail, error)`（含 `customer.order_count` 的歷史查詢），後台 handler 由它組原本的 map（含重複鍵、`snapshot`、`history_count`，以及零元訂單 `payment` 的 `kind: "none"`）；v1 的 `GET /orders/:id` 新包裝直接回 `integrationOrderDetail`。
5. `internal/server/integrations_auth.go` 的 `integrationMe` handler 改回 `integrationMeResponse` struct。
6. `internal/server/media_api.go`：`mediaFileJSON` 改回 `integrationMediaFile` struct（後台與 v1 共用，鍵不變）。
7. `internal/settings`：`Specs()` 已匯出；新增 `WritableSpecs()` 回傳扣掉 `settings_api.go:87-97` 拒絕清單後的規格，`handleSettingsWrite` 與 schema 都用它（拒絕清單只寫一次）。
8. 文件：工單的驗收紀錄；工單 27 開頭加一行指向本工單。

## 驗收（測試契約）

- R2 的比對測試對所有 v1 路由通過。
- 「測試有牙齒」：`mediaFileJSON` 在格子 6 改成 struct 之前，先在它的 map 多加一個鍵跑一次 R2 測試，必須失敗；審查時主代理執行並把輸出記在 工單的驗收紀錄。
- OpenAPI 文件驗證：不加依賴，自寫檢查：所有 `$ref` 可解析、每個 schema 都有 `type`、`oneOf` 或 `$ref`（空 schema `{}` 豁免，它是 RawMessage 的「任意 JSON」）、除例外清單外沒有 `additionalProperties: true`。
- `GET /orders/:id`（v1）的鍵集合等於 `integrationOrderDetail`，`items[]` 沒有 `qty`、`price`，`timeline[]` 沒有 `status`、`from_status`、`to_status`；三種 `payment` 情況各植一筆訂單驗證；後台 `GET /api/admin/orders/:id` 仍有 `total`、`snapshot`、`items[].qty`（守門）。
- `GET /posts/:id`（v1）的 `author` 恰好 `{id, username, display_name}` 且值正確，沒有 `deleted_at`；`PUT /posts/:id` 的回應有 `diagnostics` 且元素鍵為 `line`、`col`、`severity`、`code`、`message`，含 diagnostics 的 400 回應亦同；後台 `GET /api/admin/posts/:id` 的 `author` 形狀與後台 diagnostics 的大寫鍵不變（守門）。
- `GET /me` 有 `role_cap` 與 `effective_role` 且 schema 列出。
- `PUT /settings` 的 body schema `properties` 鍵集合等於 `WritableSpecs()`，每鍵 `oneOf` 三型；送 `cache_driver` 或未註冊鍵回 400 `bad_request`（既有行為）；密鑰鍵在 schema 標 `writeOnly`。
- `POST /media/files` 的 request body 是 `multipart/form-data` 且 `properties` 為 `file`、`alt`、`policy_id`、`group_id`。
- 工單 27 的契約測試全部維持通過。

## 不在本工單

- 匿名 API（工單 30）與 auth API 的 schema。
- 用 OpenAPI 產生 SDK。
- `/me/events` 的 `summary` 依 `action` 分型（`oneOf`）。

## 跨工單

- 32 的訂單寫入回 `integrationOrderDetail`、用 `loadOrderDetail`；32 新路由的 schema 由本工單的 `integrationSchemaOf` 產生並納入 R2 比對。本工單先做。
- 30 若先做，`integrationSchemaOf` 由 30 建在同一個檔案，本工單沿用。

## 決策紀錄

- 從 struct 反射產 schema 而不是手寫：手寫已經漏了 `role_cap`、`effective_role` 兩欄，之後只會更多；反射讓「回應是什麼」和「文件說什麼」是同一份程式。
- 例外清單只有兩項：`openapi.json` 本身與事件 `summary`；RawMessage 與 map 的子鍵不是例外，是 schema 本來就說「任意 JSON」。
- 訂單詳情定 v1 自己的 struct：後台 map 有相容重複鍵與依情況變形的 `payment`，放進文件會變成三年的包袱；工單 32 的寫入端點也要回同一個形狀。`price` 是舊欄位、`unit_price` 是快照，對外只留快照。
- `author` 改摘要並 preload：現況是沒 preload 的空 `User`，等於文件裡一個永遠是零值的物件，不如給真的三個欄位。
- `PUT /settings` 用 `WritableSpecs()` 產 schema：伺服器本來就只收這些鍵，文件應該說實話，而且拒絕清單從此只寫一次。型別維持三型 `oneOf`，因為 handler 真的收三型。
- `omitempty` 欄位用「子集合加 required 必在」比對而不是「相等」：Go 的 `omitempty` 讓回應缺鍵是正常的，硬要相等只會逼實作去掉 `omitempty` 改變回應。
- `diagnostics` 改小寫鍵是行為變更：現況的大寫鍵是忘了加 tag，v1 其他所有鍵都是小寫蛇形，趁定 struct 時一次改對；後台不動。
- 「測試有牙齒」用 `mediaFileJSON` 當樣本：回應直接序列化 struct 的路由，加欄位時回應和 schema 一起變，測不出來；只有回應來自 map 的路由能證明比對真的在比。
