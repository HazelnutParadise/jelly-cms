# JavaScript 擴充點

外掛在 `plugin.json` 的 `extensions` 陣列宣告擴充點，再以 `JellyCMS.registerExtension(point, callback)` 註冊回呼。目前可使用 `search.engine`、`media.storage`、`theme.inject`、`content.render`、`comments.component` 與 `notify.channel`；其他擴充點只保留名稱。

同一外掛再次註冊同一擴充點會取代前一個回呼。只有實際註冊、已啟用、權限核准雜湊仍符合的外掛，才會列為可用提供者；單獨宣告 manifest 不會生效。

回呼收到一個 JSON 值，並回傳可序列化的 JSON 值。單次呼叫包含排隊、外掛執行與 host API，合計上限一秒；輸入及結果各不超過 1 MiB，JSON 深度不超過 64。函式、`undefined`、循環物件及逾限結果會被拒絕。

管理員在外掛詳情頁選擇提供者，設定保存在 `extension_bindings`。網路、設定與儲存資料的 host API 仍須取得各自的權限。

擴充點分兩種。單一提供者的點（`search.engine`、`comments.component`）依啟用清單順序使用第一個可用的提供者，沒有可用的就用內建實作。多提供者的點（`media.storage`、`theme.inject`、`content.render`、`notify.channel`）同時使用多個：啟用清單為空時，所有可用的提供者依外掛 ID 順序全部生效；清單非空時只有清單內可用的提供者依排列順序生效。`GET /api/admin/extensions` 的 `multiple` 欄位標示這個差別；沒有內建實作的點，`providers` 不含 `builtin`，沒有外掛時什麼都不做。

網站頁面的擴充點（`theme.inject`、`content.render`、`comments.component`）不會每個請求都呼叫外掛：結果快取在伺服器程序的記憶體，外掛啟用、停用、重新載入、安裝、解除安裝、設定變更（含外掛自己呼叫 `settings.set`）或擴充設定變更時全部失效；任何快取結果最多沿用 60 秒。多個伺服器程序各自快取，彼此不同步。回呼失敗、逾時或回傳格式不對時，該頁面照常輸出（略過注入、用原本的內容、用內建留言區），失敗結果記 30 秒不重試，並在外掛日誌記一筆 error。

## 搜尋引擎

`search.engine` 依啟用清單順序使用第一個可用提供者。回呼輸入為：

```json
{"query":"搜尋文字","max_candidates":10000}
```

結果包含 `ids` 陣列與 `complete: true`，表示已回傳這次查詢的完整候選文章 ID。ID 須為正整數，最多 10,000 個；陣列順序就是結果順序，重複 ID 只保留第一次。有效的空陣列表示沒有結果。

核心會重新查詢資料庫，只保留既有公開搜尋範圍內的文章，排除私密、草稿、未來發布及回收桶內容，之後才計算總數與分頁。文章內容、標題與摘要由核心產生，外掛不能以結果物件帶入其他內容。只有當頁文章會載入全文。

提供者不存在、停用、逾時，或結果不完整／不合法時，會改用內建搜尋。外掛可做語意搜尋，核心不會再要求候選文章逐字包含查詢文字。

## 附件儲存

策略的 `driver` 使用 `plugin:<外掛 ID>`。沒有設定 `media.storage` 啟用清單時，策略本身就是明確選擇；清單非空時，策略的外掛須列在其中。可以同時啟用多個儲存提供者，位次不會使其他策略失效。

回呼只描述物件請求，檔案內容由父程序透過 HTTP 串流傳送，不經 JavaScript 或 JSON。

| 輸入欄位 | 內容 |
|---|---|
| `op` | `put`、`open`、`stat` 或 `delete` |
| `path` | 經驗證的相對物件路徑 |
| `size` | 上傳的位元組數，其他操作可能為零 |
| `content_type` | 上傳的 MIME，其他操作可能為空字串 |

回傳物件必須包含絕對 HTTP(S) `url`，可附字串對應表 `headers`。核心固定使用 `PUT`／`GET`／`HEAD`／`DELETE`，不接受重新導向，且會檢查 `http.fetch` 的允許主機與實際連線位址。不得在 headers 提供 Authorization、Cookie、Host 或逐跳標頭。需要簽署授權時，請由提供者回傳已簽署的物件網址。

獨占上傳會附上 `If-None-Match: *`，物件伺服器必須原子拒絕覆寫並回傳 409 或 412。一般上傳可以覆寫。不存在的讀取／查詢回 404，刪除不存在的物件可回 404 或 204。`HEAD` 必須提供正確的 `Content-Length`。

