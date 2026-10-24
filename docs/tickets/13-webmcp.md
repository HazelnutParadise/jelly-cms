# 13 — 使用者瀏覽器裡的 AI 代理可以用 WebMCP 工具搜尋、列出與閱讀網站內容

Epic：前台主題與 AI 代理
來源：GitHub issue HazelnutParadise/jelly-cms#7
User Story：訪客請瀏覽器裡的 AI 代理「找這個網站上講某主題的文章」「摘要這篇」時，代理不必讀畫面、猜按鈕，而是在任何前台頁面直接呼叫網站宣告的唯讀工具，拿到結構化結果。不論用哪個主題都一樣；草稿、私密、排程與回收桶內容照樣拿不到；站長可以在後台關掉；外掛與主題可以用宣告的方式加自己的唯讀工具。
Blocked by：無。05 的公開範圍 `publicPostVisibility`、`/api/search` 與主題錯誤頁已在 main。與草稿工作單的重疊與調和方式見「相依與調和」。
Status：draft（本工作單只交付契約、設計板、可編譯骨架與失敗的驗收測試；實作由另一批代理依本文件完成）
設計稿：`design/project/Settings.dc.html` 新增「同一頁 · 網站分頁」區塊，其中「搜尋引擎與 AI 代理」小節有 WebMCP 開關這一列（板高改為 1320）。前台畫面沒有改變，WebMCP 沒有可見介面。

## 規格查證（2026-10-24）

查證對象：https://webmachinelearning.github.io/webmcp/ ，Web Machine Learning Community Group 的 Draft Community Group Report，日期 2026-10-20，不在標準軌道上。和 issue 的寫法相比：

| 項目 | issue 寫的 | 規格現況 | 本工作單採用 |
|---|---|---|---|
| 進入點 | `document.modelContext`，早期 `navigator.modelContext` 據報在 Chromium 150 標為棄用 | IDL 只有 `partial interface Document { [SecureContext, SameObject] readonly attribute ModelContext modelContext; }`，規格裡完全沒有 `navigator.modelContext`；棄用消息無法從規格證實 | 優先 `document.modelContext`，沒有才用 `navigator.modelContext` 相容舊實作 |
| `registerTool` 回傳 | 沒寫 | `Promise<undefined> registerTool(ModelContextTool tool, optional ModelContextRegisterToolOptions options = {})`；名稱不合法、說明空字串、重名時以 `InvalidStateError` 拒絕，`tools` 權限政策不允許時以 `NotAllowedError` 拒絕，`signal` 已中止時以其中止原因拒絕 | 腳本必須接住每一個 Promise 的拒絕，否則會在 console 留下未處理的拒絕 |
| 註冊選項 | `{ signal, exposedTo }` | `ModelContextRegisterToolOptions { sequence<USVString> exposedTo; AbortSignal signal; }`，`signal` 中止即取消註冊；沒有 `unregisterTool` | 只傳 `signal`，不傳 `exposedTo` |
| 工具欄位 | `name`、`title`、`description`、`inputSchema`、`execute(input, { signal })`、`annotations` | `ModelContextTool { required DOMString name; USVString title; required DOMString description; object inputSchema; required ToolExecuteCallback execute; ToolAnnotations annotations; }`；`execute` 是 `Promise<any> (object inputObject, ToolExecuteCallbackOptions options)`，`options` 只有 `required AbortSignal signal`；結果會序列化成 JSON，無法序列化或 Promise 被拒絕時對代理回報 `OperationError` | 相同 |
| 名稱限制 | 沒寫 | 1 到 128 個字元，只能是 ASCII 英數字、`_`、`-`、`.` | 本專案更嚴：`^[a-z][a-z0-9_]{0,63}$`，是規格允許範圍的子集 |
| 註解 | `readOnlyHint`、`untrustedContentHint`、`consequentialHint`、`debugging` | `ToolAnnotations` 四個布林，預設都是 false | 相同 |
| 權限政策 | `Permissions-Policy: tools=()` 關閉整頁 | 政策功能名稱 `tools`，預設允許清單 `'self'` | 相同 |
| 其他 | 沒寫 | `ModelContext` 另有 `getTools()`、`executeTool()` 與 `toolchange`、`toolactivated`、`toolcancel` 事件 | 不使用 |

規格是草案，欄位可能再變；相容層集中在 `web/jelly/webmcp.js` 的一個函式裡，變動時只改這一處。

## 完成條件

