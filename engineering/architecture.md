# Jelly CMS 架構

## 範圍與依據

原始需求依 README.md、各工單的驗收紀錄與 git 歷史判定，文件裡的完成標記不是驗收證據。目標是完成原始功能，並讓一般內容網站、部落格、企業網站與本專案既定的電商流程能改用 Jelly CMS。

WordPress 對照來源：https://wordpress.org/about/features/ 。內容遷移與同等使用流程納入驗收。現有 Go 與 JavaScript 外掛架構不能直接執行 WordPress PHP 外掛，不可將功能替代宣稱為 PHP 外掛相容。

## 系統組成

使用 Go、Echo、GORM、PostgreSQL、HTML templates、QuickJS，不另建前端框架（決策紀錄 0002）。

瀏覽器 → HTTP 路由與登入／權限檢查 → 領域服務 → PostgreSQL；公開內容經主題模板輸出。

快取與背景工作佇列是可抽換介面，預設記憶體快取與 PostgreSQL 佇列，可切換為 Redis／Valkey 或 NATS JetStream，其他中介軟體由外掛提供（決策紀錄 0004）。外掛只能透過介面使用自己的命名空間。

前端資源全部自帶在 `web/` 下，不從 CDN 或外部網址載入（決策紀錄 0003），自帶套件的更新流程見 `/docs/release-checklist.md`。OAuth 與金流設定由後台管理。金流與 OAuth 外部服務是獨立信任邊界。

資料庫遷移由 `data.MigrateDB` 執行：先跑 GORM 模型遷移，再依序套用 `internal/data/migrations/` 嵌入的 SQL。已提交的 SQL 檔不再修改，結構調整一律新增檔案。

## 前端資源與主題

前端資源在啟動與主題啟用時建立不可變內容快照，模板透過 `assetURL` 取得 SHA-256 網址。CSS 的本地相依資源先版本化，再計算實際 CSS 回應的 hash。HTML 每次重新確認，已有 `no-store` 的頁面保留原設定；只有 hash 與回應內容相符的資源可以長期快取。既有未版本化網址保持可用並要求重新確認，過期 hash 回 404 與 `no-store`。詳細範圍、例外與驗收見工單 15。

主題選擇沿用 `options.active_theme`。切換流程為驗證並建立候選快照 → 在同一 Manager 鎖內持久化 → 發布主題與快照，儲存失敗保留目前可用狀態。啟動流程為讀取保存值 → 啟用該主題，無保存值使用 default；保存值無法載入時記錄警告並嘗試 default，不覆寫原紀錄。CLI 預覽與 runtime 使用相同的編輯器模板依賴，靜態預覽不模擬後台 API。驗收與例外見工單 16。

主題設定重載以同一讀鎖捕捉啟用名稱及不可變資源快照指標，準備後只有原狀態仍相同才發布；過期成功與錯誤均丟棄，目前重載失敗保留原快照。模板函式註冊的 generation 不代表啟用身分。詳見工單 17。

公開主題的選單函式依每次請求綁定已驗證登入狀態與 path，不保存 HTTP context。模板快取只保留未執行的解析結果，每次 Clone 後才套用請求函式。文章／頁面型選單沿用公開內容條件，登入不放寬內容可見性；登入者 HTML 使用 private, no-store。所有公開、商店、錯誤及維護頁共用相同入口，無資料庫時傳入真正 nil 介面。詳見工單 18。

## 功能現況

以下依工單 38 對照已完成工單與程式的結果整理。還沒做的部分各有工單，交付時更新這一節。

- **安裝與啟動**：輸入驗證、測試連線隔離、設定原子儲存、重複安裝防護、失敗復原與服務即時初始化已接上（工單 01，仍在進行中，設定檔寫入後的目錄 fsync 尚未處理）。
- **帳號與存取**：本機登入、OAuth、登入狀態與撤銷、請求保護、登入限流、文章所有權（工單 03，仍在進行中）。作者只能看到與管理自己上傳的媒體，沒有擁有者的檔案只有編輯與管理員能動（工單 39）。個人中心的基本資料、通知偏好、兩步驟驗證、登入裝置、第三方帳號綁定與存取金鑰導向（工單 06、22）。訪客以 Email 一次性連結查訂單。角色固定為 admin、editor、author、customer。未完成：自訂角色（工單 40）、開放註冊（工單 41）。
- **編輯與發布**：文章、頁面、分類標籤、預覽、版本、回收桶、導覽、留言（工單 04）。編輯器的 `:::` 版面區塊、動效屬性、內建互動元件（分頁、摺疊、輪播、燈箱、倒數、數字跳動、前後比較）、隔離框自訂元件、GFM 與擴充語法、Jelly Markdown／純 Markdown／HTML 匯出（工單 09）。未完成：`latex` 伺服器排版、PDF 匯出、投票與 Lottie／Rive（工單 46）。
- **外觀與擴充**：主題切換與設定持久化。外掛從上傳 zip 或網址安裝、安裝前顯示權限、啟停、重載、事件呼叫、失敗隔離、詳情與設定頁、多個外掛提供同一擴充點時選擇生效者（工單 02、28、29、36、37）。擴充點有附件儲存、搜尋、留言、通知器、內容後處理、head／footer 注入與媒體處理。附件庫有多個儲存策略、分組、從網址抓檔、貼上上傳與搬移。未完成：儀表板小工具與其他後台擴充點（工單 42）、WebP 轉檔與縮圖（工單 43）、主題預覽升級與解除安裝（工單 47）、官方外掛與市場目錄（工單 48）。
- **商店**：商品、購物車、結帳、訂單、庫存、金流驗證、重複通知與失敗恢復、折扣碼（工單 07），應付總額進位見決策紀錄 0001。未完成：數位商品下載連結與商店 Webhook（工單 44）。訂閱制與定期扣款尚未排入工單。
- **遷移與營運**：WordPress 匯入、匯出與備份還原、網址樣式與 301、RSS、Sitemap、robots、llms.txt、IndexNow、AI 爬蟲設定、維護模式、通知中心、公開 REST API 與 OpenAPI（工單 24、27）、快取與佇列後端、版本更新（工單 08、35）。未完成：Atom（工單 45）、全文搜尋與備份結果、排程發布通知（工單 49）。
- **UI 與品質**：繁中與英文、前台回到頂部按鈕、後台各頁的桌面與行動版逐頁驗收（工單 19、26）。

