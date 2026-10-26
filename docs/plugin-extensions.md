# JavaScript 擴充點

外掛在 `plugin.json` 的 `extensions` 陣列宣告擴充點，再以 `JellyCMS.registerExtension(point, callback)` 註冊回呼。目前可使用 `search.engine` 與 `media.storage`；其他擴充點只保留名稱。

同一外掛再次註冊同一擴充點會取代前一個回呼。只有實際註冊、已啟用、權限核准雜湊仍符合的外掛，才會列為可用提供者；單獨宣告 manifest 不會生效。

回呼收到一個 JSON 值，並回傳可序列化的 JSON 值。單次呼叫包含排隊、外掛執行與 host API，合計上限一秒；輸入及結果各不超過 1 MiB，JSON 深度不超過 64。函式、`undefined`、循環物件及逾限結果會被拒絕。

管理員在外掛詳情頁選擇提供者，設定保存在 `extension_bindings`。網路、設定與儲存資料的 host API 仍須取得各自的權限。

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

## 嵌入 Go 程式

`Runtime.HasExtension(id, point)` 查詢實際回呼可用性，`Runtime.CallExtension(ctx, id, point, input)` 呼叫回呼。前者不取代網站層的資料庫啟用與權限檢查。舊的 `registerHook`／`CallHook` 事件行為保持相容。