- [ ] 訪客的瀏覽器若提供 `document.modelContext`（或只有舊的 `navigator.modelContext`），在預設主題的首頁、文章總覽、單篇、頁面、分類、彙整、搜尋與 404 頁，以及任何其他主題的同類頁面，都會註冊 `search_content`、`list_posts`、`get_post`、`get_site_info` 四個唯讀工具，加上已啟用外掛與使用中主題宣告的工具。
- [ ] 訪客的瀏覽器不支援 WebMCP 時，頁面照常顯示，腳本不抓工具清單，console 沒有新增任何錯誤或警告。
- [ ] 代理呼叫 `search_content` 拿到的文章與 `/search` 畫面一致；呼叫 `get_post` 拿到的標題與段落文字與單篇畫面一致，不含任何 HTML；草稿、私密、排程、回收桶與不存在的 slug 一律拿到同一個「找不到」回應。
- [ ] 代理呼叫 `list_posts` 可以分頁列出公開文章，每頁預設 10、最多 50；呼叫 `get_site_info` 拿到網站名稱、描述、語言與網址。
- [ ] 站長在後台設定頁關掉「WebMCP」後，前台頁面不載入腳本、不註冊任何工具，回應帶 `Permissions-Policy: tools=()`，`/_jelly/webmcp.js` 與 `/_jelly/webmcp-tools.json` 回 404；`/api/public/*` 照常可用；重新開啟後立即恢復。
- [ ] 外掛作者在 `plugin.json`、主題作者在 `theme.json` 宣告 `webmcp_tools`，外掛載入（啟用）時或主題使用中時工具出現，卸載或換主題後消失；外部網址、後台路徑、名稱不合法、重名與格式錯誤的宣告被略過並記一則警告，且不影響外掛或主題本身載入。
- [ ] 後台、登入、安裝、帳號頁與維護頁不載入 WebMCP 腳本；JSON 回應不受影響。
- [ ] 文件 `docs/webmcp.md` 說明內建工具（名稱、輸入、回傳、註解）、如何關閉（後台開關與 `Permissions-Policy`）、外掛與主題如何宣告工具（欄位、驗證規則、優先順序、範例）。
- [ ] 主代理依「驗收步驟」啟動真實伺服器，`scripts/acceptance/webmcp.spec.mjs` 全數通過。

## 錯誤與邊界

`/api/public/content/{slug}` 對非公開與不存在的 slug 回應完全相同（狀態、標頭與本文，只差每次請求不同的 `correlation_id` 與 `X-Request-ID`）。`per_page` 非數字、小於 1 用 10，大於 50 用 50；`page` 非數字、小於 1 用 1；超過最後一頁回 200 與空陣列。維護模式期間 `/api/*` 依 05 回 JSON 503，維護頁不注入腳本。資料庫未初始化時新端點回 503 與統一錯誤格式。`/_jelly` 是保留路徑，任何頁面 slug 都不能用。

## 契約（骨架與測試共同依據）

### 路徑

| 路徑 | 回應 | 關閉時 |
|---|---|---|
| `GET /_jelly/webmcp.js` | 原樣送出 `web/jelly/webmcp.js`，`Content-Type: text/javascript; charset=utf-8` | 404 |
| `GET /_jelly/webmcp-tools.json` | 工具清單 JSON | 404 |
| `GET /api/public/posts?page=&per_page=` | `list_posts` 的資料 | 照常 |
| `GET /api/public/content/{slug}` | `get_post` 的資料 | 照常 |
| `GET /api/public/site` | `get_site_info` 的資料 | 照常 |
| `GET /api/search?q=` | 既有端點，`search_content` 沿用，形狀不改 | 照常 |

`/api/public/*` 在 main 與 `claude/pr-d-accounts`、`claude/pr-e-shop`、`claude/pr-g-editor`、`claude/pr-f-system` 上都沒有被用掉（2026-10-24 查過）。`_jelly` 加進 `handlers_public.go` 的 `/:slug` 保留清單。

### 注入

- 標籤固定為 `<script type="module" src="/_jelly/webmcp.js"></script>`（`webmcpScriptTag`）。
- 插入位置：最後一個 `</head>`（不分大小寫）的正前方；沒有就在最後一個 `</body>` 正前方；兩者都沒有就附加在最後。實作在 `theme.InjectBeforeHeadEnd(page []byte, snippet string) []byte`，不修改傳入的切片。
- 時機：主題模板渲染完成之後、寫回應之前，不依賴主題 layout。範圍是所有經主題 `layout.html` 渲染的 HTML 頁面：`publicRender`（首頁、總覽、單篇、頁面、搜尋）、`archiveRender`（分類、標籤、作者、彙整、月份）與錯誤處理器的主題 `error.html`（任何狀態碼）。之後新增的主題頁面（例如 07 的商店頁）也必須走同一步。
- 不注入：`/admin*` 與後台錯誤頁、`/login`、`/install*`、`/account*`、維護頁 `maintenance.html`、所有 JSON 與靜態檔。
- 開關關閉時不注入，並對上述每一個主題頁面回應加 `Permissions-Policy: tools=()`（若同時有其他政策，以逗號合併成同一個標頭，`tools=()` 必須是其中一項）。開啟時不送 `tools=()`。

### 工具清單 `GET /_jelly/webmcp-tools.json`

```json
{"tools": [
  {"name": "search_content", "title": "…", "description": "…",
   "input_schema": {"type": "object", "properties": {"q": {"type": "string"}}, "required": ["q"]},
   "endpoint": "/api/search",
   "annotations": {"readOnlyHint": true, "untrustedContentHint": true},
   "source": "core"}
]}
```