## 測試策略

Go 測試驗證公開函式與 HTTP 行為；資料庫測試使用任務專用 PostgreSQL，不能操作既有資料。以真實資料庫驗證交易、索引、JSONB 與重啟持久化。外部 OAuth／金流採官方契約與測試回應，真實外部授權與收款不得假稱已驗收。

必要流程：空白安裝 → 登入 → 建文 → 公開閱讀 → 重啟後再讀；設定主題 → 預覽 → 儲存 → 重啟保持；商品 → 訂單 → 測試付款通知 → 狀態與庫存一致。

資料模型採相容新增，保留既有資料。破壞性遷移另行確認。

## OAuth 暫存狀態與登入撤銷

OAuth 一次性狀態沿用現有資料庫撤銷儲存，以帶類型前綴的雜湊隔離 OAuth state 與登入 ID。原子新增判斷消耗結果，跨程序與重啟後仍拒絕重放。OAuth Cookie 的 600 秒期限與應用登入的七天期限分開設定。每次入口建立全新的待授權資料，只保留本次提供者，避免重簽另一提供者時延長舊 state。提供者 token 只存在處理中的伺服器記憶體，不保存於瀏覽器 Cookie。已使用狀態至少保留 601 秒，以涵蓋 Cookie 簽章的整秒精度。這類紀錄與登入撤銷共用 expires_at 清理，兩類紀錄不互相誤刪或延長。

## OAuth 固定身分與帳號關聯

第三方帳號以提供者加固定 ID 識別（決策紀錄 0005）。資料關係：

```text
users [既有帳號]
  id PK, username UNIQUE, email UNIQUE, password, provider, role
  1 ───────── 0..N oauth_identities [含解除後的歷史]
                  id PK, user_id FK RESTRICT, provider, subject
                  created_at, updated_at, deleted_at
                  active candidate keys: (provider, subject), (user_id, provider)
                  1 ─────── 1..N oauth_identity_audit_log [變更快照]
```

`oauth_identities.id` 決定該列所有屬性。未解除的 `(provider, subject)` 及 `(user_id, provider)` 各為候選鍵。第三方信箱與名稱不複製進關聯表。User 的 Provider 是建立來源，不代表目前的登入方式。

關聯表採資料庫產生的 bigint ID、timestamptz 建立／更新時間與可空軟刪時間。provider、subject 使用 C collation 精確比對，subject 最多 255 個字元。部分唯一索引只限制未解除的關聯。user_id 另有涵蓋歷史列的索引，外鍵明訂 ON DELETE RESTRICT、ON UPDATE RESTRICT。資料庫檢查拒絕不支援的提供者、空白 subject 及不存在的使用者。兩張表啟用 RLS 並不向 PUBLIC 授權，由後端資料庫擁有者存取，不提供瀏覽器直連資料庫的權限。結構定義在 `internal/data/migrations/0001_oauth_identities.sql`。

資料庫 trigger 維護 updated_at，另一 trigger 保存關聯新增、解除及重新連結的前後狀態。稽核只保存關聯 ID、目標帳號、提供者、軟刪時間、操作者 ID 與當時的 email 快照，不保存第三方 subject、code、state、token 或密碼。稽核列不掛遞迴 trigger、不軟刪，created_at 有索引。每次新增稽核時自動清理超過 90 天的此類紀錄。HTTP 請求日誌去除查詢字串，身分操作另記實際操作者與請求識別值，失敗認證也留下結果。

