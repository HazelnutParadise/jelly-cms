# 27 — 外掛可以收到網站的真實事件

Epic：外觀與擴充
User Story：外掛開發者註冊鉤子後，文章、商品、訂單、付款、帳號、留言、附件、主題與設定的真實操作會把事件交給外掛。
Blocked by：02（runtime 與重載已完成）。
Status：implemented（驗收結果見文末）

## 痛點

`internal/plugin/types.go` 宣告了 17 種鉤子，README 也逐一列出，但整個伺服器沒有任何地方呼叫 `Runtime.CallHook`；只有 OnBoot、OnShutdown 與後台動作 `action:<id>` 會真的執行。外掛寫 `registerHook("OnPostSave")` 永遠等不到事件。工作單 02 的「內容、商品、訂單、登入、付款與主題事件由對應真實操作觸發」仍未完成。另外後台外掛「日誌」分頁只讀 `system_logs.plugin_id`，而外掛的 `JellyCMS.log` 只寫到伺服器 stdout，分頁永遠是空的。

## 目標與範圍

1. 建立一個程序內事件匯流排 `internal/events`，核心流程在交易提交後才發布事件，交易失敗不發布。
2. 外掛 runtime 新增非阻塞的事件佇列：HTTP 請求不等待外掛執行，事件依序交給每個有註冊該鉤子的外掛。
3. 接線下列鉤子目錄中的每一個事件，由真實操作觸發，並以整合測試證明。
4. 排程發布：`published_at` 在未來的文章到時間時，由排程工作發出 `OnPostPublish`。
5. 外掛的 `JellyCMS.log` 與鉤子失敗寫入 `system_logs`（帶 `plugin_id`），後台日誌分頁可見。
6. README 與 docs 的鉤子目錄只列真的會觸發的鉤子，附 payload 欄位。

### 明確不做

- 不做 `OnRequest`／`OnResponse`：每個 HTTP 請求都跨程序呼叫外掛會把請求延遲與外掛逾時綁在一起，移除這兩個常數與文件。
- 不做能修改結果的「過濾器」（例如改寫渲染後 HTML、head 注入、留言元件、通知器）；那是擴充點（`registerExtension`）的範圍，另開工作單。
- 不做持久化事件佇列或重送；事件是程序內、盡力而為，程序結束前未送出的事件會丟棄並記錄數量。
- 不做外掛自訂路由、後台頁面、排程工作。
- 不改資料表。排程發布的水位存在 `options`。

## 設計

### 事件匯流排 `internal/events`

```go
type Event struct {
    Name    string         // 鉤子名稱，例如 "OnPostSave"
    At      time.Time      // 發布時間（UTC）
    Payload map[string]any // 可轉成 JSON 的資料
}
type Subscriber func(Event)
func Subscribe(fn Subscriber) (cancel func())
func Publish(e Event)                       // 立即依訂閱順序呼叫；訂閱者必須不阻塞
func WithOutbox(ctx) (context.Context, *Outbox) // ctx 已有 outbox 時回傳同一個
func Stage(ctx context.Context, e Event)   // ctx 有 outbox 就暫存，否則丟棄並記錄
func (o *Outbox) Flush()                   // 依序發布，只能有效一次
func (o *Outbox) Discard()
func Transaction(ctx context.Context, db *gorm.DB, fn func(tx *gorm.DB) error) error
```

`Transaction` 建立 outbox 放進 ctx，跑 `db.WithContext(ctx).Transaction(fn)`，fn 內用 `events.Stage(tx.Statement.Context, …)`；回傳 nil 才 Flush，否則 Discard。巢狀呼叫共用最外層 outbox，只保證最外層交易的提交語義（內層 savepoint 回滾但外層提交時，內層事件仍會發布）。`Publish` 對 nil payload 補成空 map，`At` 為零值時補成現在。訂閱者 panic 要 recover 並記錄，不影響其他訂閱者。

**Stage 在 ctx 沒有 outbox 時不發布**：丟棄並以 `log.Printf` 記錄「events: %s staged outside a transaction outbox」。GORM 的 `tx.Statement.Context` 永遠不是 nil，若允許即時發布，任何沒改用 `events.Transaction` 的呼叫者都會在提交前、甚至回滾後發出事件。沒有交易的單一寫入由呼叫者明確 `Publish`。

