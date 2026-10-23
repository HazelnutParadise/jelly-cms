# 02 — 開發者可以載入外掛並接收真實事件

Epic：外觀與擴充
User Story：使用 JavaScript 外掛擴充 CMS，熱重載不需重啟。
Blocked by：無，安裝後整合依賴 01。
Status：in_progress

## 行為與邊界

- [ ] 外掛入口實際執行，可存取 JellyCMS.registerHook、log、config。
- [ ] 載入只通知該外掛 OnBoot，卸載只通知該外掛 OnShutdown。
- [ ] CallHook 將實際資料交給回呼，執行結果可觀察。
- [ ] 各外掛事件與全域狀態互相隔離，卸載釋放自己的回呼。
- [ ] 熱重載失敗保留舊外掛，成功只保留新回呼。
- [ ] 無效 manifest、缺少入口、目錄逃逸、JS 錯誤皆明確回報且不留半套註冊。
- [ ] LoadAll 不吞掉失敗，GetPlugin 回傳不會讓外部改動內部資料。
- [ ] 無限迴圈有實際可驗證的終止方法，逾時不得卡住網站或其他外掛。
- [ ] 內容、商品、訂單、登入、付款與主題事件由對應真實操作觸發。
- [ ] 後台啟停、上傳、重載操作與持久化狀態一致。

## 設計依據

qjs v0.0.6 的 Eval 參數是檔名，內嵌程式需 Code 選項。SetFunc 綁定 Go 回呼，保存 callback 需 Clone，Invoke 執行。現有 Load 持寫鎖再 callHook 取讀鎖會死鎖。採每個外掛獨立執行狀態，manager 鎖只處理登錄，執行 JavaScript 時不持 manager 鎖。

公開函式簽名保持相容。候選外掛完成載入才交換現有狀態。錯誤回報給管理者，不洩漏敏感設定。

### 逾時隔離

採同一執行檔的明確 worker 入口，每外掛一個子程序。伺服器 main 與 plugin 測試 TestMain 在任何正常啟動前呼叫 RunWorkerIfRequested；不使用 package init 隱藏退出。NewRuntime 在未設定 dispatcher 的程序直接報錯，不能遞迴啟動一般伺服器。

父程序保存登錄與程序控制，worker 保存 JS 回呼。load、hook、shutdown 以有長度上限的 JSON 訊息交換，日誌與協定分開。候選載入成功才換版。逾時、取消或協定異常須 Kill、Wait、關閉管線並回收讀取工作，故障外掛不阻擋其他外掛。

初始限制：manifest 64 KiB、入口與事件各 1 MiB、訊息 2 MiB、單筆日誌 16 KiB、啟動 5 秒、執行與結束各 1 秒。這些是保守預設，需以後續驗收調整，不當成負載測試結論。事件 payload 僅接受 JSON 資料，完整轉換失敗不能執行部分 payload。

原因：qjs v0.0.6 的執行時間選項未啟用；context 中斷後 Close 會在 registry 清理前 panic，共享編譯引擎仍持有 host callback。來源及暫存實驗已核對，不能只 recover 後宣稱資源已釋放。

## 驗證

已建立並由主代理執行公開 API 回歸測試，觀察真正 JavaScript callback 計算後的輸出。基本生命週期、無限迴圈隔離與程序退出已有行為驗證；真實業務事件與後台持久化操作仍需另行驗收。


## 單核心與垃圾回收期間的程序退出

主代理以原始外掛測試在 Linux 單 CPU 容器重現：忙迴圈標記後殺死父程序，worker 超過三秒仍在執行。獨立 qjs 探針也證明單 Go 執行槽的 EOF 工作不會獲得排程，兩個執行槽遇到 Go 垃圾回收時亦可停住。原始 CI 沒有記錄程序 stat，不能把該次失敗直接歸因於此缺口，亦不能把重跑成功當成修復。

處理流程：worker 收到 load 或 hook → JavaScript 執行 → Wazero 迴圈週期返回 Go → 管線 EOF 的工作可以執行 → worker 退出，父程序回收。沿用現有程序隔離與 Kill／Wait，不新增 watchdog、OS 專屬死亡訊號、資料模型或依賴，不改應用主程序的 GOMAXPROCS。

修正格子：warmWorkerCache 與 newWorkerJS 的 qjs.New 都設定 CloseOnContextDone=true，保持預設 Background，不設定或取消 qjs context。此選項讓迴圈執行 Go 端的關閉檢查，恢復排程與垃圾回收檢查點，並非改回已知清理會 panic 的 context 取消策略。qjs 在同一程序內以首次編譯決定此選項。父程序預熱與 worker 則是不同程序，兩處採相同設定才能共用預熱的磁碟快取；Wazero 磁碟快取會區分此設定，不須刪除既有快取。公開入口、協定、期限與錯誤行為保持不變。

