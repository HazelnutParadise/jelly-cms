# 外掛開發指南

Jelly CMS 的外掛以 JavaScript 撰寫，執行在獨立的 QuickJS worker 子程序。外掛透過全域 `JellyCMS` 物件註冊鉤子、擴充點，並呼叫受權限保護的 host API。worker 與網站程序隔離，外掛只能使用 API 開放的資料，不能直接讀寫網站檔案或資料庫。

- 鉤子目錄與事件 payload：見 [外掛事件](plugin-events.md)。
- 擴充點契約：見 [外掛擴充點](plugin-extensions.md)。
- 可安裝範例：[`examples/plugins/hello-jelly`](../examples/plugins/hello-jelly/README.md)。

## 五分鐘做出第一個外掛

以下用 `hello-jelly` 走一次完整流程。它會在網站頁尾顯示一句招呼語，統計已發布文章數，並提供一個後台動作。

1. 建立外掛資料夾，放入 `plugin.json` 與 `index.js`。內容可直接複製 [`examples/plugins/hello-jelly`](../examples/plugins/hello-jelly/README.md)。
2. 在 `examples/plugins/hello-jelly` 目錄打包：

   ```sh
   cd examples/plugins/hello-jelly
   zip -r ../hello-jelly.zip plugin.json index.js
   ```

3. 登入後台，開「外掛」頁，上傳 `examples/plugins/hello-jelly.zip`。
4. 系統會先預覽 manifest 與權限。確認內容正確後，同意權限並安裝。新安裝的外掛預設停用。
5. 開外掛詳情頁按「啟用」。設定頁可修改 `banner` 文字。
6. 開一般公開頁面，頁尾會出現 `<div class="hello-jelly-banner">...</div>`。
7. 發布一篇文章，回外掛詳情頁的「日誌」分頁，會看到已發布文章數增加。
8. 在詳情頁執行 `stats` 動作。成功時 API 只回 `{"dispatched": true}`，實際數字請看日誌；目前動作回呼的回傳值不會顯示在後台。

外掛安裝後放在 `data/plugins/<id>/`。重新載入、停用再啟用都不會清掉已保存的設定與 `store` 資料。

## 資料夾與打包

最小外掛需要兩個檔案：

```text
hello-jelly/
├── plugin.json
└── index.js
```

- `plugin.json` 必須位於 zip 根目錄，或位於唯一一層包裝資料夾內。兩種佈局不能混用。
- zip 內只能有一個 `plugin.json`。不能有符號連結、絕對路徑、`..` 路徑片段或含冒號的路徑。
- `entrypoint` 是相對於外掛根目錄的路徑，必須指向存在且不超過 1 MiB 的檔案。
- 以 `.` 開頭的隱藏路徑不會被安裝。
- 單一檔案解壓後上限 20 MiB，整包解壓後上限 100 MiB。

打包範例：

```sh
cd examples/plugins/hello-jelly
zip -r ../hello-jelly.zip plugin.json index.js
```

也可以把整個資料夾包成一層：

```sh
cd examples/plugins
zip -r hello-jelly.zip hello-jelly
```

## plugin.json

完整範例：

```json
{
  "id": "hello-jelly",
  "name": "Hello Jelly",
  "version": "1.0.0",
  "description": "在頁尾顯示招呼語，並統計已發布文章數。",
  "author": "Jelly CMS",
  "entrypoint": "index.js",
  "settings": [
    {
      "key": "banner",
      "type": "string",
      "label": "頁尾招呼語",
      "default": "Hello from Jelly"
    }
  ],
  "permissions": [
    { "name": "settings" },
    { "name": "store" },
    { "name": "events" }
  ],
  "actions": [
    { "id": "stats", "label": "顯示統計" }
  ],
  "extensions": [
    { "point": "theme.inject" }
  ]
}
```

`plugin.json` 本身上限 64 KiB。