順序保證只對序列操作成立：並行請求各自 Flush，外掛收到的順序是 Flush 順序，不是提交順序。

### 外掛 runtime（`internal/plugin`）

- worker 的每個回應 frame 新增 `Hooks []string`（`json:"hooks"`，不加 omitempty，永遠送出；已註冊的鉤子名稱，去重）。最多 64 個、每個最長 64 字元，超限視為 load／boot 失敗（與擴充點快照相同處理）。父程序保存每個外掛的鉤子快照，欄位缺失（nil）時保留前一個快照、`[]` 表示沒有；`CallHook` 與事件佇列只把事件交給有該鉤子的外掛，其他外掛不產生跨程序呼叫。在鉤子回呼內動態 `registerHook` 的名稱，要等該次回應後才進快照。新增 `Runtime.HasHook(pluginID string, hook Hook) bool`。
- `Runtime.Emit(hook Hook, data map[string]any)`：序列化一次後，只對「鉤子快照含該鉤子、且事件過濾器允許」的外掛各放進該外掛自己的佇列（容量 256），每個外掛一條派送 goroutine 依序送 worker；一個卡住的外掛不會拖慢其他外掛。整個 runtime 另有 32 MiB 的排隊位元組上限。佇列滿或超過位元組上限時丟棄這一筆並以 runtime log 記錄「event <hook> dropped for <plugin>: queue full」，不阻塞呼叫端。`Runtime.Flush(ctx) error`：等所有外掛佇列清空且沒有進行中的派送（需要 in-flight 計數），ctx 到期回錯。`Close()` 先在 2 秒內 Flush，逾時就記錄丟棄數並繼續關閉；關閉後的 Emit 以 `closed` 旗標丟棄（不 close channel）。
- `Runtime.SetEventFilter(func(meta PluginMetadata, hook Hook) bool)`：由網站設定；nil 表示全部允許。網站用它實作 `events` 權限（下節）。
- 連續失敗退避：同一外掛的事件鉤子連續 5 次失敗（錯誤或逾時）後，該外掛的事件派送暫停，記一筆 warn「events suspended after 5 consecutive failures; reload the plugin to resume」，之後的事件直接丟棄不再記錄；重載或重新載入產生新的狀態即恢復。OnBoot／OnShutdown／action 不受影響。
- `Runtime.HookDispatches() uint64`：送到 worker 的鉤子請求數，測試用來證明沒註冊的外掛不被呼叫。
- `registerHook(name, fn)`：名稱不在鉤子目錄、也不是 manifest `actions` 宣告的 `action:<id>`（比對 ID，不是前綴）時，仍接受註冊，但以 warn 記錄「hook "X" is not dispatched by this site」，同一名稱只警告一次；`CallHook`／`Invoke` 對任意名稱保持可用（既有測試使用自訂名稱）。
- `plugin.SetLogSink(func(pluginID, level, message string))`：外掛 `JellyCMS.log.*` 與鉤子／擴充點失敗除了 stdout 外，也在日誌 goroutine 內交給 sink；sink 必須立刻返回（網站端自己用有界 channel 與獨立 goroutine 寫資料庫），panic 被 recover 後丟棄該行。
- `types.go`：移除 `HookOnRequest`、`HookOnResponse`；新增下方目錄的常數；`KnownHooks() []Hook` 回傳目錄。

### `events` 權限

訂單、顧客、留言事件帶有 email 與留言內容，外掛不能只靠註冊鉤子就取得。manifest 新增強制權限 `events`（加入 `enforcedPermissions`），後台權限頁會以「enforced」顯示。網站的事件過濾器只放行「已啟用、`permissions_granted_hash` 與 manifest 相符、且 manifest 宣告 `events`」的外掛；判斷結果按外掛快取 5 秒。沒有這個權限的外掛仍收到 OnBoot、OnShutdown 與 `action:<id>`，其他事件一律不派送。

### 伺服器橋接（`internal/server/plugin_events.go`）

