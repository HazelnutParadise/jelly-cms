# 17 — 站長可以同時儲存主題設定與切換主題

Epic：既有待修缺陷
User Story：站長儲存外觀設定時，另一個已完成的主題切換不會被舊重載覆蓋。
Blocked by：15、16 已合併。
Status：實作與局部競態回歸完成；完整測試及交付最終結果見 [PR #36](https://github.com/HazelnutParadise/jelly-cms/pull/36)。

## 行為與錯誤

設定 HTTP 請求 → 保存設定 → 重載當前主題 → 有條件發布快照。主題切換沿用工作單 16 的持久化入口。

- 新增具體方法 `Manager.ReloadActive() error`，不改原本 Activate、ActivateAndPersist 與 core.ThemeManager 介面。
- 同一讀鎖捕捉當前主題名稱與不可變 assetSnapshot 指標，在鎖外建立候選，再持鎖比較名稱及指標。只有原啟用狀態仍有效才發布候選。
- 期間已有新啟用或新重載時，丟棄過期候選並回成功，不更改名稱、快照、快取或資料庫。即使同名主題重新啟用、A→B→A，也不得讓舊工作覆寫新快照。
- 過期候選的建立錯誤同樣丟棄；目前候選失敗則回錯誤並保留原本可用快照。沒有啟用主題時回明確錯誤。
- 不以模板 generation 判斷啟用身分，因 RegisterFunc 也會增加它。函式註冊不能吞掉仍有效的重載錯誤。
- 設定 API 以 ReloadActive 取代 GetActive 後 Activate 的兩段操作。保存失敗不重載；保存成功但目前重載失敗仍回 500，說明設定已保存但重載失敗。
- 重載不寫 options.active_theme，不增加資料欄位、遷移、相依套件、正式程式測試入口或重試迴圈，不在讀取磁碟期間長時間持有 Manager 鎖。

## 分工與驗收

- 實作只改 internal/theme/manager.go 與 internal/server/agent_shared_handlers.go。測試由另一代理新增 internal/theme/reload_test.go、internal/theme/reload_unix_test.go、internal/server/theme_reload_concurrency_unix_test.go，實作者不得修改測試。
- 一般成功、空啟用狀態、目前建立失敗、過期成功／失敗、切換 B、A→B→A、同名重新啟用、RegisterFunc 交錯均有確定時序的回歸。
- Unix 測試可用 theme.json FIFO 與 writer 開啟握手阻塞 Load，再以原子替換恢復一般檔案，完成切換後才解除舊讀取。所有等待有期限及清理，Unix build constraint 保證 Windows 測試編譯相容，不加正式程式 hook。
- HTTP 回歸以真實專用 PostgreSQL 驗證設定有保存、切換的保存值／runtime／快照／hash／渲染結果一致，重載失敗契約及原快照繼續可讀。
- 主代理審查完整差異、重跑競態回歸與全套測試、必要 CI 全綠後合併並清理分支，不部署。

本工作單不改畫面；其他確認仍存在的介面缺陷另立可獨立驗收的工作單，沿用現有設計及相同交付批次。