| 操作與邊界 | 預期結果與驗證 |
|---|---|
| 單核心忙碌入口或 hook | 以真實 worker 入口，讓 worker 本身使用 GOMAXPROCS=1，先確認 JavaScript 忙迴圈標記；關閉 stdin 後三秒內退出，不能由測試先殺死來算通過 |
| 閒置管線關閉 | 真實 ready 與載入成功後關閉 stdin，期限內乾淨退出 |
| 正常使用 | 載入、回呼計算、熱重載、錯誤隔離與關機事件的既有斷言保持通過 |
| 過期與故障 | 既有父程序逾時 Kill／Wait、日誌阻塞及暫存回收保持通過，子程序測試的失敗清理只處理自己啟動的程序 |
| Linux 驗證 | 原始 busy-orphan 在 docker --cpuset-cpus=0 修正前失敗、修正後通過；完整 PostgreSQL 競態測試及 Windows／Linux 編譯通過 |

新增程序層回歸測試，使用現有 TestMain 的公開 RunWorkerIfRequested 與真實長度前綴協定；直接啟動 worker 以避免父程序環境白名單把 GOMAXPROCS=1 移除。不得新增 production 測試開關，也不得只檢查設定欄位而略過退出行為。垃圾回收反例以獨立 qjs 探針再驗證，並以原始碼核對兩處初始化均啟用檢查。

本段修正後已由主代理重跑：三條真實 worker 的單 Go 執行槽測試通過；Linux 單 CPU 容器內原始 busy-orphan 與三條新增測試連跑三次通過。主代理也重跑獨立 qjs 對照探針，原設定的單執行槽及垃圾回收情境停住，啟用檢查後皆以退出碼 0 結束。完整 PostgreSQL 競態測試、go vet、原生／Windows amd64／Linux 386 編譯通過，對抗性審查沒有已確認缺陷；PR #13 四項 CI 通過後已合併，分支已清理。Windows 只完成編譯，不宣稱該平台已完成程序行為驗收。


## 後台可以真正重新載入現有外掛

原本 `POST /api/admin/plugins/:id/reload` 只回成功訊息，沒有呼叫網站持有的 runtime。本段保留 main 的 UI 與既有 API，只完成這個既有操作；上傳、啟停與真實業務事件仍各自待驗收。

流程：管理員送出重載 → 驗證登入及管理員角色 → 在此網站的 runtime 確認該 ID 已載入 → 讀取 `data/plugins/<id>` 的候選版本 → 確認 manifest 的 ID 與請求目標相同 → 候選腳本與 OnBoot 完成 → 交換同一 runtime 的版本並關閉舊版本 → 回應成功。新版本失敗時保留舊版本，不能先卸載或改成成功訊息。

| 操作與例外 | 對外結果及驗收 |
|---|---|
| 正常重載 | 200；透過同一 runtime 的真實 JavaScript 回呼觀察版本已改變，舊回呼不再執行，另一個外掛保持原狀 |
| 匿名或非管理員 | 401／403；不能執行候選腳本、改換任何外掛或洩漏檔案內容 |
| 找不到已載入的 ID，含路徑跳脫輸入 | 404；不能把路由 ID 當成任意目錄載入，也不能載入另一個 runtime 的外掛 |
| 此網站沒有可用的 runtime | 503；不能回報重載成功 |
| manifest、入口、JavaScript 或 OnBoot 失敗 | 一般 500；舊版回呼與其他外掛仍可執行，不回傳內部路徑或原始程式錯誤 |
| manifest 的 ID 變成其他外掛 | 一般 500；在候選程式執行及 runtime 交換前拒絕，兩個既有外掛都保持原狀 |
| 請求取消或載入逾時 | 不回成功；沿用 runtime 的取消與程序回收，舊版保持可用 |
| 重啟與公開相容性 | 既有 RegisterRoutes、RegisterAdminRoutes、Runtime.Load／Reload 簽名保持相容；不新增全域 runtime 或另一份執行中的外掛清單 |

測試格子只新增 `internal/server/plugin_reload_integration_test.go`，走 RegisterRoutes 與管理 API，沿用專用 PostgreSQL harness；透過真實 runtime、worker 與 JavaScript 輸出驗證版本，不以 HTTP 200 或 metadata 變化代替。server 測試的 TestMain 可在這個測試檔呼叫既有 RunWorkerIfRequested，再執行測試，不能新增產品測試開關。

實作格子限定 `internal/server/handlers_admin.go`、`internal/server/routes.go` 與 `internal/plugin/manager.go`。將此網站的 runtime 以函式參數／閉包傳到重載 handler，保留既有公開註冊函式。runtime 可新增接受預期 ID 的重載入口，既有 Load／Reload 保持行為；預期 ID 與同一次讀取的 manifest 比對後才可啟動候選，不能在 handler 先讀一次、runtime 再讀一次而留下目標切換窗口。實作者不得修改測試。

減法審查：沿用現有 runtime 的候選交換與 worker 限制，不新增外掛管理器、背景佇列或 UI。完整驗收為成功換版、失敗留舊版、另一外掛及另一網站隔離三個流程，另以 main 原畫面的重載按鈕實際操作。

本段已由主代理先重現假成功，再驗證八個新增 HTTP 案例及完整 PostgreSQL 競態測試：332 個頂層案例通過、7 個子程序 helper 跳過，零失敗與競態警告。gofmt、go vet、原生／Windows amd64／macOS amd64 編譯通過，獨立對抗性審查沒有已確認缺陷。隔離瀏覽器以 main 原頁完成成功換版、損壞候選、ID 不符與取消操作，真實 JavaScript 輸出證明版本與外掛隔離；UI 與設計沒有改動。待 CI 交付，本段結束後依使用者指示暫停。
