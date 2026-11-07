# 29 — 外掛可以讀取網站內容，並以擴充點改變輸出與通知

Epic：外觀與擴充
User Story：外掛開發者可以讀文章、商品與訂單，可以把 HTML 注入主題、後處理文章內容、替換留言元件、把通知送到自己的通路；所有寫入路徑都會發事件。
Blocked by：28（事件接線）。
Status：in_progress

## 痛點

- 外掛只收得到事件 payload，沒有任何讀取 API：想做「同步到外部搜尋」「定時整理草稿」都做不到。
- `ext` 只出貨 `search.engine` 與 `media.storage`。ENG.md 要求的「內容渲染後處理、主題 head／footer 注入、留言元件、通知器」只有名字沒有實作，外掛無法改變網站輸出或把通知送到 Slack／LINE。
- 工作單 28 留下的寫入路徑沒有事件：回收桶到期自動清除、WXR 匯入。

## 目標與範圍

分兩個 PR 交付，同一張工作單。

PR A（事件缺口與讀取 API）
1. `PurgeExpiredTrash` 每筆清除發 purge 事件（actor null）；WXR 匯入建立文章發 `OnPostSave`（action `import`）與到期的 `OnPostPublish`（trigger `save`）。自動儲存只寫版本列、不改文章，明確記為「不是文章寫入」。
2. OAuth 首次登入的 `OnUserCreate`（source `oauth`）補自動化測試。
3. 外掛讀取 API（host API，需權限）：
   - `content.read`：`JellyCMS.content.get(ref)`、`JellyCMS.content.list(query)`。
   - `shop.read`：`JellyCMS.shop.getProduct(ref)`、`listProducts(query)`、`getOrder(ref)`、`listOrders(query)`。

PR B（擴充點）
4. `theme.inject`：外掛提供 head／footer HTML，附加在既有程式碼注入之後；多個提供者依啟用順序串接。
5. `content.render`：外掛後處理文章／頁面的公開 HTML；多個提供者依序鏈接。
6. `comments.component`：外掛提供留言區 HTML，取代主題內建留言區；單一提供者。
7. `notify.channel`：外掛通知器，每個通知事件一筆投遞列，走既有重試；多個提供者。

### 明確不做

- 認證方式、編輯器區塊、後台列表欄位、儀表板小工具、管理頁分頁等其他擴充點。
- 外掛寫入 API（建立或修改文章、商品、訂單）。
- RSS／Atom 與 `/api/v1` 的內容不經 `content.render`；只有主題渲染的文章與頁面。
- 使用者資料讀取 API（`users.*`）。
- 通知器不影響每人偏好與站內通知；它是事件層級的額外通路。

## 設計

### 讀取 API

host API 方法與 JS 包裝（`runtime_state.go` 的 `jellyAPIScript`）：

| JS | host 方法 | 參數 | 回傳 |
|---|---|---|---|
| `content.get(ref)` | `content.get` | `ref` 是數字 id 或字串 slug | 文章物件（與 `OnPostSave` 的 `post` 相同欄位，含 `content`，超過 256 KiB 省略並 `content_omitted`）；找不到回 `null` |
| `content.list(q)` | `content.list` | `{type, status, visibility, category, tag, search, page, per_page}`，全部可省略 | `{items, total, page, per_page}`；`items` 是文章物件但不含 `content` |
| `shop.getProduct(ref)` | `shop.product.get` | id 或 slug | 商品物件（與事件相同）或 `null` |
| `shop.listProducts(q)` | `shop.product.list` | `{status, page, per_page}` | `{items, total, page, per_page}` |
| `shop.getOrder(ref)` | `shop.order.get` | id 或訂單編號 | 訂單物件（含 `items`）或 `null` |
| `shop.listOrders(q)` | `shop.order.list` | `{status, email, page, per_page}` | `{items, total, page, per_page}`，不含 `items` |

- 權限 `content.read` 與 `shop.read` 加入 `enforcedPermissions`；沒宣告回 `plugin permission denied`，核准雜湊過期回 `plugin permission approval is stale`（沿用既有 host API 的訊息）。
- 回收桶內的列不回傳；`content.get` 不限狀態（草稿也可讀，外掛是管理員核准的）。
- `per_page` 預設 20、最大 100；`page` 預設 1。排序：文章依 `published_at DESC NULLS LAST, id DESC`，商品與訂單依 `id DESC`。
- 每次呼叫沿用 host API 的 1 MiB 回應上限；清單超過上限時把 `per_page` 縮小是外掛的責任，host 回錯誤「result exceeds 1 MiB」。
- 讀取用 `data.DB` 加 2 秒逾時。

### 擴充點共通