- `ConfigurePluginHost` 時 `events.Subscribe` 一個訂閱者，把事件轉成 `runtime.Emit(plugin.Hook(e.Name), payload)`，payload 固定帶 `event`（名稱）與 `at`（RFC3339）。訂閱者先補齊（enrich）薄事件：payload 有 `order_id` 而沒有 `order` 時，從資料庫讀訂單摘要放進 `order`；有 `actor_user_id` 而沒有 `actor` 時讀使用者放進 `actor`（找不到就 `null`）。補讀發生在提交後、送出回應之前，每次讀取（訂單、actor、外掛權限）以 2 秒為上限，逾時視為讀不到（權限逾時即拒絕），所以 core 與 payment 套件只需要發薄事件。
- `plugin.SetLogSink` 接到 `forwardPluginLog`：放進容量 1024 的 channel，由一條 goroutine 以 `syslog.Emit` 寫入（`Source: "plugin"`、`PluginID` 為外掛 ID、debug／info → info、warn → warn、error → error）；channel 滿就丟棄。不得在日誌 goroutine 內同步寫資料庫。
- `pluginEventsWanted(hook)`：有任何已載入外掛註冊該鉤子才回 true；OnPostView／OnProductView 先用它判斷，沒人要就不組 payload 也不 Publish。
- OnPostView／OnProductView 在 `c.Response().After` 內發布，渲染失敗（非 2xx）不發。
- payload 建構函式集中在同一檔案：`postEventPayload`、`productEventPayload`、`orderEventPayload`、`userEventPayload`、`commentEventPayload`、`mediaEventPayload`。絕不含密碼雜湊、權杖、金流原始 payload、留言 IP 與 UA。文章 `content` 超過 256 KiB 時省略並設 `content_omitted: true`。

### 排程發布

- `system_runtime.go` 新增排程 `{Name: "posts.publish_due", Spec: "* * * * *", Kind: "posts.publish_due"}` 與處理器。
- 處理器在一個交易內：以 `lockMaintenanceOption` 相同方式鎖 `options.plugin_events_publish_watermark`；第一次沒有水位時只寫入現在時間、不發事件；水位不小於 now 時直接返回、不寫入（水位只前進）；否則查 `posts` 中 `status = 'published' AND (visibility = 'public' OR visibility = '' OR visibility IS NULL) AND deleted_at IS NULL AND type IN ('post','page') AND published_at > 水位 AND published_at <= now AND published_at > updated_at` 的列，依 `published_at, id` 排序，最多 500 筆，每列 Stage `OnPostPublish`（`trigger: "schedule"`）。水位寫成 now；若剛好取滿 500 筆，水位寫成最後一筆的 `published_at`，下次排程從那裡接續。提交後 Flush。
- `published_at > updated_at` 代表最後一次儲存時發布時間仍在未來，當下儲存已經即時發布的文章不會重複觸發。
- 漏發窗口：排程時間已到但排程工作還沒跑，使用者又編輯了一次，`updated_at` 會超過 `published_at`，排程就不再認得它。儲存處理器要補這個窗口：`postPublishAnnouncement(db, existing, saved, now)` 判斷這次寫入是否讓文章首次公開可讀：`existing` 不是公開可讀（狀態、可見性、`published_at` 未到其一），或是「到期但尚未宣告」（`published_at <= now`、`published_at > existing.updated_at`、`published_at > 水位`），而 `saved` 公開可讀時，發 `OnPostPublish`（trigger save）。建立時 existing 為零值；回收桶還原與版本還原也用同一判斷，還原前讀 existing（回收桶中的列或還原前的版本），還原後讀 saved，因為還原會改寫 `updated_at`。水位空字串視為零時間。沒有外掛監聽 `OnPostSave` 與 `OnPostPublish` 時，文章寫入整段跳過這些讀取（`pluginEventsPostWanted`）；代價是寫入當下沒人收的「尚未宣告」文章不會事後補發。
- 第一次執行只寫水位不發事件，所以升級到本版本之前就到期的排程文章不會補發；之後伺服器停機期間到期的文章會在重啟後第一次執行時發出。
- 文章類型白名單 `post`、`page` 用同一個常數給儲存處理器與排程共用。

### 處理器內的規則

