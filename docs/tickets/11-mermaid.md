# 11 — 作者可以在文章與頁面寫 Mermaid 圖表，訪客與編輯預覽看到以自帶 Pele 畫出的圖

Epic：編輯與發布（內容語法）
User Story：作者在文章或頁面寫 ```` ```mermaid ```` 程式碼區塊，儲存後訪客在公開頁看到圖表，配色與字型跟著網站主題，手機上不會撐壞版面；語法寫錯時看得到原文與錯誤提示，其他圖照常顯示；原文永遠保存在文章裡，隨時可以再編輯。站長不需要裝外掛、不需要加腳本，頁面也不從任何外部網址載入資源。
來源：GitHub issue HazelnutParadise/jelly-cms#22（原生支援 Mermaid，使用 Pele 渲染）。
Blocked by：無，可以獨立於 main 合併。與草稿 PR #26（工作單 09 編輯器，分支 `claude/pr-g-editor`）有重疊，調和方式見下方「與工作單 09 的相依」；誰後合併誰負責調和。
Status：draft（本工作單只交付契約、設計板、骨架與失敗的驗收測試；實作由另一批代理依本文件完成）
設計稿：`design/project/Mermaid.dc.html`（新板，前台第一排最右；圖表是 Pele 1.9.0 的實際輸出）。

## 上游事實（查證日期 2026-10-24）

來源：https://github.com/obsidianmd/pele 、https://pele.run/api 、https://pele.run/security 、https://pele.run/theming 、npm registry，另在本機 Chromium 以 `dist/pele.global.js` 實測。

- 套件：npm 名稱 `pele`，MIT 授權（`Copyright (c) 2026 kepano`）。上游 `tests/compat` 與文法檔來自 Mermaid，同為 MIT。
- 版本：最新 `1.9.0`（npm 發布時間 2026-10-22T22:45Z）。git tag `1.9.0` 指向 `e7f092a4de147cc32210913c20e36eaa5fcd0ef1`（輕量 tag；查證時也是預設分支 HEAD）。npm tarball `https://registry.npmjs.org/pele/-/pele-1.9.0.tgz`，integrity `sha512-KUmBt7P5JduVKQaP6FKfXiRgH3LBVIKsmdgPJZU7jD3ye6F+Z8fbms+OjivE7rWY09Qx+5X65VKe/9ywKFJ9Sw==`。npm 版本資訊沒有 `gitHead`，來源 commit 只能從 tag 對應。GitHub Releases 頁是空的，沒有發版說明。
- 瀏覽器單檔：`dist/pele.global.js`（套件 export `pele/global`），706,153 位元組，SHA-256 `323c16de7844d4b609743f70449ebce041a531e4f48f23b1e9e7ec9d3b917287`；IIFE，定義全域 `pele`，開頭是 `/*! Pele 1.9.0 | MIT License | https://github.com/obsidianmd/pele */`。全域物件有 `PeleError`、`detectType`、`enableZoom`、`forgetTextWidths`、`mount`、`parse`、`register`、`registered`、`render`、`supports`；所有圖種都已打包在內，不需要再載入其他檔。可以在瀏覽器以單一自帶檔案使用。
- API：
  - `render(source: string | DiagramModel, options?: RenderOptions): RenderResult`，`RenderResult` 有 `svg`、`width`、`height`、`type`、`mermaidType`、`links[]`（`{id, href, internal}`）。失敗時丟 `PeleError`，`code` 為 `unsupported-diagram`、`syntax`、`semantic`、`limit`，另有 `line`、`column`、`snippet`；訊息可能包含原文。
  - `mount(element, text, options?: MountOptions): Mounted`，畫進元素、讀 CSS 字型、跟容器寬度調整、網頁字型載入後重畫。畫不出來時不丟錯，在容器裡顯示 `<div class="pele pele-error">`（有舊圖時蓋在變暗的舊圖上），錯誤從 `Mounted.error` 或 `onError` 取得。`MountOptions` 另有 `onRender`、`zoom`（預設 `'auto'`）、`sanitize: (svg: string) => Node`（插入前交給應用的淨化器；預設以 HTML 插入）。`Mounted` 有 `result`、`source`、`error`、`zoom`、`update(text, options?)`、`refresh()`、`destroy()`。
  - `renderAsync(text, options?)`、`mountAsync`、`load(type)` 只在 ESM 的 `pele/lazy`，全域單檔沒有。