| 欄位 | 必填 | 說明 |
| --- | --- | --- |
| `id` | 是 | 外掛識別碼，必須符合 `^[A-Za-z0-9][A-Za-z0-9._-]*$`，不可重複。 |
| `name` | 否 | 顯示名稱。 |
| `version` | 否 | 版本字串。發布新版本時請更新。 |
| `description` | 否 | 簡短說明。 |
| `author` | 否 | 作者名稱。 |
| `entrypoint` | 載入時需要 | 入口 JavaScript 檔案的相對路徑。 |
| `settings` | 否 | 設定定義，最多 64 筆。 |
| `permissions` | 否 | 權限宣告，最多 64 筆。 |
| `actions` | 否 | 後台動作，最多 32 筆。 |
| `extensions` | 否 | 擴充點宣告，最多 16 筆。 |
| `webmcp_tools` | 否 | WebMCP 工具定義。格式見 [WebMCP](webmcp.md)。個別無效工具會略過。 |

### settings

| 欄位 | 必填 | 說明 |
| --- | --- | --- |
| `key` | 是 | 必須符合 `^[a-z][a-z0-9_]{0,63}$`。 |
| `type` | 是 | `string`、`secret`、`int` 或 `bool`。 |
| `label` | 否 | 設定頁顯示名稱。 |
| `default` | 否 | 預設值。`secret` 不可設預設值。 |
| `min` | 否 | 只用於 `int`，允許的最小值。 |
| `max` | 否 | 只用於 `int`，允許的最大值。 |

設定文字值上限 64 KiB。`secret` 值只會以字串保存，讀取時不會出現在一般設定清單。

### permissions

| 欄位 | 必填 | 說明 |
| --- | --- | --- |
| `name` | 是 | 權限名稱，必須符合 `^[a-z][a-z0-9_.-]{0,63}$`。 |
| `hosts` | `http.fetch` 需要 | 允許連線的主機名稱，最多 32 筆。必須是小寫完整名稱，不可含連接埠，不支援萬用字元。 |

`permissions` 是宣告，不是自動授權。安裝或變更權限名稱後，管理員必須在後台預覽並同意。核准紀錄只綁定權限名稱：名稱集合改變會使舊核准失效，`hosts` 內容改變不會單獨觸發重新核准。

### actions

| 欄位 | 必填 | 說明 |
| --- | --- | --- |
| `id` | 是 | 必須符合 `^[a-z][a-z0-9_-]{0,63}$`。 |
| `label` | 否 | 後台按鈕文字。 |

動作 `stats` 會呼叫鉤子 `action:stats`。動作會等待外掛完成，但目前回傳值會被忽略；請把結果寫入日誌或 `store`。

### extensions

`point` 必須是本站已出貨的擴充點，而且要在此宣告。可用名稱與回傳格式見 [外掛擴充點](plugin-extensions.md)。未宣告的點即使程式呼叫 `registerExtension` 也會被拒絕。

## JellyCMS API

所有 API 都是同步呼叫。host API 的參數與結果必須是合法 JSON，不能傳 `undefined`；任何參數是 `undefined` 時會拋出：

```text
TypeError: host API arguments must be JSON values
```

每次 JavaScript 請求最多呼叫 64 次 host API。參數與結果上限 1 MiB，JSON 深度上限 64。host API 失敗時會以 JavaScript 例外拋出，請用 `try`／`catch` 處理。

### registerHook

```javascript
JellyCMS.registerHook(hookName, callback);
```

- 權限：事件鉤子需要 `events` 權限並經核准；`OnBoot`、`OnShutdown`、`action:<id>` 不需要 `events`。
- 回傳 `undefined`。
- 同一個鉤子可註冊多個回呼，依註冊順序執行。
- 回呼必須在 1 秒內完成。回傳值會被忽略。
- 未定義或本站不會派送的鉤子會寫一筆警告，但回呼仍會保留，供明確呼叫使用。

鉤子名稱與 payload 見 [外掛事件](plugin-events.md)。