- 事件一律在交易提交後發布：有交易的處理器改用 `events.Transaction` 或在 `Transaction` 回傳 nil 後 `events.Publish`；沒有交易的單一寫入（留言狀態、附件回收）在寫入成功後 `events.Publish`。
- `core.OrderService.Transition` 成功後 `events.Stage(tx.Statement.Context, …)` 一筆薄事件 `OnOrderUpdate`：`{order_id, from, to, note, actor_user_id}`；`to == paid` 時再 Stage 薄事件 `OnOrderPaid`：`{order_id, actor_user_id}`。訂單摘要與 actor 由橋接在提交後補讀，退款路徑裡 Transition 之後才更新的 `refunded_total` 因此是最終值。所有既有呼叫者（結帳零元、後台改狀態、標記已付、退款、顧客取消、付款回呼、逾期掃描）都要改用 `events.Transaction`，否則 Stage 會丟棄並記錄。
- `payment.Finalize` 以同樣方式 Stage 薄事件 `OnPaymentSuccess`／`OnPaymentFailed`：`{order_id, payment: {provider, method, merchant_trade_no, amount, currency}, reason}`；兩個回呼處理器改用 `events.Transaction`。
- `ExpireUnpaidOrders` 每筆交易改用 `events.Transaction`，逾期也會發 `OnOrderUpdate`，每筆各自 Flush。
- `/api/v1` 與 `/api/admin` 共用同一組處理器，接線一次兩邊都有。
- `OnPostView`／`OnProductView` 只在 GET（不含 HEAD）、找得到內容且回應成功時發布（`c.Response().After`），事件不帶 IP。

## 鉤子目錄與 payload

所有 payload 都含 `event`（鉤子名稱）與 `at`（RFC3339，UTC）。`actor` 為操作者 `{id, username, role}`，系統或匿名操作為 `null`。

| 鉤子 | 觸發時機 | 其他欄位 |
|---|---|---|
| OnBoot | 外掛載入完成 | `plugin` |
| OnShutdown | 外掛卸載前 | `plugin`, `reason` |
| OnPostSave | 文章或頁面新增、更新、從回收桶還原、版本還原 | `action`: create／update／restore／revision, `post`, `actor` |
| OnPostPublish | 文章或頁面首次變成公開可讀（狀態 published 且 published_at 已到）；排程到時由排程工作發出 | `trigger`: save／schedule, `post`, `actor` |
| OnPostDelete | 移到回收桶或永久刪除 | `action`: trash／purge, `post`（purge 只有 `id`, `type`, `slug`, `title`）, `actor` |
| OnPostView | 前台 GET 文章或頁面 | `post`（`id, type, slug, title, author_id`）, `path`, `signed_in` |
| OnProductSave | 商品新增、更新、複製、還原 | `action`: create／update／duplicate／restore, `product`, `actor` |
| OnProductDelete | 商品移到回收桶或永久刪除 | `action`: trash／purge, `product`, `actor` |
| OnProductView | 前台 GET 商品頁 | `product`（`id, slug, title, price, currency`）, `path`, `signed_in` |
| OnOrderCreate | 結帳建立訂單（提交後） | `order`（含 `items`）, `actor` |
| OnOrderUpdate | 任何訂單狀態轉換 | `order`, `from`, `to`, `note`, `actor` |
| OnOrderPaid | 訂單轉為 paid（付款回呼、標記已付、零元結帳） | `order`, `actor` |
| OnOrderRefund | 後台退款成功 | `order`, `amount`, `reason`, `restock`, `actor` |
| OnPaymentSuccess | 金流回呼驗證成功且訂單轉為 paid | `order`, `payment`（`provider, method, merchant_trade_no, amount, currency`） |
| OnPaymentFailed | 金流回呼為失敗 | `order`, `payment`, `reason` |
| OnUserCreate | 建立使用者列：後台邀請、OAuth 首次登入、結帳建立顧客 | `user`, `source`: invite／oauth／checkout, `actor` |
| OnUserUpdate | 後台修改角色、狀態或顯示名稱；邀請被接受（狀態變 active） | `user`, `changes`（欄位名稱陣列）, `actor` |
| OnUserDelete | 後台刪除使用者 | `user`（`id, username, email, role`）, `actor` |
| OnUserLogin | 登入成功（密碼或 OAuth） | `user`, `method`: password／oauth, `ip_masked` |
| OnUserLoginFailed | 密碼登入失敗（帳號不存在或密碼錯） | `identifier`（輸入的帳號或 email）, `ip_masked`, `reason` |
| OnUserLogout | 登出且撤銷登入 | `user`（可能只有 `id`） |
| OnCommentCreate | 前台留言或後台回覆建立 | `comment`, `post`（`id, slug, title`）, `actor` |
| OnCommentUpdate | 留言狀態改變（approve／unapprove／spam／trash／restore，含批次與回覆時自動核准） | `comment`, `from`, `to`, `actor` |
| OnCommentDelete | 留言永久刪除（含批次） | `comment`（`id, post_id`）, `actor` |
| OnMediaUpload | 附件存入（上傳、從網址抓取、舊版上傳端點） | `file`, `source`, `actor` |
| OnMediaDelete | 附件移到回收桶 | `file`（`id, path, filename, mime`）, `actor` |
| OnThemeActivate | 主題切換成功 | `theme`, `previous`, `actor` |
| OnSettingsChange | `settings.Set` 成功 | `keys`（鍵名陣列，不含值）, `actor` |

