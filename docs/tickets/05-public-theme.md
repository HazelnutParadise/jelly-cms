# 05 — 訪客可以照設計稿瀏覽網站：首頁、文章總覽、單篇、頁面、搜尋、404、維護模式

Epic：前台主題
User Story：訪客在桌機與手機上看到的首頁、文章總覽、單篇文章、靜態頁面、搜尋結果、404 與維護頁，和設計稿一致；私密、排程、回收桶內容不會外洩；頁面不從任何外部網址載入資源。
Blocked by：04 的分類標籤、選單、留言（已合併）。
Status：程式實作與本機驗收完成，CI 與合併結果見 PR #23；線上畫布依使用者指示交由 Claude 同步，尚未回報。
設計稿：`design/project/Main、PostList、Post、Page、Search、NotFound、Maintenance、MobileHome.dc.html`（Maintenance 的開關在 `SettingsRoutes.dc.html`）

## 流程與驗收

- [x] 訪客在 `/` 看到墨色 hero（站名、標語、搜尋）、三格統計（文章數、作者數、開站年份）、最新 6 篇（第一篇橫跨兩欄、標「主打」）、薄荷色標語帶（標語＋網站描述）、三欄頁尾。
- [x] 訪客在 `/posts` 看到「文章 · 第 N 頁，共 M 頁」、分類篩選 chip（全部＋前 6 個分類與篇數，點了到 `/c/<slug>`）、每列大日期、分類點、封面縮圖、閱讀分鐘；每頁 10 篇，超出範圍回 404，`?page=0` 或非數字當第 1 頁。
- [x] 訪客在 `/post/<slug>` 看到墨色標題區（分類 chip、日期、作者 · 閱讀分鐘、標題、摘要）、內文、右側黏性目錄（3 個以上標題才出現，手機收成可展開區塊）、分享、作者卡（顯示名稱、簡介）、上一篇／下一篇、標籤列、留言區。
- [x] 訪客在 `/<slug>` 只會拿到 `type=page`、已發布、公開、非排程的頁面；文章別名、草稿、私密、回收桶都回 404；`/about` 沒有頁面時回 404，不再憑空生成。
- [x] 訪客在 `/search?q=` 看到黃色搜尋區、「找到 N 篇文章」、標題與摘要片段中的關鍵字以 `<mark>` 標示、沒有結果時顯示「沒有找到「關鍵字」」卡片與到 `/posts` 的連結；比對純文字（去 HTML 標籤）、`%` `_` 會跳脫、`<script>` 不會原樣輸出；每頁 10 筆、最新優先。
- [x] 訪客打錯網址看到主題 404 頁：標籤「找不到頁面」、大號 404、搜尋表單、「回首頁」「所有文章」；使用主題自訂顏色；`/api/*` 仍回 JSON。
- [x] 站長開啟維護模式後，訪客拿到 503 與 `Retry-After`，頁面顯示站長訊息、開始時間與已過分鐘、「重新整理」「管理員登入」；已登入的 admin／editor 照常看到網站；`/login`、`/auth/*`、`/api/auth` 與其子路徑、`/admin*`、`/api/admin*`、`/install*`、`/uploads/*` 不受影響；登入頁必需的精確路徑 `/api/oauth/status` 也保持可讀，其餘 `/api/*` 回 JSON 503。
- [x] 手機（390px）頁首是品牌＋搜尋鈕＋選單鈕，兩顆按鈕有 `aria-expanded` 與 `aria-controls`，展開後顯示導覽與搜尋；hero 與卡片依 MobileHome 板縮排。
- [x] 每一個前台頁面與後台頁面都不從外部網址載入腳本、樣式、字型、圖示：Bricolage Grotesque 與 Noto Sans TC 以 woff2 子集自帶，附 LICENSE 與 VERSION，`scripts/vendor/fonts.sh` 可重現。
- [x] `/`、`/posts`、`/post/<slug>`、`/<slug>`、`/search` 全部改用同一個公開範圍：已發布、公開、`published_at` 不在未來、未回收。

## 錯誤與邊界

未知別名 404、超出頁數 404、空搜尋字串顯示空狀態（200）、維護中 503、`/api/*` 永遠 JSON。閱讀分鐘以字元數估算（中日韓 400 字／分、拉丁 200 詞／分，最少 1）。目錄 id 由標題文字產生，重複時加 `-2`、`-3`。沒有 `published_at` 的舊文章以 `created_at` 排序與顯示。

## 契約（骨架與測試共同依據）

