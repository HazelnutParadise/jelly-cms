# 37 — 多台伺服器的外掛狀態與擴充點快取保持一致

User Story：網站跑在多台伺服器時，站長在任一台啟用、停用、升級、重新載入外掛或改設定，其他台在幾秒內跟上；訪客不論打到哪一台，看到的外掛輸出都一樣。
Blocked by：36（已合併）。

## 現況

- 擴充點結果（`theme.inject`、`content.render`、`comments.component`）快取在每台伺服器自己的記憶體，以程序內的世代計數失效（`/internal/server/plugin_extensions_site.go` 的 `pluginExtensionGen`）。世代只在本機遞增，其他台要等最多 60 秒的安全上限才會重算。
- 外掛的載入狀態也只在本機：A 台啟用外掛只會在 A 台載入；B 台要到重啟時才依 `plugins.enabled` 載入。停用、升級、解除安裝同樣只影響執行操作的那一台。
- 外掛檔案放在每台自己的 `data/plugins/`，啟用或升級時只寫到執行操作的那一台。
- `plugins.updated_at` 是安裝交易的提交識別，崩潰復原以它判斷交易是否提交，不能拿來當變更訊號。

## 規則

### R1 共用的變更序號

- 新增資料表 `plugin_state_versions`，只有一列，存一個整數序號。
- 每個會改變外掛狀態或擴充點輸出的操作，在本機遞增世代的同時，把資料庫序號加一：啟用、停用、重新載入、安裝、升級、解除安裝、外掛設定寫入（含外掛自己的 `settings.set`）、擴充點設定寫入。
- 自動重啟與失效不寫資料庫：那是單一台的程序狀態。

### R2 每台定期對齊

- 每台每 3 秒讀一次序號。序號比上次看到的大時：遞增本機世代（讓擴充點快取全部失效），再依 `plugins` 表對齊本機 runtime：
  - 列為啟用、本機沒載入：載入。
  - 列為停用或已刪除、本機有載入：卸載。
  - 列上的版本和本機載入的版本不同：重新載入。
  - 狀態一致的不動，不重跑 `OnBoot`。
  - 本機處於「已停止運作」的外掛，只在列的版本或啟用狀態有變時才重新載入。
- 讀取或對齊失敗只記日誌，下次再試，不影響網站服務。
- 執行操作的那一台在自己的對齊中看到的是已一致的狀態，不會重複載入。

### R3 外掛壓縮檔存在 Postgres

多台部署不需要共用磁碟：外掛的壓縮檔本身存在資料庫，`data/plugins/` 只是每台自己的解壓副本。

- 新增資料表 `plugin_archives`：每個外掛一列，存目前版本的壓縮檔原始位元組、版本、壓縮檔 SHA-256 與解壓後目錄的雜湊（安裝交易紀錄已在算的 tree digest）。
- 安裝、升級：在寫 `plugins` 列的同一筆交易裡寫入或取代 `plugin_archives` 的那一列，兩者一起提交或一起不提交。崩潰復原仍以 `plugins` 列判斷交易是否提交，壓縮檔跟著同一個結論。
- 熱升級失敗還原：連同壓縮檔一起還原成舊版。
- 解除安裝：刪除壓縮檔列。保留設定的解除安裝也刪除壓縮檔，因為檔案已不在。
- 補齊本機副本：啟動時與每次對齊時，對每個有壓縮檔列的外掛，若本機目錄不存在或解壓後的雜湊與資料庫不同，就從資料庫取出壓縮檔，用既有的暫存加交換流程寫入 `data/plugins/<id>`，再依 R2 載入。寫入用和安裝相同的檔案鎖，不與本機的安裝互相干擾。
- 既有網站：升級後第一次啟動時，對已有 `plugins` 列但沒有壓縮檔列的外掛，把本機目錄重新打包存入資料庫。打包失敗只記錄，不影響載入。
- 本機副本與資料庫都取不到時：記錄錯誤，該外掛在本機為未載入，不影響其他外掛。
- 備份與還原：壓縮檔在資料庫裡，會跟著資料庫備份；還原後各台依 R2 自動補齊副本。

## 不改

- 單台部署的行為：序號照樣遞增，壓縮檔照樣存，對齊不會做任何事。
- 安裝交易、崩潰復原與 `plugins.updated_at` 的意義。
- 壓縮檔大小上限沿用既有的上傳限制。
- 擴充點快取原本的 60 秒安全上限與 30 秒失敗快取。

## 驗收

| 情境 | 預期 |
|---|---|
| 兩個 runtime 共用同一個資料庫與外掛目錄，A 啟用外掛 | B 在 5 秒內載入該外掛，`OnBoot` 跑一次 |
| A 停用外掛 | B 在 5 秒內卸載 |
| A 熱升級外掛 | B 在 5 秒內載入新版 |
| A 改外掛設定 | B 的 `theme.inject` 結果在 5 秒內變成新值 |
| A 執行任一操作後 | A 自己不會因為對齊而重複載入或重跑 `OnBoot` |
| A、B 各用自己的 `data/plugins/`，A 安裝並啟用外掛 | B 在 5 秒內從資料庫補齊目錄並載入 |
| A 熱升級外掛 | B 的目錄換成新版，`plugin_archives` 是新版 |
| 熱升級新版起不來 | `plugins` 列與 `plugin_archives` 都還原成舊版，B 不會載入新版 |
| 解除安裝 | `plugin_archives` 列刪除，B 卸載 |
| B 的外掛目錄被刪掉或改壞 | B 從資料庫重新補齊後載入 |
| 升級前就裝好的外掛（沒有壓縮檔列） | 第一次啟動後資料庫有了它的壓縮檔列 |
| B 的目錄與資料庫都取不到 | B 記錄錯誤、該外掛在 B 為未載入，其他外掛照常 |
| 資料庫暫時連不上 | 對齊記錄錯誤後下次再試，網站照常服務 |

## 格子與檔案

| 格子 | 檔案 |
|---|---|
| 序號資料表與讀寫 | `/internal/data/migrations/0029_plugin_state_versions.sql`（新）、`/internal/server/plugin_cluster_sync.go`（新） |
| 各操作遞增序號 | `/internal/server/plugin_detail_api.go`、`/internal/server/store_api.go`、`/internal/server/integration_shared_handlers.go`、`/internal/server/plugin_extensions.go`、`/internal/server/plugin_host.go` |
| 定期對齊 | `/internal/server/plugin_cluster_sync.go`、`/cmd/server/main.go` |
| 壓縮檔存取、補齊與回填 | `/internal/data/migrations/0029_plugin_state_versions.sql`（同一個遷移）、`/internal/server/plugin_archive_store.go`（新）、`/internal/server/store_api.go`、`/internal/server/plugin_install_recovery.go`、`/internal/server/plugin_detail_api.go` |
| 文件 | `/docs/system-operations.md`、`/docs/plugin-extensions.md`、`/AGENTS.md` follow-up |