物件欄位：

- `post`：`id, type, slug, title, status, visibility, excerpt, content, format, author_id, published_at, created_at, updated_at, categories[{id,name,slug}], tags[{id,name,slug}], pinned`。
- `product`：`id, slug, title, status, price, compare_at_price, sku, stock, kind, images, created_at, updated_at`。
- `order`：`id, number, status, email, user_id, currency, subtotal, discount_total, shipping_total, tax_total, total_amount, refunded_total, payment_provider, payment_method, coupon_code, created_at, paid_at`；`items[{product_id, variant_id, title, variant_name, sku, quantity, unit_price, line_total}]` 只在 OnOrderCreate 附上。
- `user`：`id, username, display_name, email, role, status, provider, created_at`。
- `comment`：`id, post_id, parent_id, author_id, name, status, body, created_at`。
- `file`：`id, path, filename, mime, size, width, height, alt, policy_id, group_id, created_at`。

## 驗收

| 流程 | 預期結果 |
|---|---|
| 外掛註冊全部鉤子並記錄 `event:<名稱>:<摘要>`，透過 HTTP 執行每個觸發操作 | 每個鉤子各至少一筆日誌，摘要欄位與資料庫一致；`Runtime.Flush` 後才斷言 |
| 沒有註冊某鉤子的外掛 | `HasHook` 為 false，該事件不產生對它的呼叫（以 worker 請求計數或日誌證明） |
| 交易失敗（Stage 後回錯） | 沒有事件發布 |
| 草稿儲存 | 只有 OnPostSave，沒有 OnPostPublish |
| 草稿改為已發布 | OnPostSave 與 OnPostPublish（trigger save）；再儲存一次只剩 OnPostSave |
| 發布時間在未來的文章 | 儲存時沒有 OnPostPublish；時間到後執行 `posts.publish_due` 一次得到 OnPostPublish（trigger schedule），再執行一次不重複 |
| 兩次連續儲存 | 外掛收到的順序與操作順序相同 |
| 外掛鉤子拋錯或逾時 | 其他外掛與下一個事件照常；`system_logs` 有該外掛的 error 列；連續 5 次失敗後該外掛暫停並記一筆 warn，重載後恢復 |
| 沒有 `events` 權限或權限核准雜湊過期 | 收不到任何網站事件，仍收到 OnBoot／OnShutdown／action |
| 排程到期後、排程工作執行前編輯文章 | 儲存時發 OnPostPublish（trigger save），排程工作之後不重發 |
| 佇列滿 | 呼叫端不阻塞；runtime log 記錄丟棄 |
| 後台日誌分頁 | 外掛 `JellyCMS.log.info` 的內容出現在 `GET /api/admin/plugins/:id/logs` |
| 重啟 | 事件只來自新程序；沒有重送 |

## 決策紀錄

