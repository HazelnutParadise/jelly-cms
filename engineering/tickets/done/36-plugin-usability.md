# 36 — 外掛裝得上、升得了級、掛了會自己爬起來

Epic：外觀與擴充
User Story：站長在後台「外掛」頁就能上傳、啟用、升級外掛並看到每個外掛真實的狀態；外掛一次跑太久不會從此靜默失效；開發者照一份文件和範例就能寫出能動的外掛。
Blocked by：無。關聯：AGENTS.md follow-up「外掛 worker 逾時後不自動重啟」由本工單關閉；跨實例同步見工單 37。

## 現況（以 main `f184819` 實測）

主代理在本機全新安裝的站台實測（`data/` 空目錄、專用資料庫），結果：

| 操作 | 結果 |
|---|---|
| 「外掛」頁（`/admin/plugins`）上傳宣告任何權限的外掛 | 一律失敗：「Preview this exact archive and accept its permissions before installing」。`web/admin/plugins.html:143-170` 直接送 `POST /api/admin/plugins/upload`，沒有預覽與同意權限；伺服器要求 `accept_permissions` 與 `sha256`（`handlers_admin.go:114-117`） |
| 「外掛」頁列表 | 每個外掛都顯示綠色「Active」，停用的也是（`plugins.html:89` 寫死）；列不連到詳情頁 `/admin/plugins/:id`，頁面上沒有啟用或停用 |
| 「應用市場」頁上傳 | 有預覽與同意權限，可以安裝；安裝後預設停用，要到詳情頁啟用 |
| 啟用後：`theme.inject`、`content.render`、`OnPostPublish`、`settings`、`store`、`content.read`、後台動作 | 都正常 |
| 外掛啟用中再上傳新版本（「應用市場」上傳或「全部更新」） | 409「Plugin is running; disable it before uploading a new version」（`store_api.go:746-760`）；要先停用、上傳、再啟用 |
| 後台動作跑 1.5 秒 | worker 被終止；之後所有呼叫回「Plugin action failed」，詳情頁仍顯示「執行中」（`loaded: true`），鉤子與擴充點全部停止，直到管理員按「重新載入」。啟用按鈕對這種外掛沒有作用（`plugin_detail_api.go:266` 只看 `loaded`） |
| `system.plugin_failed` 通知 | 有定義（`notify/event.go:72`）但沒有任何程式送出 |
| 開發者文件 | README 的 API 範例只有 `config`、`registerHook`、`log`；`store.get(collection, key)` 的兩個參數、`http.fetch` 的回傳形狀、`OnBoot` 在每次載入都會跑等事都沒寫。照 README 寫 `store.get('x')` 會得到「host API arguments must be JSON values」 |
| 用語 | 繁中介面的導覽與外掛頁寫「插件」，其餘頁面寫「外掛」 |

## 規則

### R1 自動重啟（runtime）

worker 因逾時、回傳超過上限、協定錯誤或程序結束而失效，且外掛沒有被卸載時，runtime 自動重新載入同一目錄的外掛。「只隔離該次呼叫」不可行：逾時的 JavaScript 無法在保留 worker 的情況下中止（`worker_process.go:279-281`），協定錯誤與程序結束也一樣。