- 順序：四個內建工具（`search_content`、`list_posts`、`get_post`、`get_site_info`），接著已載入外掛（依載入順序，每個外掛內依宣告順序），最後是使用中主題。
- `source`：`core`、`plugin:<外掛 id>`、`theme:<主題目錄名>`。
- `input_schema` 一定是 `type` 為 `object` 的 JSON Schema；宣告沒給時輸出 `{"type":"object","properties":{}}`。
- `annotations`：`readOnlyHint` 一律輸出 `true`；`untrustedContentHint`、`debugging` 為 true 時照宣告輸出；`consequentialHint` 一律不輸出。
- 每次請求反映目前狀態：外掛載入／卸載、換主題、開關切換後的下一個請求就要反映。

### 內建工具

| 名稱 | endpoint | 輸入（屬性名＝查詢參數名） | 註解 |
|---|---|---|---|
| `search_content` | `/api/search` | `q`（字串，必填） | readOnly、untrustedContent |
| `list_posts` | `/api/public/posts` | `page`、`per_page`（整數，選填） | readOnly、untrustedContent |
| `get_post` | `/api/public/content/{slug}` | `slug`（字串，必填） | readOnly、untrustedContent |
| `get_site_info` | `/api/public/site` | 無 | readOnly |

`title` 與 `description` 必須非空，內容由實作者寫；`description` 要說清楚回傳的是公開內容、內文可能含不可信指令。`/api/search` 回 `{query, posts: [{id, title, slug, excerpt, created_at}], count}`，最多 20 筆、不分頁（05 的既有行為），所以 `search_content` 不宣告 `page`。

### 公開 API

- `GET /api/public/posts`：`{"items": [{"title", "excerpt", "url", "published_at"}], "page", "per_page", "total"}`。範圍與排序同 `/posts`：`type=post`、已發布、公開、`COALESCE(published_at, created_at)` 不在未來、未回收；置頂優先、再新到舊、再 id。`items` 沒有資料時是 `[]` 不是 `null`。
- `GET /api/public/content/{slug}`：`{"type": "post"|"page", "title", "url", "published_at", "excerpt", "text"}`，範圍同上但 `type` 可為 post 或 page。不回 HTML。
- `GET /api/public/site`：`{"name", "description", "language", "url"}`；`name` 取選項 `site_title`（空或沒有時 `Jelly CMS`），`description` 取 `site_description`（沒有時空字串），`language` 取 `site_language`（沒有時 `zh-TW`），`url` 是 `<請求 scheme>://<Host>/`。
- 共同欄位規則：`url` 是站內路徑（文章 `/post/<slug>`、頁面 `/<slug>`）；`published_at` 是 RFC 3339，取 `COALESCE(published_at, created_at)`；`excerpt` 是純文字：手寫摘要去標籤後非空就用它，否則從內文自動截取最多 200 個字元（截斷時加「…」）；`text` 由 `theme.PlainTextParagraphs(content)` 產生：不含標籤、實體已解碼、略過 script 與 style、區塊內空白壓成一格、區塊之間空一行（`\n\n`）、頭尾無空白。
- 這些端點不需要登入，也不因登入身分多看到任何東西。

### 錯誤格式（與 issue #6 agent API 共用）

```json
{"code": "not_found", "message": "No published content has this slug.", "correlation_id": "req_k3m9x2q7w4p1z8c5v6b0"}
```

- `code`：`not_found`（404）、`service_unavailable`（503，資料庫未初始化）、`internal_error`（500，不帶內部錯誤文字）。`message` 給人讀，不含 slug 是否存在的線索。
- `correlation_id` 取這個請求的 request id。main 還沒有 request id 中介層，所以在回錯誤時若回應標頭 `X-Request-ID` 為空，就產生 `req_` 加 20 個小寫英數字的隨機值並設進 `X-Request-ID`；工作單 08 的 `RequestID` 中介層合併後改用它（`syslog.RequestIDContextKey`），格式相同。
- 只有新端點用這個格式；`/api/search` 與其他既有端點維持原樣。

### 開關

- 選項鍵 `webmcp_enabled`，值只能是 `"true"` 或 `"false"`；沒有這一列視為 `"true"`。
- 寫入沿用 main 的 `POST /api/admin/settings`（只限 admin）；值不是這兩個字串時整個請求回 400、不寫入任何鍵。
- 後台 `web/admin/settings.html` 的 General 分頁新增一列開關：`<input type="checkbox" id="webmcpEnabled" name="webmcp_enabled">`，載入時反映目前值（沒有列時勾選），儲存時送 `"true"`／`"false"`。對應設計板見 `Settings.dc.html` 的「搜尋引擎與 AI 代理」小節。
- 切換後下一個請求就生效，不需重啟。

### 宣告式擴充（外掛與主題）

`plugin.json` 與 `theme.json` 可以加：

```json
"webmcp_tools": [
  {"name": "list_events", "title": "List events", "description": "List upcoming public events.",
   "input_schema": {"type": "object", "properties": {"month": {"type": "string"}}},
   "endpoint": "/api/plugins/events/public",
   "annotations": {"untrustedContentHint": true}}
]
```

