# 33 — `/api/v1` 的 OpenAPI 每個回應與 request body 都有明確欄位

Epic：帳號與存取
User Story：用 OpenAPI 產 client 的開發者對 `/api/v1` 每個端點都拿到有型別的物件，不會拿到 `any`；文件說的欄位就是伺服器回的欄位，測試會抓不一致。
Status：待實作。
Blocked by：27（契約與 struct 產 schema 的做法，已合併）。

## 背景與決定

現況（`internal/server/integrations_discovery.go`，行號以 main `0dc56d9` 為準）：

| 端點 | 現況 | 實際回應 |
|---|---|---|
| `GET /categories`、`GET /tags` | 集合 items 為 `additionalProperties: true`（`:306-310`） | `core.Category`（`core/content.go:14-30`，15 欄含 `post_count`）、`core.Tag`（`:33-43`，9 欄） |
| `GET /orders/:id` | 整個物件 `additionalProperties: true`（`:339-340`） | `adminOrderGet` 的 map（`shop_orders_admin.go:305-340`）：37 個頂層鍵、`items`（含 `qty`／`quantity`、`price`／`unit_price` 重複鍵）、`timeline`、`payment`（有無付款紀錄時形狀不同）、`customer`、`snapshot`、`allowed` |
| `GET /me` | 缺 `role_cap`、`effective_role`（`:267-276`） | `integrationMe` 回這兩欄（`integrations_auth.go:132-143`） |
| `GET /themes/:name/config` | `colors`、`layout` 只有 `type: object`，`custom_fields` items 空，`webmcp_tools` 為 `{}`（`:345-346`） | `core.ThemeConfig`（`core/theme.go:6-20`），`colors` 是 `ThemeColors`（`:23` 起） |
| `GET /themes/:name/settings`、`POST` 的 body | `colors`、`layout`、`custom` 為 `{}`（`:348-352`、`:433`） | `core.ThemeSettings` 三個 `json.RawMessage`，可能為 `null` |
| `GET /posts/:id` 等文章資源 | `author` 只有 `type: object`、`meta` 為 `{}`、`categories`／`tags` items 為 `type: object`、`diagnostics` items 空（`:296-304`） | `core.Post` 含 `Author core.User`（沒 preload 時是全空物件）、`meta` 為 RawMessage |
| `GET /products*` | `variants` items 為 `additionalProperties: true`（`:324-334`） | `core.ProductVariant` |
| `GET /me/events` | `summary` 為 `additionalProperties: true` | `json.RawMessage`，內容依 `action` 不同 |
| `PUT /settings` body | 只有 `additionalProperties: oneOf[string,boolean,integer]`（`:427`） | `handleSettingsWrite` 只接受已註冊的鍵（`settings` 套件），未註冊回 400 |
| `GET /openapi.json` | `additionalProperties: true`（`:264-265`） | OpenAPI 文件本身 |

- 工單 27 的做法已證明可行：回應用 struct 定義、`integrationJSONFields` 取 json tag 產 schema、測試比對鍵集合。
- 決定：所有 v1 回應與 request body 的 schema 都從 Go struct 產生（一個 `integrationSchemaOf(v any)` 反射函式：依欄位型別給 `string`／`integer`／`number`／`boolean`／`array`／巢狀 object，指標與 `RawMessage` 為 nullable），不再手寫 map。`additionalProperties: true` 只保留在 `GET /openapi.json` 與 `/me/events` 的 `summary`（內容確實依事件而異）。
- 決定：`GET /orders/:id` 定自己的訂單詳情 struct（工單 31 也要用），去掉 `qty`／`price` 重複鍵，`payment` 固定形狀（沒有付款紀錄時欄位為 `null` 而不是換形狀），`allowed` 保留。後台 `GET /api/admin/orders/:id` 不變。
- 決定：文章資源的 `author` 改成固定的作者摘要 `{id, username, display_name}` 並真的 preload；不再輸出全空的 `User`。`GET /posts/:id`、`POST`、`PUT` 的回應跟著變（後台不變）。
- 決定：`PUT /settings` 的 body schema 列出所有已註冊的設定鍵與型別（從 `settings` 套件的註冊表產生），`additionalProperties: false`，與伺服器「未註冊回 400」的行為一致。
- 不改：各端點的業務行為；後台回應；工單 27 已定的清單信封與摘要欄位。

## 規則

### R1 schema 產生

- `integrationSchemaOf(v any) map[string]any`：struct → `{type: object, properties, required}`（`required` 為沒有 `omitempty` 的欄位）；`[]T` → array；`*T`、`json.RawMessage`、`sql.Null*`、`gorm.DeletedAt` → nullable；`time.Time` → `string`／`date-time`；`map[string]T` → `additionalProperties: schema(T)`；巢狀 struct 內嵌（`core.Product` 內嵌於 `adminProductView`）攤平。
- 每個資源一個 struct（檔案 `internal/server/integration_schemas.go`）：`integrationContentSummary`（已有）、`integrationContentResource`（單筆文章，含 `author` 摘要）、`integrationCategory`、`integrationTag`、`integrationProductView`（沿用 `adminProductView`，`variants` 為 `[]core.ProductVariant`）、`integrationOrderSummary`（已有）、`integrationOrderDetail`（新）、`integrationMediaFile`（取代手寫 `integrationMediaFileSchema`）、`integrationMe`、`integrationEvent`（沿用 `integrationEventResponse`）、`integrationThemeConfig`（沿用 `core.ThemeConfig`）、`integrationThemeSettings`、`integrationPlugin`、`integrationSetting`、`integrationDiscovery`（索引文件）。
- request body 同樣：`integrationContentInput`、`integrationProductInput`（取代手寫）、`integrationThemeSettingsInput`、`integrationMediaPatchInput`、`integrationSettingsInput`（從 `settings` 註冊表產生 properties）。

