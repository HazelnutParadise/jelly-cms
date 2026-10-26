# 10 — 訪客和站長在前台主題上遇到的 13 個落差都修好：搜尋有上限與 trim、404 是中文、上一篇／下一篇樣式正確、維護模式擋得住編碼路徑、HEAD 可用、字型一份並可快取、版面與對比對齊設計稿

Epic：前台主題（工作單 05 的驗收後續）
User Story：訪客搜尋時不會被超長字串拖慢網站、空白字詞不會列出全站文章；訪客打錯網址看到中文的 404；讀者在最新或最舊一篇文章看到方向正確的「上一篇／下一篇」；站長開維護模式時，編碼過的 `/admin` 變體不會漏出 404 而是 503；搜尋引擎與監控工具用 HEAD 檢查前台頁面得到和 GET 相同的狀態；訪客在桌機與手機上看到的首頁、文章總覽、單篇、靜態頁、搜尋與維護頁，和設計稿在間距、裝飾線、對比上一致。
Blocked by：05（前台主題，PR #23 已合併）。無其他前置工作單。
Status：實作與本機功能驗收完成。完整測試、必要 CI 與合併紀錄見 [PR #29](https://github.com/HazelnutParadise/jelly-cms/pull/29)。
來源：GitHub issue HazelnutParadise/jelly-cms#28（對 `main` `3aa5633` 的獨立驗收，13 項，沒有 blocker）。
設計稿：驗收基準是 `design/project/` 的 `Main`、`MobileHome`、`PostList`、`Post`、`Page`、`Search`、`NotFound`、`Maintenance` 各板。版面與間距以板為準、不改板；只有板本身對比不足 AA 的幾個色值要微調，並在同一次提交把板改成同一個色值，見〈決策紀錄〉第 12 條。

## 與已合併工作單的整合

- 工作單 08（PR #27 已合併至 main `c954b88`，本分支已整合）：
  - 維護頁讀取已上線的選項鍵 `site_timezone`，空值或無法解析時退回伺服器時區（`time.Local`）。
  - 08 在 `handlers_public.go` 的 `/:slug` 保留字清單與 `routes.go` 有改動，並新增 `/robots.txt`、`/sitemap.xml` 等 GET 路由；本單只要求「列出的公開頁面」接受 HEAD，08 的新路由不在本單範圍，保留既有行為。`routes.go` 與 `web/locales/*.json` 已與 main 整合。
- 工作單 06、07、09（PR #24、#25、#26）：它們動到 `web/locales/*.json`、主題模板檔頭幾行與 `web/themes/default/assets/` 的新檔。本單刪的是 `web/themes/default/assets/fonts/`，**不是整個 `assets/` 目錄**（09 會在那裡放 `jelly-components.js`、`jelly-motion.js`）。檔案層級衝突由後合併者處理。
- 工作單 05 的文字（字型兩份、`maintenanceBypass` 以解碼路徑判斷等）被本單取代；05 不修改。

## 流程與驗收

每條都是「誰可以做什麼」。標 **(測試)** 的由 Go 測試檢查，標 **(瀏覽器)** 的由主代理以真實伺服器加 Playwright（Chromium，1280 與 390 寬、亮與暗）驗收，條件在〈瀏覽器驗收條件〉。

### 應修（issue 項目 1–5）

- [x] 訪客搜尋時，`/search` 與 `/api/search` 對字詞最多 100 個字（以 rune 計，先 trim 再算）；超過時 `/api/search` 回 400 與錯誤碼 `query_too_long`，`/search` 回 400 並照搜尋頁版型顯示「搜尋字詞最多 100 個字」（i18n，繁中與 English）。10 萬字元的字詞在 1 秒內被拒絕。**(測試)**
- [x] 訪客搜尋時，網站對字詞只 fold 一次，並先用 SQL（參數綁定，`%` 與 `_` 維持字面比對）篩出候選再做高亮與摘要，不把全部公開文章全文載入 Go；2000 篇公開文章、每篇約 4 KB 時，100 字的 `q` 打 `/api/search` 與 `/search` 都在 2 秒內回應。**(測試)**
- [x] 訪客搜尋時，`/search` 與 `/api/search` 在入口先 `strings.TrimSpace`：`q=果凍%20` 與 `q=果凍` 命中數相同、輸入框與標題只回顯 trim 後的字詞；只剩空白等同空字串（`/search` 顯示空狀態而不是列出全部文章；`/api/search` 與 `q=` 的回應完全相同）。**(測試)**
- [x] 訪客打錯網址，繁中語系的 404 頁內文是設計板 NotFound 的「這一塊果凍掉到桌子底下了。網址可能打錯，或者文章已經被移走。」，頁面任何位置都不出現英文 "Not Found"；English 語系顯示對應的英文句子；`/api/*` 的 404 仍是 JSON `{"error":"Not Found","code":404}`。文字走 i18n 鍵 `errorNotFoundBody`。**(測試)**
- [x] 讀者在單篇文章底部看到方向正確的上一篇／下一篇：連到較舊文章的連結帶 `post-nav-prev`，連到較新文章的帶 `post-nav-next`，樣式用這兩個類名決定、不靠 `:last-child`；只有一個鄰篇時它仍套自己方向的樣式（最新一篇只有「上一篇」，仍是淺色靠左卡片，不變成深色靠右的「下一篇」樣式）。**(測試)** 加 **(瀏覽器)** B9。
- [x] 訪客在首頁、單篇、搜尋看到 hero 緊貼頁首（首頁依板留 16px；單篇與搜尋 0）；首頁統計帶貼近 hero（桌機 8px、手機 6px）；文章總覽、靜態頁、搜尋頁的斜線裝飾只畫一次；手機首頁 hero 的高度與內容跟 `MobileHome` 板一樣精簡，沒有搜尋框與大面積圖形。斜線只畫一次 **(測試)**；其餘 **(瀏覽器)** B1、B2、B7。

### 小修（issue 項目 6–13）

- [x] 訪客在深色模式看搜尋結果，`<mark>` 有明確的文字色（不繼承灰字），摘要對比至少 4.5:1；黃色搜尋區的小標「搜尋」至少 4.5:1。`<mark>` 有明確文字色 **(測試)**；數值 **(瀏覽器)** B4。
- [x] 訪客看首頁統計帶（藍、青、黃三格）、青色標語帶、主色按鈕（白字）時，一般字對比至少 4.5:1（預設主題色，亮與暗）。**(瀏覽器)** B4。
- [x] 站長開啟維護模式後，匿名訪客請求 `/%61dmin`、`/admin%2fx` 這類編碼路徑得到 503 與 `Retry-After`，不是 404；`/login`、`/auth/*`、`/api/auth*`、`/admin*`、`/api/admin*`、`/install*`、`/uploads/*`（含 `/uploads/%E6%96%87.png` 這種編碼檔名）、`/admin/assets/*`、`/api/oauth/status` 照舊可達。**(測試)**
- [x] 訪客在維護頁看到：1280 寬時「維護開始於…」不疊在圖形上、兩顆按鈕有板上的圖示、開始時間用伺服器時區顯示（網站時區的接法見上節）。**(瀏覽器)** B3；時區不寫測試。
- [x] 部署者只保留 `web/admin/assets/fonts/` 一份字型，`web/themes/default/assets/fonts/` 刪除；`/admin/assets/fonts/*` 回應帶 `Cache-Control: public, max-age=2592000`；其他資產的快取標頭不變。**(測試)** 加 **(瀏覽器)** B10。
- [x] 讀者看文章目錄時，`data-toc-level="3"` 的項目縮排，文字對比至少 4.5:1。縮排 **(測試)**；視覺與對比 **(瀏覽器)** B8。
- [x] 訪客在文章總覽看到：分類點與分類名稱同一行、篩選 chip 在標題列右側（桌機）、沒封面的文章有占位縮圖。占位元素與同行 **(測試)**；版位 **(瀏覽器)** B5。
- [x] 訪客在靜態頁的右側欄看到「快速連結」面板（所有文章、搜尋站內）。容器與連結 **(測試)**；版位 **(瀏覽器)** B6。
- [x] 搜尋引擎或監控以 HEAD 請求首頁、文章列表、單篇、靜態頁、分類／標籤／作者／封存（索引與月份）、搜尋，得到與 GET 相同的狀態（含 404 與維護中的 503），回應沒有本文。**(測試)**

### 不得退步（已確認無誤的既有行為）

- [x] XSS 全部逸出；`%`、`_` 字面比對且 SQL 參數綁定；分頁參數不 panic；維護模式不可繞過、不洩漏草稿；草稿、私密、排程、回收桶全部不出現；零外部請求；390 寬沒有水平溢位。既有測試 `TestPublic*`、`TestMaintenance*`、`TestArchives*`、`TestVendoredFontsPresent`、`TestPublicLayoutLoadsNoExternalAssets`、`TestAdminTemplatesLoadNoExternalAssets` 與 `internal/theme` 套件測試照樣通過。

## 項目對照表（issue 項目 → 測試或驗收條件）

| issue 項 | 內容 | 測試（`internal/server/`） | 瀏覽器條件 |
|---|---|---|---|
| 1 | 搜尋長度上限、fold 一次、SQL 篩選、2000 篇效能 | `TestFollowupSearchQueryLengthLimitOnPage`、`TestFollowupSearchQueryLengthLimitOnAPI`、`TestFollowupSearchLargeCorpus`；守門：`TestFollowupSearchStillMatchesVisibleTextCaseInsensitively`；`TestFollowupLocaleKeys`（`searchQueryTooLong`） | — |
| 2 | 搜尋 trim | `TestFollowupSearchTrimsQueryOnPage`、`TestFollowupSearchWhitespaceOnlyShowsEmptyState`、`TestFollowupSearchAPITrimsQuery` | — |
| 3 | 404 中文內文 | `TestFollowup404BodyIsLocalized`、`TestFollowupLocaleKeys`（`errorNotFoundBody`） | — |
| 4 | 上一篇／下一篇 | `TestFollowupPostNavLinksCarryTheirDirection`、`TestThemeCSSPostNavUsesDirectionClasses` | B9 |
| 5 | hero 白縫、統計帶間距、斜線只畫一次、手機首頁 hero | `TestFollowupStripeDecorationDrawnOnce`（斜線） | B1、B2、B7 |
| 6 | 深色 `<mark>`、黃區小標對比 | `TestThemeCSSMarkHasExplicitTextColor` | B4 |
| 7 | 青／黃統計格、標語帶、主色按鈕對比 | — | B4 |
| 8 | 維護模式編碼路徑 | `TestFollowupMaintenanceEncodedPathsAreNotBypassed`；守門：`TestFollowupMaintenanceBypassMatchesEscapedSegments`、`TestFollowupHeadDuringMaintenanceIs503` | — |
| 9 | 維護頁灰字疊圖、按鈕圖示、時區 | —（時區決定不寫測試） | B3 |
| 10 | 字型一份、字型 Cache-Control | `TestThemeFontsKeepOnlyTheAdminCopy`、`TestFollowupAdminFontsAreCacheable`；既有 `TestVendoredFontsPresent` 改為只檢查 admin 一份 | B10 |
| 11 | 目錄 h3 縮排 | `TestThemeCSSTocLevelThreeIsIndented` | B8 |
| 12 | 列表分類點同行、篩選 chip 在右、占位縮圖；靜態頁快速連結 | `TestFollowupPostListCategoryDotSharesRowWithCategory`、`TestFollowupPostListCoverPlaceholder`、`TestFollowupStaticPageHasQuickLinksPanel` | B5、B6 |
| 13 | HEAD | `TestFollowupPublicPagesAcceptHead`；守門：`TestFollowupHeadDuringMaintenanceIs503` | — |

## 契約

### 搜尋（`internal/server/search.go`、`handlers_public.go`）

- 入口：`query := strings.TrimSpace(c.QueryParam("q"))`（`TrimSpace` 含全形空白 U+3000）。之後所有計算、回顯、分頁連結都用 trim 後的字詞。
- 長度：`utf8.RuneCountInString(query) > 100` 為過長。判斷在任何資料庫查詢與 fold 之前，且不得先把整段 `q` 複製、正規化。
- `/search` 過長：HTTP 400，回應是 `search.html` 版型（含 `id="search-query"` 輸入框、黃色搜尋區），「搜尋字詞最多 100 個字」放在搜尋區內、沒有結果列表；輸入框與標題**不回顯**過長字詞（輸入框 value 留空）。`/search` 的狀態碼只在這個情況為 400，其他維持 200／404。
- `/api/search` 過長：HTTP 400，`Content-Type: application/json`，本文 `{"error":"<英文訊息>","code":"query_too_long"}`。`error` 沿用現有 `/api/search` 的 `{"error": "..."}` 形狀；`code` 是新增的字串鍵，**只有這個錯誤帶 `code`**，成功回應的鍵維持 `query`、`posts`、`count` 三個。`query` 欄位回顯 trim 後的字詞。
- 空字串與只剩空白：`/search` 顯示空狀態（200，沒有「找到 N 篇文章」、沒有「沒有找到」卡片）；`/api/search` 與 `q=` 的回應逐字相同（現況是 400 `{"error":"Query parameter 'q' is required"}`，測試用「與 `q=` 相同」釘住，不釘死那個 400；見〈實作者的判斷邊界〉）。
- 執行：字詞只 fold 一次（`cases.Fold()` 對 `query` 做一次，重用結果）。以 SQL 篩候選（`ILIKE`／`strpos` 搭配參數綁定；`%` 與 `_` 要跳脫成字面；或另存純文字欄位；做法由實作者決定），再在 Go 裡對候選做純文字確認、高亮、摘要。總數與分頁必須和篩選前的語意一致（比對純文字、去 HTML 標籤、標籤屬性不命中、`&amp;` 這類實體以顯示文字比對、不分大小寫）。
- 若新增欄位或索引，資料遷移編號從 **0024** 起（0001–0023 已被 main 占用），遵守 db-engineering 的遷移規則，且既有文章要回填。
- 效能上限：2000 篇、每篇約 4 KB、字詞 100 字：兩個端點各 ≤ 2 秒（寬鬆上限）；一次請求從 `posts` 表讀進 Go 的列數 ≤ 200。

### 404 文字（`errors.go`、`error.html`、`web/locales/*.json`）

- 新鍵 `errorNotFoundBody`：`zh-TW` 必須逐字等於「這一塊果凍掉到桌子底下了。網址可能打錯，或者文章已經被移走。」（取自 `design/project/NotFound.dc.html` 的段落）；`en` 的句子由實作者擬（建議：「This piece of jelly rolled under the table. The address may be mistyped, or the post has been moved.」）。
- 404 的 `ErrorPage.Message` 改為此鍵翻譯後的文字；`error.html` 的 `.error-message` 元素只含這段文字。其他狀態碼（403、500、503）本單不改。
- `wantsJSON` 為真時（`/api/*`、`/auth/*`、Accept 要 JSON）維持現有 JSON，不翻譯。

### 上一篇／下一篇（`post.html`）

- `.post-nav` 內的連結：較舊的鄰篇 `class="post-nav-prev"`，較新的 `class="post-nav-next"`（可再加其他類名，但不得缺這兩個）。CSS 用這兩個類名決定淺色靠左／深色靠右，`.post-nav` 的選擇器不得含 `:first-child`、`:last-child`、`:nth-*`、`:only-child`。只有 next 時它落在右欄（`grid-column: 2`）；手機單欄。

### 維護模式（`maintenance.go`）

- 中介層以 `c.Request().URL.EscapedPath()` 取路徑（不是 `URL.Path`）交給 `maintenanceBypass`、`isAPIPath`、`CurrentPath`；`maintenanceBypass` 以「完整段落前綴」比對：等於根或以「根 + `/`」開頭。編碼過的分隔符（`%2f`）不視為分隔。
- 維護頁時間：`StartedAt` 在模板顯示前轉為 `site_timezone` 指定時區，空值或無法解析時退回伺服器時區（`time.Local`）；顯示格式不變。

### HEAD 與靜態資產（`handlers_public.go`、`handlers_archives.go`、`routes.go`）

- 下列路由同時註冊 GET 與 HEAD（例如 `e.Match([]string{GET, HEAD}, ...)`）：`/`、`/posts`、`/post/:slug`、`/:slug`、`/search`、`/c/:slug`、`/t/:slug`、`/author/:username`、`/archive`、`/archive/:year/:month`。HEAD 的狀態碼與 GET 相同（含 404、維護 503），回應沒有本文。`/api/search` 與其他 API 不在範圍。
- `/admin/assets/fonts/*` 回應加 `Cache-Control: public, max-age=2592000`；其他 `/admin/assets/*` 與 `/uploads/*` 的標頭不變。
- 刪除 `web/themes/default/assets/fonts/`；`scripts/vendor/fonts.sh` 只輸出到 `web/admin/assets/fonts/`；`docs/release-checklist.md` 若提到兩份要一併改。

### 模板與樣式（`web/themes/default/`）

- 斜線裝飾：同一個頁首只畫一次。做法由實作者選（拿掉模板的 `.stripes` span，或拿掉 `.page-head::before`／`.article-head::before` 的繪製），但 `/posts`、靜態頁、`/search` 三頁都要剛好一個；搜尋頁的斜線依 Search 板是深色（墨色）而不是三色。
- 靜態頁右側欄：容器 `.page-quicklinks`，位於 `<aside>` 內；內容照 Page 板，固定兩個連結：「所有文章」→ `/posts`、「搜尋站內」→ `/search`，標籤「快速連結」。板上的深色拼盤方塊（果凍丁）放在面板上方。
- 文章總覽列：沒有封面（`Cover` 空且內文沒有第一張圖）的列輸出 `<div class="entry-cover-placeholder" aria-hidden="true">`，不含 `<img>`，尺寸與有封面時相同（4:3）；有封面的列只輸出 `<img class="entry-cover">`。分類點 `.entry-flavor` 放進 `.entry-cats` 的同一行，與分類名稱相鄰。
- `[data-toc-level="3"]` 的規則必須用 `padding-left`、`padding-inline-start`、`margin-left`、`margin-inline-start`、`text-indent` 其中之一給非 0 值。
- 所有 `mark` 規則都要有明確的 `color`（不得是 `inherit`、`currentColor`、`unset`、`initial`、`revert`）。
- 對比修正不得改設計稿的色相：可以把前景色或背景色向墨色微調（例如主色按鈕背景混入約 5–10% 的墨色），目標是 B4 的數值。

### 錯誤碼與格式總覽

| 情況 | 端點 | 狀態 | 本文 |
|---|---|---|---|
| 字詞過長 | `GET /api/search` | 400 | `{"error":"…","code":"query_too_long"}` |
| 字詞過長 | `GET /search` | 400 | 搜尋頁 HTML，含「搜尋字詞最多 100 個字」 |
| 空字串或只剩空白 | `GET /api/search` | 同 `q=`（現況 400） | 同 `q=` |
| 空字串或只剩空白 | `GET /search` | 200 | 搜尋頁空狀態 |
| 編碼過的受保護路徑（維護中） | 任意 | 503 | 維護頁（`/api/*` 為 JSON）加 `Retry-After` |

## 決策紀錄

1. **搜尋字詞入口 trim、以 rune 計、上限 100。** 否決「以位元組計」：中日韓每字 3 位元組，100 位元組只剩 33 字，對正常搜尋太嚴。否決「超過就截斷」：截斷後的結果會和使用者以為的搜尋不一致，也讓 `query` 回顯與實際比對不同；改 400 讓前端與客戶端看得到。
2. **fold 只做一次，先用 SQL 篩候選。** 原因：issue 實測 18 秒來自對每篇每欄位重做 `Fold`，並為算總數掃完全部全文。否決「只加長度上限就好」：長度上限擋不住 2000 篇乘 4 KB 的全表掃描，測試以「讀進 Go 的列數 ≤ 200」釘住。如何不漏掉實體與被標籤切開的詞（`&amp;`、`甜<b>果</b>`），由實作者在純文字欄位或寬鬆篩選兩者中選。
3. **`/api/search` 過長的錯誤碼放在新增的字串鍵 `code`。** main 的 `/api/search` 錯誤只有 `{"error": …}`；全域錯誤處理的 `code` 是整數狀態碼，兩者不同。為了不破壞成功回應與其他錯誤的形狀，只有 `query_too_long` 多帶字串 `code`。
4. **空白字詞的 API 行為：與 `q=` 逐字相同。** 定案文字寫「回空結果，沿用目前空字串的行為」，但 main 上 `q=` 目前回 400 `Query parameter 'q' is required`（不是空結果）。這兩句互相矛盾，測試只釘「與 `q=` 相同」；若要改成回 200 空結果，兩種輸入一起改並更新測試，請主代理決定。
5. **404 內文走 i18n 鍵 `errorNotFoundBody`，只改 404。** 否決「直接改 `error.html` 寫死中文」：English 語系會看到中文。否決「改 `http.StatusText`」：JSON 錯誤要保留狀態文字。
6. **上一篇／下一篇以方向類名決定樣式。** 否決「保留 `:last-child` 再用 `:only-child` 補丁」：每多一種組合就多一條例外。
7. **維護旁路改用 `EscapedPath()` 且精確比對段落前綴。** 否決「解碼後再比對並同時解碼路由」：Echo 以 `RawPath` 路由，兩邊不一致就是 issue 的成因。
8. **公開頁面接受 HEAD。** 否決「全域加 HEAD 中介層」：會連帶放開 `/api/*` 與管理頁；逐路由註冊範圍清楚。
9. **字型只留 `web/admin/assets/fonts/`，加 30 天快取。** 原因：只有 `/admin/assets` 有路由，主題那份從未被服務。快取只加字型，其他資產的標頭不在本單範圍（避免連帶影響 `/uploads` 與管理後台的腳本更新）。
10. **視覺落差寫成瀏覽器驗收條件，並指名對照的板、寬度與亮暗。** 能由伺服器輸出檢查的（斜線次數、快速連結容器、占位元素、分類點位置、方向類名、`<mark>` 文字色、h3 縮排）寫測試。
11. **維護頁時間採網站設定，無效時退回伺服器時區。** 工作單 08 已合併，使用 `site_timezone`，不使用資料庫連線的 `config.DatabaseConfig.Timezone`。B3 以 `TZ=Asia/Taipei` 驗證空設定的備援行為。
12. **板本身的對比不足 AA 時，色值往墨色微調到達標，並在同一次提交把對應板改成同一個色值。** 現況：`.btn.p` 白字在 #E8285B 上 4.30:1（Main、Post List、NotFound 板）；Search 板小標 #6B5300 在 #F5B800 上 4.10:1；Main 板第一格白字在 #2F6BFF 上 4.499:1。B4 列出可行的混色比例。否決「程式微調、板不動」：AGENTS.md 規定改畫面要同一次提交更新對應的板，板與程式必須一致。
13. **首頁 hero 與頁首的間距依板：1280 為 16px，390 為 0。** `Main` 板在頁首與 hero 之間有 `margin-top: 16px`，`MobileHome`、`Post`、`Search` 板是 0；issue 說的「白空隙」是目前實測的 48px（桌機）與 24px（手機）。依各板取值，而不是一律歸零。
14. **沒封面的占位縮圖不放文字。** Post List 板的占位上寫「[封面圖]」是設計稿的占位標記，不是上線文案；占位是 `aria-hidden` 的灰色 4:3 方塊。若主代理想要上線文案，另提。
15. **靜態頁快速連結是固定的兩個連結，不讀選單。** 板上看不出資料來源，兩個連結都是站內固定頁（文章總覽、搜尋）。若要改成選單驅動，請主代理決定。
16. **本單修改了一個既有測試 `TestVendoredFontsPresent`**（`assets_offline_test.go`）：刪除主題字型後，原本要求兩份都在的斷言不再成立，改為只檢查 admin 一份；「主題那份必須刪除」由新測試 `TestThemeFontsKeepOnlyTheAdminCopy` 檢查。這是本單唯一改既有測試的地方。

## 實作順序（槽位）

執行代理每槽結束時，列出的測試檔以 `JELLY_TEST_POSTGRES=1 go test ./internal/server -run '<名稱>' -count=1` 通過，並確認〈不得退步〉的既有測試仍通過。執行代理不跑 `go test ./...` 全套、不加 `-race`；主代理收尾時重跑完整競態測試與必要 CI。

1. **10-A 搜尋**（`search.go`、`handlers_public.go` 的 `/search` 與 `/api/search`、`web/locales/*.json` 的 `searchQueryTooLong`、`search.html` 的 400 提示）：`TestFollowupSearch*`、`TestFollowupLocaleKeys`（搜尋鍵）。
2. **10-B 404 文字**（`errors.go`、`error.html`、`web/locales/*.json` 的 `errorNotFoundBody`）：`TestFollowup404BodyIsLocalized`、`TestFollowupLocaleKeys`。
3. **10-E 路由與資產**（`handlers_public.go`／`handlers_archives.go` 的 HEAD、`routes.go` 的字型快取標頭、刪除主題字型、`scripts/vendor/fonts.sh`）：`TestFollowupPublicPagesAcceptHead`、`TestFollowupHeadDuringMaintenanceIs503`、`TestFollowupAdminFontsAreCacheable`、`TestThemeFontsKeepOnlyTheAdminCopy`、`TestVendoredFontsPresent`。
4. **10-D 維護模式**（`maintenance.go` 的路徑、`maintenance.html` 的圖形位置、按鈕圖示、時區）：`TestFollowupMaintenanceEncodedPathsAreNotBypassed`、`TestFollowupMaintenanceBypassMatchesEscapedSegments`、`TestMaintenance*`、`TestPublicThemeEdgeMaintenance*`；B3。
5. **10-C1 主題 CSS 與單篇／搜尋／首頁**（`layout.html`、`post.html`、`search.html`、`index.html`）：`TestThemeCSS*`、`TestFollowupPostNavLinksCarryTheirDirection`；B1、B2、B4、B7（搜尋與首頁部分）、B8、B9。
6. **10-C2 列表與靜態頁**（`posts.html`、`page.html`、`layout.html` 的 `.page-head`／`.article-head` 斜線）：`TestFollowupStripeDecorationDrawnOnce`、`TestFollowupStaticPageHasQuickLinksPanel`、`TestFollowupPostListCoverPlaceholder`、`TestFollowupPostListCategoryDotSharesRowWithCategory`；B5、B6、B7。
7. **10-F 收尾**：主代理以真實伺服器跑完整 B1–B10（亮／暗、1280／390），更新 `STATUS.md`。

### 測試另外釘下的約定

- 檔案：`internal/server/theme_followups_integration_test.go`（需 DB）、`internal/server/theme_followups_test.go`（不需 DB 的檔案與 CSS 檢查，其中 `followupParseCSS` 解析 `<style>`、去掉 `{{…}}` 與註解、攤平 `@media`，不計算 `var()`、`color-mix()`）。
- i18n 鍵名：`errorNotFoundBody`、`searchQueryTooLong`，兩個語系檔都要有；`zh-TW` 的值逐字固定（見契約）。
- `/api/search` 過長：回應 JSON 的 `code` 必須是字串 `"query_too_long"`，`error` 是非空字串。
- `/search` 過長與一般搜尋頁：輸入框 `id="search-query"`、`name="q"`；「找到 N 篇文章」句型不變；過長時回應不得含原字詞。
- 空白字詞：`/search?q=%20`（含 `%09`、`%E3%80%80`、`+`）不得出現「找到 N 篇文章」與「沒有找到「」卡片；`/api/search` 空白字詞回應必須與 `q=` 逐字相同。
- 效能測試：讀進 Go 的 `posts` 列數以 GORM Query 回呼的 `RowsAffected` 加總（資料表 `posts`），上限 200；種資料用 `CreateInBatches` 直接寫入。
- 404：`.error-message` 元素的文字必須逐字等於中文句；`?lang=en` 取英文。`/api/*` 404 本文含 `"error":"Not Found"`。
- 上一篇／下一篇：`.post-nav` 內的 `<a>` 以 `href` 對應鄰篇；較舊 `post-nav-prev`、較新 `post-nav-next`；單一鄰篇不得帶另一個方向的類名。
- 維護編碼路徑：測試用 `/%61dmin`、`/%61dmin/posts`、`/admin%2fx`、`/admin%2Fposts`、`/%2561dmin`、`/uploads%2fx.png`、`/install%2fx`、`/auth%2flogin` 預期 503 加 `Retry-After`；`/api/%61dmin/posts`、`/api%2fadmin%2fposts` 預期 503；`/uploads/%E6%96%87.png`、`/admin/assets/fonts/bricolage-grotesque.woff2` 等不得是 503；已登入 admin 對 `/%61dmin` 不得是 503。
- 字型：兩個 `.woff2` 的 `Cache-Control` 同時含 `public` 與 `max-age=2592000`；`web/themes/default/assets/fonts` 目錄不存在；`web/admin/assets/fonts/` 內 `bricolage-grotesque.woff2`、`noto-sans-tc.woff2`、`LICENSE`、`VERSION` 保留；`fonts.sh` 不得含 `themes/default/assets`。
- HEAD：測試同時用 `httptest` 記錄器（比對狀態與 `Content-Type: text/html`）和真實 `httptest.NewServer`（確認 HEAD 沒有本文）；路徑清單含 `/`、`/posts`、`/post/<slug>`、`/<頁面 slug>`、`/c/<slug>`、`/t/<slug>`、`/author/<username>`、`/archive`、`/archive/2025/06`、`/search?q=`、`/search`，以及 404 的 `/post/<不存在>`、`/<不存在>`、`/c/<不存在>`、`/posts?page=99`。
- 斜線：頁首元素以第一個帶 `page-head`、`article-head` 或 `search-band` 類名的元素認定；計數 = 該元素內帶 `stripes` 類名的元素數 + （該元素的類名上有 `::before` 規則同時具 `content` 與 `background: linear-gradient(…)` 且之後沒被 `display:none`／`content:none` 取消 ? 1 : 0）；要求剛好 1。`/search` 現況已經是 1，列為守門。
- 快速連結：`.page-quicklinks` 在 `<aside>` 內、文字含「快速連結」、連結恰好兩個：`/posts` 文字「所有文章」、`/search` 文字「搜尋站內」；以 `/about` 與另一個頁面 slug 測。
- 占位縮圖：列為 `li.entry`、標題在 `.entry-title`；無封面的列恰有一個 `.entry-cover-placeholder`（`aria-hidden="true"`、無 `<img>`）；有封面（`Cover` 或內文第一張圖）的列恰有一個 `img.entry-cover` 且無占位。
- 分類點：`/posts` 的列內，`.entry-cats` 必須含 `.entry-flavor`，且列內沒有 `.entry-flavor` 在 `.entry-cats` 之外。
- `<mark>`：所有選擇器為 `mark` 或以 `mark` 結尾的規則都要有非繼承的 `color`，且至少一條。
- 目錄縮排：選擇器含 `[data-toc-level="3"]`（引號、單引號、無引號皆可）的規則，其縮排屬性（見契約）有非 0 值。
- 守門測試（現在就會通過，不得退步）：`TestFollowupCSSParserSelfCheck`、`TestFollowupSearchStillMatchesVisibleTextCaseInsensitively`、`TestFollowupMaintenanceBypassMatchesEscapedSegments`、`TestFollowupHeadDuringMaintenanceIs503`。

### 實作者的判斷邊界

可以自己決定：
- 搜尋的 SQL 篩選做法（`ILIKE`、`strpos`、另存純文字欄位、`pg_trgm` 索引）與是否新增遷移（0024 起）；`error` 訊息的英文措辭；`errorNotFoundBody` 的英文句子；`searchQueryTooLong` 的英文句子；斜線只畫一次要拿掉哪一邊；占位方塊的底色 token（用 `--surface-low` 即可）；對比微調的確切色值與混色比例（只要 B4 達標、不改色相）；手機靜態頁側欄的排列；維護頁圖示的繪法（行內 SVG，線寬 1.75、方角線端，照板）。

必須回報、不得自行決定：
- 要不要把 `/api/search` 的空字串與空白字詞改成 200 空結果（決策 4）；
- 要改動設計板、新增設計板，或讓畫面與板有超出〈決策紀錄〉第 12、13 條的差異；
- 需要新增公開路由、改 `/api/search` 成功回應的鍵、改錯誤碼；
- 為了達標而需要更動 `theme.json` 預設色；
- 發現測試要求互相矛盾或無法達成（回報，不改測試）；
- 需要修改 08 或其他草稿 PR 的檔案。

## 瀏覽器驗收條件

以真實伺服器（專用資料庫、含至少 7 篇文章、兩個分類、一篇有封面、一篇內文有圖、一個靜態頁）加 Chromium，亮與暗兩種 `prefers-color-scheme`，寬度 1280 與 390。「±N」是像素容差。板的數字取自在 1280 寬量測板（`design/project/*.dc.html`）的結果。

- **B1 hero 緊貼頁首與統計帶間距（issue 5；板 Main、MobileHome、Post、Search）**
  - 首頁 1280：`.home-hero` 上緣 − `.masthead` 下緣 = 16 ±2；統計帶上緣 − hero 下緣 = 8 ±2。
  - 首頁 390：hero 上緣 = 頁首下緣 ±1；統計帶上緣 − hero 下緣 = 6 ±2。
  - 單篇 `.post-hero` 與搜尋 `.search-band`，1280 與 390：上緣 = 頁首下緣 ±1。
  - 參考現況：頁首下緣到 hero 上緣 48px（1280）與 24px（390）；統計帶與 hero 相距 76px（1280）與 24px（390）。
- **B2 手機首頁 hero（issue 5；板 MobileHome，390）**：hero 總高 ≤ 175px（板 143px；現況 455px）；hero 內沒有搜尋框、沒有占據獨立列的圖形區塊（果凍丁以絕對定位疊在 hero 右上，被 hero 裁切）；標題 44px 級、一行標語；統計帶三欄、欄距 6px。
- **B3 維護頁（issue 9；板 Maintenance，1280）**：`.elapsed`（「維護開始於…」）與所有文字的外框不與 `.art` 圖形外框相交；內容欄寬（含左右 80px 留白）≤ 880px；「重新整理」「管理員登入」按鈕各有一個 SVG 圖示（照板）；時間為伺服器時區（以設定 `TZ=Asia/Taipei` 啟動伺服器、`maintenance_started_at` 設 `…T16:00:00Z`，頁面應顯示 00:00）。390 寬沒有水平溢位。
- **B4 對比（issue 6、7；預設主題色；亮與暗）**：以元素的計算後 `color` 與（合成後的）背景色，用 WCAG 公式算，以下全部 ≥ 4.5:1：
  - 首頁統計帶三格的字（第一格白字在藍底：現況 4.499；第二格深青字在青底：現況 3.97；第三格深色字在黃底：現況 4.42）與數字、說明字；
  - `.home-intro`（青色標語帶）的標題與段落（現況 3.97）；
  - `.button-primary`、`.button:hover`（白字在主色底：現況 4.30）；
  - `.search-band .label`「搜尋」（黃底：現況 2.63；板色值 4.10 也不足）；
  - `.search-hit mark`（亮：灰字在淡黃底 4.44；暗：灰字在暗黃底 2.14）以及 `.search-hit p`、`.search-hit h2`。
  - 計算提示（供參考，不是指定值）：深字色在青底，把青色混入墨色 25% 得 5.0:1；黃底混入 30% 得 5.0:1、25% 得 5.8:1；主色底混入 5% 墨色得 4.66:1；藍底混入 5% 墨色得 4.81:1；暗色 `<mark>` 用 `--ink` 在暗黃底得 4.84:1。
- **B5 文章總覽（issue 12；板 PostList，1280 與 390）**：
  - 1280：`.category-chips` 與標題 `h1` 同一列，chip 群組在右側且右緣 = 內容區右緣 ±1，下緣與 `h1` 下緣相差 ≤ 12px（板為 grid 兩欄、底部對齊、右欄 360px）；全部 chip 為深色（目前選取）、其餘為淺色。
  - 390：chip 群組在標題下方、靠左。
  - 分類點與分類名稱同一行：點的垂直中心與名稱文字行垂直中心相差 ≤ 4px，點在名稱左側。
  - 占位縮圖：沒封面的列在第三欄，寬度與有封面的列的縮圖相同 ±2（板為 180px 欄），4:3，底色為淺灰（`--surface-low`），一對對角圓角；390 寬排在內文下方、不超出視窗。
- **B6 靜態頁（issue 12；板 Page，1280 與 390）**：1280：`<aside>` 在內文右側，寬 360 ±8、與內文欄距 64 ±8；由上而下是深色拼盤方塊（正方形）與「快速連結」面板（面板淺灰、一對對角圓角、兩顆全寬靠左的淺色按鈕含圖示）。390：側欄在內文下方，沒有水平溢位。
- **B7 斜線裝飾（issue 5）**：`/posts`、靜態頁、`/search`，1280 與 390、亮與暗：頁首區肉眼只看到一條斜線；搜尋頁的斜線是深色單色（板 Search，黃底上的墨色）。
- **B8 目錄（issue 11；板 Post，1280）**：有 h3 的文章，目錄中 h3 項目的文字左緣比 h2 項目右移 ≥ 12px；目錄文字（亮與暗）對比 ≥ 4.5:1。
- **B9 上一篇／下一篇（issue 4；板 Post，1280 與 390）**：中間文章：左側淺色靠左卡片「上一篇」、右側深色靠右卡片「下一篇」。最新文章：只有一張淺色靠左卡片「上一篇」，位於左欄。最舊文章：只有一張深色靠右卡片「下一篇」，位於右欄。390：單欄、文字靠左。
- **B10 字型與離線**：刪除主題字型後，所有前台頁面與維護頁仍載入 Bricolage Grotesque 與 Noto Sans TC（`document.fonts.check` 為真）；字型回應的 `Cache-Control` 為 `public, max-age=2592000`，重新整理時由快取提供；以 `route.abort()` 封鎖所有外部網域後每頁仍完整；390 寬沒有水平溢位。


## 驗收紀錄（2026-10-26）

- 主代理以最終版本執行 B1–B10：Chromium 1280／390、亮／暗，642 項通過、0 項失敗，沒有外部資源請求或非預期錯誤。腳本為 `design/tools/theme-followups.spec.mjs`，本機證據為 `/tmp/jelly29-browser/evidence.json`。
- 主代理重跑專用 PostgreSQL 的搜尋、遷移、備份還原、匯入、媒體及發布截止時間競態回歸，全部通過。新增獨立測試涵蓋 201 列跨批遷移失敗回滾、舊備份、不可信搜尋欄位、部分還原不改文章，以及建立與更新的各種 GORM 寫入方式。
- 主代理親自審查完整差異，獨立複審確認部分還原與發布截止時間修正後沒有新增確認缺陷。格式、go vet、原生及 Windows／macOS 編譯、NATS JetStream 與 theme 競態測試通過。
- 完整競態測試與最新提交的遠端 CI 結果以 PR #29 最終驗證紀錄為準。七張設計板隨實作提交，線上畫布依 PR 的同步回報確認。沒有部署或發版。