### 路由與資料

一般公開頁面經 `tm.RenderWithDB`，帶 `SiteTitle`、`Title`、`CurrentPath`（給選單 `Active`）；獨立維護文件經 `tm.RenderPageWithDB`。可見性共用 `handlers_archives.go` 的 `publicPostVisibility`，文章彙整使用限定 `type=post` 的 `archivePublicPosts`，靜態頁另限定 `type=page`。

| 路由 | 模板 | 資料鍵（型別） |
|---|---|---|
| `GET /` | `index.html` | `IsHome bool`、`Posts []core.Post`（最多 5 篇，與 `Featured` 合計最多 6 篇，Author／Categories 預載）、`Featured *core.Post`（置頂優先，否則最新；不在 `Posts` 重複）、`Stats publicSiteStats{PostCount, AuthorCount int64; SinceYear int}`、`Intro string`（選項 `site_description`） |
| `GET /posts` | `posts.html` | `Posts`、`Page`、`TotalPages`、`Total int64`、`PageSize int`（10）、`Categories []core.Category`（頂層可見分類，含 `PostCount`，最多 6） |
| `GET /post/:slug` | `post.html` | `Post`（Author／Categories／Tags 預載，`Post.Content` 已注入標題 id）、`ReadingMinutes int`、`TOC []theme.TOCEntry{ID, Text string; Level int}`、`Prev, Next *core.Post`、`AuthorCard authorCard{Name, Bio, AvatarURL, Username string; PostCount int64}`、`Comments`、`CommentCount`、`CommentsOpen`（既有） |
| `GET /:slug` | `page.html` | `Page core.Post`、`IsPage bool`、`Content template.HTML` |
| `GET /search?q=&page=` | `search.html` | `Query string`、`Results []searchResult{Post core.Post; Title, Snippet template.HTML}`、`Total int64`、`Page`、`TotalPages` |
| 未知路徑 | `error.html` | `Error ErrorPage`（既有，改走 `RenderWithDB`） |
| 維護中 | `maintenance.html` | `Message string`、`StartedAt time.Time`、`ElapsedMinutes int`、`RetryAfter int`、`SiteTitle` |

### 新增 Go 契約

- `internal/server/handlers_public.go`：`publicStats(db) (publicSiteStats, error)`、`publicPostBySlug(db, slug) (core.Post, error)`（公開範圍，`gorm.ErrRecordNotFound` → 404）、`prevNextPosts(db, post) (prev, next *core.Post, err error)`、`authorCardFor(db, user) authorCard`。
- `internal/server/search.go`（新檔）：`searchPublicPosts(db, query string, page, size int) ([]searchResult, int64, error)`；`plainText(html string) string`；搜尋使用純文字字面比對，`%` 與 `_` 不作為萬用字元。
- `internal/server/maintenance.go`（新檔）：選項 `maintenance_enabled`（"true"／"false"）、`maintenance_message`、`maintenance_started_at`（RFC3339）；`maintenanceMiddleware(tm) echo.MiddlewareFunc` 在 `RegisterRoutes` 裝在公開路由前；`maintenanceBypass(path string, user *core.User) bool`；`setMaintenance(db, enabled bool, message string) error` 開啟時寫入開始時間、關閉時清除；後台設定頁的開關在工作單 08 系統（`SettingsRoutes` 板），本工作單只提供 `POST /api/admin/settings` 已能寫這三個選項的事實與 API 測試。
- `internal/theme/manager.go` 模板函數：`readingTime(html string) int`、`highlight(text, query string) template.HTML`（先跳脫再包 `<mark>`，不分大小寫，依既有驗收測試標示第一個命中位置）、`snippet(html, query string, n int) template.HTML`（取關鍵字前後各 n 個字元，前後加「……」）、`headingIDs`（Go 函式 `HeadingIDs(html string) (string, []TOCEntry)` 為 h2／h3 加 id；模板版 `headingIDsForTemplate` 回 `map[string]any{"HTML", "TOC"}`，因為模板函數只能回一個值）、`flavor(name string) string`（分類顏色名 primary／secondary／accent／warning／ink → `--primary` 等 CSS 變數名，未知回空字串）。Go 名稱匯出為 `ReadingTime`、`Highlight`、`Snippet`、`HeadingIDs`、`Flavor`，`TOCEntry` 定義在 `internal/theme`。另新增 `(*Manager).RenderPage(name string, data any) ([]byte, error)`：只解析該檔與 `_*.html`、不套 `layout.html`，給 `maintenance.html` 這類獨立文件用；`RenderPageWithDB(name, data, db)` 同時載入主題設定。
- `internal/core/domain.go` `User` 新增 `DisplayName string`（`size:120`）、`Bio string`（`type:text`）；`internal/data/db.go` AutoMigrate 自動加欄；個人帳號頁的編輯在工作單 06（帳號）。
- `web/themes/default/theme.json`：不新增欄位；標語帶的段落讀選項 `site_description`。
- 字型：`web/themes/default/assets/fonts/` 與 `web/admin/assets/fonts/` 放 woff2 子集與 `LICENSE`、`VERSION`；`scripts/vendor/fonts.sh` 下載原始字型、以 `pyftsubset` 依 `scripts/vendor/fonts-charset.txt` 子集化；`layout.html`、`login.html`、`install.html`、主題 `layout.html` 的 Google Fonts `<link>` 全部移除，改 `@font-face`。