### registerExtension

```javascript
JellyCMS.registerExtension(point, callback);
```

- 權限：依擴充點而定，不需要 `events`。
- 回傳 `undefined`。
- 擴充點必須已由本站出貨，且在 manifest 的 `extensions` 宣告。
- 同一個擴充點重複註冊時，後者取代前者。
- 回呼輸入與結果上限 1 MiB，必須在 1 秒內完成。
- 回傳值必須可 JSON 序列化。實際欄位依擴充點而定，見 [外掛擴充點](plugin-extensions.md)。
- `theme.inject` 的 `head` 與 `footer` 各自上限 64 KiB，超過的那一格會略過並記日誌。

### log 與 console

```javascript
JellyCMS.log.debug(message);
JellyCMS.log.info(message);
JellyCMS.log.warn(message);
JellyCMS.log.error(message);
```

- 權限：無。

`console.log` 與 `console.info` 會轉成 `info`，`console.warn`、`console.error`、`console.debug` 分別對應同名等級。這些方法都回傳 `undefined`。日誌可在後台外掛詳情頁查看，也會寫入系統日誌。單筆訊息上限 16 KiB，每次請求的日誌總量上限 1 MiB。

### config.get

```javascript
const value = JellyCMS.config.get(key);
```

- 權限：有 `settings` 權限時讀取已保存設定；沒有權限時只讀取本次 worker 記憶體。
- 沒有值時回 `undefined`。
- 有 `settings` 權限但 key 未在 manifest 宣告時會拋錯。

### config.set

```javascript
const result = JellyCMS.config.set(key, value);
```

- 權限：有 `settings` 權限時保存到資料庫；沒有權限時只存在本次 worker 記憶體。
- 有 `settings` 權限時回 `{"stored": true, "persisted": true}`。
- 沒有 `settings` 權限時回：

  ```json
  {
    "stored": true,
    "persisted": false,
    "message": "stored in memory for this plugin session, persistence is not implemented yet"
  }
  ```

- 沒有權限的 `config.set` 在重新載入、停用或程序重啟後就會消失。需要保存的設定請宣告 `settings` 權限，並使用 `settings` 或 `config`。

### settings.get

```javascript
const value = JellyCMS.settings.get(key);
```

- 權限：`settings`，必須在 manifest 宣告並經核准。
- 回傳已保存值；沒有保存值時回傳 manifest 預設值。
- `secret` 沒有預設值，未設定時回 `undefined`。
- key 未宣告、權限不足或核准過期時會拋錯。

### settings.set

```javascript
const result = JellyCMS.settings.set(key, value);
```

- 權限：`settings`。
- 回傳 `{"stored": true, "persisted": true}`。
- 值必須符合 manifest 型別：`string`／`secret` 是字串，`int` 是整數並符合 `min`／`max`，`bool` 是布林。
- 傳 `null` 會刪除已保存值，之後 `settings.get` 回到預設值；`secret` 未設定時為 `undefined`。
- key 未宣告、型別不符或儲存失敗時會拋錯。

### config 與 settings 的差別

兩者指向同一組外掛設定，差別在沒有 `settings` 權限時的退路：

| 呼叫 | 有 `settings` 權限 | 沒有 `settings` 權限 |
| --- | --- | --- |
| `config.get` | 讀取已保存設定 | 讀取本次 worker 記憶體 |
| `config.set` | 保存到資料庫 | 只寫本次 worker 記憶體 |
| `settings.get` | 讀取已保存設定 | 拋出權限錯誤 |
| `settings.set` | 保存到資料庫 | 拋出權限錯誤 |

新外掛請直接用 `settings`，比較不容易誤會保存行為。

### store.get

```javascript
const value = JellyCMS.store.get(collection, key);
```

