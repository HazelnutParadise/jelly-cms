# 外掛事件（鉤子）

外掛用 `JellyCMS.registerHook(名稱, 回呼)` 訂閱網站事件。每個事件在對應的資料庫交易提交後才送出，交易失敗不會送出；事件在記憶體排隊、依序交給外掛，HTTP 請求不會等待外掛執行。事件是盡力而為：程序重啟不會重送，外掛連續 5 次失敗會暫停收事件直到重新載入，單一外掛的佇列（256 筆）滿了會丟棄並記錄。

每個回呼最多執行 1 秒；回呼拋錯或逾時會記錄到該外掛的日誌，不影響其他外掛。

## 需要 `events` 權限

訂單、帳號與留言事件帶有 email 與留言內容。外掛必須在 `plugin.json` 宣告 `events` 權限，管理員核准後才會收到網站事件；沒有這個權限的外掛只收到 `OnBoot`、`OnShutdown` 與後台動作 `action:<id>`。通知器擴充點 `notify.channel` 會收到收件人 email，同樣需要這個權限（見[JavaScript 擴充點](plugin-extensions.md)）。

```json
{
  "id": "order-notifier",
  "name": "訂單通知",
  "version": "1.0.0",
  "entrypoint": "index.js",
  "permissions": [{ "name": "events" }, { "name": "http.fetch", "hosts": ["hooks.example.com"] }]
}
```

```javascript
JellyCMS.registerHook("OnOrderPaid", function (data) {
  JellyCMS.http.fetch("https://hooks.example.com/paid", {
    method: "POST",
    body: JSON.stringify({ number: data.order.number, total: data.order.total_amount }),
  });
});
```

註冊不在目錄裡的名稱仍會成功，但日誌會出現「hook "X" is not dispatched by this site」警告。

## 共同欄位

| 欄位 | 內容 |
|---|---|
| `event` | 鉤子名稱 |
| `at` | 發布時間，RFC3339，UTC |
| `actor` | 操作者 `{id, username, role}`；系統或匿名操作為 `null` |

## 鉤子目錄

| 鉤子 | 觸發時機 | 其他欄位 |
|---|---|---|
| `OnBoot` | 外掛載入完成 | `plugin` |
| `OnShutdown` | 外掛卸載前 | `plugin`, `reason` |
| `OnPostSave` | 文章或頁面新增、更新、從回收桶還原、版本還原 | `action`（create／update／restore／revision）, `post` |
| `OnPostPublish` | 文章或頁面由不可公開讀取變成可公開讀取（狀態 published、`visibility` public、`published_at` 已到）；退回草稿再發布會再送一次；排程發布到時由每分鐘的排程工作送出 | `trigger`（save／schedule）, `post` |
| `OnPostDelete` | 移到回收桶或永久刪除；永久刪除文章會連帶刪除其留言，但不另發 `OnCommentDelete` | `action`（trash／purge）, `post`（purge 只有 `id, type, slug, title`） |
| `OnPostView` | 前台 GET 文章或頁面成功 | `post`（`id, type, slug, title, author_id`）, `path`, `signed_in` |
| `OnProductSave` | 商品新增、更新、複製、還原 | `action`（create／update／duplicate／restore）, `product` |
| `OnProductDelete` | 商品移到回收桶或永久刪除 | `action`（trash／purge）, `product`（purge 只有 `id, slug, title, price, currency`） |
| `OnProductView` | 前台 GET 商品頁成功 | `product`（`id, slug, title, price, currency`）, `path`, `signed_in` |
| `OnOrderCreate` | 結帳建立訂單 | `order`（含 `items`） |
| `OnOrderUpdate` | 任何訂單狀態轉換（含後台操作、付款回呼、顧客取消、逾期） | `order`, `from`, `to`, `note` |
| `OnOrderPaid` | 訂單轉為 paid（付款回呼、後台標記已付、零元結帳） | `order` |
| `OnOrderRefund` | 後台退款成功 | `order`, `amount`, `reason`, `restock` |
| `OnPaymentSuccess` | 金流回呼驗證成功且訂單轉為 paid | `order`, `payment` |
| `OnPaymentFailed` | 金流回呼為失敗 | `order`, `payment`, `reason` |
| `OnUserCreate` | 建立使用者：後台邀請、OAuth 首次登入、結帳建立顧客 | `user`, `source`（invite／oauth／checkout） |
| `OnUserUpdate` | 後台修改角色、狀態或顯示名稱；邀請被接受 | `user`, `changes`（欄位名稱陣列） |
| `OnUserDelete` | 後台刪除使用者 | `user`（`id, username, email, role`） |
| `OnUserLogin` | 登入成功 | `user`, `method`（password／oauth／recovery）, `ip_masked` |
| `OnUserLoginFailed` | 密碼登入失敗 | `identifier`, `ip_masked`, `reason` |
| `OnUserLogout` | 登出 | `user`（可能只有 `id`） |
| `OnCommentCreate` | 前台留言或後台回覆建立 | `comment`, `post`（`id, slug, title`） |
| `OnCommentUpdate` | 留言狀態改變（approve／unapprove／spam／trash／restore，含批次） | `comment`, `from`, `to` |
| `OnCommentDelete` | 留言永久刪除 | `comment`（`id, post_id`） |
| `OnMediaUpload` | 附件存入（上傳、從網址抓取） | `file`, `source` |
| `OnMediaDelete` | 附件移到回收桶 | `file`（`id, path, filename, mime`） |
| `OnThemeActivate` | 主題切換成功 | `theme`, `previous` |
| `OnSettingsChange` | 系統設定儲存 | `keys`（鍵名陣列，不含值） |