- 程序結束由 runtime 監看 worker 的讀取結束偵測，不等下一次呼叫。正常的關閉、卸載與換版不算失效。
- 重新載入走既有的候選載入流程（新 worker、入口腳本、`OnBoot`），成功才換上；JavaScript 記憶體狀態不保留，`settings` 與 `store` 在資料庫裡不受影響。
- 等待時間依序 1 秒、5 秒、30 秒、2 分鐘。30 分鐘內第 5 次失效就進入「已停止運作」，所以只有四段等待。
- 等待期間：動作與擴充點呼叫立即回「外掛未載入」；事件直接丟棄，不逐筆記錄；後台動作回 `{"dispatched": false, "reason": "plugin_restarting"}`。
- 失效次數只計最近 30 分鐘內的失效。達到 5 次時外掛進入「已停止運作」：不再自動重啟，直到管理員重新載入、重新啟用或上傳新版本。
- 重啟綁定失效當下的那個 worker 狀態。等待結束時若該外掛已被卸載、停用、重新載入、重新啟用或升級（登錄裡已不是同一個狀態），重啟取消且不算失效。
- 重啟用 runtime 自己的期限，不使用任何請求的 ctx；不寫 `plugins` 列。
- 失效狀態留在登錄裡（`IsLoaded` 仍為 true），所以擴充點會判為不可用、上傳新版本會走 R3 的熱升級。
- 失效日誌由 runtime 寫入該外掛日誌（level error，含原因與下次重啟時間）。
- `Runtime.Status(id) (PluginStatus, bool)`：`State` 為 `running`、`restarting`、`crashed`；附 `Faults`（30 分鐘內失效次數）、`LastError`、`LastFailureAt`、`NextRestartAt`。欄位不用 `failures`，那個名稱已是事件失敗計數（`events.go:70`）。
- `Runtime.SetFailureHandler(func(id string, status PluginStatus))`：每次失效與每次進入 `crashed` 都呼叫一次，在 runtime 鎖外執行。`Runtime.SetRecoveryHandler(func(id string))`：自動重啟成功時呼叫，伺服器用它遞增擴充點世代。
- 管理員的載入、重新載入與卸載會先取消已排程的重啟並等待進行中的重啟結束，期間不排新的重啟；操作失敗而外掛仍在等待重啟時重新排程。
- 等待時間、次數與時間窗是套件變數，測試可以縮短。

### R2 伺服器接線

- 失效處理：遞增擴充點世代。進入 `crashed` 時送 `system.plugin_failed` 給管理員，`params` 含 `plugin`（ID）、`name`、`error`，`GroupKey` 為 `system.plugin_failed:<id>`；每次進入 `crashed` 一則。
- 啟用：外掛已載入但狀態不是 `running` 時，啟用等於重新載入。失敗時列的 `enabled` 不變，回 409「外掛無法啟動」；只有原本未載入的外掛才在寫列失敗時卸載。
- 管理員的重新載入在 `withPluginInstallLock` 內執行。`plugins.updated_at` 是安裝交易的提交識別（`plugin_install_recovery.go:265`），重新載入與自動重啟都不改它；工單 37 的跨實例同步另定訊號。

### R3 升級執行中的外掛

上傳新版本（「外掛」頁、「應用市場」上傳、從網址安裝、「應用市場」更新）時，外掛若已載入（含 `restarting`、`crashed`）：

1. 照既有流程暫存、驗證並以交易紀錄換上新檔案。
2. 寫入 `plugins` 列（version、permissions_granted_hash、updated_at 為提交戳記）。寫入失敗時還原檔案，回 500，原本的狀態不變。先寫列是因為新版 `OnBoot` 呼叫 host API 時會比對列上的權限核准（`plugin_host.go:135-138`）。
3. 從新檔案熱重載。失敗時先還原列、再還原檔案（還原用獨立的 10 秒期限；列還原失敗時保留交易紀錄交給啟動復原），回 409「新版本無法啟動，已保留原版本」，附外掛日誌的最後一則錯誤；原本 `running` 的繼續執行，原本 `restarting`、`crashed` 的狀態不變。還原列失敗時回 500，並在外掛日誌記下「列為新版、檔案為舊版」。
4. 從步驟 1 到完成或還原為止不跟隨請求取消（`context.WithoutCancel` 加自己的期限）。
5. 程序在換檔與重載之間崩潰時，啟動復原以 `plugins` 列為準：列已是新版就保留新檔並在啟動時載入，否則還原舊檔。

外掛沒有載入時行為不變。新版本宣告的權限與已核准的不同，仍要先預覽並同意。熱升級期間持有安裝鎖，最長約 8 秒，期間其他外掛的安裝、啟用與停用會等待。

### R4 狀態欄位

