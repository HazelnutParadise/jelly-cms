# 18 — 訪客看到符合登入狀態與公開範圍的選單

Epic：既有待修缺陷
User Story：登入者看得到標記 login_only 的項目，匿名訪客看不到；文章／頁面型選單項目只連結公開內容。
Blocked by：04、05、16 已合併；實作修改同一 Manager 檔案時須等待工作單 17 的實作代理結束。
Status：測試先行，實作與驗收中。

## 契約與相容性

- 保留 `{{menu "header"}}` 等既有語法與 menuForLocation、menuNodesFrom 的匿名預設。新增私有 resolver 接受登入布林值及目前 path，匿名剪除 login_only 節點及整個子樹，登入者保留；Active 以非空 path 與 URL 精確相等決定。
- 文章／頁面 target 使用現有 publicPostVisibility，排除私密、未知 visibility、未到發布時間、草稿與軟刪。登入不放寬內容可見性。沒有密碼欄位，不新增密碼內容模式。資料庫查詢失敗沿用回空選單的契約。
- 登入狀態只採 auth.GetCurrentUser 的完整認證及撤銷檢查，無效／過期／撤銷登入視為匿名，不因 Cookie 存在就判定登入。
- 每次公開渲染綁定獨立 menu closure，只捕捉已驗證登入布林與 path，不捕捉或保存 Echo context，不修改 Manager 的全域註冊函式。
- Manager 新增 RenderWithDBAndFuncs 與 RenderPageWithDBAndFuncs，參數為現有 templateName、data、First DB 介面及 template.FuncMap，原本四種 Render 入口與 core.ThemeManager 不改簽名。
- 快取只保存尚未執行的 parsed template，所有入口都先 Clone 再套用請求函式並執行；不得把已執行模板拿來 Clone，也不得把請求 closure 存入快取。複製、設定查詢、解析與執行失敗保持錯誤回傳。
- 共用 renderPublicTheme／renderPublicThemePage 處理完整 HTTP consumer：公開內容、彙整、商店、商品、付款替身、錯誤頁與維護頁。維護頁保留 standalone 渲染。沒有 DB 的錯誤頁轉成真正 nil DB 介面，不能讓 typed nil 引發 panic。
- 登入者 HTML 使用 private, no-store，保留原有 no-store；匿名維持原本重新確認政策，不讓共享快取混用登入者選單。不改資料表、角色權限或相依套件。

## 分工與驗收

- 實作範圍：internal/theme/manager.go、internal/server/handlers_menus.go、新增 internal/server/public_theme_render.go，以及所有上述 RenderWithDB／RenderPageWithDB HTTP 呼叫處。主代理確認確切清單後派工，不同代理不得並行改同一檔案。
- 測試代理新增 internal/theme/render_funcs_test.go 與 internal/server/menu_visibility_test.go，實作者不得修改測試。
- 覆蓋先匿名暖快取再登入再匿名、相反順序、並行不同訪客與 path、原 Render 與新入口交錯、standalone、無 DB 錯誤頁、撤銷／失效登入、login_only 父子剪枝與 typed target 公開條件。
- HTTP 包含 GET／HEAD 與登入者 no-store，現有匿名 menu helper 測試保持通過；瀏覽器實際登入／登出確認選單差異，沒有把選單當成內容存取授權。
- 主代理完整差異審查、競態與全套測試、必要 CI 全綠後合併清理，不部署。沒有畫面設計變動，不新增設計板。