## 物件欄位

- `post`：`id, type, slug, title, status, visibility, excerpt, content, format, author_id, published_at, created_at, updated_at, categories[{id,name,slug}], tags[{id,name,slug}], pinned`。`content` 超過 256 KiB 時省略並附 `content_omitted: true`。
- `product`：`id, slug, title, status, price, compare_at_price, sku, stock, kind, images, created_at, updated_at`。金額是最小貨幣單位的整數。
- `order`：`id, number, status, email, user_id, currency, subtotal, discount_total, shipping_total, tax_total, total_amount, refunded_total, payment_provider, payment_method, coupon_code, created_at, paid_at`；`items[{product_id, variant_id, title, variant_name, sku, quantity, unit_price, line_total}]` 只在 `OnOrderCreate` 附上。不含收件資料、發票與任何權杖。
- `payment`：`provider, method, merchant_trade_no, amount, currency`。不含金流原始回呼內容。
- `user`：`id, username, display_name, email, role, status, provider, created_at`。
- `comment`：`id, post_id, parent_id, author_id, name, status, body, created_at`。不含 IP、User-Agent 與 email。
- `file`：`id, path, filename, mime, size, width, height, alt, policy_id, group_id, created_at`。

## 讀取 API

事件 payload 之外，外掛可以主動讀取內容。方法都是同步呼叫，回傳 JSON 值；host 回錯誤時會拋出例外，請用 `try/catch`。每次呼叫的結果上限 1 MiB，清單太大時縮小 `per_page`。

| 權限 | 方法 | 參數 | 回傳 |
|---|---|---|---|
| `content.read` | `JellyCMS.content.get(ref)` | 數字 id 或字串 slug | 文章物件（與 `OnPostSave` 的 `post` 相同欄位，含 `content`）；找不到或在回收桶回 `null` |
| `content.read` | `JellyCMS.content.list(query)` | `{type, status, visibility, category, tag, search, page, per_page}`，全部可省略 | `{items, total, page, per_page}`；`items` 不含 `content`（`content_omitted: true`） |
| `shop.read` | `JellyCMS.shop.getProduct(ref)` | id 或 slug | 商品物件或 `null` |
| `shop.read` | `JellyCMS.shop.listProducts(query)` | `{status, page, per_page}` | `{items, total, page, per_page}` |
| `shop.read` | `JellyCMS.shop.getOrder(ref)` | id 或訂單編號 | 訂單物件（含 `items`）或 `null` |
| `shop.read` | `JellyCMS.shop.listOrders(query)` | `{status, email, page, per_page}` | `{items, total, page, per_page}`，不含 `items` |

`content.get` 的 `content` 超過 256 KiB 時省略並附 `content_omitted: true`。`per_page` 預設 20、最大 100；`page` 預設 1。文章依 `published_at` 新到舊（沒有發布時間的草稿排在後面、依 id 新到舊），商品與訂單依 id 新到舊。`search` 比對標題，`category`／`tag` 用 slug。沒有宣告對應權限時每次呼叫都回 `plugin permission denied`；宣告了但管理員核准的權限清單與目前 manifest 不符時回 `plugin permission approval is stale`，要重新核准。

```javascript
JellyCMS.registerHook("OnPostPublish", function (data) {
  var related = JellyCMS.content.list({ type: "post", category: data.post.categories[0] && data.post.categories[0].slug, per_page: 5 });
  JellyCMS.log.info("同分類文章 " + related.total + " 篇");
});
```

## 排程發布

`published_at` 在未來的文章儲存時不會送 `OnPostPublish`；每分鐘的 `posts.publish_due` 工作在時間到後送出 `trigger: "schedule"`。第一次執行只記錄水位、不補送過去的文章；之後伺服器停機期間到期的文章會在重啟後的第一次執行送出。排程時間已到、工作還沒執行就被編輯的文章，由那次儲存送出 `trigger: "save"`，工作不會重發。

## 匯入

WordPress 匯入建立的文章發 `OnPostSave`（`action: "import"`），已發布且可公開讀取的再發 `OnPostPublish`（`trigger: "save"`）。事件裡的 `content` 是改寫站內連結之前的版本；匯入完成後的連結改寫不另發事件。匯出檔裡已發布但沒有日期的項目，以匯入時間當作 `published_at`。

## 自動儲存

編輯器的自動儲存只寫入版本（revisions），不改文章本身，所以不會發 `OnPostSave`；按下儲存才會。

## 日誌

`JellyCMS.log.*` 與回呼失敗會寫進後台外掛詳情的「日誌」分頁（`system_logs`，帶 `plugin_id`），也會印到伺服器標準輸出。