- `GET /api/admin/plugins`（與 `/api/v1/plugins` 共用 `queryPlugins`）每個項目改以 struct 定義，新增 `enabled`（布林）、`managed`（有 `plugins` 資料列為 true）、`state`（`disabled`、`running`、`restarting`、`crashed`、`stopped`：已啟用但沒有載入）、`last_error`（沒有時省略）。沒有資料列的目錄 `enabled` 為 true、`managed` 為 false，與啟動時的載入規則相同。既有欄位不變。`/api/v1/plugins` 的 OpenAPI schema（`integrations_discovery.go:357-358`）同步更新。
- `GET /api/admin/plugins/:id` 新增 `state`（值域同列表）、`faults`、`last_error`、`last_failure_at`、`next_restart_at`（沒有時省略）。`loaded` 意義不變，`restarting`、`crashed` 時仍為 true。

### R5 「外掛」頁

依設計板 `design/project/Plugins.dc.html`：

- 標題下方摘要「N 啟用 · N 停用 · N 異常」。
- 每列：名稱連到詳情頁（`managed` 為 false 的不連、也不顯示啟用或停用）；狀態徽章（已啟用、已停用、重新啟動中、已停止運作、未載入）；異常時顯示 `last_error`；動作為啟用或停用、重新載入（已啟用時）、已停止運作時顯示「重試」（＝重新載入）。
- 上傳：選檔 → 預覽（`?dry=1`）→ 對話框列出名稱、版本、未簽章提示、權限清單與擴充點，勾選同意後安裝；成功後提示並連到詳情頁。外掛執行中時說明「會直接升級，失敗時保留目前版本」。
- 頁尾連到 GitHub 上的外掛開發文件（一般連結，不載入外部資源）。
- 繁中語系「插件」全部改「外掛」。

詳情頁（`system-admin.js` 的外掛詳情）顯示狀態徽章與 `last_error`；`crashed` 時「啟用」按鈕文字為「重試」。

### R6 開發者文件與範例

- 新增 `docs/plugin-development.md`：資料夾結構與打包、`plugin.json` 全部欄位、`JellyCMS` 每個方法的簽名與回傳、生命週期（每次載入都跑入口與 `OnBoot`、自動重啟、熱升級）、限制、權限、除錯（外掛日誌）、上架前檢查清單。鉤子目錄與擴充點契約連到既有文件，不重抄。
- 新增 `examples/plugins/hello-jelly/`（`plugin.json`、`index.js`、`README.md`）：用到設定、`store`、`OnPostPublish`、`theme.inject`、一個後台動作。新增測試以真實 runtime 載入它，確保範例一直能跑。
- README 外掛章節改成摘要並連到開發文件；修正 `config.set` 範例。

## 不改

- 1 秒單次呼叫上限、1 MiB 上限、事件「連續 5 次失敗暫停」的規則。
- 外掛安裝後預設停用。
- 擴充點快取的世代規則（工單 29）。

## 驗收

| 流程 | 預期 |
|---|---|
| 外掛頁上傳宣告 `events`、`settings` 的外掛 | 出現權限確認；同意後安裝成功，列表顯示「已停用」，名稱可點進詳情頁 |
| 外掛頁按「啟用」 | 列表徽章變「已啟用」，前台出現 `theme.inject` 的內容 |
| 動作跑 1.5 秒（測試把等待縮成 50 毫秒） | 該次回失敗；之後狀態 `restarting`，等待中再呼叫動作回 `dispatched: false`、`reason: plugin_restarting`；等待後回到 `running`，下一次動作成功；外掛日誌有失效紀錄 |
| worker 程序在閒置時被外部終止 | 不必等下一次呼叫，狀態轉 `restarting` 並自動回到 `running` |
| 連續失效 5 次 | 狀態 `crashed`，不再重啟；管理員收到一則 `system.plugin_failed`；按「重試」後回到 `running`，失效次數歸零 |
| 停用一個 `restarting` 的外掛 | 等待中的重啟取消，不會再被載入 |
| 重新載入一個 `restarting` 的外掛 | 只載入一次（`OnBoot` 一次），等待中的重啟取消 |
| 執行中上傳新版本（入口正常） | 200；不必停用；新版本的鉤子生效，`OnShutdown` 給舊版、`OnBoot` 給新版 |
| 執行中上傳權限增加的新版本（已預覽同意），`OnBoot` 讀 `settings` | 200；`OnBoot` 讀到設定值，沒有「approval is stale」 |
| 執行中上傳新版本（入口拋錯） | 409；舊版繼續處理鉤子；`data/plugins/<id>` 內容與上傳前相同；`plugins.version` 不變 |
| `GET /api/admin/plugins` | 每項有 `enabled`、`state`；停用的外掛 `state` 為 `disabled` |
| `examples/plugins/hello-jelly` | 測試以真實 runtime 載入並觸發 `OnPostPublish` 與擴充點，結果符合 README |

