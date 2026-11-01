# 24 — 機器 API 不再叫 agent：網址改 `/api/v1`，介面與文件改稱「串接」與「存取金鑰」

Epic：帳號與存取
User Story：程式與 AI agent 共用同一套機器 API（工單 22、23），名稱不該再限定 agent。對外網址、後台頁面、說明文件、錯誤訊息與設計板改用中性名稱：一個「串接」（API integration）擁有一把或多把「存取金鑰」。
Status：第三階段（工單 22 併入個人權杖、工單 23 後台只收網頁登入，皆已合併）。
Blocked by：22、23。專案沒有任何已部署站台或版本標籤，所以舊網址與舊名稱直接移除，不留別名。

## 名詞與命名

| 對象 | 之前 | 之後 |
|---|---|---|
| 機器 API 網址 | `/api/agent/v1` | `/api/v1`（舊網址回 404，不留別名） |
| 後台頁面 | `/admin/agents`、`/admin/agents/:id` | `/admin/integrations`、`/admin/integrations/:id`；舊路徑 302 轉到新路徑 |
| 側欄與頁面標題 | 中文「AI agent」、英文「AI agents」 | 中文「API 串接」、英文「API integrations」 |
| 實體 | agent | 中文「串接」、英文「integration」 |
| 憑證 | agent key／金鑰 | 中文「存取金鑰」、英文「access key」 |
| OpenAPI 安全機制名 | `AgentKey` | `AccessKey` |
| 機器 API 錯誤訊息文字 | 「A valid agent key is required」等 | 「A valid access key is required」等（英文訊息中 `agent key` 一律改 `access key`） |

不改：金鑰前綴（`jly_`，舊 `jla_` 仍有效）、權限字串（`posts.read` 等）、錯誤碼（`key_missing`、`key_invalid`、`key_revoked`、`key_expired`、`permission_missing`、`agent_disabled`、`owner_unavailable` 等）、後台私有 API 路徑 `/api/admin/agents*`（只給網頁用，屬內部名稱）、資料表（`agents`、`agent_keys`、`agent_events`）、Go 套件與型別名（`internal/agent`、`core.Agent`）、稽核值（`agent.create`、`actor_type = 'agent'`）。內部名稱的改名另案，不在本工單。

## 程式

1. 在 `internal/server/agent_routes.go` 新增常數 `machineAPIBasePath = "/api/v1"`，所有硬編碼 `/api/agent/v1` 的地方改用它：`agent_routes.go`（兩處以 `len(...)` 切字串）、`routes.go`（`isUploadRoute` 的上傳路徑清單，新路徑 `/api/v1/media`、`/api/v1/media/:filename`、`/api/v1/media/files`、`/api/v1/media/files/:id/replace` 要繼續略過全域 2 MiB 上限）、`maintenance.go`（維護模式略過清單，新路徑加入、舊路徑移除）、`agents_discovery.go`（說明端點的 `base_path`、`openapi_url`、OpenAPI 的 `paths` 鍵與 `servers` 不設）、`errors.go`、`principal.go` 與 `api_documentation.go` 內的文字。
2. 說明端點 `GET /api/v1` 與 `GET /api/v1/openapi.json`：JSON 中不得再出現 `/api/agent/v1`；`components.securitySchemes` 的鍵為 `AccessKey`，每個需要金鑰的路徑的 `security` 引用 `AccessKey`；`authentication` 區塊的說明改用「access key」。
3. 錯誤訊息（`agents_auth.go`）：`A valid agent key is required` → `A valid access key is required`；`Invalid agent key` → `Invalid access key`；`Agent key is no longer valid` → `Access key is no longer valid`；`Agent key is not currently usable` → `Access key is not currently usable`；`Agent key lacks required permission` → `Access key lacks required permission`；說明端點 `errors` 表中的文字同步（`Bearer agent key is required` → `Bearer access key is required`、`Agent key is invalid` → `Access key is invalid`，其餘含 `agent` 字樣的描述同理）。錯誤碼不變。
4. 後台頁面：`/admin/integrations` 與 `/admin/integrations/:id` 渲染原本的頁面（模板檔名可保留）；`/admin/agents` 與 `/admin/agents/:id` 以 302 轉到新路徑，保留 `:id` 與查詢字串；側欄項目 `Key: "agents"` 的 `Href` 改為 `/admin/integrations`，標籤鍵 `agents` 的值依上表；個人資料頁「API 權杖」分頁的連結改指 `/admin/integrations`；`web/admin/assets/agents.js` 內所有呼叫 `/api/admin/agents*` 的請求維持原路徑（內部 API 不改），畫面上的網址說明改為 `/api/v1`。
5. 介面文字（`web/admin/agents.html`、`agent_detail.html`、`web/admin/assets/agents.js`、`web/locales/zh-TW.json`、`en.json`）：畫面上使用者看得到的「AI agent」「agent」依上表改為「API 串接」「串接」（英文「API integrations」「integration」），「金鑰」維持；`agents.js` 內以 `jly_` 或 `jla_` 為前綴的檢查維持。元素 id、CSS 類別與 JS 變數名（`agentsPage`、`agent-dialog` 等）不改。
6. 文件與腳本：`README.md`、`ENG.md`、`STATUS.md` 內的機器 API 網址改為 `/api/v1`，並說明 `/api/agent/v1` 已移除；`skills/jelly-cms/SKILL.md` 的標題、描述與所有網址與稱呼改為存取金鑰與 `/api/v1`（技能名稱 `jelly-cms` 與檔案路徑不變）；`docs/tickets/14-agent-api.md` 開頭加一行指向本工單。
7. 設計板：`design/gen/gen_agents.py` 產生的 `Agents.dc.html`、`AgentDetail.dc.html` 內的使用者可見文字與網址依上表更新（板檔名與 id 可保留，板標題用新名稱），用 `/opt/homebrew/bin/python3.15` 重新產生，只提交相關板與 `canvas.json` 標題；`design/gen/gen_halo.py` 的 `Tokens` 板中指向 agent 管理頁的按鈕文字同步。

