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

已建立並由主代理執行公開 API 回歸測試，觀察真正 JavaScript callback 計算後的輸出，不以 Go 端成功日誌當成完成。整合事件與無限迴圈隔離需額外測試，不以基本生命週期通過取代。