- `ext` 新增出貨點 `theme.inject`、`content.render`、`comments.component`、`notify.channel`。`internal/plugin` 把出貨判斷改為 `ext.Shipped`，快照上限改為出貨點數量；`registeredExtensions` 依 `ext.Points()` 列出。
- 後台「擴充設定」的 `extensionView` 新增 `multiple`（`media.storage`、`theme.inject`、`content.render`、`notify.channel` 為 true），`system-admin.js` 依它決定多選。
- 提供者的選擇與可用性沿用 `extensionSelection`／`extensionPluginMeta`（啟用、核准雜湊、實際註冊）。
- 快取：伺服器保存一個「外掛世代」計數（`pluginExtensionGeneration`），外掛啟用、停用、重載、解除安裝、設定變更、擴充設定變更都遞增；注入、渲染與留言元件的快取鍵都含世代，世代變了就重算。
- 回呼失敗或逾時：注入略過該外掛、渲染回原 HTML、留言元件退回內建留言區、通知器依投遞重試；都記一筆外掛 error 日誌（走既有 `enqueuePluginLog`）。

### `theme.inject`

- 回呼輸入 `{}`，回傳 `{head, footer}`，兩者皆為字串，可缺。每個外掛的結果在同一世代只計算一次並快取。
- 輸出位置：`publicInjectionFor` 之後，`Head` 附加各外掛的 `head`、`Footer` 附加 `footer`，依啟用順序；商務頁（`/checkout`、`/account`）沿用 `inject_on_commerce` 的規則。
- HTML 原樣輸出（與既有程式碼注入相同信任層級：管理員核准的外掛）。單一外掛 head 與 footer 各最多 64 KiB，超過略過並記錄。

### `content.render`

- 回呼輸入 `{post: 摘要（id, type, slug, title, author_id）, html}`，回傳 `{html}`；多個提供者依啟用順序鏈接，前一個的輸出是下一個的輸入。
- 位置：`publicPostContent` 的結果（文章與頁面）；標題編號與目錄（`theme.HeadingIDs`）在之後執行。
- 快取鍵 `(post.id, post.updated_at, 世代)`，LRU 256 筆、總量 32 MiB；超過 1 MiB 的 HTML 不送外掛、直接用原文。

### `comments.component`

- 回呼輸入 `{post: 摘要, path, comments_open, comment_count}`，回傳 `{html}`。
- 單一提供者（啟用清單第一個可用者）。有結果時 `post.html` 以 `{{.CommentsHTML}}` 輸出它，不渲染內建 `comments` 區塊；沒有提供者或失敗時維持內建。文章尾的「留言 N」按鈕維持原邏輯。
- 快取鍵 `(post.id, 世代)`。內建留言 API 不受影響（外掛留言系統自己保存留言）。

### `notify.channel`

- `notify.Emit` 在處理完收件人後，若 `notify.PluginChannelWanted`（server 設定的函式變數；查 `extension_bindings` 有無啟用的 `notify.channel` 提供者，快取 5 秒）回 true，就在同一交易建立一筆 `NotificationDelivery`：`Channel: "plugin"`、`NotificationID: 0`、`Params` 為 `{"kind","category","params","recipients":{"roles","user_ids","emails"}}`。
- `sendSystemNotification` 的 channel `plugin`：對每個啟用且可用的提供者呼叫回呼，輸入即上述 Params 加 `at`；任一提供者失敗回錯誤，由既有流程重試（最多 5 次），成功的提供者在重試時會再收到一次，外掛要用 `kind` 與參數自行去重。
- 外掛拿到收件人 email（PII），因此 `notify.channel` 的提供者除了擴充點宣告外，manifest 還必須宣告 `events` 權限（沿用「收事件要核准」的規則）。

### 事件缺口

- `PurgeExpiredTrash`：每筆改用 `events.Transaction(ctx, db, …)` 並以 `purgeTrashedStaged` 的無 echo 版本（actor null）Stage purge 事件。
- WXR 匯入：文章建立的交易改用 `events.Transaction`；Stage `OnPostSave` action `import`，若 `postIsPublic` 再 Stage `OnPostPublish` trigger `save`；actor 為匯入工作的執行者（`actor_user_id`，可為 null）。`importer` 可以 import `events`，payload 的文章物件用與 `postEventPayload` 相同欄位（importer 不能 import server，在 importer 內組 map，欄位名稱一致，測試比對）。
- 自動儲存只寫 `revisions`，文件註明不是文章寫入。
- 匯入事件的 `content` 是連結改寫（`rewriteLinks`）之前的版本，改寫不另發事件；匯出檔裡已發布但沒有日期的項目以匯入時間為 `published_at`（否則永遠不會公開可讀，也不會有 `OnPostPublish`）。
- 讀取沿用 host 呼叫的 ctx 並以 2 秒為上限；`content.list` 只讀 payload 需要的欄位，不載入 `content`、`source`、`search_*`；`content.get` 編碼後超過 1 MiB 時改為省略 `content` 再回傳。