- 權限：`store`。
- 找不到時回 `undefined`。
- `collection` 必須符合 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`。
- `key` 必須符合 `^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`。

### store.set

```javascript
const result = JellyCMS.store.set(collection, key, value);
```

- 權限：`store`。
- 回傳 `{"stored": true, "persisted": true}`。
- `value` 必須是合法 JSON，上限 1 MiB，深度上限 64。
- 同一個 `collection`、`key` 會覆寫舊值。

### store.delete

```javascript
const result = JellyCMS.store.delete(collection, key);
```

- 權限：`store`。
- 回傳 `{"deleted": true}` 或 `{"deleted": false}`。
- 刪除不存在的鍵不算錯誤。

`store` 只適合小量外掛資料，資料已按外掛 ID 隔離。`collection` 與 `key` 仍建議加上用途前綴，避免同一外掛的不同功能互相覆寫。

### http.fetch

```javascript
const response = JellyCMS.http.fetch(url, options);
```

- 權限：`http.fetch`，且 manifest 要列明確 `hosts`。
- `options` 可省略，欄位為 `method`、`headers`、`body`；`headers` 是字串對字串的物件，`body` 是字串。
- 允許方法：`GET`、`POST`、`PUT`、`PATCH`、`DELETE`、`HEAD`；預設 `GET`。
- URL 最長 8192 bytes。
- 連線與整體呼叫都受 1 秒上限。
- 請求 body 與回應 body 上限 512 KiB；請求 header 最多 32 個，單一值上限 8 KiB；回應 header 總量上限 64 KiB。
- 禁止設定 `authorization`、`cookie`、`proxy-authorization`、`set-cookie` 與 hop-by-hop header。
- 最多追隨 3 次重新導向，且每次導向的主機都必須在允許清單。HTTPS 不可降級為 HTTP。
- `hosts` 必須是完整小寫主機名稱，不接受萬用字元、私有網路或 loopback 位址。
- 回傳：

  ```json
  {
    "status": 200,
    "headers": { "Content-Type": "application/json" },
    "body": "{\"ok\":true}"
  }
  ```

- 連線失敗、主機未允許、body 或 header 過大時會拋錯。
- 不要把秘密寫進日誌；外掛保存的 `secret` 設定可以用在 header，但回應中的敏感 header 會被移除。

```javascript
const response = JellyCMS.http.fetch("https://api.example.com/status", {
  method: "GET",
  headers: { Accept: "application/json" },
});
JellyCMS.store.set("hello-jelly", "last_fetch", {
  status: response.status,
  at: new Date().toISOString(),
});
```

### content.get

```javascript
const post = JellyCMS.content.get(ref);
```

- 權限：`content.read`。
- `ref` 可以是文章 ID（正整數）或 slug（字串），最長 255 bytes。
- 回傳文章或頁面物件；找不到或在回收桶時回 `null`。草稿與私人內容也會回傳，請依 `status`、`visibility` 自行判斷。
- `content` 本身超過 256 KiB，或完整結果超過 1 MiB 時，會省略 `content` 並帶 `content_omitted: true`；省略後仍超過 1 MiB 就回錯誤。
- 整體仍受 1 秒呼叫上限。

### content.list

```javascript
const result = JellyCMS.content.list(query);
```

- 權限：`content.read`。
- `query` 可省略，支援 `page`、`per_page`、`type`、`status`、`visibility`、`category`、`tag`、`search`；未知鍵會忽略，文字篩選最長 160 字元。
- `page` 預設 1，`per_page` 預設 20、最大 100。
- 回傳 `{items, total, page, per_page}`。`items` 是摘要，一定不含 `content`，並帶 `content_omitted: true`。
- 結果上限 1 MiB。超過時請縮小 `per_page` 或加篩選。
- 回收桶項目不會回傳。

### shop.getProduct

```javascript
const product = JellyCMS.shop.getProduct(ref);
```

- 權限：`shop.read`。
- `ref` 是商品 ID 或 slug，最長 255 bytes。
- 找不到時回 `null`。欄位與事件 payload 的商品物件相同。

### shop.listProducts

```javascript
const result = JellyCMS.shop.listProducts(query);
```

- 權限：`shop.read`。
- 支援 `page`、`per_page`、`status`；其他未知鍵會忽略。
- `page` 預設 1，`per_page` 預設 20、最大 100。
- 回傳 `{items, total, page, per_page}`，內容為商品摘要。
- 結果上限 1 MiB。

### shop.getOrder

```javascript
const order = JellyCMS.shop.getOrder(ref);
```

- 權限：`shop.read`。
- `ref` 是訂單 ID 或訂單編號，最長 255 bytes。
- 找不到時回 `null`。欄位與事件 payload 的訂單物件相同。

### shop.listOrders

```javascript
const result = JellyCMS.shop.listOrders(query);
```

- 權限：`shop.read`。
- 支援 `page`、`per_page`、`status`、`email`；其他未知鍵會忽略。
- `page` 預設 1，`per_page` 預設 20、最大 100。
- 回傳 `{items, total, page, per_page}`。清單項目不含訂單明細 `items`；需要明細請用 `getOrder`。
- 結果上限 1 MiB。

## 生命週期

### 載入

啟用外掛時會依序：

1. 建立 worker 與隔離工作目錄。
2. 載入 manifest 與入口腳本。
3. 執行 `OnBoot`。
4. 標記外掛可用。

每次載入都會重新執行入口腳本與 `OnBoot`。JavaScript 全域變數、模組狀態與計時器都不會跨載入保留。需要保留的資料請放 `settings` 或 `store`。

### 重新載入

後台按「重新載入」會以同一個外掛目錄建立新 worker，重新執行入口腳本與 `OnBoot`，成功後才替換舊 worker。新 worker 載入失敗時，舊 worker 會繼續服務，後台顯示錯誤。

### 停用與 OnShutdown

停用外掛時會先將資料庫狀態改為停用，再卸載 worker，並執行一次 `OnShutdown`。卸載原因可能是 `unloaded`、`replaced` 或 `runtime closed`。`OnShutdown` 必須在 1 秒內完成；失敗或逾時不會阻止卸載。

停用、解除安裝或程序關閉後，不應再假設任何記憶體狀態存在。

### 自動重啟

<!-- 依工單 36 R1 -->

外掛在執行中因下列原因失效，而且沒有被卸載時，系統會自動重新載入：

- 單次呼叫逾時。
- 回傳值超過大小上限。
- worker 協定錯誤。
- worker 程序意外結束。

自動重啟使用與手動重新載入相同的候選流程：先建立新 worker、執行入口腳本與 `OnBoot`，成功後才替換。JavaScript 記憶體不會保留；`settings` 與 `store` 不受影響。

失效後依序等待 1 秒、5 秒、30 秒、2 分鐘、10 分鐘再重新載入，之後每次等 10 分鐘。等待期間：

- 擴充點與後台動作的呼叫立即失敗，不排隊；後台動作回 `{"dispatched": false, "reason": "plugin_restarting"}`。
- 網站事件直接丟棄，不會補送。
- worker 程序在閒置時意外結束也會被偵測到，不必等下一次呼叫。

失效次數只計最近 30 分鐘內的失效。達到 5 次時外掛進入「已停止運作」，不再自動重啟，並通知管理員（每次進入都通知一次）。管理員手動重新載入、重新啟用或上傳新版本後重新開始。

停用、解除安裝或程序關閉會取消等待中的自動重啟。

### 熱升級

<!-- 依工單 36 R3 -->

外掛已載入時（包含執行中、等待重啟與已停止運作）可以直接上傳新版本。系統會先驗證新封包與權限，再以交易方式替換外掛檔案並建立新 worker：

1. 舊 worker 繼續服務。
2. 新版本解壓到暫存位置並驗證 manifest、權限與入口腳本。
3. 寫入外掛資料列的版本、權限核准雜湊與更新時間，讓新 worker 的 `OnBoot` 能比對目前核准。寫入失敗時還原檔案並回 500。
4. 新 worker 執行入口腳本與 `OnBoot`。
5. 成功後舊 worker 收到 `OnShutdown`（原因 `replaced`），新 worker 接手。

新版本啟動失敗時會回 409，訊息為「新版本無法啟動，已保留原版本」，並附上外掛日誌的最後一則錯誤；檔案與資料列都會還原。原本正在執行、等待重啟或已停止運作的狀態都保留。新版本若變更權限名稱，仍需先預覽並同意新權限；未同意前不會替換執行中的外掛。整個換版流程有自己的期限，不會因上傳請求取消而中斷；程序若在換檔與重新載入之間結束，下次啟動會依提交紀錄保留新版或還原舊版。`settings` 與 `store` 會保留，但 JavaScript 記憶體不會轉移。

## 限制

| 項目 | 上限 |
| --- | --- |
| `plugin.json` | 64 KiB |
| 入口檔案 | 1 MiB |
| 事件 payload | 1 MiB |
| `content.get` 直接附帶的 `content` | 256 KiB |
| 擴充點輸入／結果 | 1 MiB |
| host API 參數／結果 | 1 MiB |
| JSON 深度 | 64 |
| 單次 worker 呼叫（hook／extension／boot／shutdown） | 1 秒 |
| worker 啟動握手 | 5 秒 |
| 每次 JS 請求的 host API 呼叫數 | 64 |
| 單筆日誌 | 16 KiB |
| 每次請求的日誌總量 | 1 MiB |
| HTTP URL | 8192 bytes |
| HTTP 請求 body | 512 KiB |
| HTTP 回應 body | 512 KiB |
| HTTP 請求 header 數 | 32 |
| HTTP 單一 header 值 | 8 KiB |
| HTTP 回應 header 總量 | 64 KiB |
| HTTP 重新導向 | 3 次 |
| `store` collection | 64 字元 |
| `store` key | 255 字元 |
| 單一設定文字值 | 64 KiB |
| 每次上傳的單一檔案 | 20 MiB |
| 每次上傳的總解壓大小 | 100 MiB |

事件外掛另有自己的佇列與暫停規則，細節見 [外掛事件](plugin-events.md)。

## 權限

只有以下權限會實際限制 API。其他名稱會顯示在權限清單，但不會開放任何功能。

| 權限 | 開放內容 |
| --- | --- |
| `content.read` | `JellyCMS.content.get`、`JellyCMS.content.list` |
| `events` | 接收網站事件鉤子 |
| `http.fetch` | `JellyCMS.http.fetch`，並受 manifest `hosts` 限制 |
| `settings` | 保存與讀取外掛設定；`config` 的持久化也靠這個權限 |
| `shop.read` | `JellyCMS.shop.*` 讀取 API |
| `store` | `JellyCMS.store.*` |

`OnBoot`、`OnShutdown` 與後台 `action:<id>` 不受 `events` 權限限制。

權限核准的雜湊只包含權限名稱，名稱會去重、排序後以換行串接再計算 SHA-256。新增、移除或改名都會讓舊核准失效。因為雜湊不含 `hosts`，只改允許主機不會自動讓舊核准失效；更新 `hosts` 後仍請在後台重新預覽確認。安裝或更新後請在後台重新預覽並同意。

## 除錯

### 看日誌

- 後台外掛詳情頁的「日誌」分頁會顯示最近 200 筆。
- 外掛日誌也會寫入系統日誌，`debug` 與 `info` 會以 `info` 等級保存，`warn`、`error` 保留原等級。
- 主機 stdout 會顯示 worker 啟動、重啟、逾時與程序結束訊息。
- 發生例外時先看日誌，再看鉤子 payload 是否符合 [外掛事件](plugin-events.md) 的格式。

### 常見錯誤

| 訊息 | 原因 | 處理 |
| --- | --- | --- |
| `host API arguments must be JSON values` | 傳了 `undefined`，常見於 `store.get("x")` 少傳 key。 | 補齊參數，或先給預設值。 |
| `plugin permission denied: store` | 沒有宣告或核准 `store`。 | 在 manifest 宣告權限，重新預覽並同意。 |
| `plugin permission approval is stale: settings` | 權限名稱清單和已核准的不同。 | 到後台重新預覽並同意目前權限。 |
| `setting is not declared by the plugin` | 讀寫未在 `settings` 定義的 key。 | 在 manifest 加入設定定義。 |
| `plugin store collection is invalid` | collection 空白、過長或含不允許字元。 | 使用英數字開頭，只含英數、`.`、`_`、`-`。 |
| `plugin store key is invalid` | key 空白、過長或含不允許字元。 | 同上，長度上限 255。 |
| `plugin worker hook request timed out after 1s` | 回呼超過 1 秒。 | 拆小工作，避免同步網路或大量迴圈。 |
| `Plugin action failed` | 後台動作的回呼拋錯或超過 1 秒。 | 看外掛日誌的錯誤，修正後重新載入。 |
| `plugin is not loaded` | 外掛停用、正在自動重啟或已停止運作。 | 看後台狀態與日誌，必要時手動重新載入。 |
| `hook "X" is not dispatched by this site` | 鉤子名稱拼錯或版本不支援。 | 對照 [外掛事件](plugin-events.md)。 |
| `extension point "X" is not declared by this plugin` | 已出貨但 manifest 沒宣告。 | 在 `extensions` 加入該點。 |
| `http.fetch host is not allowed` | 主機不在 `hosts`，或 URL 主機大小寫與允許清單不符。 | 在權限的 `hosts` 加入完整小寫主機名稱。 |
| `http.fetch requires an explicit host allowlist` | 宣告了 `http.fetch` 但沒有 `hosts`。 | 補上允許的主機清單。 |
| `Preview this exact archive and accept its permissions before installing` | 上傳後直接安裝，沒有先預覽並同意這份封包。 | 重新上傳，使用預覽流程同意權限。 |

### 開發建議

- 先用最小 manifest 與入口腳本確認能載入，再逐項加權限、鉤子與擴充點。
- 在回呼外層包 `try`／`catch`，把錯誤寫進 `JellyCMS.log.error`，比較容易定位。
- 鉤子與擴充點都有 1 秒上限；不要在同一次呼叫裡做大量資料庫查詢或連續網路請求。
- 用 `per_page` 與篩選控制讀取結果，避免碰到 1 MiB 上限。
- 不要把 `secret`、存取權杖或完整訂單個資寫進日誌。
- 測試安裝時用乾淨的站點與專用資料庫，不要對正式資料操作。

## 發布前檢查清單

- [ ] `plugin.json` 是合法 JSON，`id` 唯一且符合格式。
- [ ] 已更新 `version`，`entrypoint` 指向存在的檔案。
- [ ] zip 只有一個 `plugin.json`，沒有符號連結、絕對路徑或 `..`。
- [ ] manifest 只宣告實際需要的權限；`http.fetch` 有明確 `hosts`。
- [ ] 所有 `settings` key、型別與預設值正確，`secret` 沒有預設值。
- [ ] 所有 `actions.id` 與 `extensions.point` 都存在且已宣告。
- [ ] 入口腳本重新載入後仍能運作，不依賴跨載入的記憶體。
- [ ] 鉤子與擴充點都在 1 秒內完成，payload 與結果未超過大小上限。
- [ ] `store` collection 與 key 有外掛專屬前綴。
- [ ] 已在乾淨站點完成上傳、同意權限、啟用、停用與重新載入。
- [ ] 已實際看過公開頁輸出、後台動作與日誌。
- [ ] 日誌沒有秘密或完整個資。
- [ ] 範例與文件中的 API 名稱、限制和實際版本一致。