- 預設上限與選項：`limit` 原文 50,000 字元（超過丟 `limit`，訊息為 `Diagram source is longer than the limit of 50000 characters.`）、`outputLimit` SVG 4,000,000 字元、`maxEdges` 5,000（flowchart、agentflow）、`linkSchemes` `http https mailto tel`、`imageSchemes` `http https`、`links`／`images` 預設開、`linkRel` 預設 `noopener`、`responsive` 預設開（SVG 帶 `style="max-width:100%;height:auto;…"`）、`fontFamily` `sans-serif`、`fontSize` 16、`autoDirection` 開且 `directionBreakpoint` 640、`mount()` 會替每張圖給不同的 `idPrefix`（`render()` 要自己給）。沒有時間上限。
- 安全文件：Pele 不把原文變成腳本、事件處理器或 HTML 標籤，忽略 `click` 裡的 JavaScript 回呼；`javascript:`、`data:`、`vbscript:` 即使加進允許清單也一律封鎖；相對網址允許，`//host` 網路路徑封鎖；外部連結預設 `rel="noopener"`；圖片顯示時會發出網路請求，可用 `images: false` 關掉；`icons` 與 `math` 回傳的標記不經過濾。建議：錯誤訊息用 `textContent` 顯示、`mount()` 傳 `sanitize` 再過一次自己的淨化器、不讓圖表作者提供 `options` 或 `options.config`、要時間上限就放在可終止的 worker 裡。樣式過濾的具體規則（`classDef`、`style`、`%%{init}%%`）文件沒寫：**未確認**。CSP 建議文件沒寫：**未確認**。全域單檔能否在 worker 內執行：**未確認**。
- 主題 CSS 變數（設在 SVG 根 `.pele` 或任何外層元素；切換深色不需重畫）：`--pele-bg`、`--pele-fg`、`--pele-muted`、`--pele-line`、`--pele-surface`、`--pele-surface-alt`、`--pele-border`、`--pele-accent`、`--pele-critical`、`--pele-error`、`--pele-font`、`--pele-font-mono`、`--pele-title-weight`、`--pele-heading-weight`、`--pele-bold-weight`、`--pele-radius`、`--pele-series-1` 到 `--pele-series-8`。輸出類名：根 `svg.pele` 加 `pele-<type>`（例如 `pele-flowchart`）、`pele-title`、`pele-node`（帶 `data-id`）、`pele-edge`、`pele-edge-label`、`pele-cluster`、`pele-cluster-label`、`pele-label`（節點文字 `<text>`）、`pele-marker`、`pele-icon`、`pele-math`。
- 支援的語法範圍：以 Mermaid 12.1.0 的語法為目標，解析器以 Mermaid 的解析器規格測試並做差分模糊測試。圖種（`DiagramType`）：flowchart、sequence、class、state、er、pie、gantt、gitGraph、mindmap、timeline、journey、xychart、quadrantChart、requirement、c4、sankey、block、packet、kanban、architecture、radar、treemap、info、agentflow、usecase、railroad、treeView、venn、cynefin、wardley、swimlane、ishikawa、eventmodeling。排版與樣式是 Pele 自己的，不是 Mermaid 的；驗收以語法正確、可讀、符合網站設計為準。
- 本機實測（Chromium，1.9.0）：中文標籤的流程圖、循序圖、ER 圖都正確畫出；`click A "javascript:…"` 與 `click B href "javascript:…"` 不產生連結；標籤裡的 `<script>`、`<img onerror>` 只當文字，輸出沒有 `<script>`、`on*` 屬性與 `foreignObject`；`click X "/post/x"` 與 `https://` 連結保留並帶 `rel="noopener"`；語法錯誤丟 `PeleError`（`code: "syntax"`，訊息多行，第二行起是原文片段與指標）；未知圖種丟 `code: "unsupported-diagram"`（`No diagram type detected for the given text.`）。

## 流程與驗收