## 驗收（測試契約）

- 既有測試：所有以 `/api/agent/v1` 為路徑的測試（約 143 處、6 個檔案）改為 `/api/v1`；斷言訊息文字的改為新文字；其餘斷言不得放寬，整套 `internal/server` 測試維持通過。
- 新測試檔 `internal/server/api_v1_rename_integration_test.go`：
  - `GET /api/agent/v1`、`/api/agent/v1/openapi.json`、`/api/agent/v1/me` 一律 404。
  - `GET /api/v1` 的 JSON：`base_path` 為 `/api/v1`，`openapi_url` 為 `/api/v1/openapi.json`，整份回應字串不含 `/api/agent/v1`、`AgentKey`、`agent key`（不分大小寫）。
  - `GET /api/v1/openapi.json`：`openapi` 為 `3.1.0`，所有 `paths` 的鍵以 `/api/v1` 開頭，`components.securitySchemes` 只有 `AccessKey`，整份字串不含 `/api/agent/v1`、`AgentKey`。
  - 沒帶金鑰打 `GET /api/v1/me` 回 401 `key_missing`，body 的 message 含 `access key`、不含 `agent key`（不分大小寫）；格式錯誤的金鑰回 `key_invalid`，同樣檢查文字。
  - 維護模式開啟時 `GET /api/v1` 仍可用（回 200）。
  - 上傳超過 2 MiB 的檔案到 `POST /api/v1/media/files`（使用有 `media.write` 的金鑰）不被全域大小上限擋下（回應不是 413 的全域上限訊息；可回 201 或業務錯誤）。
  - `GET /admin/agents` 與 `GET /admin/agents/1` 以管理員 Cookie 登入回 302，`Location` 為 `/admin/integrations` 與 `/admin/integrations/1`；`GET /admin/integrations` 回 200 且 HTML 含「API 串接」、不含「AI agent」。
  - 側欄：以管理員 Cookie 取任一後台頁，HTML 內有連到 `/admin/integrations` 的導覽項，沒有連到 `/admin/agents` 的導覽項。
  - 英文介面（`?lang=en`）的 `/admin/integrations` 含「API integrations」。
- 守護測試：既有的 `TestAgentsPageAcceptsTheKeyPrefixTheServerIssues` 維持通過；新增靜態檢查：`web/admin/assets/agents.js`、`web/admin/agents.html`、`agent_detail.html`、`web/locales/*.json` 內使用者可見字串不含 `AI agent`、`AI agents`（用 `strings.Contains` 掃描，註解與 `User-Agent` 除外）。

## 不在本工單

- 資料表、Go 套件、型別、稽核值、後台私有 API 路徑的內部改名。
- 錯誤碼與權限字串的改名。
- 刪除 `api_tokens` 資料表。
- 讓 `/api/auth/*`、`/api/account/*` 只收瀏覽器登入。
