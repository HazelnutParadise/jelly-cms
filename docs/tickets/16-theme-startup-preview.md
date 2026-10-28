# 16 — 重啟還原已選主題，CLI 完整產生預覽

User Story：站長成功切換主題後，重啟或系統更新重啟會還原該選擇；維護者可用 jellypreview 產生其明列的全部後台預覽。
Blocked by：05、09、15 已整合 main。
Status：實作完成，驗收與交付證據見 STATUS.md 與對應 PR 最終紀錄。

## 修正範圍與行為

- 啟動完成資料庫初始化後讀取 active_theme，使用保存的精確名稱建立主題與資源快照。
- 尚未安裝、沒有紀錄或空白值時使用 default。保存的主題缺失、名稱非法、設定或資源損壞，以及讀取資料庫失敗時，保留原紀錄，嘗試 default 並回傳可記錄的警告；default 也失敗時保留兩個原因。沿用既有啟動記錄錯誤後繼續的政策，不新增終止程序條件。
- 主題切換先驗證並建立候選快照，在同一 Manager 鎖內完成已檢查錯誤的持久化，再原子發布主題、模板世代與快照。儲存失敗回 500，維持原本可用主題；不使用切回舊主題的補償流程。
- 持久化使用現有 options 的 active_theme，不新增欄位、遷移或依賴。資料庫操作沿用請求 context 並限制等待時間。並行切換完成後，保存值與 runtime 一致；不新增多實例即時同步。
- CLI 的 post_editor.html、page_editor.html 與 runtime 一樣載入 editor.html。完整預覽指目前命令明列的 21 個輸出，不擴增後台路由清單或模擬後台 API。
- CLI 的 HTML、CSS、JS 與字型引用繼續符合工作單 15 的實際內容 hash；所有可用相依檔案都複製到輸出目錄。

## 骨架與分工

- cmd/server/theme_startup.go：activateStartupTheme(ctx context.Context, tm *theme.Manager, db *gorm.DB) error。負責保存值讀取、fallback 與可識別錯誤；cmd/server/main.go 在既有啟動位置呼叫。
- internal/theme/manager.go：新增具體方法 ActivateAndPersist(name string, db *gorm.DB) error，與 Activate 共用驗證、快照及發布流程；保留原 Activate 及 core.ThemeManager 介面。
- internal/server/agent_shared_handlers.go：activateTheme 使用持久化入口，任何儲存失敗不得回成功。
- cmd/jellypreview/main.go：只對齊兩種編輯器頁面的模板依賴，不另建渲染抽象或改畫面。
- 測試代理只改 cmd/server/theme_startup_test.go、internal/theme/activation_persistence_test.go、internal/server/theme_activation_persistence_test.go、cmd/jellypreview/main_test.go；實作代理不得修改測試。主代理審查完整差異並獨立驗證。

## 驗收

- 保存另一主題後，以新 Manager 及真正新程序重啟，主題與 hash 資源均正確，保存值不被覆寫。
- 無 DB、無紀錄、空白值、非法／缺失／損壞主題、快照失敗、DB 讀取失敗、default 也失敗有明確結果及錯誤證據。
- 首次與既有選擇都能保存；無效主題不寫 DB；儲存失敗不改 runtime；並行成功切換後 DB 與 runtime 一致。
- 真正執行 CLI 子程序，繁中與英文皆正常退出，明確檢查 21 個檔名，兩種 editor 主體與腳本完整，所有本地資源與 CSS 相依 hash 對應輸出檔案位元組。
- 實際查看 CLI 產生的編輯器與一般頁面，確認資源載入、字型與版面，不把靜態預覽宣稱為有後端 API 的互動網站。
- 專用 PostgreSQL／Redis 完整競態測試、NATS、格式、vet、原生及跨平台編譯、必要 CI，全部完成後合併並清理分支。沒有正式部署。

本次恢復既有主題選擇與模板依賴，不修改 HTML、CSS 或畫面設計，因此沒有設計板差異。