- [ ] 作者在文章或頁面寫 ```` ```mermaid ```` 區塊並儲存，訪客打開公開頁看到圖表；儲存的 `content` 只有 `<pre><code class="language-mermaid">` 與逸出後的原文，從不儲存 SVG。
- [ ] 作者重新打開編輯器，原文（`meta.markdown`）與儲存前完全相同。
- [ ] 訪客在同一頁看到多張圖（流程圖、循序圖、ER 圖，含中文標籤），每張圖畫在原本程式碼區塊的位置，原本的 `<pre>` 隱藏但留在 DOM。
- [ ] 訪客打開沒有 Mermaid 的頁面時，頁面完全不載入 Pele 與整合腳本。
- [ ] 圖表的文字、線條、節點底色跟著主題變數走，亮色與深色模式都一致；中文標籤用主題字型（Noto Sans TC）。
- [ ] 訪客在 390px 手機上整頁沒有橫向溢出；縮不下的寬圖只在圖表框內橫向捲動。
- [ ] 語法錯誤、不支援的圖種、原文過長、Pele 沒載入或沒輸出時，訪客看到原文與「圖表無法顯示：…」提示（英文介面為「Diagram could not be displayed: …」），同頁其他圖照常顯示。
- [ ] 同一頁重複呼叫整合腳本不會重畫或重複產生圖；原文變了才重畫，舊圖移除；程式碼區塊被移除時對應的圖一起清掉。
- [ ] 惡意圖表（`click` 指向 `javascript:`、標籤內嵌 `<script>` 或 `<img onerror>`、`tel:`、`//host` 連結）不會執行任何腳本，輸出沒有 `javascript:`、`on*` 屬性、`script`、`foreignObject`；外部連結帶 `rel="noopener noreferrer"`。
- [ ] 封鎖所有外部網域後，圖表仍完整畫出，頁面沒有任何外部請求。
- [ ] Pele 以建置後的單檔自帶在 `web/admin/assets/pele/`，附 `LICENSE` 與 `VERSION`（版本、來源 commit、建置日期）；`scripts/vendor/pele.sh` 可重現；`docs/release-checklist.md` 列出它。
- [ ] 主題作者照「主題整合」一節在自己的 `layout.html` 呼叫 `{{contentAssets .}}` 就能支援圖表；`docs/mermaid.md` 寫明作者用法、支援的圖種、上限與主題整合方式。
- [ ] 編輯預覽顯示圖表：main 沒有編輯預覽，這一條在工作單 09 合併後驗收（見「與工作單 09 的相依」）。

## 錯誤與邊界

原文以 `code.textContent`（JavaScript 字串長度，UTF-16 單位）計算，超過 50,000 不交給 Pele，直接顯示提示。一張圖失敗不影響同頁其他圖；整合腳本本身不得丟出未處理的例外。原文為空白的區塊視為錯誤（Pele 的 `unsupported-diagram`）。沒有時間上限（Pele 不提供，worker 方案未確認，見「實作者的判斷邊界」）。深色模式切換只靠 CSS 變數，不重畫。

## 契約（骨架與測試共同依據）

### 儲存與輸出（不改資料表）

- 文章與頁面沿用既有欄位：`content` 是編輯器轉出的 HTML，原文在 `meta.markdown`。marked 15 對 ```` ```mermaid ```` 的輸出是 `<pre><code class="language-mermaid">…逸出後原文…\n</code></pre>`（已驗證）；伺服器不改寫這段，前台以 `safeHTML` 輸出。`post.html` 經 `headingIDs` 重新序列化時，`&quot;` 會變成 `&#34;`，解碼後的文字不變；測試比對解碼後的文字。
- 不新增資料遷移、不新增 API。

### 主題整合：`contentAssets`（`internal/theme/content_assets.go`）

- `func ContentAssets(contents ...string) template.HTML`：任一內容以 HTML 解析後含有 class 清單包含 `language-mermaid` 的 `<code>` 元素時，依序回傳下面三個標籤（各一次，不論幾張圖）；否則回傳空字串。純文字或行內程式碼裡出現「language-mermaid」字樣、`language-mermaidx`、非 `<code>` 元素上的同名 class 都不算。

  ```html
  <script src="/admin/assets/pele/pele.global.js" defer></script>
  <script src="/admin/assets/jelly-mermaid.js" defer></script>
  <script>document.addEventListener("DOMContentLoaded",function(){window.JellyMermaid&&window.JellyMermaid.render(document)});</script>
  ```

  第三個標籤的確切寫法可以不同，但必須是行內腳本、在 `DOMContentLoaded` 呼叫 `JellyMermaid.render(document)`，且位在 `jelly-mermaid.js` 之後。所有網址都是站內絕對路徑 `/admin/assets/…`。
