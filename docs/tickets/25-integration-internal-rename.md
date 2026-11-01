# 25 — 內部名稱也不再叫 agent：資料表、套件、稽核值、後台 API 與欄位全面改為 integration

Epic：帳號與存取
User Story：工單 24 只改了使用者看得到的名稱，資料表、Go 套件、稽核值、JSON 欄位、錯誤碼、後台私有 API 與設計板仍叫 agent，讀程式與資料的人會被兩套名稱混淆。本工單把剩下的 agent 名稱全部改成 integration，並刪除已無用途的 `api_tokens` 資料表。
Status：第四階段（工單 22、23、24 皆已合併）。
Blocked by：24。專案沒有任何已部署站台或版本標籤，所以舊名稱、舊路徑與舊欄位直接移除，不留別名，也不留 `/admin/agents` 的 302。

分兩個 PR：25a 只改內部（對外行為完全不變，用整套既有測試證明），25b 改對外。

## 名詞與命名

| 對象 | 之前 | 之後 |
|---|---|---|
| 資料表 | `agents`、`agent_keys`、`agent_events` | `integrations`、`integration_keys`、`integration_events` |
| 欄位 | `agent_keys.agent_id`、`agent_events.agent_id`、`agent_events.agent_name` | `integration_id`、`integration_name` |
| 稽核 `actor_type` | `'agent'` | `'integration'`（欄位加寬為 `VARCHAR(16)`） |
| 稽核 `module` | `'agents'` | `'integrations'` |
| 稽核 `action` | `agent.create`／`update`／`delete`／`disable`／`enable` | `integration.*` |
| Go 套件與型別 | `internal/agent`、`core.Agent`、`core.AgentKey`、`core.AgentEvent` | `internal/integration`、`core.Integration`、`core.IntegrationKey`、`core.IntegrationEvent` |
| echo context key | `agent.identity`、`agent.key`、`agent.permission`、`agent.content_type`、`agent.owner_role`、`agent.base_updated_at` | `integration.*` |
| JSON 欄位 | `agent_id`、`agent_name`、頂層 `agent` | `integration_id`、`integration_name`、`integration` |
| 查詢參數 | `agent_id`、`actor_type=agent` | `integration_id`、`actor_type=integration` |
| 錯誤碼 | `agent_disabled`、`agent_not_found`、`agent_name_taken` | `integration_disabled`、`integration_not_found`、`integration_name_taken` |
| 後台私有 API | `/api/admin/agents*` | `/api/admin/integrations*` |
| 後台頁面舊路徑 | `/admin/agents*` 302 | 移除（404） |
| 前端檔與鍵 | `agents.html`、`agent_detail.html`、`agents.js`、`agents.css`、locale 鍵 `agents`、側欄 Key `agents`、元素 id／CSS 類別／JS 變數含 agent | 全部改為 integration(s) |
| 設計板 | `Agents.dc.html`、`AgentDetail.dc.html`、`gen_agents.py`、canvas 鍵、範例金鑰 `jla_…` | `Integrations.dc.html`、`IntegrationDetail.dc.html`、`gen_integrations.py`、範例金鑰 `jly_…` |

不改：舊 `jla_` 金鑰仍可驗證（`integration_keys` 的前綴 CHECK 內容不變）；HTTP `User-Agent`（`user_agent`）；已提交的遷移 0025、0026；歷史工單 14、22–24 與 STATUS.md 既有段落的敘述。

## 25a 內部

1. 新遷移 `0027_integration_rename.sql`，冪等（以 `to_regclass`、`pg_constraint` 等判斷守衛）：
   - 三張表改名；三個欄位改名；`actor_type` 加寬為 `VARCHAR(16)`。
   - 表改名不會連帶改的物件逐一改名：主鍵、外鍵、CHECK、UNIQUE（`agents_*`、`agent_keys_*`、`agent_events_*`，含 0026 的 `agents_role_cap_check`、`agent_keys_prefix_check`）、9 個 `idx_agent*` 索引、觸發器 `agents_set_updated_at`、函式 `jelly_agent_set_updated_at()`、三個 IDENTITY 序列（以 `pg_get_serial_sequence` 取名）。改名後資料庫中不得再有名稱含 `agent` 的物件。
   - `actor_type` CHECK：DROP → 歷史列 `'agent'` UPDATE 為 `'integration'` → 重建為 `IN ('user','integration')`。
   - 稽核歷史列：`integration_events` 的 `module 'agents'`、`action 'agent.*'`、`route` 中的 `/api/admin/agents`、`/api/agent/v1` 前綴改新值；`system_logs` 限定 `source = 'audit'` 且 `message LIKE 'agent.%'` 的列改 `message`，所有列 `fields` 的 `agent_id` 鍵改為 `integration_id`、`module`、`route` 值依同規則改。
   - `DROP TABLE IF EXISTS api_tokens`（資料已在 0026 轉為金鑰）。