## 格子與檔案

| 格子 | 檔案 |
|---|---|
| A runtime 自動重啟與狀態 | `internal/plugin/supervisor.go`（新）、`internal/plugin/manager.go` |
| B 伺服器接線、啟用與重新載入 | `internal/server/plugin_supervision.go`（新）、`plugin_detail_api.go`、`integration_shared_handlers.go`、`cmd/server/main.go` |
| C 熱升級 | `internal/server/store_api.go`、`handlers_admin.go` |
| D 外掛頁與詳情頁 | `web/admin/plugins.html`、`web/admin/assets/system-admin.js`、`web/locales/zh-TW.json`、`web/locales/en.json`、`design/project/Plugins.dc.html`、`design/gen/gen_halo.py`（詳情頁板） |
| E 文件與範例 | `docs/plugin-development.md`（新）、`examples/plugins/hello-jelly/`（新）、`README.md`、`docs/plugin-extensions.md`（逾時段落） |

測試檔（實作者不得修改）：`internal/plugin/supervisor_test.go`、`internal/server/plugin_supervision_integration_test.go`、`internal/server/plugin_hot_upgrade_integration_test.go`、`internal/server/plugin_example_integration_test.go`。

## 驗收紀錄

交付時記下的驗證結果與裁決，依時間由新到舊。

- 工單 36：外掛裝得上、升得了級、掛了會自己重新啟動。實測發現「外掛」頁上傳任何宣告權限的外掛都失敗、列表一律顯示 Active、逾時一次外掛就永久失效、執行中無法升級。現在：(R1) runtime 監督 worker，逾時、回傳超限、協定錯誤或程序結束後依 1 秒、5 秒、30 秒、2 分鐘自動重新載入，30 分鐘內失效 5 次進入已停止運作並送 `system.plugin_failed`；重啟綁定失效的那個狀態，管理員操作會先取消並等待進行中的重啟。(R2) 啟用對失效的外掛等於重新載入，重啟中的動作回 `plugin_restarting`、已停止運作回 `plugin_crashed`。(R3) 已載入的外掛上傳新版本直接熱升級：換檔、寫列、重載，失敗時先還原列再還原檔案並回 409。(R4) `GET /api/admin/plugins` 與 `/api/v1/plugins` 新增 `enabled`、`managed`、`state`、`last_error`，詳情新增 `state`、`faults` 等。(R5) 外掛頁改為預覽並同意權限後安裝，顯示真實狀態、可啟用停用重試，繁中「插件」全改「外掛」。(R6) 新增 `docs/plugin-development.md` 與 `examples/plugins/hello-jelly/`。驗證：測試先於實作（`internal/plugin/supervisor_test.go` 10 條在空骨架失敗；`plugin_supervision_integration_test.go`、`plugin_hot_upgrade_integration_test.go` 8 條中 7 條在原程式失敗），另有範例守門測試；獨立審查 0 個 P0／P1、5 個 P2、6 個 P3 全部處理。主代理重跑：`go test ./internal/plugin -count=1 -race` 通過，`internal/server` 外掛、擴充點、應用市場相關測試通過，其餘 26 個套件通過；完整 `internal/server` 套件本機因磁碟空間不足導致測試資料庫中斷，以 CI 為準。瀏覽器驗收（本機專用資料庫）：上傳含權限外掛、啟用、前台注入、逾時後自動恢復、熱升級成功與失敗還原、重啟伺服器後維持啟用。設計板 `Plugins.dc.html` 同批更新，線上畫布已同步（第 71 版）。PR 上的自動審查另提出 13 項，全部處理並各有回歸測試：升級前暫停自動重啟、升級途中舊版核准仍有效、已啟用未載入的外掛改走啟用、列表讀取失敗回 500、還原不完整時停止自動重啟、停用不受請求中斷影響、上傳核對檔案雜湊並綁定預覽。沒有部署。