- 模板函式 `contentAssets`（由 `(*Manager).templateFuncs` 提供給所有主題，骨架已註冊）：主題在 `layout.html` 的 `<head>` 寫 `{{contentAssets .}}`。它從模板資料（`map[string]interface{}`）讀 `Post`、`Page`（`core.Post` 或 `*core.Post` 的 `Content`）與 `Content`（`string` 或 `template.HTML`），其他鍵（`Posts`、`Featured` 等列表）一律不看；`nil`、非 map、型別不符都回空字串，不得讓模板執行失敗。
- 預設主題 `web/themes/default/layout.html`：在 `<head>` 呼叫 `{{contentAssets .}}`，並在主題 `<style>` 定義 `.jc-mermaid`、`.jc-mermaid-error` 與下表的 `--pele-*` 對應（亮暗共用同一組對應，靠主題變數本身在深色模式改值）：

  | Pele 變數 | 預設主題 |
  |---|---|
  | `--pele-bg` | `var(--surface-low)`（圖表框底色） |
  | `--pele-fg` | `var(--ink)` |
  | `--pele-muted` | `var(--muted)` |
  | `--pele-line` | `color-mix(in srgb, var(--ink) 72%, var(--surface))` |
  | `--pele-surface` | `var(--surface)` |
  | `--pele-surface-alt` | `var(--surface-mid)` |
  | `--pele-border` | `color-mix(in srgb, var(--ink) 30%, var(--surface))` |
  | `--pele-accent` | `var(--secondary)` |
  | `--pele-critical`、`--pele-error` | `var(--error)` |
  | `--pele-font` | `var(--font)` |
  | `--pele-radius` | `3px` |
  | `--pele-series-1`…`5` | `var(--primary)`、`var(--secondary)`、`var(--accent)`、`var(--warning)`、`var(--ink)` |

  圖表框：`margin: 1.5em 0`、`padding: 1.5rem`（手機 `1rem`）、`background: var(--surface-low)`、`border-radius: var(--r-md)`；錯誤提示：小字、`color-mix(in srgb, var(--error) 9%, var(--surface))` 底、左側 3px `var(--error)` 邊、`var(--r-sm)` 圓角。外觀以 `Mermaid.dc.html` 為準。
- 其他主題：只要在 `<head>` 呼叫 `{{contentAssets .}}` 就能畫圖；沒有定義 `--pele-*` 時使用 Pele 預設配色。結構樣式（寬度、捲動）由整合腳本以行內樣式保證，不依賴主題 CSS。

### 整合腳本：`web/admin/assets/jelly-mermaid.js`

- 不依賴任何模組系統；載入後定義 `window.JellyMermaid = { render(root) }`，不自動執行（自動執行由 `contentAssets` 的行內腳本負責，編輯預覽另行呼叫）。
- `JellyMermaid.render(root = document)`：`root` 可以是 `Document` 或 `Element`。回傳 `Promise<void>`，在 `root` 內每個區塊都處理完（畫好或顯示錯誤）後 resolve，永不 reject。開始量測前等待 `document.fonts.ready`（瀏覽器支援時）。
- 每個 `pre > code.language-mermaid` 是一個區塊，原文為 `code.textContent`。處理後 DOM 必須是：
  - 成功：`<pre hidden>…</pre>` 緊接 `<figure class="jc-mermaid" style="max-width:100%;overflow-x:auto">` ＋ Pele 的 `<svg class="pele …">`（figure 是 `pre.nextElementSibling`）。SVG 隨框寬縮放，但以行內 `min-width: <Math.round(result.width * 0.75)>px` 限制最小為原寬 75%；更窄時在 figure 內橫向捲動。
  - 失敗：`<pre>` 不隱藏，緊接 `<p class="jc-mermaid-error" role="status">`，文字為「前綴＋原因」，以 `textContent` 寫入（可以另放一個裝飾用的 `<svg>` 圖示，不得有其他元素）。
- 重複呼叫：同一個 `<pre>` 原文沒變就保留原本的 figure 與 SVG 元素（不重畫、不換節點）；原文變了先移除舊 figure 或舊提示再處理；失敗變成功時移除提示並隱藏 `<pre>`，成功變失敗時移除 figure 並顯示 `<pre>`。`root` 內沒有對應 `<pre>` 的 `figure.jc-mermaid` 與 `p.jc-mermaid-error`（原本的區塊被移除或內容被替換）要清掉；若用 `pele.mount()`，清掉時呼叫 `Mounted.destroy()`。同一區塊之後永遠只有一個 figure 或一個提示。
- 呼叫 Pele 的選項由腳本固定，不讀原文或頁面提供的設定：`idPrefix` 每張圖唯一（只含英數、`-`、`_`）、`fontFamily` 取 figure 的計算字型、`maxWidth` 取 figure 內容寬、`linkSchemes: ['http','https','mailto']`、`images: false`、`linkRel: 'noopener noreferrer'`、`limit: 50000`、`responsive: true`。用 `mount()` 時另設 `zoom: false` 並以 `sanitize` 掛上下面的淨化器。
- 插入前的淨化（不論 Pele 是否已過濾）：以 `DOMParser`（`image/svg+xml`）解析，解析失敗當成渲染失敗；移除 `script`、`foreignObject`（以及 `iframe`、`object`、`embed`）元素；移除所有名稱以 `on` 開頭的屬性；`href` 與 `xlink:href` 只留 `http:`、`https:`、`mailto:` 或沒有 scheme 且不以 `//` 開頭的站內相對路徑，其他值連同屬性移除；指向其他來源的 `http(s)` 連結設定 `rel="noopener noreferrer"`。
- 原因文字（`<原因>`）：
  - 原文超過 50,000：繁中「圖表原文超過 50000 字元」、英文「Diagram source exceeds 50000 characters」，不呼叫 Pele。
  - `window.pele` 不存在：「繪圖元件沒有載入」／「The diagram renderer did not load」。
  - Pele 丟錯：錯誤 `message` 的第一行（去頭尾空白）；不是 `Error` 時用 `String(error)` 的第一行。
  - Pele 回傳空的 `svg`：「沒有產生圖表」／「No diagram was produced」。