### R2 不一致即失敗

`integrations_regression_test.go` 的 `TestIntegrationRegressionDiscoveryAndOpenAPISchemasMatchHandlers`（`:1091-1211`）擴成：對 **每一條** v1 路由，用測試資料實際呼叫一次，把回應 JSON 的鍵集合（遞迴到第二層）與 OpenAPI 該操作的 schema 鍵集合比對，必須相等；request body 也用一個合法 body 打一次，body 的鍵必須都在 schema 的 `properties` 裡。例外清單只允許 `GET /openapi.json` 與 `/me/events` 的 `summary`。

## 程式（格子）

1. `internal/server/integration_schemas.go`（新檔）：`integrationSchemaOf` 與所有 struct。
2. `internal/server/integrations_discovery.go`：`integrationSuccessSchema`、`integrationRequestBody`、`integrationResourceSchema`、`integrationMediaFileSchema`、`integrationProductInputSchema`、`integrationContentSummarySchema`、`integrationOrderSummarySchema` 全部改成呼叫 `integrationSchemaOf(對應 struct)`；手寫 map 刪除。
3. `internal/server/handlers_posts.go` 的 `getPost`、`createPost`、`updatePost` 回應：v1 路徑（`c.Get("integration.identity") != nil`）改回 `integrationContentResource`（含 preload 的作者摘要）；後台路徑不變。做法：在 `integrationTypeAndVersion` 之後加一個回應改寫中介層，或在 handler 內依 context 分流，擇一但後台測試不得受影響。
4. `internal/server/shop_orders_admin.go`：`adminOrderGet` 抽出 `loadOrderDetail(ctx, id) (orderDetail, error)`，後台 handler 由它組原本的 map；v1 的 `GET /orders/:id` 新包裝回 `integrationOrderDetail`。
5. `internal/server/integrations_auth.go` 的 `integrationMe` 改回 `integrationMe` struct。
6. `internal/settings`：匯出註冊表的讀取（鍵、型別、是否密鑰）供 schema 產生；`PUT /settings` 的 body schema 由它產生，密鑰欄位標 `writeOnly`。
7. 文件：`STATUS.md`；工單 27 開頭加一行指向本工單。

## 驗收（測試契約）

- R2 的比對測試對所有 v1 路由通過；刻意在某個 struct 加一個欄位但不改 handler（或反過來）時測試失敗（用一個 `t.Run` 以反射暫時注入來證明測試有牙齒，或在工單審查時手動驗證一次並記錄）。
- `GET /openapi.json` 用 `kin-openapi`（或 repo 已有的 OpenAPI 驗證器；若沒有，`go.mod` 新增 `github.com/getkin/kin-openapi` 僅限測試）載入並 `Validate` 通過，沒有 `additionalProperties: true` 出現在例外清單以外。
- `GET /orders/:id`（v1）的鍵集合等於 `integrationOrderDetail` 欄位，`items[]` 沒有 `qty`、`price`；後台 `GET /api/admin/orders/:id` 仍有（守門）。`payment` 在沒有付款紀錄時所有欄位為 `null`、`provider` 為 `"none"`。
- `GET /posts/:id`（v1）的 `author` 恰好 `{id, username, display_name}` 且值正確；後台 `GET /api/admin/posts/:id` 的 `author` 形狀不變（守門）。
- `GET /me` 有 `role_cap` 與 `effective_role` 且 schema 列出。
- `PUT /settings` 的 body schema `properties` 鍵集合等於 `settings` 註冊表；送未註冊鍵回 400 `bad_request`（既有行為）。
- 工單 27 的契約測試全部維持通過。

## 不在本工單

- 匿名 API（工單 29）與 auth API 的 schema。
- 用 OpenAPI 產生 SDK。
- `/me/events` 的 `summary` 依 `action` 分型（`oneOf`）。

## 決策紀錄

- 從 struct 反射產 schema 而不是手寫：手寫已經漏了 `role_cap`、`effective_role` 兩欄，之後只會更多；反射讓「回應是什麼」和「文件說什麼」是同一份程式。
- 例外清單只有兩項：`openapi.json` 本身與事件 `summary`；其他任何 `additionalProperties: true` 都是文件在偷懶。
- 訂單詳情定 v1 自己的 struct：後台 map 有相容重複鍵與依情況變形的 `payment`，放進文件會變成三年的包袱；工單 31 的寫入端點也要回同一個形狀。
- `author` 改摘要並 preload：現況是沒 preload 的空 `User`，等於文件裡一個永遠是零值的物件，不如給真的三個欄位。
- `PUT /settings` 用註冊表產 schema：伺服器本來就只收已註冊的鍵，文件應該說實話。