- 欄位放在既有結構上：`plugin.PluginMetadata.WebMCPTools` 與 `core.ThemeConfig.WebMCPTools`，型別都是 `json.RawMessage`（保留原文），每一筆解成 `core.WebMCPToolDecl`。保留原文是為了讓一筆格式錯誤只略過那一筆，不讓 `readPluginManifest` 或 `theme.Manager.Load` 失敗。
- 已啟用外掛＝已載入 `plugin.Runtime` 的外掛，從新方法 `(*plugin.Runtime).LoadedPlugins() []PluginMetadata`（依載入順序回傳複本；nil runtime 回空）讀。使用中主題＝`tm.GetActive()`，以 `tm.Load(name)` 讀 `theme.json`。
- 每一筆依序驗證，任一條不符就略過：
  1. 能解成 `core.WebMCPToolDecl`（例如 `name` 是字串、註解是布林）。
  2. `name` 符合 `^[a-z][a-z0-9_]{0,63}$`。
  3. `description` 去空白後非空。
  4. `endpoint` 以 `/` 開頭、第二個字元不是 `/` 也不是 `\`，全文不含 `\`、`?`、`#`、空白或控制字元（因此也不可能含 scheme 或主機）；不得等於或位於 `/admin`、`/api/admin`、`/auth`、`/api/auth`、`/install`、`/_jelly` 之下。
  5. `input_schema` 沒給，或是 JSON 物件。
  6. 名稱沒有被前面的來源（內建 → 外掛依載入順序 → 主題）用掉；後來者略過。
- 每一筆被略過的宣告用標準 `log` 記一行警告，內容包含 `webmcp`、來源字串（`plugin:<id>` 或 `theme:<name>`）與宣告的名稱（名稱不是字串時記原文）。
- 方法固定 GET；宣告裡沒有方法欄位，有也不讀。`readOnlyHint` 宣告成 false 也輸出 true，`consequentialHint` 不輸出。

### 腳本 `web/jelly/webmcp.js`

- ES module，原始檔不超過 8192 位元組，不含 `http://` 或 `https://` 字樣，只對同源路徑發請求；不需建置步驟、不引用第三方套件。
- 相容層集中在一個函式：有 `document.modelContext` 且具 `registerTool` 就用它，否則看 `navigator.modelContext`，都沒有就回 null；讀取屬性時的例外也視為沒有。
- 沒有宿主時什麼都不做：不抓工具清單、不寫 console。
- 有宿主時：建一個 `AbortController`，`pagehide` 時 `abort()`；`fetch('/_jelly/webmcp-tools.json')`（同源、帶 signal），對每一筆呼叫 `registerTool({name, title, description, inputSchema: input_schema, annotations, execute}, {signal})`。`registerTool` 同步丟出的例外與回傳 Promise 的拒絕都要接住，任何情況都不能在 console 留下錯誤或未處理的拒絕。
- `execute(input, {signal})`：endpoint 裡的 `{name}` 以 `encodeURIComponent(input[name])` 取代；其餘 `input` 屬性（略過 `undefined`、`null`）轉成查詢參數；以 `fetch(url, {signal, credentials: 'same-origin', headers: {Accept: 'application/json'}})` 送 GET，解析 JSON 後原樣回傳，非 2xx 也回傳解析後的錯誤物件（例如 `{code: "not_found", …}`），網路錯誤、中止或非 JSON 時讓 Promise 拒絕。

### Go 骨架（本工作單已建立）

- `internal/core/webmcp.go`：`WebMCPToolDecl{Name, Title, Description string; InputSchema json.RawMessage; Endpoint string; Annotations WebMCPAnnotations}`、`WebMCPAnnotations{ReadOnlyHint, UntrustedContentHint, ConsequentialHint, Debugging bool}`，JSON 鍵為 `name`、`title`、`description`、`input_schema`、`endpoint`、`annotations`、`readOnlyHint`、`untrustedContentHint`、`consequentialHint`、`debugging`。
- `internal/core/theme.go`：`ThemeConfig.WebMCPTools json.RawMessage`（`webmcp_tools`）。
- `internal/plugin/types.go`：`PluginMetadata.WebMCPTools json.RawMessage`（`webmcp_tools`）；`internal/plugin/manager.go`：`(*Runtime).LoadedPlugins()`（目前回 nil）。
- `internal/theme/inject.go`：`InjectBeforeHeadEnd`、`PlainTextParagraphs`（目前回零值）。
- `internal/server/webmcp.go`：常數 `webmcpEnabledKey`、`webmcpScriptTag`、`webmcpScriptFile`，`registerWebMCPRoutes(e, tm, pluginRuntime)`（兩條路由目前回 501）。
- `internal/server/public_api.go`：`registerPublicAPIRoutes(e)`（三條路由目前回 501）。
- `internal/server/routes.go`：在 `registerAccountPages` 與 `RegisterPublicRoutes` 之前註冊上面兩組路由。

### 安全規則