- 語言：取 `<pre>` 最近的帶 `lang` 屬性祖先（沒有就 `document.documentElement.lang`），以 `zh` 開頭用繁中，否則英文。前綴：繁中「圖表無法顯示：」、英文「Diagram could not be displayed: 」。

### 自帶套件

- `web/admin/assets/pele/pele.global.js`：npm 套件的 `dist/pele.global.js` 原樣複製，不改寫。
- `web/admin/assets/pele/LICENSE`：套件的 `LICENSE` 原樣。
- `web/admin/assets/pele/VERSION`：

  ```text
  Pele
  Version: 1.9.0
  Source: https://github.com/obsidianmd/pele
  Commit: e7f092a4de147cc32210913c20e36eaa5fcd0ef1
  Package: https://registry.npmjs.org/pele/-/pele-1.9.0.tgz
  Package integrity: sha512-KUmBt7P5JduVKQaP6FKfXiRgH3LBVIKsmdgPJZU7jD3ye6F+Z8fbms+OjivE7rWY09Qx+5X65VKe/9ywKFJ9Sw==
  File: dist/pele.global.js
  File SHA-256: 323c16de7844d4b609743f70449ebce041a531e4f48f23b1e9e7ec9d3b917287
  Build date (UTC): YYYY-MM-DD
  ```

- `scripts/vendor/pele.sh`（可執行，`bash`，`set -euo pipefail`）：在腳本開頭釘 `pele_version`、`pele_commit`、`pele_integrity`；以 `npm pack pele@<version>` 取 tarball，核對 npm integrity 與 `git ls-remote --tags https://github.com/obsidianmd/pele refs/tags/<version>` 的 commit，任一不符就以非零結束且不動 `web/`；解出 `package/dist/pele.global.js` 與 `package/LICENSE`，寫 `VERSION`（建置日期取當天 UTC）。升版只改腳本開頭的三個值再重跑。
- `docs/release-checklist.md` 第 1 節加一張自帶套件清單（套件、目錄、重建腳本），至少列出既有字型與 Pele：`Pele | web/admin/assets/pele/ | scripts/vendor/pele.sh`。

### 安全規則

- 資料作者只能提供圖表原文，不能提供 Pele 選項或 `config`；腳本固定的選項見上。
- 錯誤訊息一律 `textContent`；不使用 `innerHTML` 插入任何來自原文或 Pele 錯誤的字串。SVG 只在淨化後以 DOM 節點插入。
- `images: false`：圖表內的圖片會向外部網址發請求，與「不從外部網址載入資源」衝突。
- 不新增 CSP（目前專案沒有 CSP）；行內初始化腳本沿用主題已有的行內腳本作法。

### 測試另外釘下的約定

- 腳本標籤字面值：`<script src="/admin/assets/pele/pele.global.js" defer></script>`、`<script src="/admin/assets/jelly-mermaid.js" defer></script>`；兩個檔名在整頁各只出現一次；行內腳本含 `DOMContentLoaded` 與 `JellyMermaid.render(document)`。
- `contentAssets` 的資料鍵：列表頁把 `Page` 當整數頁碼傳入，這時忽略；`*core.Post` 為 nil、`Post` 是字串、非 map 資料（例如 struct）都回空字串。
- 預設主題的 `<head>` 含 `.jc-mermaid`、`.jc-mermaid-error`、`--pele-fg`、`--pele-bg`、`--pele-font`。
- `GET /admin/assets/pele/pele.global.js` 與 `/admin/assets/jelly-mermaid.js` 回 200、`Content-Type` 含 `javascript`；前者含 `/*! Pele `，後者含 `JellyMermaid`。
- `pele.global.js` 至少 100,000 位元組，開頭符合 `/*! Pele X.Y.Z | MIT License`；`LICENSE` 含 `MIT License` 與 `kepano`；`VERSION` 有 `Version: X.Y.Z`、`Commit: <40 位小寫十六進位>`、`Build date (UTC): YYYY-MM-DD`、`Source: https://github.com/obsidianmd/pele` 各一行，版本與單檔開頭一致；`scripts/vendor/pele.sh` 可執行、含 `VERSION` 的版本與 commit 字串、含 `npm`；`docs/release-checklist.md` 含 `Pele`、`scripts/vendor/pele.sh`、`web/admin/assets/pele/`。
- 瀏覽器：區塊完成的判斷是 `pre.nextElementSibling` 為 `figure.jc-mermaid` 或 `p.jc-mermaid-error`；成功時 `pre.hidden === true` 且 figure 內有 `svg.pele`；figure 的計算 `overflow-x` 為 `auto`；節點文字取 `svg text.pele-label`，亮暗模式下其計算 `fill` 等於 `body` 的計算 `color`，字型含 `Noto Sans TC`；英文提示在把 `<html lang>` 改成 `en` 後新增的區塊上檢查。
- 瀏覽器測試的伺服器帳號與網址以環境變數 `JELLY_BASE_URL`、`JELLY_ADMIN_USER`、`JELLY_ADMIN_PASSWORD` 提供；登入用 `POST /auth/login`（`Accept: application/json`），夾具經 `POST /api/admin/posts` 建立、結束時 `DELETE` 移到回收桶。

