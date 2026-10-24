# 12 — 站長在不連外網的情況下使用完整後台，並且開關、分段選擇、確認對話框、通知條有果凍質感

Epic：前端資源與介面元件
User Story：站長與訪客的每一頁都只用 Jelly CMS 自己提供的腳本、樣式、字型與圖示，離線環境照常完整可用，CI 與驗收腳本會擋下任何外部資源；後台的設定開關、文章狀態篩選、確認對話框與通知條改用 Jelly UI（軟體物理感的 Web Components），按下去有果凍質感，但版面、側欄、卡片、表格、按鈕、自家圖示和白底墨字的視覺語言維持原樣。
Blocked by：05 的字型自帶（已合併）。與 08（設定頁）、09（編輯器，移除 marked）有重疊，見〈相依與合併順序〉。
Status：draft（本工作單只交付契約、設計板、檢查程式與「現在會失敗」的驗收測試；Jelly UI 的取得、對接與頁面改寫由另一批代理依本文件完成）
設計稿：`design/project/Settings、SettingsRoutes、SettingsCrawlers、Posts、Trash、States.dc.html`（元件對照見 `design/README.md` 的〈Jelly UI 元件對照〉）；字型改自帶後全部 65 張板的 helmet 都改了。
來源：[issue #18](https://github.com/HazelnutParadise/jelly-cms/issues/18)

## 現況盤點（2026-10-24，main `3aa5633`）

issue 的目標有一部分在 main 已完成，下列以實測為準。

- 已完成：Bricolage Grotesque 與 Noto Sans TC 的 woff2 子集在 `web/admin/assets/fonts/` 與 `web/themes/default/assets/fonts/`，附 `LICENSE`、`VERSION`；`scripts/vendor/fonts.sh` 與 `fonts-charset.txt` 可重現；`layout.html`、`login.html`、`install.html`、主題 `layout.html`、`maintenance.html` 都用 `@font-face`；`internal/server/assets_offline_test.go` 檢查前台頁面輸出與 `web/admin`、`web/themes/default` 的模板。以隔離伺服器、Chromium 封鎖所有非本站網域實測：16 個後台頁（含側欄連結）與 5 個前台頁（登入、首頁、文章總覽、搜尋、404）沒有任何外部請求或失敗請求，`document.fonts.check` 為真。
- 沒做完，列為本工作單的完成條件：
  1. `design/project/*.dc.html` 65 張板的 helmet 載入 `fonts.googleapis.com`／`fonts.gstatic.com`（共 195 個 `<link>`），而 issue 與 `docs/release-checklist.md` 都要求涵蓋 `design/`。本工作單**已改完**（見〈設計板〉）。
  2. 零 CDN 檢查只是字串比對的幾個測試，不涵蓋 `design/`、`cmd/`、CSS 與 JS 的載入。本工作單**已交付** `internal/assetcheck`（見〈契約 · 外部資源檢查〉）。
  3. `web/admin/assets/marked.min.js` 是鬆散的單檔，沒有 `LICENSE`、`VERSION`，也沒有取得與重建腳本。
  4. 沒有 Jelly UI：沒有 `web/admin/assets/jelly-ui/`、沒有 `scripts/vendor/jelly-ui.sh`，後台也沒有任何元件改用它。
  5. `docs/release-checklist.md` 沒有列出套件清單；沒有「系統需求」文件（issue 要求寫明舊瀏覽器不支援）。
- `web/admin/account_layout.html`（個人帳號頁）用系統字型 `Inter, -apple-system, …`，不載入外部資源，不在本工作單範圍；它不載入 Jelly UI。
- 側欄鈴鐺連到 `/admin/notifications`，目前回 404（工作單 06 的頁面）。屬既有缺口，驗收腳本只對側欄動態找到的連結警告，不算失敗。

## 上游事實（2026-10-24 查證）

來源：`https://github.com/jelly-org/ui` 實際 clone 並閱讀原始碼；`https://jelly-ui.com/api/` 的網頁內容只有導言，元件細節要從原始碼讀。**沒有實際建置**：`npm ci` 會執行第三方相依套件的安裝程序，這個環境的沙盒不允許，所以下列「建置結果」類的事實（檔案大小、打包內有無外部網址）沒有親自驗證，驗收時由 `scripts/vendor/jelly-ui.sh` 的實作者與主代理補。

與 issue（2026-10-23 調查）一致：

- MIT；內建 Fluent System Icons（也是 MIT，`README.md` 寫明，**上游 `LICENSE` 檔沒有附 Microsoft 的授權文字**）；`package.json` 版本 `1.1.0`；零執行期相依；`vite build` 以 `src/jelly.ts` 打包成單一 ESM `dist/jelly.js`（target `es2022`，`sourcemap: true`）；`dist/` 不進版控；沒有 npm 套件；`package.js` 只有 `export * from './dist/jelly.js'`。
- 38 個元件目錄、40 個 `customElements.define`（`jelly-segment` 與 `jelly-segmented` 在不同目錄）。
- 原始碼內沒有載入外部資源的程式：所有 `http(s)://` 只出現在 SVG 的 `xmlns="http://www.w3.org/2000/svg"` 與一則註解；圖示是內嵌 SVG；字型用 `system-ui` 等系統字型堆疊，不帶字型檔。因此打包檔會含 `http://www.w3.org/2000/svg` 這類命名空間字串，零 CDN 檢查只看載入行為，不會誤判。
- 沒有 `tag`／release；最新提交（`main`）是 **`1b775385b17884ecb48374615c96ec6ee1b58d21`**（2026-09-30，「Merge pull request #2 … jelly-pagination-fix-duplicate-page-5」），共 7 個提交。釘這個 commit，不釘分支。

與 issue 或 brief 不同、以實際為準：

1. **沒有 `--jelly-accent` 全域變數。** 強調色的全域 token 是 `--jelly-color-background-accent`（`--jelly-accent` 只是 `progress`、`select` 元件內部的區域變數）。全域 token 一律是 `--jelly-color-<background|foreground|border>-<role>`：`background-{default,surface,muted,neutral,neutral-emphasis,white,rose,amber,azure,mint,accent}`、`foreground-{default,muted,on-emphasis,on-neutral,on-white,on-accent}`、`border-{default,focus}`、`shadow`（RGB 三元組）。另有 `--jelly-font-display`、`--jelly-font-text`、`--jelly-font-mono`、`--jelly-ring-*`、`--jelly-shadow-raised`、`--jelly-shadow-overlay`。
2. **token 在 `@layer jelly` 裡，且亮暗兩套都在。** 沒有放進 layer 的 `:root { --jelly-color-… }` 一律贏過它們（不論權重與載入順序），所以 `layout.html` 把 `--jelly-color-*` 指到既有的 `--surface`、`--ink` 等變數，亮暗就自動跟著既有的 `prefers-color-scheme` 切換。
3. **只有對話框與通知有圓角變數**（`--jelly-dialog-radius`，預設 24px；`--jelly-toast-radius`，預設 999px），可以給對角單角圓角。**開關與分段選擇是 canvas 繪製的膠囊形，圓角寫死為高度的一半，沒有變數可改**，所以這兩個維持膠囊形（設計板與 `design/README.md` 已寫明）。
4. **通知的 API 比現有的小**：`jellyToast(message: string, { tone?: 'info'|'success'|'warning'|'danger', duration?: number = 3500 }): HTMLElement`。沒有 `detail`、沒有 `action`、沒有滑過暫停、沒有數量上限、錯誤也會自動消失（要不消失傳 `duration: 0`）、`tone` 叫 `danger` 不叫 `error`。點通知任何地方就關掉；有 ✕ 按鈕。`jellyToast` 只掛在 `window.jellyToast`（沒有 `window.Jelly` 命名空間，所以 `window.Jelly` 不會被上游覆蓋）。通知的外觀可由外部 CSS 用 `::part(toast)`、`::part(dot)`、`::part(close)` 改，主機元素 `<jelly-toaster position="top|bottom">`。`push()` 回傳的元素是 shadow 裡的 `.toast`，內含 `.dot`、`.sr-tone`、`.toast-text`、`.close`。
5. **對話框沒有確認／取消的概念**：`<jelly-dialog open label>` 只有 slot；Esc（`keydown` 在 document 上且沒有 `defaultPrevented`）、點背景、右上 ✕ 都會把 `open` 收起來，發出 `open`／`close` 事件（`bubbles`、`composed`）；打開時移到 `<body>`、鎖捲動、把外面設 `inert`、focus 到對話框本體（`tabindex=-1`，不是第一個按鈕）、關閉後把 focus 還給原本的元素。**沒有 Enter 處理**，所以確認鈕的 Enter 要靠「focus 在按鈕上」。`open = false` 會先跑退場動畫才移除屬性，`close` 事件在那之後。內容是 light DOM（slot），所以頁面既有的 `.btn` 樣式照常作用。
6. **開關**：`<jelly-switch checked disabled label name value size variant>`；`checked` 反映到屬性；`value` 預設 `"on"`；勾選時 `setFormValue(value)`，沒勾送出 `null`（和原生 checkbox 一樣不出現）；空白鍵**與 Enter** 都會切換（所以在 `<form>` 裡的開關按 Enter 不會送出表單）；`change` 事件（`bubbles`、`composed`）只在使用者操作時發出，程式改屬性不發。**元素升級前**對它指定 `el.checked = x` 會變成實例自己的屬性、永遠遮住升級後的 accessor，所以頁面要改用 `el.toggleAttribute('checked', x)`、讀用 `el.hasAttribute('checked')`（`jelly.js` 是 deferred module，頁面內嵌腳本比它先跑）。沒有 `label` 屬性或 slot 文字時沒有可存取名稱。
7. **分段選擇**：`<jelly-segmented value name label roles disabled size>` 包 `<jelly-segment value selected disabled>文字</jelly-segment>`；`value` 反映到屬性，預設選第一個；表單值是被選段的 `value`；`change` 事件的 `detail.value`；方向鍵（隨文字方向翻轉）、Home、End 移動並選取，roving tabindex；段的內容只有純文字標籤。子節點的任何變動（含改文字）都會讓它重建內部按鈕，focus 會掉，所以更新篇數文字只在資料載入後做一次。

## 流程與驗收

- [ ] 站長在後台任何一頁都不會有外部請求：Chromium 封鎖所有非本站網域後，後台與前台每一頁沒有被擋或失敗的請求，Bricolage Grotesque 與 Noto Sans TC 載入成功。
- [x] 設計畫布的 65 張板不從 Google Fonts 載入字型：每張板的 helmet 只有 `<link rel="stylesheet" href="./fonts.css">`，字型來自 `design/project/fonts/*.woff2`（不進版控，由 `scripts/vendor/fonts.sh` 複製）。
- [x] 任何人在 `web/`、`design/`、`cmd/` 的 `.html`、`.tmpl`、`.css`、`.js` 加入外部 script、link、img、iframe、source、`@import`、`url()` 時，`go test ./internal/assetcheck` 會失敗並指出檔名與行號；`<a href>` 與 `<textarea>`、`<code>` 內的示範文字不算。
- [ ] 維護者執行 `scripts/vendor/jelly-ui.sh` 就得到 `web/admin/assets/jelly-ui/{jelly.js,LICENSE,VERSION}`，釘住上游 commit；改 commit 重跑就重現。`marked` 同樣有 `scripts/vendor/marked.sh`、`LICENSE`、`VERSION`，並移到 `web/admin/assets/marked/`。
- [ ] 站長打開任何後台頁（含錯誤頁）都載入 Jelly UI 模組一次；登入頁、安裝頁、個人帳號頁、前台頁都不載入。
- [ ] 站長在設定頁用滑鼠或空白鍵切換 Google／GitHub OAuth、綠界、藍新的啟用，以及兩個測試模式，畫面與資料行為和原本的核取方塊一致（儲存的 JSON 與資料庫內容不變）。
- [ ] 站長在文章列表用「全部／已發布／草稿／待審核」分段選擇（每段顯示篇數）篩選列表，滑鼠點擊、方向鍵、Home 都可用。
- [ ] 站長在後台做刪除、永久刪除、清空回收桶、還原版本、重載外掛、重設主題等需要確認的動作時，看到 Jelly UI 對話框而不是瀏覽器的 `confirm()`；Esc、取消、點背景、✕ 都不執行動作；回收桶的「輸入刪除確認」改由同一個函式提供。
- [ ] 後台所有通知（既有 `Jelly.toast(message, {kind, detail, action, duration})` 呼叫端與伺服器端 `toast` 範本）改由 Jelly UI 的通知條顯示，呼叫端與參數都不改；成功、警告、資訊 4 秒、帶動作的 8 秒、錯誤不自動消失、滑過或聚焦暫停、最多同時 4 則。
- [ ] 深色模式下 `<html data-jelly-mode="dark">`，亮暗切換即時同步；鍵盤可操作四種元件；`dir="rtl"` 下開關與對話框照常運作（`prefers-reduced-motion` 由上游處理）。
- [ ] `docs/release-checklist.md` 列出字型、Jelly UI、marked 三個自帶套件與各自的腳本；`docs/system-requirements.md` 寫明瀏覽器需求。
- [ ] `design/README.md` 記下元件對照；板已改（Settings 家族、Posts、Trash、States），線上畫布由主代理在合併後同步，且要把 `project/fonts.css` 與 `project/fonts/*.woff2` 一起發布。

## 錯誤與邊界

- **沒有 JavaScript 或瀏覽器不支援 Custom Elements、Shadow DOM、`ElementInternals`、`ResizeObserver`、Canvas 2D、`color-mix()` 時**，Jelly UI 元件不渲染或失效；後台因此無法切換設定或確認刪除。issue 接受這個風險，處理方式是寫進 `docs/system-requirements.md`，**不做**原生 `confirm()` 或核取方塊的後備（見決策紀錄）。前台公開表單與前台主題不載入 Jelly UI，不受影響。
- `jelly.js` 載入失敗（404、被擋）時，`Jelly.toast` 的呼叫在佇列裡等不到元素，不丟例外；`Jelly.confirm` 回傳的 Promise 一直不解決（呼叫端的動作不會執行，是安全的方向）。
- `Jelly.toast` 在 `jelly.js` 執行前被呼叫（伺服器端 `toast` 範本在頁面解析中就呼叫）要排隊，模組載入後依序顯示。
- 對話框開著時，外面是 `inert`：此時新來的通知看得到但點不到，可接受。
- 開關在 `<form>` 內按 Enter 會切換而不是送出表單（上游行為）；設定頁的表單本來就由 JavaScript 送出，無影響。
- 任何頁面都不得再用 `el.checked = …` 寫 `jelly-switch`（見上游事實 6）。
- 沒有 CSP（`grep Content-Security-Policy` 在非測試程式為空）。Jelly UI 會在 `<head>` 插入 `<style data-jelly-tokens>` 並在元素上設 inline style；日後後台若加 CSP，`style-src` 要放行。

## 契約（骨架與測試共同依據）

### 契約 · 外部資源檢查（已隨本工作單交付）

`internal/assetcheck`（不需 DB，`go test ./...` 本來就會跑，CI 不用加步驟）：

- `ScanHTML`、`ScanCSS`、`ScanJS`、`ScanFile(name, src) []Finding{Line, Kind, URL}`、`IsExternal(ref)`。以 `golang.org/x/net/html` 的 tokenizer 解析（已在 `go.mod`）。
- 算外部：值以 `http:`、`https:`、`//` 開頭（不分大小寫，前導空白忽略）。
- 檢查的載入點：`<script src>`、`<link href>`（`rel` 為 canonical、alternate、next、prev、author、license、me、help、search、bookmark、nofollow、noopener、noreferrer、webmention、pingback、shortlink 的不算；沒有 `rel` 算）、`<link imagesrcset>`、`<img src|srcset>`、`<source src|srcset>`、`<iframe|video|audio|embed|track src>`、`<video poster>`、`<object data>`、`<input type=image src>`、`<style>` 與 `style=""` 內的 CSS、CSS 檔的 `url(...)` 與 `@import "…"`；JS 檔的靜態與動態 `import`、`export … from`、`importScripts`、`new Worker`、`.src = "…"`。不算：`<a href>`、`<textarea>`／`<code>` 內的文字、JS 字串裡單純出現網址。
- `TestRepositoryLoadsNoExternalResources` 掃 `web/`、`design/`、`cmd/` 下所有 `.html`、`.tmpl`、`.css`、`.js`、`.mjs`（跳過 `design/shots`、`node_modules`），並要求至少掃到 30 個 `web` 檔與 60 個 `design` 檔，避免掃空而假通過。

### 契約 · 自帶套件

每個套件是 `web/admin/assets/<套件>/`（後台）、`web/themes/<主題>/assets/<套件>/`（主題）或 `web/vendor/<套件>/`（共用）下的一個目錄，內有 `LICENSE`（非空）與 `VERSION`。`VERSION` 的格式（測試用正規表示式檢查）：

```
Version: 1.1.0
Source: https://github.com/jelly-org/ui
Commit: <40 位十六進位>
Build date (UTC): 2026-10-24
```

可以有其他行（建置工具版本等），但上面三行（`Version:`、`Commit:`、`Build date (UTC):`）必須各出現一次且獨立成行。目錄旁邊不得有沒標示的鬆散檔案；第一方檔案列在 `internal/assetcheck/vendor_test.go` 的 `firstPartyAssets`（`jelly-mark.svg`、`sortable.js`、`jelly-motion.js`、`jelly-components.js`）。

**`web/admin/assets/jelly-ui/`**：`jelly.js`（上游 `dist/jelly.js`，去掉結尾的 `//# sourceMappingURL=…` 行，因為不一併發 `.map`，留著會在開發者工具裡 404）、`LICENSE`（上游 MIT 全文，**再附上 Fluent System Icons 的 MIT 授權文字，含 `Microsoft Corporation` 著作權行**）、`VERSION`。

**`scripts/vendor/jelly-ui.sh`**（可執行，`set -euo pipefail`，風格同 `fonts.sh`）：

- 以 `jelly_ui_commit=<40 位十六進位>` 這一行釘上游 commit（測試要求這行存在，且和 `VERSION` 的 `Commit:` 相同）。
- clone `https://github.com/jelly-org/ui.git` 到暫存目錄、`git checkout` 該 commit、`npm ci`（`PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1`，我們不跑上游測試）、`npm run build`，複製 `dist/jelly.js`、`LICENSE` 到 `web/admin/assets/jelly-ui/`，寫入 `VERSION`（版本取 `package.json`，建置日期取 UTC 今天）。
- 腳本內容必須含 `npm ci`、`npm run build`、`web/admin/assets/jelly-ui`、`dist/jelly.js`、`LICENSE`、`VERSION` 這些字（測試檢查）。
- 不提交 `node_modules` 或暫存目錄；Node 只在維護者跑這支腳本時需要，Dockerfile 與 CI 不需要 Node。

**`scripts/vendor/marked.sh`**：以 `marked_version=x.y.z` 這一行釘版本，從 npm 發行檔（或 GitHub release）取得 UMD 檔，寫到 `web/admin/assets/marked/marked.min.js`，附 `LICENSE`、`VERSION`；`web/admin/components.html` 的 `<script src>` 改成 `/admin/assets/marked/marked.min.js`。`VERSION` 的 `Version:` 必須等於腳本的 `marked_version`。

**`docs/release-checklist.md`**：第 1 節加一張套件清單表（字型、Jelly UI、marked 各一列：目錄、腳本、查上游最新版的地方）。測試檢查它包含 `scripts/vendor/fonts.sh`、`scripts/vendor/jelly-ui.sh`、`scripts/vendor/marked.sh`、`web/admin/assets/jelly-ui`、`Jelly UI`、`marked`、`字型`。

**`docs/system-requirements.md`**（新檔，並從 `README.md` 連過去）：寫明後台需要啟用 JavaScript 與支援 Custom Elements、Shadow DOM、`ElementInternals`、`ResizeObserver`、Canvas 2D 與 CSS `color-mix()` 的瀏覽器（近期的 Chrome、Edge、Safari、Firefox）；不支援時後台的開關、分段選擇、對話框與通知不能用；前台主題不需要。測試檢查這幾個詞都出現。

### 契約 · 載入與對接（`web/admin/layout.html`）

1. 載入：在 `{{block "scripts" .}}` 之前放一次 `<script type="module" src="/admin/assets/jelly-ui/jelly.js"></script>`（字串必須一字不差）。`login.html`、`install.html`、`account_layout.html`、`web/themes/**` 不載入。靜態資源由既有的 `e.Static("/admin/assets", "web/admin/assets")` 提供，`.js` 要是 JavaScript MIME。
2. Token 對接（寫在 `layout.html` 現有的未分層 `:root` 區塊與其 `prefers-color-scheme: dark` 區塊之外，值全部引用既有變數，所以亮暗自動跟著；列表是必須出現的宣告，值由實作者依視覺決定，建議值如下）：

   | 宣告 | 建議值 |
   |---|---|
   | `--jelly-color-background-default`、`-surface` | `var(--surface)` |
   | `--jelly-color-background-muted` | `var(--field)` |
   | `--jelly-color-background-neutral`（開關關閉的軌道、分段軌道） | `var(--surface-high)` |
   | `--jelly-color-background-accent`（開關開啟） | `var(--accent)` |
   | `--jelly-color-foreground-default` | `var(--ink)` |
   | `--jelly-color-foreground-muted` | `var(--muted)` |
   | `--jelly-color-foreground-on-accent` | 墨色（薄荷底上白字對比不足） |
   | `--jelly-color-border-default` | `var(--outline)` |
   | `--jelly-color-border-focus`（焦點環） | `var(--secondary)` |
   | `--jelly-font-display`、`--jelly-font-text`、`--jelly-font-mono` | `var(--font-display)`、`var(--font)`、`var(--mono)` |
   | `--jelly-dialog-radius` | `0 1.5rem 0 1.5rem`（`.rv-dlg` 與 Trash 板的圓角） |
   | `--jelly-toast-radius` | `0 .75rem 0 .75rem` |

   分段選擇的選中段要是墨色：在 `jelly-segmented` 上設 `--jelly-pill: var(--ink)`、`--jelly-label-on: var(--surface)`、`--jelly-track: var(--surface-mid)`（這是上游元件的區域變數，文件層的規則會贏過 shadow 內的 `:host` 預設值）。通知條用 `jelly-toaster::part(toast) { background: var(--toast-bg); color: #fff; border: 0; }`，`::part(close)` 的顏色同理，色點的顏色見下方 `Jelly.toast`。測試檢查表中每個 `--jelly-…:` 都出現在 `layout.html`。
3. 亮暗同步：`<head>` 內放一小段同步腳本，把 `document.documentElement.dataset.jellyMode` 設成 `matchMedia('(prefers-color-scheme: dark)')` 對應的 `'dark'` 或 `'light'`，並監聽該 media query 的 `change`，每次改完屬性後 `window.dispatchEvent(new CustomEvent('jelly-theme-change'))`，讓 canvas 繪製的元件重畫。（後台目前只有 `prefers-color-scheme`，沒有手動切換。）
4. 通知主機：把 `<div class="toasts" id="toasts" role="region" aria-label="{{.T.toastRegion}}">` 換成 `<jelly-toaster position="bottom" role="region" aria-label="{{.T.toastRegion}}"></jelly-toaster>`，刪除 `.toasts`、`.toast*`、`@keyframes toast-in` 這些舊樣式與舊的 `toast()` 實作。
5. `window.Jelly` 命名空間保留（`toast`、`palette`），新增 `window.Jelly.confirm`。

### 契約 · `window.Jelly.toast(message, options)`

簽名與回傳不變：`Jelly.toast(message: string | {message, …}, options?: { kind?: 'success'|'error'|'info'|'warning', detail?: string, action?: { label: string, onClick?: (dismiss) => void, href?: string }, duration?: number }) → { dismiss(): void, element: HTMLElement }`。內部呼叫 `window.jellyToast`，對應規則：

- `kind` → `tone`：`success`→`success`、`error`→`danger`、`warning`→`warning`、其他（含缺省）→`info`。
- 色點維持現有品牌色：success 用 `--accent`、error 用 `--primary`、warning 用 `--warning`、info 用 `--secondary`（在回傳的 `.toast` 裡的 `.dot` 上設 inline `background`）。
- 文字：粗體訊息；有 `detail` 時其後接一個較淡的 `<span>`；一律用 `textContent`（不當 HTML）。
- `action`：在 `.toast` 裡、`.close` 之前加一個按鈕（有 `href` 則是 `<a>`，`http(s):` 開頭加 `target="_blank" rel="noopener"`）；點了呼叫 `onClick(dismiss)`，沒有 `href` 時隨後關閉。按鈕要 inline 樣式（它在上游的 shadow DOM 裡，吃不到頁面 CSS），外觀對應 States 板的 `.jt` 動作按鈕。
- 時間：呼叫端的計時自己管——傳 `duration: 0` 給 `jellyToast`，由包裝層計時；預設 4000 ms，有 `action` 8000 ms，`error` 不自動消失，`options.duration`（毫秒，0 不消失）覆寫；滑過或聚焦時暫停、離開或失焦時續算且至少再留 1500 ms（現有行為）；關閉用 `.close` 的 `click()`。
- 最多同時 4 則，超過就關掉最舊的。
- `error` 的 `.toast` 把 `role` 改成 `alert`，其餘維持上游的 `status`。
- 在 `jelly.js` 還沒執行前呼叫要排隊（以 `customElements.whenDefined('jelly-toaster')` 為準），回傳的 `dismiss()` 在排隊期間也有效（元素出現前被 dismiss 就不顯示）。
- `window.jellyToast` 是上游留下的全域函式，不是對外 API，既有頁面不要直接用它。

### 契約 · `window.Jelly.confirm(options) → Promise<boolean>`

```
options = {
  title:        string,                       // 必填，對話框標題（<h2>）
  message?:     string,                       // 內文（<p>），純文字
  confirmLabel?: string,                      // 預設 'OK'
  cancelLabel?:  string,                      // 預設 'Cancel'
  tone?:        'primary' | 'danger',         // 預設 'danger'
  requireText?: string                        // 有值：要輸入同樣的字（trim 後完全相等）才能確認
}
```

- 每次呼叫建立一個新的 `<jelly-dialog data-jelly-confirm>`，附加到 `<body>` 後設 `open`；`label` 用 `title`；內容為 `<h2>`、`<p>`（有 `message` 時）、`requireText` 時的欄位列、按鈕列。關閉並動畫結束後把元素從 DOM 移除。
- 按鈕沿用既有 `.btn`：取消 `.btn.btn-secondary` 帶 `data-jelly-cancel`；確認 `tone: 'danger'` 用 `.btn.btn-danger`、`primary` 用 `.btn.btn-primary`，帶 `data-jelly-confirm`。有 `requireText` 時另有 `<input data-jelly-confirm-input>` 與一個說明列（沿用現有 `trTypeToConfirm`、`trTypeToConfirmTail` 文案的句型，由呼叫端傳入完整字串，不由 `Jelly.confirm` 組字），確認鈕在字不相等時 `disabled`。
- 回傳值：按確認鈕或在確認鈕上按 Enter→`true`；Esc、取消鈕、點背景、右上 ✕→`false`；對話框被程式移除→`false`。永遠只解決一次。
- 初始 focus：`requireText` 時在輸入欄；否則 `tone: 'danger'` 在取消鈕（現有 taxonomy 與 menus 的行為，防止誤按 Enter）；`tone: 'primary'` 在確認鈕。字不相等時在輸入欄按 Enter 不解決。
- 沒有原生 `confirm()` 後備。
- 取代範圍（每一處都改成 `await Jelly.confirm({…})`，文案沿用各頁既有字串）：
  `comments.html`（單筆與批次刪除）、`media.html`、`plugins.html`（重載外掛）、`products.html`、`themes.html`（重設主題）這五頁的 `confirm(` / `window.confirm(`；`menus.html` 與 `taxonomy.html` 的自製 `confirmSheet`；`revisions.html` 的還原確認（`.rv-overlay`、`.rv-dlg`）；`trash.html` 的永久刪除與清空確認（`.tr-overlay`、`.tr-dlg`，`requireText` 版）。`menus.html` 的「選單編輯側板」`openMenuSheet`（表單，不是確認）不動，但它與 `confirmSheet` 共用的 `sheet()` 若只剩一個用途，可就地簡化。
- 刪掉被取代的元素、事件處理與樣式；頁面內不得留下 `confirmSheet`、`tr-dlg`、`rv-dlg`、`tr-overlay`、`rv-overlay` 這些字（測試檢查）。

### 契約 · 設定頁開關（`web/admin/settings.html`）

六個布林欄位全部換成 `<jelly-switch>`，`id` 不變（頁面腳本用 id 取值），新增 `name` 與 `label`：

| id | name | label |
|---|---|---|
| `googleEnabled` | `google_enabled` | `Google OAuth` |
| `githubEnabled` | `github_enabled` | `GitHub OAuth` |
| `ecpayEnabled` | `ecpay_enabled` | `綠界 ECPay` |
| `ecpayTestMode` | `ecpay_test_mode` | `測試模式 (Test Mode)` |
| `newebpayEnabled` | `newebpay_enabled` | `藍新 NewebPay` |
| `newebpayTestMode` | `newebpay_test_mode` | `測試模式 (Test Mode)` |

- 原本在 `settings-group-head` 裡的 `<label class="toggle-switch"><input type="checkbox" …><span class="slider"></span></label>` 整段換成單一 `<jelly-switch …></jelly-switch>`；兩個 `測試模式` 原本是 `<div class="control"><input type="checkbox" …></div>`，同樣換掉。`onchange="toggleGoogle()"` 等行內處理器保留（`change` 事件在元素本身上發出）。
- 頁面腳本寫入一律 `el.toggleAttribute('checked', value)`，讀取一律 `el.hasAttribute('checked')`；測試禁止 `…Enabled').checked =`／`…TestMode').checked =` 的賦值寫法。建議在腳本頂端加兩個小函式 `isOn(id)`、`setOn(id, value)`，取代現有二十多處 `.checked`。
- 伺服器輸出的開關不帶 `checked`（預設值由腳本在讀到設定後設定）。
- 送到 `POST /api/admin/payment/gateways/{ecpay,newebpay}` 與 `POST /api/admin/oauth/providers/{google,github}` 的 JSON（`enabled`、`test_mode` 為布林）與資料庫內容（`payment_ecpay_enabled`、`payment_ecpay_test_mode` 為 `"true"`／`"false"` 字串）完全不變，後端不動。
- 設定頁其餘的 `alert(...)` 不在本工作單範圍（`COMPONENTS.md` 已註記「之後逐頁改成 `Jelly.toast`」）。
- `layout.html` 的 `.toggle-switch`／`.slider` 樣式保留：`menus.html`、`taxonomy.html`、`revisions.html`、`components.html` 還在用（見決策紀錄）。

### 契約 · 文章列表狀態篩選（`web/admin/posts.html`）

文章列表目前沒有狀態篩選（設計板有）。本工作單新增，純前端：

```html
<jelly-segmented name="status" value="all" label="{{.T.status}}">
  <jelly-segment value="all">{{.T.all}}</jelly-segment>
  <jelly-segment value="published">{{.T.published}}</jelly-segment>
  <jelly-segment value="draft">{{.T.draft}}</jelly-segment>
  <jelly-segment value="pending">{{.T.pending}}</jelly-segment>
</jelly-segmented>
```

- 放在標題 `<h1>` 旁（Posts 板的位置）。四個 `value` 與順序固定（測試檢查）；伺服器輸出的標籤是上面四個翻譯字串，不帶篇數。
- `loadPosts()` 取回 `GET /api/admin/posts` 後（一次取回全部，沿用現狀），把每段的文字改成「標籤 空格 篇數」（例如 `已發布 96`；上游的段標籤只能是純文字，板上也畫成同字級），只做這一次；`all` 的篇數是全部，其餘是 `post.status` 相等的篇數。
- `change` 事件（`event.detail.value`）時重畫表格：`all` 全部，其餘只留 `post.status === value` 的列；沒有符合的列顯示既有的 `T.noPosts` 空狀態。篩選只在前端，不改 API、不寫進網址。
- 讀文章前的狀態：段的預設選取是 `all`。

### 契約 · 設計板（已隨本工作單交付）

- `design/project/fonts.css`（兩個 `@font-face`，`font-weight: 100 900`、`font-display: swap`，路徑 `fonts/<檔名>.woff2`）；每張板 helmet 是 `<link rel="stylesheet" href="./fonts.css">`；`design/gen/gen.py` 的 `FONTS` 同步，其餘產生器透過 `gen.py` 共用；九支產生器都重新執行過，沒有手改產生出來的板。
- `.gitignore`：`design/project/fonts/`。`scripts/vendor/fonts.sh` 在產生網站字型時也複製到這裡（已改）。
- 板上的元件樣子：`.tg.j`（開關）、`.seg.j`（分段選擇）、`.jd`（對話框，含背景遮罩 `.jd-scrim`）、`.jt`（通知條）；Settings 板的 ECPay「環境（測試／正式）」分段選擇改成與程式一致的「測試模式」開關列，NewebPay 補上同一列。
- 發布線上畫布時（`design/README.md` 的步驟 3），`files` 要列出 `project/fonts.css` 與 `project/fonts/*.woff2`；這是主代理合併後的步驟，本工作單不發布。

## 決策紀錄

1. **外部資源檢查放新套件 `internal/assetcheck`，用 HTML tokenizer，不用字串比對。** 否決：擴充 `internal/server/assets_offline_test.go`——它的字串比對會誤判設計板裡的 `x.com` 連結與 textarea 內的逸出示範文字，也不看 CSS 與 JS；放 `internal/server` 還會讓一個不需 DB 的檢查綁在慢測試套件裡。
2. **設計板改用自帶字型：`fonts.css` 加不進版控的 `fonts/`，由 `fonts.sh` 複製。** 否決：在 `design/` 再提交一份 woff2（重複 1.9 MB 且要同步兩份）；用 data URI 內嵌進每張板（65 份）。
3. **Jelly UI 以釘 commit 的腳本建置後提交 `jelly.js`。** 否決：git submodule 加 Docker 建置時編譯（把 Node 拉進映像檔建置與 CI）；直接載入 `https://jelly-ui.com/package.js`（違反零 CDN）。上游沒有 tag，釘 commit；不釘分支名稱。
4. **只換四類控制項，不換版面。** 側欄、topbar、卡片、表格、⌘K 面板、既有按鈕與自家 sprite 圖示維持現有 CSS。否決：順手換 `jelly-button`、`jelly-input`、`jelly-select`（表單欄位的視覺語言是「底線式輸入框」，canvas 繪製的 Jelly 輸入框會換掉它）。
5. **開關與分段選擇維持膠囊形，不嘗試對角單角圓角。** 它們是 canvas 繪製、圓角寫死。否決：修改上游原始碼或在 `jelly.js` 上打補丁（失去「改 commit 重跑就重現」）；用 CSS 把 canvas 裁成對角形（會切掉焦點環與陰影）。對話框與通知有圓角變數，所以維持對角單角圓角。
6. **`window.Jelly.toast` 轉呼叫 `jellyToast()`，既有呼叫端與參數不變，不保留兩套通知。** 上游的通知比現有的小（見上游事實 4），所以包裝層補回 `detail`、`action`、品牌色點、計時與暫停、數量上限、`role="alert"`。這些補丁會碰到上游 shadow DOM 裡的 `.toast`、`.dot`、`.toast-text`、`.close` 這幾個類名，上游改名時會壞，由驗收腳本的通知項目偵測（每次升版都要跑）。否決：保留舊的自製 toast、只讓新程式用 Jelly（兩套 API 與兩種外觀）；把 `detail` 與 `action` 砍掉（既有呼叫端有「復原」「重試」）；改上游加 slot（同決策 5 的理由）。
7. **`Jelly.confirm` 一個函式取代所有確認，包含回收桶的輸入確認與版本還原。** 否決：只換原生 `confirm()`、保留自製的 `confirmSheet`／`.tr-dlg`／`.rv-dlg`（四套樣式、四套焦點陷阱與 Esc 處理，正是要收斂的）；對話框內放表單的通用 API（沒有第二個用例）。版本還原原本的「按下後在對話框內顯示忙碌」改成關閉對話框後由頁面既有的狀態列與通知回報。
8. **不做原生 `confirm()` 與核取方塊的後備。** 兩條程式路徑的維護成本，加上測試要求頁面內零原生 `confirm(`。issue 已決定寫進系統需求。
9. **設定頁開關以 `toggleAttribute`／`hasAttribute` 讀寫，不碰 `.checked`。** 升級競態見上游事實 6。否決：把頁面腳本全包進 `customElements.whenDefined`（整頁邏輯延後，載入前的空白更久，且仍要處理失敗情形）。
10. **文章狀態篩選純前端、不寫進網址。** API 一次取回全部文章（沿用），篩選在前端；加 `?status=` 要同時改 API 與分頁，超出本工作單。否決：改後端 `GET /api/admin/posts?status=`。
11. **只有設定頁的布林欄位換開關；`menus.html`（`iNewTab`、`iLoginOnly`）、`taxonomy.html`（`fHide`、`fNoCascade`）、`revisions.html`（`collapse`）、`components.html` 的 `toggle` 範本仍是 `.toggle-switch`。** issue 與 brief 明定範圍是設定頁；那些頁的腳本在載入時同步寫 `.checked`，改動風險與測試面都不小。結果是後台暫時有兩種開關外觀，列為後續；板上除設定家族外的 `.tg` 也維持舊樣式，以免板與程式不一致。
12. **`marked` 移到 `web/admin/assets/marked/` 目錄。** 讓「每個套件一個目錄、各有 `LICENSE` 與 `VERSION`」成為單一規則，檢查才不必為鬆散單檔另立慣例。工作單 09 本來就要移除 marked，所以只是過渡；09 先合併時 `TestMarkedIsVendoredAsAPackageDirectory` 自動跳過。否決：在 `web/admin/assets/` 旁放 `marked.min.js.LICENSE`／`.VERSION`（規則不一致）。
13. **`design/` 與 `cmd/` 也納入掃描。** issue 明寫。`design/tools/preview-admin.mjs` 仍有一行 `page.route(/fonts\.(googleapis|gstatic)\.com/, route => route.abort())`，那是擋外部請求的用法，不是載入，掃描不算，維持不動。
14. **Jelly UI 的 `LICENSE` 要附 Fluent System Icons 的授權文字。** 打包檔內含該圖示集（MIT，需保留著作權與授權聲明），上游 `LICENSE` 檔沒有附，所以腳本要補。
15. **驗收腳本不用 `@playwright/test`，也不進 CI。** 環境事實：Playwright 只能以 CommonJS 載入，ESM 的 `import('playwright')` 會失敗，所以用 `createRequire(import.meta.url)('playwright')`，與 `scripts/acceptance/webmcp.spec.mjs`（另一個 PR）同樣是一般 node 腳本；環境變數 `JELLY_BASE_URL`、`JELLY_ADMIN_USER`、`JELLY_ADMIN_PASSWORD`。

## 相依與合併順序

- **工作單 09（編輯器，PR #26 `claude/pr-g-editor`）**：移除 `marked.min.js`、新增 `web/admin/assets/codemirror/`、`web/vendor/katex/`、`web/admin/assets/sortable.js`、`scripts/vendor/{codemirror,katex}.sh` 與 `scripts/vendor/README.md`。後合併者調和：
  - 09 先合併：本分支的 `marked` 目錄、`marked.sh`、`components.html` 的路徑改動整段拿掉；`release-checklist.md` 的套件表不列 marked，把 CodeMirror 與 KaTeX 加進去；`internal/assetcheck/vendor_test.go` 的 marked 測試自動跳過，不必改測試。
  - 本分支先合併：09 rebase 時刪除 `web/admin/assets/marked/` 與 `scripts/vendor/marked.sh`，並把 `scripts/vendor/README.md` 的表加上 `jelly-ui.sh`、`fonts.sh`。
  - `scripts/vendor/README.md` 屬 09；本工作單不建立它。
- **工作單 08（系統，PR #27 `claude/pr-f-system`）**：改 `web/admin/settings.html`（分頁改成連結、新增「維護模式」等設定）。後合併者調和：08 新增的所有布林欄位（維護模式、索引開關等）也要用 `<jelly-switch>`，`name` 與 `label` 依本工作單的規則；`TestSettingsBooleansAreJellySwitches` 的 `settingsSwitches` 表要加列，`TestSettingsPageRendersJellySwitchesWithTheOriginalNames` 的 `want` 同步；`switchTab` 改成連結後，驗收腳本的 OAuth 分頁點擊仍然成立（點 `.tabs .tab` 含 `OAuth` 的項目）。
- **工作單 06（帳號，PR #24）、07（商店，PR #25）**：新增 `web/admin/{notifications,profile,users,coupons,order_detail}.html` 與前台商店頁。本工作單的 `TestAdminPagesHaveNoNativeOrHandMadeConfirmDialogs` 會掃到它們；目前這四個分支沒有原生 `confirm(`。它們若日後加確認動作，就用 `Jelly.confirm`。前台頁不載入 Jelly UI。
- `design/project/canvas.json`：本工作單沒有新增板，不動；其他草稿 PR 都動它，無衝突。
- 其他草稿 PR 也各自新增板，那些板的 helmet 要用 `./fonts.css`；合併順序在後的，`gen.py` 重新產生後這一點自動成立，手寫板要自己加。

## 實作順序

每槽結束時列出的測試要全過。**目前 `go test ./internal/assetcheck` 與瀏覽器腳本的字型與設計畫布項目已通過（Z1 已完成）。**

1. **Z1 零 CDN 檢查與設計板字型（已隨本工作單完成）。** `internal/assetcheck/assetcheck.go`、`assetcheck_test.go`；65 張板、`fonts.css`、`gen.py`、`.gitignore`、`fonts.sh` 複製；`TestDesignBoardsUseSelfHostedFonts`。
2. **Z2 自帶套件與文件。** `scripts/vendor/jelly-ui.sh`、執行它產生 `web/admin/assets/jelly-ui/`；`scripts/vendor/marked.sh` 與 marked 目錄搬移；`docs/release-checklist.md` 套件表；`docs/system-requirements.md`。測試：`vendor_test.go` 全部、`TestVendoredJellyUIIsServedAsJavaScript`。
3. **Z3 載入、token、通知。** `layout.html` 的模組標籤、token 對接、亮暗同步、`<jelly-toaster>`、`Jelly.toast` 包裝層與排隊、移除舊通知碼；`components.html` 的 `toast` 範本維持呼叫 `Jelly.toast`。測試：`TestOnlyTheAdminLayoutLoadsJellyUI`、`TestAdminLayoutMapsTokensAndDefinesTheHelpers`（`window.Jelly.confirm` 一項要等 Z4）、`TestAdminPagesLoadTheJellyUIModuleOnce`、`TestLoginInstallAccountAndPublicPagesDoNotLoadJellyUI`；瀏覽器腳本的 `toast:` 兩項與 `dark mode`。
4. **Z4 確認對話框。** `Jelly.confirm` 與十處呼叫端改寫、刪舊碼；各頁的 i18n 字串沿用。測試：`TestAdminPagesHaveNoNativeOrHandMadeConfirmDialogs`、`TestAdminLayoutMapsTokensAndDefinesTheHelpers`；腳本的三個 `dialog:` 項目。
5. **Z5 設定頁開關。** 六個 `<jelly-switch>`、`isOn`／`setOn`、腳本改寫。測試：`TestSettingsBooleansAreJellySwitches`、`TestSettingsPageRendersJellySwitchesWithTheOriginalNames`、`TestSettingsPayloadsStayTheSame`（先後都綠）；腳本的 `switch:` 與 `RTL`。
6. **Z6 文章列表狀態篩選。** `posts.html`。測試：`TestPostsListHasAStatusSegmentedControl`、`TestPostsPageRendersTheStatusSegmentedControl`；腳本的 `segmented:`。
7. **Z7 主代理驗收與畫布。** 以真實伺服器跑 `scripts/acceptance/jelly-ui.spec.mjs`，1280px 與 390px 手動對照 Settings、Posts、Trash、States 板截圖；重跑 `fonts.sh`、`jelly-ui.sh` 比對 SHA-256 與 `VERSION`；`go test -race`、`go vet`、`gofmt`、三平台編譯；發布線上畫布（連同 `fonts.css` 與字型）。

## issue 完成條件 → 測試名或驗收步驟

| issue 完成條件 | 驗證 |
|---|---|
| 後台與預設主題所有 HTML 與模板沒有外部 script、link、font 來源，CI 檢查通過 | `internal/assetcheck`：`TestRepositoryLoadsNoExternalResources`（涵蓋 `web/`、`design/`、`cmd/`，CI 的 `go test` 會跑）；既有 `TestAdminTemplatesLoadNoExternalAssets`、`TestPublicLayoutLoadsNoExternalAssets`；掃描器的正反例 `TestScanReportsOnlyRealExternalResourceLoads`、`TestScanReportsLineNumbers` |
| Playwright 封鎖外部網域跑過後台與前台每一頁確認離線可用 | 腳本：`admin: every page loads offline …`、`public: login, home, posts, search and 404 …`、`design canvas: every board loads its fonts …` |
| 字型 woff2 子集自帶、`fonts.sh` 可重現、離線字體相同 | 既有 `TestVendoredFontsPresent`；腳本的 `document.fonts.check`；驗收步驟：重跑 `scripts/vendor/fonts.sh`，兩處 woff2 的 SHA-256 與重跑前相同；`TestDesignBoardsUseSelfHostedFonts`（設計板與 `.gitignore`、`gen.py`） |
| `web/admin/assets/jelly-ui/` 含 `jelly.js`、`LICENSE`、`VERSION`，`jelly-ui.sh` 可重現 | `TestJellyUIVendoredBuildIsPresent`、`TestVendorScriptsExistAndRebuild`、`TestVendoredJellyUIIsServedAsJavaScript`；驗收步驟：重跑 `scripts/vendor/jelly-ui.sh`，`jelly.js` 的 SHA-256 與提交的相同 |
| 每個自帶套件附 `LICENSE`、`VERSION`，取得與重建腳本在 `scripts/vendor/` | `TestEveryThirdPartyPackageHasLicenseAndVersion`、`TestMarkedIsVendoredAsAPackageDirectory` |
| 至少 switch、segmented、dialog、toaster 四種在後台實際頁面上線 | `TestSettingsPageRendersJellySwitchesWithTheOriginalNames`、`TestPostsPageRendersTheStatusSegmentedControl`、`TestAdminPagesHaveNoNativeOrHandMadeConfirmDialogs`、`TestAdminPagesLoadTheJellyUIModuleOnce`（含 `<jelly-toaster>`）；靜態版 `TestSettingsBooleansAreJellySwitches`、`TestPostsListHasAStatusSegmentedControl`、`TestAdminLayoutMapsTokensAndDefinesTheHelpers`；腳本的 `switch:`、`segmented:`、`dialog:`、`toast:` |
| 對照設計板 | 驗收步驟：Settings、Posts、Trash、States 板截圖對照實機（1280px，亮暗）；`design/README.md` 的對照表 |
| `window.Jelly.toast` 與 `jellyToast()` 的取捨實作 | 決策 6；腳本 `toast: Jelly.toast renders a Jelly UI toast …`、`toast: a call before jelly.js has loaded is queued and shown` |
| 深色模式、鍵盤操作、RTL 不退步（Playwright 覆蓋） | 腳本：`dark mode: …`、`RTL: …`，以及 `switch:`（空白鍵）、`segmented:`（方向鍵、Home）、`dialog:`（Esc、Enter、焦點） |
| 設計板與 `design/README.md` 更新 | 本工作單已交付；`TestDesignBoardsUseSelfHostedFonts`；線上畫布同步屬 Z7 |
| release checklist 包含字型與 Jelly UI | `TestReleaseChecklistListsVendoredPackages`；`TestSystemRequirementsNameTheBrowserFeaturesJellyUINeeds` |
| 登入、安裝、前台不載入 Jelly UI；不影響既有設定值 | `TestLoginInstallAccountAndPublicPagesDoNotLoadJellyUI`、`TestOnlyTheAdminLayoutLoadsJellyUI`、`TestSettingsPayloadsStayTheSame`；腳本 `no page other than the admin loads Jelly UI` |

### 測試檔與目前狀態

| 檔案 | 測試 | 現在 |
|---|---|---|
| `internal/assetcheck/assetcheck_test.go` | `TestScanReportsOnlyRealExternalResourceLoads`、`TestScanReportsLineNumbers`、`TestRepositoryLoadsNoExternalResources` | 通過（Z1 已完成） |
| `internal/assetcheck/vendor_test.go` | `TestJellyUIVendoredBuildIsPresent`、`TestVendorScriptsExistAndRebuild`、`TestMarkedIsVendoredAsAPackageDirectory`、`TestEveryThirdPartyPackageHasLicenseAndVersion`、`TestReleaseChecklistListsVendoredPackages`、`TestSystemRequirementsNameTheBrowserFeaturesJellyUINeeds`、`TestDesignBoardsUseSelfHostedFonts` | 前六項失敗（Z2 未做）；最後一項通過 |
| `internal/assetcheck/adoption_test.go` | `TestOnlyTheAdminLayoutLoadsJellyUI`、`TestAdminLayoutMapsTokensAndDefinesTheHelpers`、`TestAdminPagesHaveNoNativeOrHandMadeConfirmDialogs`、`TestSettingsBooleansAreJellySwitches`、`TestPostsListHasAStatusSegmentedControl` | 全部失敗（Z3 至 Z6 未做） |
| `internal/server/jelly_ui_integration_test.go`（`JELLY_TEST_POSTGRES=1`） | `TestAdminPagesLoadTheJellyUIModuleOnce`、`TestVendoredJellyUIIsServedAsJavaScript`、`TestLoginInstallAccountAndPublicPagesDoNotLoadJellyUI`、`TestSettingsPageRendersJellySwitchesWithTheOriginalNames`、`TestPostsPageRendersTheStatusSegmentedControl`、`TestSettingsPayloadsStayTheSame` | 除 `TestLoginInstallAccountAndPublicPagesDoNotLoadJellyUI` 與 `TestSettingsPayloadsStayTheSame`（防退步，現在就通過）外都失敗 |
| `scripts/acceptance/jelly-ui.spec.mjs`（不進 CI） | 13 項 | 現在通過 4 項（後台離線、前台離線、其他頁不載入 Jelly UI、設計畫布字型），其餘 9 項失敗於「jelly.js 未載入」 |

### 測試另外釘下的約定

這些名稱在上面的契約裡沒有寫死，是測試裡釘死的：

- 模組標籤字串：`<script type="module" src="/admin/assets/jelly-ui/jelly.js"></script>`；通知主機：`<jelly-toaster position="bottom"`（開頭字串）。
- 未分層 token 宣告必須出現在 `layout.html` 的清單：`--jelly-color-background-surface:`、`--jelly-color-background-accent:`、`--jelly-color-foreground-default:`、`--jelly-color-foreground-muted:`、`--jelly-color-border-default:`、`--jelly-font-display:`、`--jelly-font-text:`、`--jelly-font-mono:`、`--jelly-dialog-radius:`、`--jelly-toast-radius:`；另需字串 `data-jelly-mode`、`jelly-theme-change`、`window.Jelly.toast`、`window.Jelly.confirm`、`jellyToast`；不得再有 `class="toasts"`、`.toast-dot`、`toastRoot`。
- 設定頁 `id` → `name` 對照表（見契約表）；每個開關要有非空 `label`；伺服器輸出不帶 `checked`。
- 文章列表分段選擇：`name="status"`、`value="all"`、非空 `label`、`<jelly-segment>` 的 `value` 依序 `all,published,draft,pending`，每段有文字。
- `Jelly.confirm` 的 DOM 約定：`<jelly-dialog data-jelly-confirm>`，確認鈕 `button[data-jelly-confirm]`、取消鈕 `button[data-jelly-cancel]`、輸入欄 `input[data-jelly-confirm-input]`；回傳後元素從 DOM 移除；`tone: 'primary'` 初始 focus 在確認鈕、`'danger'` 在取消鈕、有 `requireText` 在輸入欄。
- `Jelly.toast` 的 DOM 約定：通知在 `jelly-toaster` 的 shadow 內，類名 `.toast`；動作按鈕是 `.toast` 內的 `<button>`；成功通知 4 秒內仍在、約 4.5 秒後消失；錯誤通知 4.5 秒後仍在；`window.jellyToast` 仍是函式。
- 舊通知容器 `.toasts`、`.toast-dot` 不得留在頁面。
- 分段選擇的 `value` 屬性隨選取更新；`jelly-segmented button[role=radio]` 是上游內部按鈕（驗收腳本以此點擊）。
- 排隊測試：腳本把 `/admin/assets/jelly-ui/jelly.js` 延遲 2.5 秒，要求 `Jelly.toast` 在 `customElements.get('jelly-toaster')` 還是 undefined 時就已存在並可呼叫。
- 暗色測試：`page.emulateMedia({ colorScheme })` 切換後 `data-jelly-mode` 在 5 秒內變成 `light`／`dark`，且 `<jelly-dialog>` 的 `.dialog` 背景色不同。
- 驗收腳本把 `/favicon.ico` 與 `/support.js` 的 404 視為已知缺口，不算失敗；其餘 4xx、5xx、失敗請求、被封鎖的外部請求、`pageerror` 都算。
- 整合測試的公開頁面檢查不得出現字串 `jelly-ui`、`jelly.js`、`<jelly-`（所以測試資料的使用者與文章不要取名含 `jelly-ui`）。
- `GET /api/admin/payment/gateways` 目前在同一個 `Option` 變數上重複查詢，回傳的 `config` 是空的（既有缺陷，不在本工作單）；`TestSettingsPayloadsStayTheSame` 因此直接讀資料庫的 `options` 表驗證。

### 實作者的判斷邊界

可以自己決定：

- token 對接的實際值、陰影與間距、深色模式下的微調（只要上面的宣告都在、板與實機看起來一致）。
- `isOn`／`setOn` 的命名與放哪裡；`Jelly.confirm` 內部如何建 DOM、如何做焦點陷阱以外的細節；包裝層的內部實作（佇列、計時），只要契約與驗收腳本通過。
- 各頁確認對話框的標題與內文文案（沿用既有字串；缺的按該頁既有語言補，並同步 `web/locales/en.json` 與 `zh-TW.json`）。
- 行動版（窄螢幕）通知條要不要拉滿寬（現有行為是底部滿寬；上游預設在底部靠右；可用 `jelly-toaster` 的外部 CSS 調整）。
- `jelly-ui.sh` 的暫存目錄與 `npm` 參數（要保持 `npm ci` 與鎖定檔）；`VERSION` 的額外說明行。

必須回報、不得自行決定：

- 上游 commit 要不要換掉本工作單建議的 `1b775385b17884ecb48374615c96ec6ee1b58d21`（發版前升版是 release checklist 的事，不是實作者的）。
- 打包後 `jelly.js` 若含任何 `http(s)://` 載入行為（不是 `xmlns`），或大小與 issue 記載的約 318 KB 差很多。
- 要不要把 `menus`、`taxonomy`、`revisions` 的 `.toggle-switch` 一起換掉、要不要把文章編輯器的狀態單選換成分段選擇（決策 11，列為後續）。
- 任何需要修改驗收測試或驗收腳本才能通過的情況。
- 上游在 `Jelly.toast` 包裝層依賴的內部類名（`.toast`、`.dot`、`.toast-text`、`.close`）與本工作單所述不符。
- 需要新增 CSP、新增 Node 到 Dockerfile 或 CI 的任何想法。