## 鉤子與權限文件

`docs/plugin-events.md` 新增「讀取 API」與權限表；`docs/plugin-extensions.md` 新增四個擴充點的契約；README 外掛章節連結。

## 驗收

| 流程 | 預期結果 |
|---|---|
| 外掛宣告 `content.read`，`content.get(id)`／`get(slug)`／`list({type:"post"})` | 回傳與資料庫一致；沒宣告權限回錯誤；回收桶文章回 `null` |
| 外掛宣告 `shop.read`，讀商品與訂單 | 訂單含 `items`；清單分頁正確；沒權限回錯誤 |
| 回收桶到期自動清除 | 每筆 `OnPostDelete`／`OnProductDelete`／`OnCommentDelete` purge，actor null |
| WXR 匯入兩篇（一篇 publish、一篇 draft） | 兩筆 `OnPostSave` action import；只有 publish 那篇有 `OnPostPublish` |
| OAuth 首次登入 | `OnUserCreate` source oauth、`OnUserLogin` method oauth |
| 兩個 `theme.inject` 外掛啟用 | 公開頁 head 與 footer 依序含兩段；商務頁依 `inject_on_commerce`；停用其中一個後世代遞增、輸出只剩一段 |
| `content.render` 外掛把 `<p>` 換成 `<p class="x">` | 文章頁輸出含 class；第二次請求不再呼叫外掛（`HookDispatches`／擴充呼叫計數不變）；外掛拋錯時輸出原文並有 error 日誌 |
| `comments.component` 外掛回傳 `<div id="ext-comments">` | 文章頁含該片段、不含內建 `comments-head`；停用後恢復內建 |
| `notify.channel` 外掛啟用後觸發 `comment.pending` | 投遞列 channel plugin，外掛收到 `kind`、`params`、`recipients`；外掛拋錯時投遞狀態 pending 且 attempts 1；成功時 sent |
| 擴充設定頁 | `GET /api/admin/extensions` 每個新點有 `multiple` 欄位；`PUT` 可存多個或單一提供者 |

## 決策紀錄

- 讀取 API 的物件沿用事件 payload 的欄位：外掛只需學一種形狀；實作共用 `postEventPayload` 等建構函式。
- 注入與渲染結果快取在記憶體並以世代失效，而不是每次請求呼叫外掛：每次呼叫是跨程序 IPC 加 1 秒逾時，直接掛在熱路徑會把頁面延遲綁在外掛上。
- 通知器用投遞列而不是直接在 `Emit` 內呼叫外掛：`Emit` 在交易內，外掛呼叫不能在交易內；投遞列讓失敗可重試、後台可見。
- `comments.component` 只允許單一提供者：兩個留言系統同時出現沒有意義。
- `content.render` 不套用在 feed 與 API：那些輸出有自己的契約與快取，後處理只影響主題頁面。

## 格子與檔案

PR A：
| 格子 | 檔案 |
|---|---|
| A1 事件缺口 | `internal/server/handlers_trash.go`（PurgeExpiredTrash）、`internal/importer/run.go`、`internal/server/plugin_events.go`（無 echo 的 purge helper） |
| A2 讀取 API | `internal/plugin/manifest.go`（權限）、`internal/plugin/runtime_state.go`（JS 包裝）、`internal/server/plugin_host.go`（方法分派）、`internal/server/plugin_host_read.go`（新：查詢與分頁） |
| A3 文件 | `docs/plugin-events.md`、README |

PR B：
| 格子 | 檔案 |
|---|---|
| B1 擴充點登錄與 runtime 一般化 | `internal/ext/ext.go`、`internal/plugin/extensions.go`、`internal/plugin/runtime_state.go` |
| B2 世代與快取、注入、渲染、留言元件 | `internal/server/plugin_extensions_site.go`（新）、`seo_routes.go`（注入）、`handlers_public.go`（渲染與留言）、`web/themes/default/post.html`、`plugin_detail_api.go`（世代遞增、`multiple`）、`store_api.go`、`integration_shared_handlers.go`、`web/admin/assets/system-admin.js` |
| B3 通知器 | `internal/notify/event.go`、`internal/notify/worker.go`（如需）、`internal/server/system_runtime.go`（`sendSystemNotification`）、`internal/server/plugin_extensions_site.go` |
| B4 文件 | `docs/plugin-extensions.md`、README |

測試檔：PR A `internal/server/plugin_read_api_integration_test.go`、`internal/server/plugin_events_gaps_test.go`、`internal/importer/events_test.go`（或 server 內的匯入事件測試）；PR B `internal/server/plugin_extensions_site_test.go`、`internal/notify/plugin_channel_test.go`、`internal/plugin/extensions_points_test.go`。實作者不得修改測試檔。