### 實作者的判斷邊界

可以自己決定：用 `pele.render()` 自己插入，或用 `pele.mount()` 加 `sanitize`；Promise 與重入的實作（同一 `root` 並行呼叫時以最後一次為準即可）；`idPrefix` 的產生方式；HTML 解析用 `golang.org/x/net/html`（已在 go.mod）或 tokenizer；`.jc-mermaid` 細部間距只要和板一致；`docs/mermaid.md` 的章節安排；`pele.sh` 是否支援以環境變數改用本機 tarball 重建。

必須回報、不得自行改變：腳本標籤字面值與路徑、`contentAssets` 讀取的資料鍵、DOM 結構與 class 名稱、錯誤文案、50,000 上限、連結允許清單、`images: false`、把 SVG 存進資料庫或改成伺服器端產生、改用 Pele 以外的套件或從 CDN 載入、修改既有的板或測試。上游升版若改了全域名稱、`render` 簽名或錯誤型別，先回報再升。若要加時間上限（worker），先確認全域單檔能在 worker 執行並回報方案。

## 與工作單 09 的相依（誰後合併誰調和）

工作單 09（草稿 PR #26）把渲染改成伺服器端 `internal/markup`、加分割預覽、移除 marked、淨化器禁止 `svg`。本工作單不改 09 的檔案；兩者後合併的一方負責：

1. `internal/markup` 對 ```` ```mermaid ```` 輸出同樣的 `<pre><code class="language-mermaid">逸出後原文</code></pre>`，不走 chroma 高亮、不加 `chroma` class 或 token `<span>`；兩級淨化政策都保留這個 `class`。
2. `markup.Features(html)` 回報 `mermaid`；`ContentAssets` 的偵測改成使用 `Features`，09 的「頁面用到才載入」（`jelly-motion.js`、`jelly-components.js` 等）與 `contentAssets` 合併成同一個模板函式入口，名稱維持 `contentAssets`，Mermaid 的三個標籤不變。
3. 09 的分割預覽每次以 `POST /api/admin/render` 更新預覽後，對預覽根元素呼叫 `JellyMermaid.render(previewRoot)`；編輯器頁面在 `features` 含 `mermaid` 時載入 Pele 與 `jelly-mermaid.js`（同樣是 `/admin/assets/…` 的自帶檔）。這時驗收「編輯預覽顯示圖表、反覆編輯不重複產生」。
4. 09 的匯出：`jmd`／`md` 原樣保留 ```` ```mermaid ```` 圍欄；`html` 獨立文件內嵌 `pele.global.js` 與 `jelly-mermaid.js`（和 KaTeX CSS 一樣內嵌），離線開啟也看得到圖；`pdf` 走列印樣式，列印前圖已在頁面上畫好。
5. 09 的 `Post.dc.html`／`EditorFull.dc.html` 若要畫出預覽中的圖表，引用 `Mermaid.dc.html` 的樣式，不另造一套。

## 與工作單 12（issue #18 零 CDN，另一個新 PR）的調和

`Mermaid.dc.html` 沿用目前所有板的 helmet，從 Google Fonts 載字型。工作單 12 會把所有板改成自帶的 `design/project/fonts.css`；後合併的一方把這張板（`design/tools/mermaid-board/board.css` 或 `build.cjs` 裡的 helmet）一併改掉，並讓 12 的外部資源檢查涵蓋它。

## 決策紀錄

