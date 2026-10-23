# Jelly CMS 開發狀態報告

## 目前驗收狀態

- 階段：OAuth 固定身分、本人連結／解除與舊帳號復原已完成本機驗收，準備 CI 交付。完整 CMS 尚未完成。
- 階段目標：推進 [03 帳號與存取](docs/tickets/03-access-control.md) 的穩定身分與安全帳號連結。
- 工作分工：主代理指揮與驗收；獨立代理撰寫測試與程式。
- 已驗證：2026-10-22 基線 `go test ./...` 可編譯，全部套件均無測試，不能作為功能完成證據。
- 測試環境：專用 PostgreSQL 已啟動；安裝頁的未初始化 DB 錯誤已修正並經瀏覽器確認恢復顯示。
- 局部驗證：設定原子儲存、連線隔離、安裝交易／重試／並行、中斷紀錄復原與 HTTP 安裝邊界測試通過；主代理重跑安裝競態測試通過。
- 畫面驗證：安裝頁繁中／英文切換保留內容、資料庫連線成功／失敗、離線保留輸入、密碼驗證，以及 360px／1280px 排版已實際操作確認。
- 啟動驗證：8 個 initializeSite 測試由主代理重跑競態檢查通過；設定損壞、連線失敗、資料表更新失敗與中斷復原均有實際測試。
- 完整使用流程：瀏覽器完成空白安裝、登入、發文與公開閱讀；重啟後文章、商品與登入狀態保留。公開文章畫面證據保存在本機驗收輸出。
- 外掛驗證：主代理重跑基本生命週期、逾時、日誌阻塞、閒置與忙碌子程序回收、網站檔案隔離及暫存目錄回收的競態測試通過；Linux 短日誌壓力測試的事件／關閉耗時約 1 秒。
- 邊界修正：重複安裝請求不再阻擋全站讀取；全空白管理員密碼由前後端拒絕，合法密碼保留首尾空白，已完成瀏覽器表單驗證。
- 全套檢查：主代理執行 `JELLY_TEST_POSTGRES=1 go test -race ./... -count=1 -timeout=240s`、`go vet ./...`、`git diff --check` 通過；尚無測試的 core/payment/theme 不視為已驗收。
- 已知商店差異：輸入 NT$120 的商品在列表顯示 NT$12,000；屬價格單位呈現問題，商店階段須修正並驗證，未將商品畫面標為完成。
- 已知訂單差異：`web/admin/orders.html` 送出 `PUT /api/admin/orders/:id` 更新狀態，但 `handlers_ecommerce.go` 目前只有訂單 GET；屬 P2 流程缺口，商店階段須補齊授權、狀態轉換及畫面回查。
- 已知金流設定差異：web/admin/settings.html 的 loadPaymentSettings 仍有缺欄位就自動 POST 停用的既有分支；屬 P2，商店階段須改為只讀載入，另驗證完整／部分憑證與啟停儲存。
- 關機驗證：主代理獨立驗證連接埠占用時退出碼為 1、隔離容器收到 SIGTERM 後退出碼為 0，兩者均觀察到外掛 OnShutdown；HTTP 取消、等待進行中請求與逾時強制關閉的競態測試通過。
- 已交付：[PR #1](https://github.com/HazelnutParadise/jelly-cms/pull/1) 四項 CI 通過後合併為 `cbf555a`，遠端與本機 `codex/cms-completion` 分支已刪除；未部署。
- 權限驗證：主代理重現匿名 API、敏感設定、文章所有權與降權後存取的失敗，代理修正後已由主代理執行全套 PostgreSQL 競態測試與 go vet 通過；額外發現的尾端多餘 JSON 也經獨立重現、修正與回歸驗證。
- 審查結果：HTTP 權限與文章處理的對抗性審查沒有其他已確認回歸；既有編輯器使用 JSON，其他外部輸入格式尚無相容性證據。發布時間是可編輯內容欄位，建立與修改時間由伺服器管理。
- HTTP 權限交付：[PR #2](https://github.com/HazelnutParadise/jelly-cms/pull/2) 四項 CI 通過後合併為 `73065ea`；遠端與本機 `codex/access-control` 分支已刪除，未部署。
- 憑證驗證：主代理重現缺少到期時間及不合法使用者 ID 仍通過的問題；代理修正後，主代理完整 PostgreSQL 競態測試、go vet 與 Linux 386 測試編譯通過。憑證限制 HS256、必要到期時間與可安全轉換的正整數 ID；已隨 PR #3 交付，登出撤銷另隨 PR #4 交付。
- 金鑰保存驗證：獨立亂數、跨程序重啟、並行首次啟動、環境覆寫、損毀拒絕啟動及安裝後保留登入狀態通過主代理全套 PostgreSQL 競態測試與 go vet。審查發現的 Unix 目錄同步缺口已修正並複審，隨 PR #3 交付；Windows 只完成編譯，不保證目錄更新的斷電持久性，真實斷電尚未驗證。
- 驗證異常：測試代理違反程序操作限制，停止並以同一執行檔重啟本機驗收站；首頁已確認恢復，重啟前後登入狀態尚未確認一致。
- 金鑰保存交付：[PR #3](https://github.com/HazelnutParadise/jelly-cms/pull/3) 四項 CI 通過後合併為 `a4175d3`，遠端與本機 `codex/auth-lifecycle` 已刪除；未部署。
- 登出驗證：主代理重現並驗證成對撤銷、不同登入隔離、跨程序拒絕重放與資料庫故障拒絕存取；OAuth 重簽延長登入期限的 P1 已加入失敗測試並修正，對抗性複審沒有其他已確認缺陷。主代理全套 PostgreSQL 競態測試、go vet、Windows amd64 編譯及 Linux 386 認證測試編譯通過，已隨 PR #4 交付。
- 登出畫面：修正前的 12 個瀏覽器負向案例全部重現問題；修正後，繁中／英文、1280px／360px 的失敗留頁、離線提示、雙擊限制與重試共 12 個案例由主代理重跑通過。四個成功操作均回登入頁且 API 回查 401；console 的 404 已由瀏覽器追蹤定位到 `/favicon.ico`，介面整合階段須補齊，尚未宣稱 console 全部通過。
- 重啟回查：最新執行檔重啟前後設定與登入金鑰檔雜湊一致，首頁內容相同；四個瀏覽器登出成功案例均回登入頁且 API 回查 401。登入頁尚未保留英文選擇，與 favicon 缺漏一併留待介面整合。
- 登出交付：[PR #4](https://github.com/HazelnutParadise/jelly-cms/pull/4) 四項 CI 通過後合併為 `c9ca3c9`，遠端與本機 `codex/auth-revocation` 已刪除；未部署。
- 請求保護驗證：IPv6 等價位址誤擋與 IPv4／mapped IPv6 混淆已經失敗測試、修正與對抗性複審；同步主分支的 Go 1.27.3 與套件升級後，主代理重跑全套 PostgreSQL 競態測試、go vet、Windows amd64 編譯、Linux 386 認證測試編譯及 8 個瀏覽器案例通過，沒有其他已確認缺陷。
- 請求保護畫面：主代理在 Chrome 的 1280px／360px 完成 8 個案例；正常登入、草稿建立回查與刪除、登出通過，另一來源的登入及登出表單回 403。跨站頂層登出不再送出清除登入 Cookie 的回應，原登入保留。既有 favicon 404 另列，沒有新增相關 console 錯誤。
- 請求保護交付：[PR #8](https://github.com/HazelnutParadise/jelly-cms/pull/8) 同步 Go 1.27.3 升級後通過四項 CI，已合併為 `96935ba`；未部署。
- 返回網址驗證：本機登入、OAuth 入口與回呼、登入頁共用站內網址檢查；主代理重現修正前的站外導向與 JavaScript 執行，修正後 18 個桌面／手機案例全數通過。全套 PostgreSQL 競態測試、go vet、Windows amd64 與 Linux 386 編譯通過，對抗性審查沒有已確認缺陷。
- 返回網址交付：[PR #9](https://github.com/HazelnutParadise/jelly-cms/pull/9) 四項 CI 通過後合併為 `e23d809`；遠端與本機 `codex/login-return` 分支已刪除，未部署。
- 已知手機差異：360px 的文章列表將中文標題擠成直排並裁切操作欄；屬 P2 版面問題，介面整合階段須修正並實際操作驗收。
- 登入限制驗證：主代理重現第 31 次仍可登入並查詢 DB 的問題；固定 60 秒內最多 30 次的修正已通過全套 PostgreSQL 競態測試、go vet、Windows amd64 與 Linux 386 編譯，對抗性審查沒有已確認缺陷。容量、來源正規化、並行與到期恢復均有公開路由測試。
- 登入限制畫面：主代理在 1280px／360px 完成錯誤密碼 401、超量 429 留頁並顯示等待秒數、實際等待到期後登入 302／API 200、登出後 API 401 的完整流程。429 沒有送出登入 Cookie；預期的 401／429 console 與既有 favicon 404 分別記錄，沒有其他相關錯誤。
- 登入限制交付：[PR #10](https://github.com/HazelnutParadise/jelly-cms/pull/10) 四項 CI 通過後合併為 `d073628`；未部署。
- OAuth 輸入驗證：主代理先重現提供者與 state 被查詢覆蓋、無效 state 與 Session 保存失敗仍進入授權的問題；修正後完整 PostgreSQL 競態測試、go vet、Windows amd64 與 Linux 386 編譯通過，對抗性審查沒有其他已確認缺陷。Google／GitHub 替身的正常登入、JSON token 回查與站內返回通過；真實提供者授權尚未驗證。
- OAuth 輸入交付：[PR #11](https://github.com/HazelnutParadise/jelly-cms/pull/11) 四項 CI 通過後合併為 `0da59e9`；遠端與本機 `codex/oauth-protocol` 分支已刪除，未部署。
- OAuth 一次性驗證：主代理以提供者替身重現已授權 Cookie 重放、並行重複授權、跨提供者重簽續期及整秒到期窗口；修正後完整 PostgreSQL 競態測試、go vet、原生／Windows amd64／Linux 386 編譯通過，對抗性複審沒有其他已確認缺陷。簽章十分鐘、一次性紀錄跨程序保存，回呼不再把提供者權杖寫入 Cookie；真實 Google／GitHub 授權尚未驗證。
- OAuth 一次性交付：[PR #12](https://github.com/HazelnutParadise/jelly-cms/pull/12) 四項 CI 通過後合併為 `3b6e014`；遠端與本機分支已刪除，未部署。首次 CI 的外掛回收失敗原始紀錄保留，重跑通過不視為缺陷已修正。
- 外掛回收驗證：主代理先重現 Linux 單 CPU 下父程序結束後 worker 超過三秒未退出，再驗證修正後原始案例與三條真實程序測試連跑三次通過；單 Go 執行槽及垃圾回收的獨立探針也確認恢復退出。完整 PostgreSQL 競態測試、go vet、原生／Windows amd64／Linux 386 編譯通過，對抗性審查沒有已確認缺陷。原始 CI 沒有程序 stat，確切失敗原因仍未知。
- 外掛回收交付：[PR #13](https://github.com/HazelnutParadise/jelly-cms/pull/13) 四項 CI 通過後合併為 `ef0932b`；遠端與本機分支已刪除，未部署。
- 日誌保護驗證：主代理先以 12 個 HTTP 情境重現查詢外洩，再驗證修正後 OAuth、一般路由、404、錯誤與導向的日誌只保留路徑；輸入、回應與錯誤處理保持一致。完整 PostgreSQL 競態測試、go vet、原生／Windows amd64／Linux 386 編譯通過，對抗性審查沒有已確認缺陷，已隨 PR #14 交付。
- 日誌保護交付：[PR #14](https://github.com/HazelnutParadise/jelly-cms/pull/14) 四項 CI 通過後合併為 `73eb610`，遠端與本機分支已刪除，未部署。
- OAuth 請求驗證：主代理以同一份測試重現原版的 14 個負向失敗，再驗證修正版的共用 30 秒期限、較短期限、標頭與本文取消及並行隔離。真實 Goth 經本機假上游的 JSON 登入、API 回查與站內導向通過；完整 PostgreSQL 競態測試、go vet、原生／Windows amd64／Linux 386 編譯通過，對抗性審查沒有已確認缺陷。真實外部授權尚未驗證，已隨 PR #15 交付。
- OAuth 請求交付：[PR #15](https://github.com/HazelnutParadise/jelly-cms/pull/15) 四項 CI 通過後合併為 `cade274`，遠端與本機分支已刪除，未部署。
- 固定身分驗證：主代理已重現不同第三方 ID 依同信箱取得既有管理員憑證、同一 ID 改信箱後建立另一帳號，以及查詢故障仍建立帳號。代理修正後，主代理已重跑公開 HTTP 競態測試通過，涵蓋四種各 12 並行、新帳號與關聯同成同敗；審查另找到回查時序的誤判，獨立測試以固定排程先重現 409，再由代理修正；主代理重跑固定身分及完整 auth 套件競態測試通過，尚未交付。獨立 PostgreSQL 的三種 12 並行交易各重跑三次通過，寫入失敗一併回滾且沒有多餘帳號，測試資料庫已清理。
- 連結補充驗證：主代理先重現無限流、舊 Goth Cookie 阻擋重試、查詢等待後登入過期仍寫入及操作紀錄缺漏；修正後親跑公開 HTTP 與完整 auth 套件競態測試通過，尚未交付。
- 提供者設定驗證：三處 Option 主鍵累加問題已修正，公開／管理員查詢及真實提供者載入測試通過。瀏覽器確認完整／部分設定載入沒有自動 POST，兩個提供者能獨立停用、保留憑證重新啟用及失敗重試；停用狀態同步至本人帳號頁。
- 復原工具驗證：主代理先驗證合法操作未實作，再重現 DB 連線錯誤污染 stdout；修正後主代理重跑完整 CLI 競態測試通過，站檔、帳號與關聯沒有被工具改寫，尚未交付。
- 帳號頁驗證：主代理以本機替身完成連結、解除、Google／GitHub 舊帳號復原、最後登入方式保護及三角色入口。繁中／英文、360px／1280px、深／淺色、鍵盤焦點、載入、失敗、離線、逾時與重試已實際操作。兩個新頁面的 Lighthouse 效能與無障礙均為 100，最佳實務為 96，既有 favicon 404 另列；沒有量測實際使用者 INP，也沒有完成真實外部授權。
- 已知後台差異：author／editor 的儀表板讀取管理員限定設定會回 403，web/admin/index.html:215 仍對錯誤物件呼叫 settings.find，產生 console 錯誤；屬既有 P2，介面整合階段應停止非管理員讀取敏感設定，或改用安全的主題狀態來源。
- 本批完整檢查：主代理重跑全套 PostgreSQL 競態測試、go vet、原生／Windows amd64 編譯及 Linux 386 認證測試編譯通過；最後的設定頁變更另重跑受影響競態測試與瀏覽器驗收通過。對抗性審查發現的 SQL 日誌洩漏及啟停無法儲存均已重現、修正與複審。
- 首次 CI 差異：兩組 Linux CI 都在復原測試的 User 時間比對失敗，記憶體奈秒值與 DB 微秒值不同。主代理以固定時間重現後，測試改用操作前的 DB 原值，保留全部斷言；相同案例、與 CI 相同 180 秒設定的完整競態測試及 go vet 已重跑通過，待新一輪 CI。
- 固定身分遷移：兩張相容新增資料表、有效關聯唯一鍵、外鍵、軟刪、更新時間、稽核及重跑相容性通過主代理真實資料庫驗證。只套用於任務專用資料庫，未部署或更動正式資料。
- 下一工作單：本批 CI 全綠合併後接續 03 的媒體所有權與帳號管理。01 的服務整合與 02 的真實事件／後台操作仍保留。
- 介面重設計：預設主題（web/themes/default）、後台全部頁面、登入與安裝頁改為同一套視覺語言：白底墨字、墨色調出的冷灰階、對角線對稱圓角、果凍丁品牌記號（web/admin/assets/jelly-mark.svg）、Bricolage Grotesque 與 Noto Sans TC 網路字型。後台改為工作區型版面（路徑列、列式清單、文件式編輯器、系統偏好式設定列、右側抽屜）。
- 文章編輯器：改為 Markdown 編輯器（工具列、即時預覽），儲存時以 marked（已內建於 web/admin/assets）轉成 HTML 寫入 content，原稿保存在 meta.markdown；舊的 HTML 內容可直接當 Markdown 編輯。
- 主題模板函數：新增 safeHTML、excerpt（去標籤、依字元截斷）、initial，並有單元測試；post.html 改用 safeHTML 輸出文章 HTML（原本 `{{.Post.Content | html}}` 會把標籤轉成純文字）。
- 介面驗證：以靜態渲染加 Chromium 截圖檢查桌面（1280）與手機（390）、亮暗色模式，含 API 假資料；尚未在真實伺服器以瀏覽器完整操作驗收，表單送出與主題客製化儲存仍待實機確認。
- 已知缺口：後台部分頁面（文章列表按鈕、編輯器、設定、商品）仍是英文字串，需併入 07 的 i18n 工作；網路字型與自架離線環境不相容時會退回系統字型。
- 設計稿：原始檔已進 repo 的 `design/`（51 張板、畫布索引、產生腳本、截圖工具，同步規則見 design/README.md 與 AGENTS.md）。40 張畫面已在設計畫布定稿（前台 6、登入安裝手機 4、後台既有功能 11，以及程式尚未有的 19 張：商店列表／商品頁／購物車／結帳／付款完成／分類頁與留言區、分類與標籤、導覽選單、留言管理、使用者與角色、個人帳號、回收桶、版本比較、WordPress 匯入、備份與匯出、轉址與 SEO、系統日誌、⌘K 指令面板、空白／載入／錯誤／無權限狀態）。後 19 張對應 ENG.md 里程碑 2、3、5、6、7，實作時以設計稿為準；畫布上的後台側欄比程式多出「分類與標籤、留言、回收桶、導覽選單、使用者、轉址與 SEO、工具、日誌」八個入口。
- 內容組織（工作單 04）：分類（階層、循環檢查、拖曳排序）、標籤、文章的分類標籤與摘要／封面／可見性／置頂／留言開關；導覽選單（位置、兩層樹、主題 `menu` 函式、自訂網址只接受 /、#、http、https、mailto、tel）；留言（訪客與登入者、蜜罐、長度、Email、每分鐘 5 則、goldmark + bluemonday 轉 HTML、審核／垃圾／回收桶／批次／站長回覆／編輯）；回收桶（文章、頁面、商品、留言軟刪、別名加後綴讓新文章可用、還原、永久刪除、清空、保留天數與每日清除）；版本（每次有變更的儲存留版本、自動儲存留 20 個、逐行比較、還原）；前台 /c、/t、/author、/archive、/archive/年/月 與單篇留言區；後台分類與標籤、導覽選單、留言、回收桶、版本比較五頁與編輯器屬性面板。主題 `_*.html` 檔案會隨每頁一起解析作為局部模板。
- 內容組織驗證：新增 120 個以上整合測試（分類標籤、選單、留言、回收桶、版本、彙整頁、局部模板），完整 PostgreSQL 測試、go vet、gofmt 通過。後台五頁與編輯器面板以 jellypreview 靜態渲染加 Playwright 在 1280／390、亮／暗截圖對照設計板；主代理另以真實伺服器安裝、API 建資料後在 Chromium 操作：建分類、核准留言、還原文章、看版本比較、前台表單送留言進待審，13 個頁面全部 200、console 無錯誤。驗收發現前台版面沒有使用 `menu "header"` 與首頁 1280 寬橫向溢出，已在同一 PR 修正。
- 內容組織缺口：分類合併、WordPress 匯入按鈕、留言 IP 與疑似垃圾狀態、回收桶附件類型、編輯器排程／作者／SEO 列、`login_only` 選單項目顯示，列在工作單 04 的「留待後續」。
- 決策：沿用現有 Go 與模板架構，由代理完成程式，主代理獨立驗收。使用者已授權驗收後提交、推送與開 PR，必要 CI 全綠後合併及清理分支；部署另行授權。
- 架構與總目標：[ENG.md](ENG.md)。

下列為既有開發紀錄，完成標記尚未重新驗收，不代表目前已可取代 WordPress。

## 專案概述
Jelly CMS 是一個使用 Go 語言開發的開源 CMS 系統，目標是比 WordPress 更快，並提供現代化的功能。

## 需求對照表

### ✅ 已完成功能

1. **比 WordPress 快，不要 PHP**
   - ✅ 使用 Go 語言開發（Echo 框架）
   - ✅ 使用 PostgreSQL 資料庫
   - ✅ 使用 goccy/go-json 提升 JSON 處理效能

2. **至少支援繁體中文和英文**
   - ✅ 已建立 i18n 系統（使用 go-i18n）
   - ✅ 已建立 `web/locales/zh-TW.json` 和 `web/locales/en.json`
   - ✅ 已實作 I18nMiddleware 支援語言切換
   - ⚠️ 翻譯內容還很少，需要擴充

3. **有支援自訂頁面布局和配色的主題系統**
   - ✅ 已建立主題管理器（`internal/theme/manager.go`）
   - ✅ 已建立主題配置結構（`internal/core/theme.go`）
   - ✅ 支援主題切換（`/api/admin/themes/activate`）
   - ✅ 主題模板渲染系統
   - ✅ **已完成：主題配色自訂功能（ThemeColors 結構，支援儲存和讀取）**
   - ✅ **已完成：主題布局自訂功能（ThemeLayout 結構，支援自訂欄位）**
   - ✅ **已完成：主題設定 API（GET/POST `/api/admin/themes/:name/settings`）**

4. **有外掛系統**
   - ✅ 已建立外掛管理器架構（`internal/plugin/manager.go`）
   - ✅ 使用 qjs (QuickJS) 作為 JavaScript 運行時
   - ✅ 支援外掛載入和卸載
   - ✅ **已完成：外掛 API 註冊功能（JellyCMS 全域物件，包含 registerHook, log, config）**
   - ✅ **已完成：外掛 Hook 系統基礎架構（支援 Hook 註冊和觸發）**
   - ✅ **已完成：外掛熱重載機制（Reload 方法）**
   - ✅ **已完成：外掛管理 API（GET /api/admin/plugins）**

5. **更改主題或外掛不用重啟主程式**
   - ✅ 主題切換已支援熱重載（清除快取後重新載入）
   - ✅ **已完成：外掛系統熱重載機制（Reload 方法，支援動態重新載入外掛）**

6. **支援 OAuth 登入，可直接使用第三方登入自動創建帳號**
   - ✅ 已整合 goth 庫支援 OAuth
   - ✅ 已實作 Google 和 GitHub OAuth
   - ✅ 已實作自動創建帳號功能
   - ✅ **已完成：Session 管理（使用 gorilla/sessions）**
   - ✅ **已完成：JWT Token 發放（使用 golang-jwt/jwt/v5）**
   - ✅ **已完成：登入狀態驗證中間件（RequireAuth, RequireAdmin, RequireRole）**
   - ✅ **已完成：登出功能（`/auth/logout`）**
   - ✅ **已完成：當前使用者查詢 API（`/api/auth/me`）**

7. **有像 WordPress 的初始化安裝精靈**
   - ✅ 已實作安裝精靈（`internal/install/service.go`）
   - ✅ 已建立安裝頁面（`web/admin/install.html`）
   - ✅ 支援資料庫連線測試
   - ✅ 支援建立管理員帳號
   - ✅ 安裝狀態檢查中間件

8. **希望能串金流**
   - ✅ **已完成：金流介面抽象層（PaymentGateway interface）**
   - ✅ **已完成：綠界 ECPay 整合（`internal/payment/ecpay.go`）**
   - ✅ **已完成：藍新 NewebPay 整合（`internal/payment/newebpay.go`）**
   - ✅ **已完成：金流服務管理器（`internal/payment/service.go`）**
   - ✅ **已完成：支付訂單建立 API（`POST /api/payment/create`）**
   - ✅ **已完成：支付回調處理（`POST/GET /api/payment/:gateway/callback`）**
   - ✅ **已完成：支付返回處理（`GET /api/payment/:gateway/return`）**
   - ✅ **已完成：訂單狀態查詢（`GET /api/payment/order/:id/status`）**

## 詳細功能狀態

### 核心功能
- ✅ 使用者管理（User model）
- ✅ 文章/頁面管理（Post model, PostService）
- ✅ 電商產品管理（Product model, ProductService）
- ✅ 訂單管理（Order, OrderItem models）
- ✅ 設定管理（Option model）
- ✅ 媒體上傳功能
- ✅ 資料庫自動遷移

### 管理後台
- ✅ 管理後台路由結構
- ✅ 文章/頁面編輯器頁面
- ✅ 產品管理頁面
- ✅ 訂單管理頁面
- ✅ 主題管理頁面
- ✅ 外掛管理頁面
- ✅ 設定頁面
- ⚠️ 缺少認證中間件（目前所有路由都開放）

### API 端點
- ✅ `/api/admin/posts` - 文章 CRUD
- ✅ `/api/admin/products` - 產品 CRUD
- ✅ `/api/admin/orders` - 訂單查詢
- ✅ `/api/admin/media` - 媒體管理
- ✅ `/api/admin/themes` - 主題管理
- ✅ `/api/admin/plugins` - 外掛列表
- ✅ `/api/admin/settings` - 設定管理
- ✅ `/api/install` - 安裝 API
- ✅ `/auth/:provider` - OAuth 登入
- ✅ `/auth/:provider/callback` - OAuth 回調

## 待完成工作

### 高優先級
1. ✅ **完善 OAuth 登入系統** - 已完成
   - ✅ 實作 Session 管理
   - ✅ 實作 JWT Token 發放
   - ✅ 實作認證中間件
   - ✅ 實作登出功能

2. ✅ **實作金流系統** - 已完成
   - ✅ 建立金流介面抽象層
   - ✅ 實作綠界 ECPay 整合
   - ✅ 實作藍新 NewebPay 整合
   - ✅ 建立訂單支付流程
   - ✅ 實作支付回調處理

3. ✅ **完善主題系統** - 已完成
   - ✅ 實作主題配色自訂功能
   - ✅ 實作主題布局自訂功能（自訂欄位）
   - ✅ 建立主題設定 API

4. **完善外掛系統**
   - 完成外掛 API 註冊
   - 實作 Hook 系統
   - 實作外掛熱重載機制
   - 建立外掛開發文檔

### 中優先級
5. **擴充 i18n 翻譯**
   - 補充管理後台所有頁面的翻譯
   - 補充錯誤訊息的翻譯

6. **完善管理後台**
   - 實作認證中間件
   - 實作權限管理（Role-based access control）
   - 完善前端 UI

### 低優先級
7. **效能優化**
   - 實作快取機制
   - 資料庫查詢優化

8. **安全性增強**
   - 實作 CSRF 保護
   - 實作 XSS 防護
   - 實作 SQL 注入防護（GORM 已提供部分保護）

## 技術架構

### 後端
- **語言**: Go 1.27.3
- **框架**: Echo v4
- **資料庫**: PostgreSQL (GORM)
- **JSON**: goccy/go-json
- **OAuth**: goth
- **Session**: gorilla/sessions
- **i18n**: go-i18n
- **外掛運行時**: qjs (QuickJS)

### 前端
- HTML 模板系統
- 管理後台使用傳統 HTML 頁面（可考慮未來改用 SPA）

### 資料庫結構
- Users (使用者)
- Posts (文章/頁面)
- Products (產品)
- Orders (訂單)
- OrderItems (訂單項目)
- Options (設定)

## 下一步行動計劃

1. ✅ 完善 OAuth 登入系統（Session + JWT） - **已完成**
2. ✅ 實作金流系統基礎架構 - **已完成**
3. ✅ 完善主題配色和布局自訂功能 - **已完成**
4. 完善外掛系統的 API 和 Hook
5. 擴充 i18n 翻譯內容

## 最新更新（本次開發）

### 已完成的主要功能

1. **OAuth 登入系統完善**
   - 實作了完整的 Session 管理機制
   - 整合 JWT Token 發放和驗證
   - 建立了認證中間件（RequireAuth, RequireAdmin, RequireRole）
   - 實作了登出功能和當前使用者查詢 API
   - 所有管理後台路由已加上認證保護

2. **金流系統實作**
   - 建立了完整的金流介面抽象層（PaymentGateway）
   - 實作了綠界 ECPay 完整整合（建立訂單、驗證回調、查詢狀態）
   - 實作了藍新 NewebPay 完整整合
   - 建立了金流服務管理器，支援多個金流服務
   - 實作了支付訂單建立、回調處理、返回處理等完整流程
   - 建立了訂單狀態查詢 API

3. **主題系統完善**
   - 擴展了主題配置結構，支援配色（ThemeColors）和布局（ThemeLayout）
   - 實作了主題自訂欄位系統（ThemeField）
   - 建立了主題設定資料庫模型（ThemeSettings）
   - 實作了主題設定 API（讀取和儲存）
   - 支援主題設定的熱重載（更改設定後自動清除快取）

### 技術實現細節

- **認證系統**：使用 `gorilla/sessions` 管理 Session，使用 `golang-jwt/jwt/v5` 處理 JWT
- **金流系統**：使用介面模式實現可擴展的金流整合，支援多個金流服務商
- **主題系統**：使用 JSONB 儲存主題設定，支援動態配置和熱重載

### 待完成功能

1. **外掛系統完善**：需要完成外掛 API 註冊和 Hook 系統
2. **i18n 翻譯擴充**：需要補充更多翻譯內容