### 模板

- `web/themes/default/layout.html`：手機頁首兩顆切換鈕、`CurrentPath` 作用中狀態、字型改自帶、移除外部 `<link>`。
- `index.html`、`posts.html`、`post.html`、`page.html`、`search.html`、`error.html` 依對應板重寫；新增 `maintenance.html`（獨立文件，不用 `layout`）。
- 設計同步：Post 板補上標籤列與留言區入口（程式已有），Maintenance 板標題移除「尚未實作」，canvas 重新發佈。

## 我先決定了這些

- 「主打」＝置頂文章，沒有就是最新一篇。
- 文章總覽的分類 chip 直接連到 `/c/<slug>`，不另做 `?category=` 篩選。
- 開站年份取第一篇公開文章的年份，沒有文章就用今年；統計帶固定顯示。
- 標語帶大字用主題 `tagline`，段落用既有選項 `site_description`，兩者皆空就不顯示整條帶。
- 作者卡的顯示名稱與簡介欄位在本工作單加進 `User`，編輯介面留給帳號工作單 06；沒填就顯示使用者名稱、不顯示簡介。
- 搜尋最新優先、每頁 10 筆；`/about` 沒頁面就 404。
- 維護模式 admin 與 editor 可繞過，`Retry-After` 預設 600 秒，開啟時自動記開始時間，已過分鐘由伺服器算、不做即時計數。

## 分段實作與驗收

每段先由測試代理照本文件建立失敗案例（已隨本工作單交付），實作代理依契約實作，主代理審查後以真實伺服器與 Playwright 驗收。

1. B1 公開範圍與資料：`handlers_public.go` 的五條路由改用 `archivePublicPosts`、新資料鍵、`User` 新欄位。測試檔 `public_theme_integration_test.go`（Home／Posts／Post／Page 段）。
2. B2 搜尋：`search.go` 與模板函數 `highlight`、`snippet`、`plainText`。測試檔同上（Search 段）與 `internal/theme/funcs_test.go`。
3. B3 維護模式：`maintenance.go`、中介層、`maintenance.html`。測試檔 `maintenance_integration_test.go`。
4. B4 模板與字型：七個模板依板重寫、手機切換鈕、字型自帶與 `scripts/vendor/fonts.sh`、移除所有外部資源。測試 `TestPublicLayoutLoadsNoExternalAssets`、`TestAdminLayoutLoadsNoExternalAssets`；驗收以 Playwright 在 1280／390、亮／暗截圖對照板，並以 `route.abort()` 封鎖外部網域確認每頁完整。
5. B5 設計同步：Post 板、Maintenance 板標題、canvas 發佈。

## 本批驗收結果

- 完整 PostgreSQL 競態測試、go vet、gofmt、原生／Windows／macOS 編譯通過；最後的搜尋日期調整另重跑搜尋測試通過。
- 主代理以真實伺服器執行 251 項 Chromium 驗收，全部通過；涵蓋 1280px／390px、亮／暗、外部資源封鎖、字型載入、搜尋分頁、手機目錄定位、日期間距與維護模式。
- 主代理實際送出訪客留言，畫面顯示待審核，專用資料庫回查只有一筆 pending。
- Main、Post、Search、MobileHome、Maintenance 本機板與程式在同批提交更新；Maintenance 已由腳本重新產生並逐字比對。
- [ ] 線上畫布同步：依使用者分工交由 Claude，在 [PR 留言](https://github.com/HazelnutParadise/jelly-cms/pull/23#issuecomment-6370797119) 回報完成版本；尚未確認。