- 瀏覽器端渲染，原文是唯一真相：`content` 只存 `<pre><code class="language-mermaid">` 原文，頁面載入時由瀏覽器畫圖。否決：伺服器端產生 SVG 存進內容——09 的淨化器禁止 `svg`，存下來的 SVG 也無法跟隨網站配色與深色模式，換主題就要全部重算。
- 一支共用整合腳本 `jelly-mermaid.js` 提供 `JellyMermaid.render(root)`，前台與編輯預覽共用。否決：各頁各寫一段初始化——重複且無法保證「不重複產生、清理舊圖」的一致行為；否決：直接用 `pele.mount()` 的預設錯誤畫面——它把錯誤畫在容器裡並以 HTML 插入 SVG，不符合「保留原文並提示」與自訂淨化的要求。
- 主題共用的載入方式是模板函式 `contentAssets`，只在內容含 Mermaid 的頁面輸出。否決：每頁都載入 Pele——706 KB 的腳本，大部分頁面沒有圖表；否決：要求每個主題自己判斷——主題作者會漏掉，也無法和 09 的功能偵測合併。
- 列表頁（首頁、文章總覽、搜尋、彙整）只看 `Post`、`Page`、`Content` 三個鍵，不看 `Posts`、`Featured`：列表只顯示摘要，不會出現程式碼區塊。
- 錯誤時保留可讀原文並在後面加 `role="status"` 提示，同頁其他圖照常。否決：隱藏整個區塊——讀者看不到內容；否決：只用 Pele 內建錯誤框——會被淨化流程與主題樣式繞過。
- 錯誤文案放在 `jelly-mermaid.js` 的繁中／英文對照，依最近的 `lang` 選擇。否決：放進 `web/locales/*.json` 再由伺服器傳入——`contentAssets` 是不帶請求語言的模板函式，預設主題本身也以 `lang="zh-TW"` 固定繁中，伺服器端翻譯在這裡拿不到語言。
- 除了 Pele 的過濾，插入前再用 DOMParser 淨化一次，`images: false`，連結只留 http(s)、mailto 與站內相對路徑。否決：只信任 Pele——上游文件自己建議再過一次應用的淨化器，且 `tel:` 與外部圖片不在本站允許範圍。
- 原文超過 50,000 字元不渲染，與 Pele 預設 `limit` 相同，在呼叫前先擋，提示文字由我們控制。
- 外觀：`.jc-mermaid` 以行內樣式保證 `max-width:100%`、`overflow-x:auto`，SVG 最小為原寬 75%，更窄時框內捲動。否決：讓 SVG 無限縮小——手機上文字小到不可讀；否決：一律原寬加捲動——小圖在手機上也要捲，且違反「跟隨容器寬度」。75% 是本工作單的選擇（Pele 1.9.0 在 326px 寬下會先換行壓縮，只有 10 個參與者以上的循序圖這類真正寬的圖才會觸發捲動）。
- 配色用 `--pele-*` 對應主題變數，亮暗共用一組對應。否決：在整合腳本裡寫死顏色——換主題色就失準。
- 自帶 npm 發布的建置檔（`dist/pele.global.js`）原樣，不自己從原始碼建置。否決：從 git 原始碼建置——需要上游的建置工具鏈，結果不一定和 npm 發布檔相同；以 tag commit 與 npm integrity 雙重核對來源。
- 設計板 `Mermaid.dc.html` 由 `design/tools/mermaid-board/` 的兩支腳本產生：`render.cjs` 用自帶的 Pele 在 Chromium 畫出圖表（選項與本工作單相同，套上上表的配色對應），`build.cjs` 組成板。升級 Pele 後從 repo 根目錄重跑兩支腳本。否決：改既有的 `Post.dc.html`——本工作單不改既有板；否決：手畫示意圖——和實際排版會有落差；否決：寫成 `design/gen/` 的 Python 產生器——Pele 只能在瀏覽器執行。
- 09 的 HTML 獨立匯出內嵌 Pele 與整合腳本。否決：匯出時把圖表留成程式碼區塊——讀者離線打開匯出檔看不到圖，和網站上看到的不一致。
- 不另加渲染時間上限，靠 Pele 的原文 50,000 字元、輸出 4,000,000 字元、5,000 條邊上限。否決：放進 worker 加逾時——全域單檔能否在 worker 執行未確認，第一版成本高於效益；若驗收時發現單張圖能讓頁面卡住超過 1 秒，回報主代理另開工作單。
- 使用文件放 `docs/mermaid.md`（M4 撰寫）：支援的語法範圍、主題如何呼叫 `contentAssets`、錯誤提示的意思、如何升級 Pele。

## 實作順序

