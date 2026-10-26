# 09 — 作者可以用專業的 Markdown 編輯器寫作：滿版、專心、版面模式、動效與互動元件、匯出；內容語法由伺服器統一渲染與淨化

Epic：編輯與發布
User Story：作者在一個比 WordPress 好用的編輯器裡寫 Markdown：有大綱、斜線選單、即時預覽、專心模式、自動儲存與版本；能用 `:::` 版面區塊排出多欄與英雄區、用屬性加上動效、插入內建互動元件；能寫 GFM、腳註、待辦、警示框、YAML 表格、KaTeX 數學、LaTeX 文件、HTML 區塊；能把單篇匯出成 Jelly MD、純 MD、HTML、PDF。伺服器是唯一的渲染器，所有輸出經淨化。
Blocked by：05 前台主題的 `content.css` 抽出與字型自帶（PR #23 已合併）、04 的版本（已合併）。07 只影響 `:::products` 區塊。
Status：功能與本機畫面驗收完成（完整測試、必要 CI、合併與清理紀錄見 [PR #26](https://github.com/HazelnutParadise/jelly-cms/pull/26) 及 STATUS.md，沒有部署）
設計稿：`design/project/Editor、EditorFull、EditorFocus、EditorFocus2、EditorLayout、EditorMotion、EditorExport、Widgets、Revisions、Post.dc.html`；本工作單先補：手機版編輯器（單欄）、斜線選單的三種狀態（開啟、篩選、空）、診斷列（YAML 錯誤、未知屬性）、`Widgets` 板的 `::tabs` 改成 `:::tabs`。

## 先修的安全問題（G0 第一件事）

現在 `handlers_posts.go` 把前端送來的 `content`（HTML）原樣存進資料庫，`post.html` 以 `safeHTML` 原樣輸出；`author` 角色可以存入 `<script>`。G0 把伺服器渲染與淨化接上後，任何只帶 `content` 的舊式寫入也要先過淨化（依呼叫者角色），並對既有資料跑一次重新渲染。

## 流程與驗收

- [x] 作者儲存文章時，伺服器從 `source`（Markdown）渲染出 `content`（已淨化 HTML）；請求裡的 `content` 在有 `source` 時一律忽略；只帶 `content` 的請求以呼叫者角色的政策淨化後儲存；回應附 `diagnostics`。
- [x] 支援的語法與輸出照本文件「語法表」：GFM（表格、刪除線、連結化、待辦）、腳註、自動標題 id、`> [!NOTE]` 警示框、`<!--more-->`、`:::name {attrs}` 容器（`:::` 收合最內層；`hero`、`section`、`columns`／`col`、`sidebar`、`cta`、`gallery`、`faq`／`item`、`tabs`／`tab`、`products`、`posts`）、`::name{attrs}` 單行指令（`embed`、`widget`、`counter`、`compare`、`countdown`）、`{#id .cls key=value}` 屬性、`$…$` 與 `$$…$$` 數學、```` ```table ```` YAML 表格、```` ```latex ````、```` ```html ````、程式碼高亮。
- [x] 淨化政策分兩級：`strict`（author）與 `rich`（admin／editor，另允許 `html` 區塊的允許清單元素）；兩級都不允許 `script`、`on*`、`javascript:`、`data:`、`style`、`iframe`（嵌入與元件由伺服器產生）、`form`、`svg`、`math`、`base`、`meta`、`object`、`embed`，`id`／`name` 在 `html` 區塊內剝除；伺服器產生的片段（KaTeX、嵌入、元件框）以每次渲染不同的 nonce 佔位，作者無法偽造。
- [x] 作者在編輯器看到：滿版外殼（原稿／分割／預覽、左側大綱與統計、右側設定抽屜）、斜線選單、狀態列（行列、選取字數、⌘/）、預覽 250 ms 去抖動經 `POST /api/admin/render`、離線時繼續打字並顯示「離線」、圖片貼上即上傳到 `POST /api/admin/media` 並替換佔位。
- [x] 專心模式：介面閒置 3 秒淡出、打字機模式、獨立深色切換、字數目標與今日進度、小抄面板可展開或收成一顆按鈕且每位使用者記住狀態、快捷鍵表。
- [x] 版面模式：左側調色盤插入區塊文字、中間區塊卡片來自伺服器的 `blocks[]`（行範圍）、拖曳等於搬移原稿行範圍、右側檢視器只改開頭那一行的 `{…}`；未知屬性、順序與註解原樣保留；桌面／手機預覽寬度切換。
- [x] 動效與互動元件：檢視器的 `reveal`、`hover`、`scroll`、`delay`、`dur`、`stagger`、`once` 只產生 `data-*`，由主題 `content.css` 與約 5 KB 的 `jelly-motion.js`／`jelly-components.js` 呈現，頁面用到才載入，尊重 `prefers-reduced-motion`；內建元件：分頁、摺疊、輪播、燈箱、倒數、數字跳動、前後比較。
- [x] 自訂元件（admin）：登錄頁（內建、自訂、來自外掛、草稿／已發布、用到的頁數）、單檔 HTML＋CSS＋JS 編輯、參數宣告、發布前自動檢查（無外部腳本、無 `eval`／`new Function`、大小上限、減少動態下可用）；前台以 `GET /widgets/:name/:v/frame` 在帶 CSP 的 `sandbox` iframe 內執行，`postMessage` 回報高度。
- [x] 自動儲存每 60 秒與失焦時打 `POST /api/admin/posts/:id/autosave`，只寫版本不動正式內容；重新載入時提示還原；版本比較改比 `source`。
- [x] 匯出：`GET /api/admin/posts/:id/export?format=jmd|md|html|pdf`；jmd 原樣（可加 YAML front matter）、md 依展平規則、html 獨立文件（可選 zip 含圖片）、pdf 第一版以列印樣式由瀏覽器「存成 PDF」，伺服器無引擎時回 501 `pdf_engine_unavailable`；作者不能匯出別人的文章。
- [x] 所有編輯器資產自帶（CodeMirror 6、KaTeX CSS 與字型、chroma 樣式），附 LICENSE 與 VERSION，`scripts/vendor/codemirror.sh`、`scripts/vendor/katex.sh` 可重現；`marked.min.js` 移除。
- [x] 後台與前台頁面照設計板，手機 390px 可用（單欄編輯器）。

## 錯誤與邊界

原稿上限 2 MB、容器深度 8、渲染 2 秒逾時；超過回 413／400 並附診斷。`$5 and $10` 不是數學；`$` 後緊接空白或前面是數字時不開啟。YAML 表格上限 20 欄、500 列、64 KB，壞 YAML 以程式碼呈現並附行號診斷。嵌入只允許 YouTube（nocookie）、Vimeo、Bilibili，其餘變成連結卡；遠端圖片永不由伺服器抓取。`html` 區塊在 `strict` 政策下輸出為程式碼並附診斷；作者儲存含編輯者 `html` 區塊的文章時，該區塊從 `content` 移除但 `source` 不動。`base_updated_at` 不符回 409。

## 契約（骨架與測試共同依據）

### 儲存

遷移 `internal/data/migrations/00NN_post_source.sql`（編號接在 06／07 之後）：`posts.source text`、`posts.format varchar(8)`（`html` 舊資料、`md` 舊 Markdown、`jmd` 新語法）、`posts.render_version int`、`posts.render_trust varchar(8)`（strict／rich）；`revisions.source`、`revisions.format`。回填：有 `meta.markdown` 的列 `source` 取之、`format='md'`；只有 HTML 的列 `format='html'`。`content` 維持為淨化後的渲染結果；只有物件型 `meta` 的 `meta.markdown` 雙寫一個版本。既有陣列、字串、數值、布林與明確 `null` 原樣保留，原稿以 `source` 為準，避免編輯與版本還原覆蓋原有資料。`render_version` 落後的列由背景工作重新渲染（不在請求內做）。

### `internal/markup`（新套件）

```go
type Options struct{ Trust string /* strict|rich */; Expand bool; SourceMap bool; Format string /* md|jmd */ }
type Result struct{ HTML string; TOC []TOCEntry; Stats Stats; Features []string; Diagnostics []Diagnostic; Blocks []Block }
type Stats struct{ Chars, Words, Minutes, Images, Links, OpenTasks, Todos int }
type Diagnostic struct{ Line, Col int; Severity, Code, Message string }
type Block struct{ Kind, Name string; Attrs map[string]string; StartLine, EndLine int }
func Render(ctx context.Context, source string, opt Options) (Result, error)
func Flatten(source string) (string, error)          // Jelly MD → 純 MD
func Expand(ctx context.Context, html string, data DynamicData) (string, error) // :::products / :::posts
func Features(html string) []string                   // 主題用：math、motion、tabs、carousel、lightbox、widget
func Schema() SchemaDoc                                // 區塊、屬性、列舉、斜線選單項目
```

`TOCEntry` 與 05 的 `theme.TOCEntry` 同形；`HeadingIDs` 必須冪等（已有 id 的標題不再改）。數學：KaTeX 在伺服器以 QuickJS 執行（G0 先做一日驗證；失敗則改前台 auto-render，但 `markup` 契約不變，輸出同樣的 `jc-math` 元素帶 `data-tex`）。`latex` 區塊：沒有 `\documentclass` 的片段走 KaTeX 區塊；完整文件走可選的 tectonic 背景工作並以雜湊快取在 `data/cache/latex/`，沒有引擎時輸出原始碼卡。

### 語法表（原稿 → 輸出）

| 原稿 | 輸出 |
|---|---|
| `## 標題{reveal=fade-up}` | `<h2 id="標題" data-reveal="fade-up">標題</h2>` |
| `- [x] a` | `<ul class="contains-task-list"><li class="task-list-item"><input type="checkbox" disabled checked> a</li></ul>` |
| `text[^1]` | `<sup id="fnref:1"><a href="#fn:1" class="footnote-ref" role="doc-noteref">1</a></sup>` ＋ `<div class="footnotes" role="doc-endnotes">…</div>` |
| `> [!NOTE]\n> x` | `<div class="jc-callout" data-kind="note" role="note"><p class="jc-callout-title">Note</p><p>x</p></div>` |
| `<!--more-->` | `<span class="jc-more" id="more"></span>` |
| ```` ```table ````（YAML：`caption, align, columns, rows, header_col`） | `<figure class="jc-table"><table><caption>…</caption><thead>…<th scope="col" data-align="right">…</thead><tbody>…</tbody></table></figure>` |
| `$a_b$`／`$$…$$` | `<span class="jc-math" data-tex="a_b">`＋KaTeX 標記／`<div class="jc-math" data-display="block">…`；無引擎時同元素內放跳脫後的 TeX |
| ```` ```latex ````（含 `\documentclass`） | `<figure class="jc-latex" data-state="ready|source" data-paper="a4" data-hash="sha256:…">…</figure>` |
| ```` ```html ```` | rich：`<div class="jc-html">淨化後片段</div>`；strict：`<pre><code class="language-html">…</code></pre>`；要顯示 HTML 原始碼用 ```` ```html {code} ```` |
| ```` ```go ```` | `<pre class="chroma"><code class="language-go">…</code></pre>` |
| `:::hero {bg=mint align=left height=normal cubes}` | `<section class="jc-hero" data-bg="mint" data-align="left" data-height="normal" data-cubes><div class="jc-hero-inner">…</div></section>` |
| `:::columns {ratio=2:1}` ＋ `::: col` ×2 | `<div class="jc-columns" data-ratio="2:1"><div class="jc-col">…</div><div class="jc-col">…</div></div>`（`section`、`sidebar`、`cta`、`gallery` 同形） |
| `:::faq`／`::: item {title="Q"}` | `<div class="jc-faq"><details class="jc-accordion"><summary>Q</summary>…</details></div>` |
| `:::tabs`／`::: tab {title="A"}` | `<div class="jc-tabs" data-jc="tabs"><section class="jc-tab" data-title="A"><h3 class="jc-tab-title">A</h3>…</section></div>` |
| `::counter{to=1200}` | `<span class="jc-counter" data-jc="counter" data-to="1200">1200</span>` |
| `::embed{url=…}` | 允許清單：`<figure class="jc-embed"><iframe src="…" loading="lazy" sandbox="allow-scripts allow-same-origin allow-presentation" referrerpolicy="no-referrer" allowfullscreen></iframe></figure>`；其他：連結卡 |
| `::widget{name=countdown to="…"}` | `<div class="jc-widget" data-widget="countdown" data-v="3"><iframe sandbox="allow-scripts" src="/widgets/countdown/3/frame?to=…" loading="lazy" title="…"></iframe></div>` |
| `:::products {category=gift limit=3}` | 儲存 `<div class="jc-dyn" data-kind="products" data-args="…">`，請求時 `Expand` 成 `<ul class="jc-products">…` |

屬性值只接受列舉：`bg` 接受主題口味名（primary、secondary、accent、warning、ink）與別名（raspberry、blue、mint、yellow、ink）；類別只接受 `btn`、`t`、`lead`、`center`、`muted`（輸出為 `jc-*`）；未知值丟棄並附診斷。動效屬性全部輸出為 `data-*`。

### API

| 端點 | 角色 | 說明 |
|---|---|---|
| `POST /api/admin/render {source, post_id?, mode: fragment|page, sourcemap, expand}` → `{html, toc, stats, features, diagnostics, blocks, renderer}` | admin／editor／author | 每人限流、2 MB、不寫入、政策依角色 |
| `GET /api/admin/render/schema` | 同上 | 區塊、屬性、列舉、斜線選單 |
| `POST|PUT /api/admin/posts[/:id]` 接受 `source`、`format`、`base_updated_at` | 既有規則 | 回應加 `diagnostics`；衝突 409 |
| `POST /api/admin/posts/:id/autosave {title, source, excerpt}` | 擁有者或編輯 | 只寫 autosave 版本；`GET /api/admin/posts/:id` 回 `autosave`（比 `updated_at` 新時） |
| `GET /api/admin/posts/:id/revisions/diff` | 既有 | 兩邊都有 `source` 時以 `source` 比對（`basis:"source"`） |
| `GET /api/admin/posts/:id/export?format=jmd|md|html|pdf&images=1&frontmatter=1` | 擁有者或編輯 | 404 他人文章、400 格式錯、501 `pdf_engine_unavailable` |
| `GET|POST|PUT|DELETE /api/admin/widgets`、`POST /api/admin/widgets/:id/publish` | admin | 登錄、版本、自動檢查 |
| `GET /widgets/:name/:v/frame?…` | 公開 | `Content-Security-Policy: sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'`；參數依宣告驗證 |

### 前端與資產

CodeMirror 6（state、view、commands、language、lang-markdown、search、autocomplete、lezer）建置到 `web/admin/assets/codemirror/`；KaTeX CSS 與 woff2 到 `web/vendor/katex/`（後台與主題共用）；chroma 樣式進 `content.css`；`web/admin/assets/sortable.js` 從分類與選單頁抽出；`jelly-motion.js`、`jelly-components.js` 放主題 `assets/`。編輯器共用元件 `web/admin/editor.html`（`{{define "editor"}}`）供 `post_editor.html`、`page_editor.html` 使用。

### 匯出規則

jmd 原樣；md 依 AST 行範圍展平（欄位→連續段落、products→連結清單、`table`→管線表格、警示框→`> **Note**`、tabs／faq→標題、屬性與動效剝除、元件與嵌入→連結、`html`／`latex` 保留為圍欄、`$…$` 保留）；html 獨立文件內嵌 `content.css` 與 KaTeX CSS，`images=1` 時 zip 並把 `/uploads/*` 改成相對路徑；pdf 第一版用列印樣式交給瀏覽器。

## 我先決定了這些

- 伺服器是唯一渲染器；前台與後台都不再帶 Markdown 函式庫，`marked` 移除。
- KaTeX 先試伺服器端 QuickJS；不行就前台 auto-render，契約不變。
- `latex` 完整文件走可選 tectonic 背景工作，否則原始碼卡；PDF 第一版用瀏覽器列印。
- 版面模式只搬動原稿行範圍，保證無損；衝突用 `base_updated_at`。
- 單行指令用 `::name{}`、容器用 `:::name`；口味名接受主題名與 Jelly 別名；`html` 區塊只有 rich 政策會渲染，顯示原始碼用 ```` ```html {code} ````；舊的 `meta.markdown` 文章標成 `format=md`，其中 ```` ```html ```` 維持程式碼語意。
- 作者可用已發布的元件與允許清單內的嵌入；`html` 區塊永遠不允許 `style`。
- G4 的互動元件只做分頁、摺疊、輪播、燈箱、倒數、數字跳動、前後比較；投票、Lottie、Rive、地圖延後。
- 「待補」＝未勾選的待辦加 `TODO` 標記；每日目標存瀏覽器本機；預覽切換 ⌘E、深色切換 ⌘⇧L，避開瀏覽器內建快捷鍵。
- 先合併 05（`content.css` 與字型），本工作單的主題側才動手。

## 分段實作與驗收

每段先由測試代理照本文件建立失敗案例（已隨本工作單交付），實作代理依契約實作，主代理審查後以真實伺服器與 Playwright 驗收。

1. G0 淨化與 `markup` 核心：遷移與回填、`Render` 全部語法、兩級政策、KaTeX 驗證、`handlers_posts.go` 改走伺服器渲染、既有資料重新渲染工作。測試：`internal/markup` 黃金檔與模糊測試、`sanitizer_test.go` 矩陣、`post_render_integration_test.go`。
2. G1 編輯器外殼：CodeMirror、滿版、分割預覽、大綱、斜線選單、狀態列、自動儲存、圖片貼上。測試：`render_api_integration_test.go`、`autosave_integration_test.go`、Playwright `design/tools/editor.spec.mjs`。
3. G2 專心模式。
4. G3 版面模式：`blocks[]`、搬移與檢視器只改開頭行。測試：`layout_roundtrip_test.go`。
5. G4 動效與互動元件：`content.css`、`jelly-motion.js`、`jelly-components.js`、`features`。
6. G5 匯出：`Flatten`、HTML 獨立文件、zip、列印 PDF。測試：`export_test.go`、`export_integration_test.go`。
7. G6 自訂元件與 `latex`：登錄、自動檢查、frame 端點與 CSP、tectonic 工作。測試：`widgets_integration_test.go`。
8. G7 驗收：真實伺服器寫一篇含所有語法的文章、三種模式操作、匯出四種格式、前台渲染與無外部資源檢查。