- 工具只讀：內建端點只有 GET，外掛與主題只能宣告 GET 的同源路徑，且不能指向後台、登入、安裝與 `/_jelly` 路徑。腳本以目前瀏覽者的 Cookie 發請求，所以權限完全由伺服器端既有檢查決定，WebMCP 不新增也不繞過任何權限。
- 公開 API 只回公開範圍的內容，非公開與不存在的 slug 回應一致，不洩漏存在與否。
- 回傳作者內容（摘要、內文）的內建工具標 `untrustedContentHint: true`；`text` 與 `excerpt` 不含 HTML。
- 腳本、工具清單與 API 都不從外部網址載入任何東西。

## 決策紀錄

1. 資料驅動的單一腳本：核心提供 `/_jelly/webmcp.js`（相容層加註冊邏輯）與 `/_jelly/webmcp-tools.json`（合併後的清單），腳本讀清單後逐一註冊，`execute` 對同源端點發 GET。否決：每個工具一支腳本（難以統一開關與相容層）；讓外掛注入任意 JS 執行函式（無法保證唯讀與權限一致）。
2. 注入不依賴主題 layout：在主題渲染輸出之後、寫回應之前插入，位置依序是最後一個 `</head>`、最後一個 `</body>`、文件結尾。否決：要求每個主題在 layout 加一行（第三方主題會漏）；用模板函式或 `{{.Inject.Head}}`（同樣依賴主題配合）。
3. 第一版四個內建唯讀工具，`search_content` 沿用既有 `/api/search` 不改形狀，其餘三個新增 `/api/public/*`；`get_post` 只回伺服器轉出的純文字。否決：回 HTML（代理容易把標記當指令、也較大）；為 WebMCP 另做一套搜尋端點（和畫面結果可能分歧）。
4. 新端點錯誤格式 `{code, message, correlation_id}`，與 issue #6 共用；main 沒有 request id 時自己產生 `req_` 開頭的值並放進 `X-Request-ID`。否決：沿用既有 `{"error", "code": 數字}`（無法對應日誌，也和 #6 不一致）。
5. 開關存在 main 的選項表 `webmcp_enabled`，後台設定頁加一列；關閉時不注入、兩個 `/_jelly` 路徑 404、主題頁帶 `Permissions-Policy: tools=()`，公開 API 照常。否決：關閉時連 `/api/public/*` 一起關（那是一般公開資料，也會給 #6 與其他客戶端用）；只靠前端判斷（腳本快取時會繼續註冊）。
6. 外掛與主題以宣告式 `webmcp_tools` 擴充，伺服器端驗證與合併。否決：讓外掛直接提供 JS（同決策 1）；只開放外掛不開放主題（issue 要求主題也能擴充）。
7. 不做：寫入類工具、後台頁面的 WebMCP、`exposedTo` 跨網域、宣告式 HTML 表單。
8. `webmcp_tools` 以 `json.RawMessage` 掛在既有的 `PluginMetadata` 與 `ThemeConfig` 上，加一個 `Runtime.LoadedPlugins()` 讀已載入外掛。這是改動最少的做法。否決：新套件 `internal/webmcp`（多一個套件，還是要改兩個結構）；伺服器自己重讀 `data/plugins/*/plugin.json`（會列出沒載入的外掛，也可能和執行中的版本不同）；欄位用強型別切片（一筆型別錯誤會讓整個外掛或主題載入失敗）。
9. endpoint 除了同源，還禁止 `?`、`#`、`\` 與後台、登入、安裝、`/_jelly` 路徑。原因：`fetch` 帶瀏覽者的 Cookie，管理員逛前台時，宣告指向 `/api/admin/*` 的工具會把後台資料交給代理；`/\host` 在瀏覽器等同 `//host`。否決：只檢查 scheme 與 `//`。
10. 輸入屬性名直接當查詢參數名，`{name}` 佔位取代路徑。否決：每個工具另寫對應設定（宣告變複雜，外掛作者容易寫錯）。
11. `search_content` 不宣告 `page`：`/api/search` 固定回 20 筆且忽略 `page`，宣告了會讓代理以為能翻頁。否決：為此改 `/api/search`（任務要求沿用不改）。
12. `execute` 對非 2xx 回傳解析後的錯誤物件而不是拒絕：代理能看到 `not_found` 這種結構化原因；拒絕時規格只回報沒有細節的 `OperationError`。
13. 維護頁不注入：維護期間 `/api/*` 回 503，工具全部無法使用。否決：照樣注入（代理會拿到一串 503）。
14. `webmcp_enabled` 只接受 `"true"`／`"false"`，其他值 400，和 05 的維護模式選項驗證一致。否決：任何非 `"false"` 都當開啟（打錯字時站長以為關了其實沒關）。
15. `list_posts` 超過最後一頁回 200 與空陣列：代理翻到空頁就停。否決：照 `/posts` 回 404（代理要處理錯誤才知道結束）。
16. `url` 用站內路徑，`get_site_info.url` 用請求的 scheme 與 Host 組出絕對網址：main 沒有「網站網址」選項。否決：每個項目都回絕對網址（依賴 Host 標頭，且 08 之後才會有網址設定）。
17. 腳本上限 8 KiB、不含絕對網址，作為「腳本要小」與「不從外部載入」的可檢驗條件。
18. main 沒有外掛啟用／停用 API，載入於 runtime 即視為啟用（啟動時 `LoadAll` 載入 `data/plugins` 下全部外掛）；測試以 `Runtime.Load`／`Unload` 模擬啟用與停用。08 的啟用／停用端點合併後沿用同一個判斷。
19. `list_posts` 也標 `untrustedContentHint`：`excerpt` 和 `search_content` 的摘要一樣是作者寫的內容。否決：只標 `search_content` 與 `get_post`（同樣的內容換個工具就不提醒代理）。

## 相依與調和

- 工作單 08（草稿 PR #27，`claude/pr-f-system`）：
  - 設定登記表：`webmcp_enabled` 要以 `settings.Spec{Key: "webmcp_enabled", Kind: "bool", Default: "true", Tab: "crawlers"}` 登記到「搜尋引擎與 AI 爬蟲」分頁，寫入改走 `PUT /api/admin/settings`，「只接受 true／false」改由登記表的型別檢查負責；後台開關從 General 分頁移到 crawlers 分頁；設計板上的這一列從 `Settings.dc.html` 移到 `SettingsCrawlers.dc.html`（改 `design/gen/gen_crawlers.py` 後重新產生），並刪掉 `Settings.dc.html` 新增的「同一頁 · 網站分頁」區塊、板高改回。誰後合併誰調和，並同步更新本工作單與 08 的測試。
  - Request ID：08 的 `RequestID` 中介層（`req_` 加 20 個小寫 base32 字元，`X-Request-ID`）合併後，新端點的 `correlation_id` 直接取它，不再自己產生。
  - 保留路徑：08 若改寫 `/:slug` 的保留清單，`_jelly` 必須保留。
  - 外掛：08 的 manifest v2、`plugins.enabled` 與啟用／停用端點合併後，「已啟用」以 08 的狀態為準，`webmcp_tools` 欄位名不變；停用的外掛不得出現在工具清單。
- 工作單 07（草稿 PR #25，`claude/pr-e-shop`）：商店前台頁若不經 `publicRender`／`archiveRender` 渲染，後合併者要讓它們也經過同一個注入步驟。
- issue #6（agent API，另一個新 PR）：共用本工作單的錯誤格式；先合併者把格式寫成共用的輔助函式，後合併者改用它。
- 06、09 沒有重疊。

## 實作順序

每一槽先跑該槽列出的測試，全部通過才進下一槽。整合測試需要 `JELLY_TEST_POSTGRES=1`。

1. W1 主題輔助函式：`internal/theme/inject.go` 的 `InjectBeforeHeadEnd`、`PlainTextParagraphs`。測試 `internal/theme/inject_test.go`：`TestWebMCPInjectBeforeHeadEndPositions`、`TestWebMCPInjectBeforeHeadEndPrefersHeadOverBody`、`TestWebMCPPlainTextParagraphs`、`TestWebMCPPlainTextParagraphsEmpty`。
2. W2 公開 API：`internal/server/public_api.go` 三個端點與錯誤輔助函式。測試 `internal/server/webmcp_integration_test.go`：`TestWebMCPPublicPostsShape`、`TestWebMCPPublicPostsPaging`、`TestWebMCPPublicContentPostAndPage`、`TestWebMCPPublicContentHidesNonPublicLikeUnknown`、`TestWebMCPPublicSiteShape`。
3. W3 腳本、清單與注入：`web/jelly/webmcp.js`、`/_jelly/*` 路由、內建工具清單、`publicRender`／`archiveRender`／錯誤處理器的注入、`_jelly` 保留 slug。測試：`TestWebMCPInjectsScriptOnceOnDefaultThemePages`、`TestWebMCPInjectsScriptOnceOnSecondTheme`、`TestWebMCPDoesNotInjectOutsideThemePages`、`TestWebMCPMaintenancePageHasNoScript`、`TestWebMCPReservedSlug`、`TestWebMCPScriptIsSmallSameOriginJavaScript`、`TestWebMCPToolListHasFourReadOnlyBuiltins`。
4. W4 外掛與主題宣告：`Runtime.LoadedPlugins`、宣告驗證與合併、警告。測試：`TestWebMCPPluginToolsFollowLoadState`、`TestWebMCPSecondPluginDuplicateNameIsSkipped`、`TestWebMCPThemeToolsOnlyForActiveTheme`。
5. W5 開關與後台：選項讀取、關閉時的 404 與標頭、設定寫入驗證、`settings.html` 的開關。測試：`TestWebMCPDisabledSwitchesOffScriptAndSendsPolicy`、`TestWebMCPSettingAcceptsOnlyBooleans`、`TestWebMCPAdminSettingsPageHasSwitch`。
6. W6 瀏覽器驗收：`scripts/acceptance/webmcp.spec.mjs` 依「驗收步驟」全數通過；同時人工確認後台開關在 1280px 與 390px 和 `Settings.dc.html` 一致。
7. W7 文件與狀態：`docs/webmcp.md`；`STATUS.md` 記錄驗收結果。

`TestWebMCPDoesNotInjectOutsideThemePages`、`TestWebMCPMaintenancePageHasNoScript`、`TestWebMCPPlainTextParagraphsEmpty` 是防護測試，骨架階段就會通過，實作後必須維持通過。

### 測試另外釘下的約定

- 測試固定檔：`internal/server/testdata/webmcp/plugins/webmcp-fixture/`（`plugin.json` 含有效與無效的 `webmcp_tools`、空的 `index.js`）與 `internal/server/testdata/webmcp/themes/webmcp-alt/`（`<HEAD>` 大寫的 layout、`class="webmcp-alt-theme"` 標記、宣告 `theme_lookup`、重名的 `list_posts` 與 `fixture_ping`）。測試把它們複製到工作目錄的 `data/plugins/webmcp-fixture` 與 `web/themes/webmcp-alt`；第二個外掛 `webmcp-fixture-two` 由測試當場寫出。
- 換主題走 `POST /api/admin/themes/activate {"name"}`；外掛啟用與停用用 `Runtime.Load`／`Runtime.Unload`；開關走 `POST /api/admin/settings {"webmcp_enabled": "true"|"false"}`。
- 工具清單：最上層鍵 `tools`；項目鍵 `name`、`title`、`description`、`input_schema`、`endpoint`、`annotations`、`source`；`source` 值 `core`、`plugin:<id>`、`theme:<目錄名>`；內建順序與 endpoint 見上表；`search_content` 的 `required` 恰為 `["q"]`、`get_post` 恰為 `["slug"]`、`list_posts` 有 `page` 與 `per_page` 且沒有必填。
- 有效 64 字元名稱會列出、65 字元會略過；`fixture_events` 宣告 `readOnlyHint: false`、`consequentialHint: true`、`untrustedContentHint: true`，輸出必須是 true、省略、true。
- 警告：標準 `log` 輸出的一行，同時包含 `webmcp`（不分大小寫）、來源字串與工具名稱。
- 腳本：`web/jelly/webmcp.js` 由 `/_jelly/webmcp.js` 原樣送出，1 到 8192 位元組，`Content-Type` 以 `text/javascript` 開頭，不含 `http://`、`https://`。
- 注入標籤緊接在最後一個 `</head>`（不分大小寫）之前，每頁恰好一次。
- 公開 API 回應鍵完全等於契約所列；`items` 是陣列；`published_at` 可解析為 RFC 3339 並等於種子時間；手寫摘要原樣回傳；自動摘要不超過 201 個字元（含「…」）；`text` 在段落間有換行、不含 `<` `>`、不含 script 內容。
- 錯誤：`code` 為 `not_found`，`correlation_id` 符合 `^req_[0-9a-z]+$`、等於 `X-Request-ID`、每個請求不同；非公開與不存在的 slug 除 `X-Request-ID` 之外的標頭集合與本文都相同。
- `get_site_info` 在 Host 為 `webmcp.example` 的 httptest 請求下 `url` 為 `http://webmcp.example/`；預設值 `Jelly CMS`、空字串、`zh-TW`。
- 後台設定頁含 `<input … id="webmcpEnabled" … type="checkbox" …>` 與字串 `webmcp_enabled`。
- `Permissions-Policy` 以逗號分隔的其中一項（去空白後）等於 `tools=()`。
- 瀏覽器驗收的環境變數：`JELLY_BASE_URL`（預設 `http://127.0.0.1:8080`）、`JELLY_ADMIN_USER`、`JELLY_ADMIN_PASSWORD`、`PLAYWRIGHT_MODULE`（選用）。腳本以 `X-Jelly-CSRF: 1` 呼叫 `POST /auth/login`、`POST /api/admin/posts`、`POST /api/admin/settings`，自己建立並在結束時刪除測試文章，結束時把 `webmcp_enabled` 設回 `"true"`。
- 瀏覽器驗收釘下的行為：`execute` 對 `get_post` 的非公開 slug 解析出 `{code: "not_found"}`；`list_posts` 收到 `per_page: 500` 回 `per_page: 50`；觸發 `pagehide` 後每一個註冊用的 signal 都已中止；`registerTool` 回傳被拒絕的 Promise 時 console 沒有錯誤；沒有宿主時不請求 `/_jelly/webmcp-tools.json`。

### 實作者的判斷邊界

可以自己決定：

- 內建工具的 `title`、`description` 文字與 `input_schema` 裡額外的說明、`minimum`、`maximum` 等（不得改屬性名稱與必填）。
- 合併後清單要不要快取、怎麼失效（必須在外掛載入／卸載、換主題、切換開關後的下一個請求就反映），以及警告記一次還是每次合併都記（至少要記一次）。
- 自動摘要的實際長度（不超過 200 字元）、錯誤 `message` 的文字、`Cache-Control` 標頭、輔助函式放在哪個檔案。
- 腳本的寫法，只要符合大小、同源與行為約定。
- 後台開關的英文標籤與說明（設定頁目前是英文硬寫），以及載入與儲存時的狀態提示。

必須先回報：

- 改動任何本文件或測試釘下的名稱、路徑、鍵、狀態碼、順序。
- 想讓 `search_content` 支援分頁（需要改 `/api/search`）或改 `/api/search` 的回應。
- 想加寫入類工具、在後台頁面註冊工具、使用 `exposedTo`。
- 需要改 `web/themes/default` 的模板，或 `plugin.Runtime` 除 `LoadedPlugins` 外的公開介面。
- 腳本做不到 8 KiB 以內。

## issue 完成條件 → 測試或驗收步驟

| issue #7 完成條件 | 對應 |
|---|---|
| 支援的 Chrome 在首頁、列表、單篇、搜尋頁列出四個工具 | `TestWebMCPInjectsScriptOnceOnDefaultThemePages`、`TestWebMCPToolListHasFourReadOnlyBuiltins`；`webmcp.spec.mjs` 情境 1、2 |
| 預設主題與另一主題都有 | `TestWebMCPInjectsScriptOnceOnSecondTheme`（`webmcp-alt`）、`TestWebMCPInjectBeforeHeadEndPositions` |
| `search_content` 與 `get_post` 結果與畫面一致，草稿或私密 slug 回找不到 | `TestWebMCPPublicContentPostAndPage`、`TestWebMCPPublicContentHidesNonPublicLikeUnknown`、既有 `TestPublicThemeEdgeSearchAPIShapeScopeAndSafeSummary`；`webmcp.spec.mjs` 情境 1 |
| 不支援的瀏覽器畫面正常、console 無新錯誤 | `webmcp.spec.mjs` 情境 3、4 |
| 後台關閉後不註冊、帶標頭，重開恢復 | `TestWebMCPDisabledSwitchesOffScriptAndSendsPolicy`、`TestWebMCPSettingAcceptsOnlyBooleans`、`TestWebMCPAdminSettingsPageHasSwitch`；`webmcp.spec.mjs` 情境 5 |
| 範例外掛可註冊額外工具，停用與 WebMCP 關閉時都不出現 | `TestWebMCPPluginToolsFollowLoadState`、`TestWebMCPSecondPluginDuplicateNameIsSkipped`、`TestWebMCPThemeToolsOnlyForActiveTheme`、`TestWebMCPDisabledSwitchesOffScriptAndSendsPolicy`（關閉時清單 404） |
| 伺服器端資料存取有整合測試 | W2 的五個測試 |
| 前台註冊有瀏覽器自動化測試 | `scripts/acceptance/webmcp.spec.mjs` |
| 說明文件 | W7 `docs/webmcp.md`，主代理人工審閱 |
| 風險：腳本要小、內文可能含惡意指令 | `TestWebMCPScriptIsSmallSameOriginJavaScript`；`TestWebMCPToolListHasFourReadOnlyBuiltins` 的 `untrustedContentHint`；`TestWebMCPPublicContentPostAndPage` 的純文字檢查 |

## 驗收步驟（瀏覽器，主代理執行，不進 CI）

伺服器固定聽 `:8080`，並以工作目錄的相對路徑讀 `web/` 與寫 `data/`，所以在一個全新的目錄執行：

```sh
pg_isready -h 127.0.0.1 -p 55432
PGPASSWORD=jelly-local-acceptance psql -h 127.0.0.1 -p 55432 -U jelly_test -d jelly_acceptance \
  -c 'CREATE DATABASE "jelly_webmcp_acceptance"'
go build -o /tmp/jelly-webmcp ./cmd/server
work=$(mktemp -d) && cp -r web "$work"/ && cd "$work" && /tmp/jelly-webmcp &
curl -sS -X POST http://127.0.0.1:8080/api/install -H 'Content-Type: application/json' -H 'X-Jelly-CSRF: 1' \
  -d '{"db_host":"127.0.0.1","db_port":"55432","db_user":"jelly_test","db_pass":"jelly-local-acceptance",
       "db_name":"jelly_webmcp_acceptance","db_timezone":"UTC","site_title":"WebMCP 驗收",
       "admin_user":"admin","admin_email":"admin@example.test","admin_pass":"webmcp-acceptance-pass"}'
cd /path/to/repo
JELLY_BASE_URL=http://127.0.0.1:8080 JELLY_ADMIN_USER=admin JELLY_ADMIN_PASSWORD=webmcp-acceptance-pass \
  node scripts/acceptance/webmcp.spec.mjs
```

腳本最後印出「N/N checks passed」並以 0 結束才算通過。要驗外掛工具在瀏覽器裡註冊，把 `internal/server/testdata/webmcp/plugins/webmcp-fixture` 複製到工作目錄的 `data/plugins/` 後重啟伺服器再跑一次：情境 1 會要求清單裡的每一個工具（含外掛的）都被註冊。結束後停止伺服器、刪除工作目錄與 `jelly_webmcp_acceptance` 資料庫。