每段先由測試代理照本文件建立失敗案例（已隨本工作單交付），實作代理依契約實作，主代理審查後以真實伺服器與 Playwright 驗收。

1. M1 主題整合：`ContentAssets`、`contentAssetsForTemplate`；預設主題 `layout.html` 在 `<head>` 呼叫 `{{contentAssets .}}` 並加上 `.jc-mermaid`、`.jc-mermaid-error` 與 `--pele-*` 對應。要通過：`internal/theme/content_assets_test.go` 全部；`internal/server/mermaid_integration_test.go` 的 `TestMermaidPostPageLoadsDiagramAssets`、`TestMermaidStaticPageLoadsDiagramAssets`、`TestMermaidPagesWithoutDiagramsLoadNoAssets`、`TestMermaidSourceSurvivesAdminSaveAndReopen`。
2. M2 Pele 自帶：`scripts/vendor/pele.sh`、執行後產生的 `web/admin/assets/pele/` 三個檔、`docs/release-checklist.md` 的套件清單。要通過：`TestMermaidVendoredPelePackage`（`jelly-mermaid.js` 的存在檢查在 M3 才會通過）。
3. M3 整合腳本：`web/admin/assets/jelly-mermaid.js`。要通過：`TestMermaidVendoredFilesAreServed`、`TestMermaidVendoredPelePackage`，以及 `scripts/acceptance/mermaid.spec.mjs` 全部檢查。
4. M4 文件與驗收：`docs/mermaid.md`；主代理以真實伺服器跑 `scripts/acceptance/mermaid.spec.mjs`（1280／390、亮／暗、封鎖外部網域）並對照 `Mermaid.dc.html` 截圖；板標題移除「（尚未實作）」並發布線上畫布。09 合併後補驗編輯預覽。

## issue #22 驗收條件對照

| issue 驗收條件 | 測試或驗收步驟 |
|---|---|
| 文章與頁面的 Mermaid 在公開頁正確，原文不遺失 | `TestMermaidPostPageLoadsDiagramAssets`、`TestMermaidStaticPageLoadsDiagramAssets`；spec「三種圖…」「頁面（type=page）…」 |
| 儲存後重新開啟正確 | `TestMermaidSourceSurvivesAdminSaveAndReopen`；spec「儲存後重新開啟：原文不遺失」 |
| 編輯預覽正確 | 09 合併後：預覽更新呼叫 `JellyMermaid.render(previewRoot)`，以 spec 的重複呼叫與改原文情境在預覽根元素重跑（見「與工作單 09 的相依」第 3 點） |
| 流程圖、循序圖、ER 圖實例，中文標籤、同頁多張 | spec「三種圖（中文標籤）在同一頁都畫成 SVG…」 |
| 反覆編輯不重複產生、清理舊資源 | spec「重複呼叫 JellyMermaid.render(document) 不重畫也不重複產生」「改原文後重畫且只剩一個 figure…」「錯誤修正後重畫…」；`TestMermaidSourceSurvivesAdminSaveAndReopen` 的更新段 |
| 桌面與手機，配色一致、無整頁橫向溢出 | spec「手機 390…」「配色跟隨主題（light）」「配色跟隨主題（dark）」；主代理對照 `Mermaid.dc.html` 截圖 |
| 錯誤語法、不支援語法、渲染失敗有測試 | spec「錯誤語法、不支援的圖種與過長原文…」「英文介面的錯誤提示」 |
| 圖表文字、連結與 SVG 輸出的安全案例 | spec「惡意圖表…」；錯誤訊息內嵌 `<img onerror>` 的情境在「錯誤語法…」 |
| 封鎖外部網域後仍能渲染 | spec 全程以 `context.route` 封鎖非本站請求，「封鎖所有外部網域後仍完整渲染」檢查沒有任何外部請求 |
| 沒有圖表的頁面不載入 | `TestMermaidPagesWithoutDiagramsLoadNoAssets`、`TestContentAssetsEmptyWithoutMermaidBlock`、`TestContentAssetsTemplateFuncIgnoresListingsAndOtherData` |
| 主題共用的整合方式 | `TestContentAssetsTemplateFuncReadsPageContent`、`TestContentAssetsEmitsScriptsForMermaidBlock`、`TestContentAssetsUsesOnlySameSitePaths` |
| Pele 釘版本與來源 commit、自帶、LICENSE 與 VERSION、重建腳本、發版清單 | `TestMermaidVendoredPelePackage`、`TestMermaidVendoredFilesAreServed` |
| 使用文件 | M4 審查 `docs/mermaid.md` |
| 設計板同步 | `design/project/Mermaid.dc.html` 與 `canvas.json`（本工作單已交付）；M4 移除「尚未實作」並發布線上畫布 |