2. Go：`git mv` 套件與檔案（`internal/agent` → `internal/integration`；`internal/server` 的 `agent_*.go`、`agents_*.go` → `integration_*.go`、`integrations_*.go`）；型別、函式、變數、context key、GORM `TableName()` 與 `column:` 標籤依上表；刪除 `core.APIToken` 與其他只服務 `api_tokens` 的程式。區域變數避免命名為 `integration`，以免遮蔽套件。
3. 稽核寫入改用新值（`actor_type`、`module`、`action`）。對外 JSON、錯誤碼、網址在 25a 不變。

## 25b 對外

4. JSON 欄位、查詢參數、錯誤碼依上表（`/api/v1` 與後台 API、說明端點與 OpenAPI 的屬性與 `errors` 表、`not_exposed` 清單）。
5. `/api/admin/agents*` → `/api/admin/integrations*`；移除 `/admin/agents*` 的 302。
6. 前端：`web/admin/agents.html`、`agent_detail.html`、`assets/agents.js`、`assets/agents.css` 改名；元素 id、CSS 類別、JS 變數、locale 鍵、側欄 Key 依上表；`internal/assetcheck` 第一方清單同步。
7. 驗收腳本 `scripts/acceptance/jelly-ui.spec.mjs` 改用新路徑與元素 id。
8. 設計板：`gen_agents.py` → `gen_integrations.py`，板檔名、canvas 鍵與 id、`design/README.md` 清單、`gen_halo.py` 的連結同步，範例金鑰改 `jly_`；用 `/opt/homebrew/bin/python3.15` 重新產生，只提交相關板。

## 驗收（測試契約）

25a：
- 新遷移測試（`internal/data`）：跑到 0025 → 插入 `api_tokens` 與 `agents`／`agent_keys`／`agent_events`（含 `actor_type 'agent'`、`action 'agent.create'`、`module 'agents'`、`route '/api/admin/agents/1'`）與對應 `system_logs` 列 → 跑 0026、0027 → 斷言：新表與新欄位存在、舊表不存在、`api_tokens` 不存在、`pg_class`／`pg_constraint`／`pg_trigger`／`pg_proc` 中沒有名稱含 `agent` 的物件（排除系統物件）、序列名為新名、稽核歷史列已是新值、`system_logs.fields` 無 `agent_id` 鍵、`jla_` 與 `jly_` 前綴的金鑰列仍可寫入；重跑 0027 冪等。
- 0026 的遷移測試改為「跑到 0025 → 插 `api_tokens` → 跑 0026 → 斷言」，不再於完整遷移後重跑 0026。需要在 `internal/data` 加一個未匯出、可指定最高版本的遷移函式，只加參數不改既有語意。
- server 中直接插 `api_tokens` 或重跑 0026 的測試改為直接插 `integrations`／`integration_keys`。
- 新增稽核測試：建立串接後 `integration_events` 寫入 `actor_type 'user'`、`module 'integrations'`、`action 'integration.create'`；以金鑰呼叫後寫入 `actor_type 'integration'`。
- 其餘既有測試只做機械改名（表名、型別、套件），斷言不得放寬，整套測試維持通過。

25b：
- 既有測試的 JSON 欄位、錯誤碼、路徑改為新名稱，斷言不得放寬。
- 新測試：`/api/admin/agents`、`/admin/agents`、`/admin/agents/1` 回 404；`/api/v1/me/events` 回應有 `integration_id`、`integration_name`，沒有 `agent_id`；說明端點與 OpenAPI 整份字串不含 `agent`（`User-Agent` 除外）；`web/admin`、`web/locales`、`internal/server` 非測試程式、`design/project` 中除 `User-Agent` 外不含 `agent` 字樣（靜態掃描）。
- 瀏覽器實測：串接列表、詳情、建立與輪替金鑰、停用與啟用、中英文。