- 事件在交易提交後發布，用 ctx 內的 outbox 而非 GORM callback：GORM 沒有提交後回呼，outbox 能跨 core／server／payment 套件共用同一條規則。
- 佇列放在 `plugin.Runtime` 而不是系統工作佇列：外掛 worker 是本程序的子程序，持久化佇列無法跨程序重送；盡力而為並記錄丟棄即可。
- 移除 OnRequest／OnResponse 而不是保留不觸發：保留會重演「宣告有、觸發沒有」的問題。
- `registerHook` 未知名稱只警告不拒絕：既有測試與未來自訂鉤子仍可用 `Invoke` 直接呼叫。
- 排程發布的水位存在 `options`：不改資料表；`published_at > updated_at` 判斷免去新增欄位。
- 文章 `content` 隨事件附上但有 256 KiB 上限：外掛沒有讀取 API，沒有內容就做不了同步類外掛；超限省略並標記，事件不因大小丟失。runtime 另以 32 MiB 位元組上限保護記憶體。
- 事件要 `events` 權限（獨立諮詢意見）：沒有閘門等於「安裝外掛就拿到全部顧客 email」，與既有權限核准頁的承諾不一致。
- 核心只發薄事件、橋接補讀（獨立諮詢意見）：core 不該知道 username／role，也避免退款路徑讀到更新前的 `refunded_total`。
- Stage 沒有 outbox 時丟棄而不是即時發布（獨立諮詢意見）：即時發布會在提交前、回滾後發出事件，漏改的呼叫者會靜默退化。
- 每個外掛獨立佇列（獨立諮詢意見）：單一派送 goroutine 會讓一個卡住的外掛拖住所有外掛，1 秒逾時乘以外掛數。

## 格子與檔案

| 格子 | 檔案 | 說明 |
|---|---|---|
| A 事件匯流排 | `internal/events/events.go` | Subscribe／Publish／Outbox／Stage／Transaction |
| B runtime 佇列與鉤子快照 | `internal/plugin/{types.go,manager.go,events.go,worker.go,runtime_state.go,worker_protocol.go,worker_logs.go}` | Hooks frame、HasHook、Emit／Flush、SetLogSink、registerHook 警告、KnownHooks |
| C 橋接與 payload | `internal/server/plugin_events.go`, `plugin_host.go` | 訂閱、log sink、payload 建構 |
| D 內容接線 | `handlers_posts.go`, `handlers_revisions.go`, `handlers_trash.go`, `handlers_public.go`, `system_runtime.go`（posts.publish_due） | 文章／頁面事件與排程發布 |
| E 商店接線 | `shop_products_admin.go`, `handlers_products.go`, `shop_catalog.go`, `shop_checkout.go`, `shop_orders_admin.go`, `internal/core/order_state.go`, `internal/core/order_expiry.go`, `internal/payment/finalize.go`, `shop_payment.go`/`handlers_payment.go`（回呼處理器） | 商品、訂單、付款 |
| F 帳號接線 | `internal/auth/service.go`, `internal/auth/oauth_identity.go`, `users_service.go`, `users_handlers.go`, `internal/core/customer.go` | 使用者建立、更新、刪除、登入、登出、登入失敗 |
| G 其他接線 | `handlers_comments.go`, `media_api.go`, `integration_shared_handlers.go`（主題）, `internal/settings/settings.go` | 留言、附件、主題、設定 |
| H 文件 | `README.md`, `docs/plugin-extensions.md`（連結）, `docs/plugin-events.md`（新） | 鉤子目錄與 payload |

測試檔：`internal/events/events_test.go`、`internal/plugin/events_test.go`、`internal/server/plugin_events_harness_test.go`（共用）、`internal/server/plugin_events_content_test.go`、`internal/server/plugin_events_shop_test.go`、`internal/server/plugin_events_users_test.go`、`internal/server/plugin_events_misc_test.go`。實作者不得修改測試檔。

## 驗證紀錄

- 測試先於實作：七個測試檔（`internal/events/events_test.go`、`internal/plugin/events_test.go`、`internal/server/plugin_events_{harness,content,shop,users,misc}_test.go`）在空格子上全部失敗（server 21 個整合案例、plugin 13 個、events 6 個），實作後全部通過。
- 實作過程中修正的規格偏差：零元結帳的 `OnOrderCreate` 帶建立當下的 `pending` 狀態，付款由 `OnOrderUpdate`／`OnOrderPaid` 表示（原測試要求帶提交後的 paid 快照，改回真實狀態）；`TestPluginEventsContentWithoutEventsPermission` 的 `reset` 原本會清掉第二個外掛的載入日誌，調整順序。
- 已知未接線的寫入路徑：`PurgeExpiredTrash`（保留期限到期自動清除）不發 `OnPostDelete purge`；自動儲存、WXR 匯入、批次操作不發 `OnPostSave`。記在 AGENTS.md Follow-ups。
- 完整套件、gofmt、go vet、獨立對抗式審查與 CI 結果見 STATUS.md 的本工作單條目。