策略測試會真的上傳、讀回核對、查詢大小並刪除一個隨機探測物件。對外附件網址固定由核心的 `/media/:id` 提供；外掛物件網址不會存進文章。外掛停用、核准過期或策略被排除時，既有 driver 也會拒絕操作，不會改存到本機。

## 主題注入

`theme.inject` 讓外掛把 HTML 加進每個公開頁面的 `<head>` 結尾與 `</body>` 之前，接在後台「程式碼注入」設定的內容之後；多個提供者依啟用順序串接。回呼輸入固定是 `{}`，回傳：

```json
{"head":"<meta name=\"generator\" content=\"my-plugin\">","footer":"<script src=\"/plugins/my-plugin/track.js\"></script>"}
```

兩個欄位都是字串，可以省略其中一個。HTML 原樣輸出，不做轉義或清理：外掛是管理員核准的，信任層級與程式碼注入相同。每個外掛的 `head` 與 `footer` 各最多 64 KiB，超過的那一格會略過並記錄。商務頁面（`/checkout` 與 `/account` 之下）沿用「商務頁也注入」的設定，關閉時外掛注入也不輸出。每個外掛的結果在同一世代只計算一次，不是每個請求都呼叫。

## 內容渲染

`content.render` 在文章、頁面與首頁指定頁面的公開 HTML 產生之後、標題編號與目錄之前，讓外掛後處理內容；RSS／Atom 與 `/api/v1` 的輸出不經過它。多個提供者依啟用順序串接，前一個的輸出是下一個的輸入。回呼輸入：

```json
{"post":{"id":12,"type":"post","slug":"hello","title":"Hello","author_id":1},"html":"<p>…</p>"}
```

回傳 `{"html":"…"}`。整條鏈共用 3 秒預算，每次呼叫仍各有 1 秒上限。結果以文章 ID、輸入 HTML 的雜湊與世代為鍵快取（最多 256 筆、32 MiB），文章內容改變就重算；超過 1 MiB 的內容不送外掛。任一提供者失敗，整篇用原本的 HTML。

## 留言元件

`comments.component` 讓外掛取代主題的留言區。單一提供者：有可用的外掛時，文章頁以外掛回傳的 HTML 包在 `<section class="comments" id="comments">` 裡輸出，不渲染主題的 `comments` 區塊，也不載入內建留言；文章尾的「留言」按鈕照常顯示。回呼輸入：

```json
{"post":{"id":12,"type":"post","slug":"hello","title":"Hello","author_id":1}}
```

回傳 `{"html":"<div id=\"my-comments\" data-slug=\"hello\"></div>"}`。結果以文章 ID 與世代為鍵快取。內建留言 API 與後台留言管理不受影響；外掛留言系統自己保存留言。站長要關掉內建留言表單時，另外在留言設定關閉 `comments_enabled`。

## 通知器

`notify.channel` 讓外掛收到每一個通知事件（新留言待審、新訂單、付款失敗、新裝置登入等，與站內通知中心相同的事件），把它送到 Slack、LINE 或任何自己的通路。它是事件層級的額外通路，不影響每人的站內、Email 與 Webhook 偏好。

通知事件發生時，每個生效的提供者各排一筆投遞（`notification_deliveries`，channel `plugin`），由背景工作依序送出，失敗時沿用既有的重試（最多 5 次）；每個外掛獨立一筆，某個外掛失敗不影響其他外掛。回呼輸入：

```json
{"kind":"comment.pending","category":"comments","params":{"post":"hello","name":"Mei"},"recipients":{"roles":["admin","editor"],"user_ids":[],"emails":[]},"at":"2026-11-07T03:00:00Z"}
```

回傳任何 JSON 值都算成功，拋出例外或逾時就算失敗。`recipients` 可能帶收件人 Email，因此除了宣告擴充點，manifest 還必須宣告並由管理員核准 `events` 權限；沒有的外掛投遞會失敗並記錄。送出時會重新確認外掛仍啟用、核准仍有效。

## 嵌入 Go 程式

`Runtime.HasExtension(id, point)` 查詢實際回呼可用性，`Runtime.CallExtension(ctx, id, point, input)` 呼叫回呼，`Runtime.ExtensionCalls()` 計數實際送到外掛的擴充呼叫。前兩者不取代網站層的資料庫啟用與權限檢查。事件鉤子（`registerHook`）與網站事件的派送見[外掛事件](plugin-events.md)。