登入以精確提供者及固定 ID 解析。既有關聯直接取原使用者，不改寫個人資料。新的 User 與關聯在同一交易建立。唯一違反先完整回滾，再於交易外回查精確關聯，只有同一關聯的競爭勝方可被登入。username 唯一違反可有限次換名重試，不判成信箱衝突。其他查詢／寫入錯誤回一般服務錯誤。

連結與解除共用目標 User 的資料列鎖。連結的簽章意圖包含本人與實際已驗證的登入 ID，不能獨立讀取另一份 Session 代替。回呼取得第三方身分後、寫入前再次驗證該登入尚未到期或撤銷。互動連結要求瀏覽器可攜帶的登入 Cookie。正常登入入口不接受查詢參數切換成連結。連結成功不重發登入或延長期限。可用提供者狀態與真正已載入的設定一致，解除不得移除最後一種可用登入方式。

## 編輯器渲染與隔離元件

元件資料關係如下，元件名稱是全歷史唯一鍵，版本以 `(widget_id, version)` 唯一識別。名稱與元件 ID 各自決定元件目前標題及編輯狀態，版本鍵決定原始碼、參數與發布狀態。

```text
widgets [id PK, name UNIQUE, title, version, status, timestamps, deleted_at]
  1 ---- 1..N widget_versions [id PK, widget_id FK RESTRICT, version, source, params, published, timestamps, deleted_at]
                 1 ---- 0..N widget_version_audit_log [version identity, states, actor snapshots]
  1 ---- 0..N widget_audit_log [widget_id, name snapshot, action, states, actor snapshots]
users [existing id PK]
  0..1 ---- 0..N widget_audit_log [actor_id FK SET NULL, actor_email, actor_name]
```

已發布版本不可改寫，由資料庫 trigger 強制（決策紀錄 0007）。元件登錄採軟刪除，保留版本歷史與已發布頁面的版本網址。兩層資料變更各自保存操作者 ID、當時信箱與名稱快照，紀錄保留 90 天，不記錄原始碼或參數預設值。資料表由後端擁有者存取，啟用 RLS 並禁止 PUBLIC 權限，不提供瀏覽器直接讀取。

內建 `::compare` 由渲染器產生圖片與滑桿 HTML，交給自帶的 `jelly-components.js` 操作。`::widget{name=compare}` 命中已發布自訂元件時使用其版本 iframe，自訂框的 CSP 不因內建圖片功能放寬。

伺服器統一渲染與淨化內容。已發布元件的型別宣告經 `markup.Options.Widgets` 傳入渲染器，參數由共用驗證函式轉為字串、有限數值、布林或日期時間。動態商品與文章依種類、分類與數量建立獨立查詢鍵，由 HTTP 層提供公開資料，渲染器不存取資料庫。

數學式由瀏覽器渲染（決策紀錄 0006）：`RenderMath` 回 `ErrNoEngine`，伺服器輸出跳脫後的 TeX 與 `jc-math[data-tex]`，瀏覽器使用自帶 KaTeX 且關閉 trust。

編輯器以 `source` 保存原稿，只在物件型 `meta` 雙寫舊版 `markdown` 欄位。合法的非物件 JSON（陣列、字串、數值、布林與明確 `null`）在建立、編輯、舊式 HTML 寫入與版本還原時保持原值。未初始化的空 metadata 可建立物件，不能將明確 `null` 當成未初始化。

HTML 匯出將精確發布版本的元件文件放入 `sandbox="allow-scripts"` 的 `srcdoc`，與線上 frame 共用參數驗證、倒數備援及主題色接收器。只接受父視窗傳來的固定色彩欄位，不放寬元件 CSP。外層文件自帶內容樣式、數學字型及所需互動資源；圖片 ZIP 改寫本地圖片路徑，伺服器不抓取遠端圖片。元件文件以兩次批次查詢載入，寫入前檢查展開內容的 8 MiB 上限，整個請求沿用兩秒 context。

## 公開搜尋的文字與寫入一致性

公開搜尋以既有 HTML 可見文字解析與 Unicode case folding 為同一份語意來源。文章 ID 決定文章原文與三個內部搜尋欄位，標題、內文與摘要各自正規化並存在資料庫，SQL 在計數及分頁前精確篩選（決策紀錄 0008）。這些欄位不加入公開 JSON，也不是可獨立編輯的內容。

建立與更新透過 GORM callback（`internal/data/search_index.go`）在同一寫入中維護搜尋欄位，包含批次、map、明確 Select／Omit、UpdateColumns 與 SkipHooks。僅衍生實際寫入的來源欄位，無法取得實際字串的來源 SQL expression 會被拒絕。匯入連結、媒體網址改寫與備份還原的 raw SQL 路徑在同一交易重算。任意外部 SQL writer 不在應用程式寫入契約內。

欄位允許 NULL，以接受舊版備份未攜帶的欄位。包含文章的還原在提交前重建全部文章的搜尋值，不信任備份中的衍生值；只還原設定或附件時不改寫文章。搜尋查詢共用公開條件，以同一快照計數及分頁；外掛搜尋保留提供者排序、候選上限與公開資料過濾，只在 SQL 分頁後載入文章。
