# 08 — 站長可以經營與維運網站：設定分頁、網址與爬蟲、程式碼注入、維護模式、轉址與 404、系統日誌、版本更新、快取與佇列、備份匯出、WordPress 匯入、應用市場、外掛詳情、附件策略

Epic：系統與維運
User Story：站長在一個分頁式設定頁完成網站、網址、搜尋引擎與 AI 爬蟲、程式碼注入、維護模式、郵件、快取與佇列、更新的設定；管理轉址與 404；看系統日誌；備份、匯出、還原；從 WordPress 匯入；從應用市場安裝與更新外掛與主題、調整外掛設定與擴充點；管理附件的儲存策略與分組。
Blocked by：05（維護模式中介層與選項、`content.css`）、06（`internal/notify`、`RequireInteractive`、員工閘、`user_invites`）、07（金流與運送分頁由 07 註冊）、09（`markup.Flatten` 供匯出）。本工作單在同一條 main 上接續時以各該契約為準；本分支放標示「shared with ticket NN」的最小存根。
Status：draft（本工作單只交付設計板補齊、契約、骨架與失敗的驗收測試；實作由另一批代理依本文件完成）
設計稿：`design/project/Settings、SettingsRoutes、SettingsCrawlers、Redirects、Logs、Update、UpdateContainer、Infra、Backup、Import、AppStore、PluginDetail、MediaPolicies、Media、Palette、States.dc.html`；本工作單先補：`Settings` 板補齊沒畫的分頁（網站、登入與註冊、留言、郵件與通知、SEO 預設）並統一所有板的分頁列；`SettingsRoutes` 預設網址改成 `/post/{slug}`、每頁 10；`Redirects` 板補 SEO 預設分頁；`Infra` 板移除 Memcached；`canvas.json` 把 Palette 與 States 的「尚未實作」拿掉。

## 先修的安全問題（08a 第一批）

1. 外掛上傳把 manifest 的 `id` 直接接進路徑（目錄穿越）：先用 `pluginIDPattern` 驗證再 join，解壓到暫存目錄後 rename，單檔與總量上限。
2. `/uploads` 以 inline 方式提供任何檔案（SVG／HTML 可執行腳本）：加 `X-Content-Type-Options: nosniff`，非圖片一律 `Content-Disposition: attachment` 與 `Content-Security-Policy: sandbox`，SVG 只能下載。
3. `c.RealIP()` 可被偽造：加 `e.IPExtractor` 與 `JELLY_TRUSTED_PROXIES`。
4. 沒有請求大小上限：全域 `BodyLimit` 2 MiB，上傳路由另設。
5. `GET /api/admin/settings` 回傳明文金鑰、`POST` 接受任意鍵：讀取以 `****1234` 遮罩、寫入只接受已登記的鍵且稽核。

## 流程與驗收

- [ ] 站長在 `/admin/settings/<tab>` 看到統一的分頁列（網站、閱讀與網址、搜尋引擎與 AI 爬蟲、登入與 OAuth、留言、金流、運送、郵件與通知、快取與佇列、更新；備份與匯入、使用者、系統日誌為連結）；分頁由各工作單以 `RegisterSettingsTab` 註冊；`/admin/settings` 導向 `site`；每個分頁的設定值存成已登記的鍵，寫入有稽核。
- [ ] 站長在「閱讀與網址」設定文章網址樣式（預設 `/post/{slug}`，可用 `{slug} {year} {month} {day} {category} {id}`，最多 5 組舊樣式自動 301）、分類／標籤／作者／彙整前綴、別名策略、首頁模式（最新文章或指定頁面）、每頁篇數、自動摘要長度、Feed 模式；`robots.txt`、`sitemap.xml`（分檔，每檔 ≤ 50000）、`feed.xml`、`llms.txt`、`/.well-known/tdmrep.json`、IndexNow 金鑰檔都在 `/:slug` 之前註冊。
- [ ] 站長在「搜尋引擎與 AI 爬蟲」設定各類型是否索引、圖片與摘要政策、IndexNow、AI 爬蟲矩陣（預設封鎖訓練放行搜尋，可自訂規則且不被目錄更新覆蓋）、對不遵守 robots 的爬蟲伺服器端 403、TDM 保留宣告、llms.txt 內容；看 robots 預覽與 7 天爬蟲統計；文章與商品有 `seo_index`、`seo_ai` 覆寫欄位，被設為 noindex 的頁面不進 sitemap。
- [ ] 站長在「程式碼注入」填 `<head>` 全站、`<head>` 內容頁、頁尾三段原始 HTML（各 ≤ 64 KiB），只有 admin 且互動限定並重新輸入密碼才能寫；只輸出在前台公開頁，後台、登入、安裝不輸出，`/checkout*` 與 `/account*` 預設不輸出；寫入留稽核（誰、長度、sha256）。
- [ ] 站長在「維護模式」開關、填訊息（≤ 280）、設 `Retry-After`；開啟時 `setMaintenance` 記錄開始時間；前台行為照 05。
- [ ] 站長在 `/admin/redirects` 管理 301／302／410、萬用字元規則（只允許 `*`）、保留查詢字串、到期、命中數、目標失效標記、CSV 匯入（預跑）與匯出、測試路徑；改文章別名自動建立 301；看 404 紀錄（彙總、建議目標、忽略、清空）。
- [ ] 站長在 `/admin/logs` 依等級、來源、關鍵字、時間篩選，看即時尾巴（SSE）、下載 24 小時 NDJSON、看摘要卡；日誌存資料庫、自動遮罩敏感欄位與 IP、保留 30 天（錯誤 90 天）；每個請求有 `req_` 開頭的 Request ID，錯誤頁顯示並可連到日誌。
- [ ] 站長在「更新」看到目前版本、最新版本、檢查時間、執行方式（容器／Kubernetes／套件管理／獨立執行檔，可用 `JELLY_RUNTIME` 覆寫）；容器與套件管理只顯示新版本與換版指令；獨立執行檔可按更新：下載、驗證 sha256 與 minisign 簽章、先備份、換執行檔、重啟、啟動時遷移與健康檢查，任一步失敗自動退回舊版；每日自動檢查並發 `system.update_available` 通知。
- [ ] 站長在「快取與佇列」選後端（快取：記憶體、Redis／Valkey；佇列：PostgreSQL、Redis Streams、NATS JetStream）、測試連線、看統計與佇列表、重試失敗、暫停／恢復、無損切換後端；環境變數 `JELLY_CACHE_URL`、`JELLY_QUEUE_URL`、`JELLY_QUEUE_WORKERS` 優先。
- [ ] 背景工作全部走佇列與排程表：更新檢查、日誌清理、備份與保留、轉址命中回寫與目標檢查、商店目錄刷新、回收桶清除、06 的投遞、07 的庫存逾期；多節點只有一個排程器，重複觸發以 `name:slot` 去重；工作至少執行一次且冪等。
- [ ] 站長在 `/admin/tools/backup` 立刻備份（選內容：資料庫、附件、設定）、排程與保留、遠端目的地、還原（輸入 `RESTORE` 確認，先做 `pre_restore` 備份並開維護模式）、匯出 Markdown zip／WXR／JSON；備份檔 AES-GCM 加密、不放在 `/uploads` 下、下載互動限定。
- [ ] 站長在 `/admin/tools/import` 上傳 WXR（`.xml`、`.xml.gz`、`.zip`，≤ 512 MiB，串流解析）、對應作者（建立為邀請中的使用者）、選類型與附件抓取、預跑看衝突、執行、取消；重跑冪等；內文網址改寫；舊網址自動建轉址；垃圾留言略過。
- [ ] 站長在 `/admin/store` 瀏覽、搜尋、分類、看更新、一鍵全部更新、上傳 zip、從網址安裝；官方套件帶 minisign 簽章，第三方安裝前顯示所需權限並要求接受（雜湊比對）。
- [ ] 站長在 `/admin/plugins/:id` 看詳情、設定表單（manifest v2 宣告，秘密欄位遮罩）、測試、擴充點選擇、權限（已強制的標「已強制」，其餘標「已宣告、尚未強制」）、日誌、啟用／停用、移除（保留或刪除設定）；外掛資料改存 `plugin_data`，`config.set` 持久化。
- [ ] 站長在 `/admin/media/policies` 管理儲存策略（本機、S3、外掛提供）、分組、預設策略、搬移；附件有模型（尺寸、alt、用在哪裡、sha256、來源）、EXIF 去除、大小與型別上限（預設 20 MB）、從網址抓取（封鎖內網）；啟動時把 `data/uploads` 匯入本機策略且網址不變。
- [ ] ⌘K 新增「立刻備份」「清除快取」「看日誌」「從 WordPress 匯入」；所有新頁面照設計板、手機 390px 可用、不從外部網址載入資源。

## 錯誤與邊界

設定鍵未登記 400、值超過 64 KiB 413；轉址目標只接受同站路徑（不以 `//` 或 `/\` 開頭）或 admin 輸入的絕對 http(s)，拒絕 `javascript:`、`data:`、CRLF、userinfo，`$1` 不可在開頭，迴圈與鏈深度 5 在儲存時檢查；404 紀錄路徑 ≤ 512 且不含查詢字串，表上限 5000 列；日誌查詢上限 200 列；更新只接受 HTTPS 與主機允許清單、200 MiB 上限、不接受降版（明確退回除外）、tar 內只允許 `jelly-cms[.exe]` 與 `web/**`；備份還原驗證 manifest 與校驗和、大小與壓縮比上限、不以條目名稱直接寫檔；匯入拒絕非 WXR、深度與 token 上限；`internal/safehttp` 在撥號時封鎖 loopback／私有／link-local／CGNAT，最多 3 次轉址；外掛 `id` 先驗證再組路徑。

## 契約（骨架與測試共同依據）

### 設定分頁註冊

`internal/server/settings_tabs.go`：`type SettingsTab struct{ Key, Label, Href string; Order int; Roles []string }`、`RegisterSettingsTab(tabs ...SettingsTab)`（同 Key 覆蓋）、`settingsTabsFor(user) []SettingsTab`；頁面 `GET /admin/settings/:tab` 經 `renderAdmin` 帶 `Tabs`、`Active`；`GET /admin/settings` 302 到 `site`。本工作單註冊 site、routes、crawlers、auth、comments、mail、infra、update；07 註冊 payment、shipping。

### 資料（遷移 0016 起；0015 為 09 的 `post_source`）

| 檔 | 內容 |
|---|---|
| 0016_schema_migrations | `schema_migrations(version int pk, name, applied_at)`；執行器首次補記 0001–0015；DB 版本高於執行檔時啟動拒絕 |
| 0017_redirects | `redirects(id, from_path, to_url, kind 301|302|410, is_rule, keep_query, source manual|auto|import, expires_at, hits, last_hit_at, target_status, checked_at, created_by, timestamps)` 唯一索引 `lower(from_path)` where not is_rule；`not_found_log(id, path, path_hash unique, count, first_seen, last_seen, referrer_kind, ua_kind, ignored)` |
| 0018_system_logs | `system_logs(id, at, level 0–3, source app|http|payment|plugin|audit|cron|job|crawler, message, fields jsonb, request_id, actor_user_id, plugin_id, ip_masked)` 索引 (at desc)、(source, at)、(level, at)、(request_id) |
| 0019_jobs | `jobs(id uuid, queue, kind, payload jsonb, state queued|running|done|failed, attempts, max_attempts, run_at, locked_by, locked_until, last_error, dedupe_key unique null, created_at, finished_at)` 索引 (queue, state, run_at) |
| 0020_backups_imports | `backups(...)`、`import_jobs(...)`、`import_map(job_id, wp_type, wp_id, local_type, local_id, wp_url)` |
| 0021_plugins_store | `plugins(id pk, kind, version, source, enabled, signature official|unsigned, permissions_granted_hash, previous_version, ...)`、`plugin_settings(plugin_id pk, values jsonb, secrets_enc bytea, version, ...)`、`plugin_setting_snapshots`（留 3 版）、`plugin_data(plugin_id, collection, key, value jsonb)`、`extension_bindings(point, provider_id, position)`、`store_catalog`、`store_meta` |
| 0022_media | `media_policies(id, name, driver local|s3|plugin:<id>, purpose media|backup, config jsonb, secret_enc, is_default, deleted_at)`、`media_groups`、`media_files(id, policy_id, group_id, owner_id, path, filename, mime, size, width, height, alt, sha256, source, original_of_id, variants jsonb, exif_stripped, deleted_at; unique(policy_id, path))`、`media_refs(file_id, ref_type, ref_id)` |
| 0023_seo_columns | `posts`、`products` 加 `seo_index varchar(8) default 'inherit'`、`seo_ai varchar(8) default 'inherit'` |

### Go 介面與套件

```go
// internal/settings：已登記鍵的型別化讀寫、遮罩、稽核
type Spec struct{ Key, Kind string /* string|int|bool|json|secret */; Default string; MaxBytes int; Tab string }
func Register(specs ...Spec); func Get(ctx, key) string; func Set(ctx, actor uint, kv map[string]string) error; func Masked(key, value string) string

// internal/version：ldflags 注入 Version、Commit、BuiltAt；Runtime() {Kind container|kubernetes|package|binary; DetectedBy string; Writable bool}；偵測邏輯放在 internal/update：`DetectRuntime(Probe{Env, Files, Executable, ExecDirWritable}) version.RuntimeInfo`，version.Runtime() 組出真實 Probe 後呼叫它

// internal/syslog：Sink 寫 system_logs，Redact(fields)；echo 中介層 RequestID（req_ 前綴）與 HTTP 存取記錄（錯誤與 > 1 s）

// internal/cache
type Store interface{ Get(ctx, ns, key string) ([]byte, bool, error); Set(ctx, ns, key string, v []byte, ttl time.Duration) error; Delete(ctx, ns string, keys ...string) error; Flush(ctx, ns string) error; Incr(ctx, ns, key string, ttl time.Duration) (int64, error); Stats(ctx) (Stats, error); Ping(ctx) (time.Duration, error); Close() error }
func Open(cfg Config) (Store, error) // Driver memory|redis
func Namespaced(s Store, ns string) Store

// internal/queue
type Job struct{ ID, Queue, Kind, DedupeKey string; Payload json.RawMessage; Attempts, MaxAttempts int; RunAt time.Time }
type Queue interface{ Enqueue(ctx, j Job) (string, error); Reserve(ctx, queues []string, worker string, lease time.Duration) (*Job, error); Ack(ctx, id string) error; Fail(ctx, id string, cause error, retryAt *time.Time) error; Retry(ctx, id string) error; Stats(ctx) ([]QueueStats, error); Drain(ctx, limit int) ([]Job, error); Ping(ctx) (time.Duration, error); Close() error }
func Open(cfg Config) (Queue, error) // Driver postgres|redis|nats（nats 以 build tag）
func Register(kind string, h func(context.Context, Job) error)
func Switch(ctx, from, to Queue) error

// internal/schedule：Schedule{Name, Spec, Queue, Kind, Jitter}；單一排程器以 pg_try_advisory_lock(731984201, 2) 選出；DedupeKey = name:slot
// internal/safehttp：Client(opts) *http.Client，撥號時封鎖內網，3 次轉址，大小與時間上限
// internal/storage：Driver{Put, Open, Stat, Delete, URL, Test}；local、s3（minio-go）
// internal/ext：Register(point, id, provider)、Resolve(point)；擴充點 search.engine、media.storage（出貨），其餘只宣告
// internal/seo：robots、sitemap、feed、llms.txt、tdmrep、AI 爬蟲目錄（CatalogVersion、ignores_robots）
// internal/redirects：Match(path) (*Redirect, bool)、Validate(to)；命中計數記憶體累積每 30 秒回寫
// internal/update：Check(ctx) (Release, error)、Apply(ctx, version) 狀態寫 data/update-state.json、Rollback；minisign 公鑰內嵌
// internal/backup、internal/importer（WXR 串流）、internal/plugin manifest v2（permissions、settings、extensions）
```

佇列名稱：`mail`、`media`、`webhook`、`import`、`schedule`。工作種類：`mail.send`、`webhook.deliver`、`media.thumbnail|webp|fetch_url|move_policy`、`import.wxr|attachments`、`backup.run|prune`、`update.check|apply`、`logs.prune`、`trash.purge`、`notifications.prune`、`redirects.flush_hits|check_targets`、`store.refresh`、`sitemap.indexnow`。重試退避 30 秒、2 分、10 分，最多 5 次，失敗保留 7 天。

### 端點（全部 `/api/admin/*` 限 admin；`[I]` 為互動限定）

- 設定：`GET|PUT /api/admin/settings`（GET 遮罩；PUT 未登記鍵 400、稽核）、`GET|PUT[I] /api/admin/settings/injection {head_all, head_content, footer, current_password}`、`GET|PUT /api/admin/maintenance {enabled, message, retry_after}`。
- 公開：`/robots.txt`、`/sitemap.xml`、`/sitemap-{posts|pages|categories|products}-{n}.xml`、`/llms.txt`、`/.well-known/tdmrep.json`、`/feed.xml`、`/c/:slug/feed.xml`、`/author/:u/feed.xml`、`/<indexnow_key>.txt`、`/healthz`。
- SEO：`GET /api/admin/seo/robots-preview`、`GET /api/admin/seo/crawler-stats?days=7`、`GET /api/admin/seo/overrides`。
- 轉址：`GET /api/admin/redirects?kind=&source=&q=&page=&per_page=` → `{items, total, counts:{all,301,302,410,auto,broken}}`、`POST`、`PUT /:id`、`DELETE /:id`、`POST /redirects/test {path}`、`POST[I] /redirects/import?dry=1`、`GET /redirects/export.csv`；`GET /api/admin/not-found?days=&ignored=`、`POST /not-found/:id/ignore`、`DELETE /not-found`。
- 日誌：`GET /api/admin/logs?source=&level=&q=&since=&before_id=&limit=`、`GET /logs/stream`（SSE）、`GET[I] /logs/export?hours=24`、`GET /logs/summary`。
- 更新：`GET /api/admin/update`、`POST /update/check`、`POST[I] /update/apply {version}`（容器 409 `{runtime}`）、`POST /update/cancel`、`POST[I] /update/rollback`、`PUT /update/settings`。
- 基礎設施：`GET /api/admin/infra`、`PUT /infra/cache`、`POST /infra/cache/test`、`POST /infra/cache/flush {scope}`、`PUT /infra/queue`、`POST /infra/queue/test`、`POST /infra/queue/switch`（202）、`POST /infra/queue/pause|resume`、`GET /api/admin/jobs?queue=&state=`、`POST /jobs/:id/retry`、`POST /jobs/retry-failed`、`DELETE /jobs/:id`、`GET|PUT /api/admin/mail/settings`、`POST /mail/test`。
- 備份與匯入：`GET /api/admin/backups`、`POST[I] /backups {content[], note}`（202）、`POST /backups/:id/retry`、`GET[I] /backups/:id/download`、`POST[I] /backups/:id/restore {confirm:"RESTORE"}`、`DELETE /backups/:id`、`PUT /backups/schedule`、`POST[I] /export {format: markdown_zip|wxr|json}`、`GET[I] /exports/:id/download`；`POST /api/admin/import/wxr`（multipart）、`PUT /import/:id/mapping`、`POST /import/:id/dry-run`、`POST[I] /import/:id/run`、`POST /import/:id/cancel`、`GET /import/:id`、`GET /import`。
- 擴充：`GET /api/admin/store?kind=&category=&q=&installed=&updates=&page=`、`POST /store/refresh`、`POST[I] /store/install {id, version?, accept_permissions}`、`POST[I] /store/update {ids|all}`、`POST[I] /plugins/install-url {url, sha256?}`、`POST /plugins/upload`（強化）、`GET /plugins/:id`、`POST /plugins/:id/enable|disable`、`DELETE[I] /plugins/:id?keep_settings=`、`GET|PUT /plugins/:id/settings`、`POST /plugins/:id/settings/reset`、`POST /plugins/:id/test`、`POST /plugins/:id/actions/:action`、`GET /plugins/:id/logs`、`GET /extensions`、`PUT /extensions/:point {active:[ids]}`。
- 附件：`GET|POST /media/policies`、`PUT|DELETE /media/policies/:id`（409 `{code:"policy_in_use"}`）、`POST /:id/test`、`POST /:id/default`、`POST /:id/move {to_policy_id}`、`GET|POST /media/groups`、`PUT|DELETE /media/groups/:id?files=ungroup|trash`、`GET|POST /media/files`、`PATCH|DELETE /media/files/:id`、`POST /media/files/:id/replace`、`POST /media/fetch {urls[]}`；舊的 `/api/admin/media*` 維持可用並走同一服務。

頁面：`/admin/settings/:tab`、`/admin/redirects`、`/admin/logs`、`/admin/tools/backup`、`/admin/tools/import`、`/admin/store`、`/admin/plugins/:id`、`/admin/media/policies`；側欄新增 redirects、tools、logs（system 區）、store（appearance 區），全部限 admin。

### 選項鍵

網站：`site_title`、`site_description`、`site_language`、`site_timezone`。網址：`permalink_post`（`/post/{slug}`）、`permalink_legacy`（JSON ≤ 5）、`permalink_category_prefix`（`/c`）、`permalink_tag_prefix`（`/t`）、`permalink_author_prefix`（`/author`）、`permalink_archive_prefix`（`/archive`）、`slug_strategy`、`home_show`、`home_page_id`、`per_page_home|taxonomy|author|search`（10）、`excerpt_auto_length`（110）、`feed_mode`、`feed_items`。SEO：`seo_index_site`、`seo_index_types`（JSON）、`seo_images_index`、`seo_snippet_mode`、`seo_indexnow_enabled`、`seo_indexnow_key`、`seo_title_format`、`seo_default_og_image`。AI：`ai_policy_preset`（`block_training_allow_search` 預設｜`block_all`｜`allow_all`）、`ai_bots`（JSON）、`ai_custom_rules`（JSON）、`ai_block_server_side`、`tdm_reservation`、`tdm_policy_url`、`llms_txt_enabled`、`llms_txt_intro`。注入：`inject_head_all`、`inject_head_content`、`inject_footer`、`inject_on_commerce`（false）。維護：05 的三個鍵加 `maintenance_retry_after`（600）。基礎設施：`cache_driver`、`cache_url_enc`、`cache_ttl_page`、`cache_object_enabled`、`queue_driver`、`queue_url_enc`、`queue_workers`、`queue_paused`。日誌：`logs_retention_days`（30）、`logs_error_retention_days`（90）、`logs_http_level`。更新：`update_auto_check`、`update_channel`、`update_on_new`、`update_auto_time`、`update_keep_versions`、`update_latest`。備份：`backup_enabled`、`backup_time`（03:00）、`backup_keep_daily`（14）、`backup_keep_weekly_months`（3）、`backup_local_keep`（3）、`backup_policy_id`。附件：`media_webp`、`media_thumb_sizes`、`media_strip_exif`、`media_max_bytes`（20 MB）、`media_allowed_types`。郵件：`mail_host|port|user|password_enc|from|tls|enabled`。秘密一律 `*_enc`（AES-GCM，金鑰 `settings_key` 存 `data/auth-secrets.json`）或 `env:JELLY_*` 參照。

### 測試另外釘下的約定（08c、08d）

- 測試用環境變數（實作必須支援，正式環境不設）：`JELLY_TEST_ALLOW_LOOPBACK=1` 讓 `safehttp` 允許連 127.0.0.1 的假伺服器（只在此旗標下放行，預設仍封鎖）；`JELLY_UPDATE_API_BASE`、`JELLY_UPDATE_PUBLIC_KEY`（base64 的 minisign 公鑰行，legacy `Ed` 演算法）、`JELLY_UPDATE_EXECUTABLE`；`JELLY_STORE_CATALOG_URL`、`JELLY_STORE_PUBLIC_KEY`。
- 回 202 的工作（備份、還原、匯入、更新、商店安裝）在測試情境下同步執行完才回應，效果立即可讀。
- 回應鍵：匯入摘要 `posts`、`pages`、`attachments`、`comments`、`authors`，試跑回 `conflicts`，狀態字串 `cancelled`，封鎖來源列在 `blocked`；更新狀態 `failed`、`rolled_back`，容器 409 帶 `runtime`；附件策略使用中回 409 `policy_in_use`。
- 請求欄位：備份 `content`、`note`、`passphrase`、`confirm`，排程 `enabled`、`time`、`keep_daily`；匯出 `format`；策略搬移 `to_policy_id`；更新 `channel`；商店安裝 `accept_permissions`（權限雜湊）；上傳欄位一律 `file`。
- 外掛 manifest：`settings [{key,type,min,max}]`、`permissions [{name}]`（已強制者標 `enforced`）、`actions`。
- 匯入的作者建成受邀使用者（06 的 `user_invites`）；舊 `uploads/` 匯入在同一個資料庫上再次 `RegisterRoutes` 時執行。
- 08b：轉址規則的 JSON 欄位與資料表欄名相同；CSV 匯入 multipart 欄位 `file`，表頭 `from_path,to_url,kind`；測試端點回 `chain`；404 清單項目 `path`、`count`、`suggestion`；爬蟲統計 `bot`、`action`、`count`；維護回 `enabled`、`message`、`retry_after`、`elapsed_minutes`；自訂規則 `user_agent`、`action`、`path`；`seo_ai` 關閉值 `noai`。互動限定 = `jwt_token` cookie 加 `X-Jelly-CSRF` 且無 Authorization，Bearer 打注入寫入回 403。點擊數由 `redirects.FlushHits()` 落庫；維護中 `/robots.txt` 回 503 帶 `Retry-After`，`/admin` 維持 200。更改 `ai_policy_preset` 即重算 robots，不另設目錄更新端點。
- 跳過而非失敗：通知相關（需 06 的 notify）、S3 策略（minio-go 尚未進 go.mod）。

### 安全規則

程式碼注入只輸出於前台公開頁且經 `template.HTML` 自選項讀出；主題契約 `{{.Inject.Head}}`、`{{.Inject.ContentHead}}`、`{{.Inject.Footer}}`。日誌遮罩 `password|secret|token|key|hash|authorization|cookie`，不記查詢字串。更新：主機允許清單 `api.github.com`、`github.com`、`objects.githubusercontent.com`，不跨主機轉址，sha256（`checksums.txt`）加 minisign，退回由 `main()` 先讀 `update-state.json`，連續兩次啟動失敗自動換回 `jelly-cms.prev`。備份目錄 `data/backups` 0700，排除 `config.json`，`auth-secrets.json` 只在輸入通關密語時包含。匯入與抓取一律經 `safehttp`。上傳檔名正規化（NFC、無控制與雙向字元、無保留名）。

## 我先決定了這些

- 沒畫的五個設定分頁先補板再做；文章網址預設 `/post/{slug}`、每頁 10，板改成和程式一致。
- 發版來源是本 repo 的 GitHub Releases（`release.yml` 產出各平台壓縮檔、`checksums.txt`、minisign 簽章與 ghcr 映像）；外掛目錄 `HazelnutParadise/jelly-registry` 之後再建，先用內嵌的官方清單。
- `web/` 以 `go:embed` 進執行檔，`--web-dir` 可覆寫；自訂主題放 `data/themes`。
- 快取後端只有記憶體與 Redis／Valkey（Memcached 刪掉）；Redis 只用於快取與限流，登入狀態留在 PostgreSQL。
- 只做 IndexNow，不做 Google ping。
- 更新後的重啟：Unix 以 `syscall.Exec` 自我替換，Windows 先啟新程序再結束；都在文件說明支援的監督程式。
- WebP 轉檔先不做，留 `media.processor` 擴充點；S3 用 minio-go；go-redis 進核心、NATS 以 build tag；Markdown 展平用 09 的 `markup.Flatten`。
- 外掛權限只強制 `http.fetch`（允許清單）、`store`、`settings`，其餘在介面標「已宣告、尚未強制」。
- 備份用每表 JSONL 加 manifest，不綁 pg_dump；還原從後台或 `jelly-cms restore`；秘密只在輸入通關密語時包含。
- 附件上限預設 20 MB；匯入的 WordPress 作者建成邀請中的使用者。

## 分段實作與驗收

每段先由測試代理照本文件建立失敗案例（已隨本工作單交付），實作代理依契約實作，主代理審查後以真實伺服器與 Playwright 驗收。

1. 08-0 設計板：五個設定分頁、分頁列統一、網址預設、Redirects 的 SEO 分頁、Infra 去 Memcached、Palette／States 狀態；canvas 發佈。
2. 08a 基礎：五個安全修正、`settings` 登記與遮罩、分頁註冊與頁面、`version` 與 `schema_migrations`、`syslog` 與日誌頁、`cache`／`queue`／`schedule` 與基礎設施頁、郵件設定、⌘K 指令。測試：`settings_integration_test.go`、`logs_integration_test.go`、`infra_integration_test.go`、`internal/cache`、`internal/queue`、`internal/schedule` 契約測試、`internal/data` 遷移。
3. 08b 網址與 SEO：保留別名、網址樣式與舊樣式 301、轉址與 404 紀錄、sitemap／robots／feed／llms／TDM、爬蟲設定、文章與商品的 SEO 欄位、注入、維護模式管理。測試：`routes_settings_integration_test.go`、`redirects_integration_test.go`、`seo_integration_test.go`、`injection_integration_test.go`、`maintenance_admin_integration_test.go`。
4. 08c 維運：備份、匯出、還原、WXR 匯入、更新檢查與套用與退回、`release.yml`、Dockerfile（ldflags、healthz、非 root）。測試：`backup_integration_test.go`、`import_integration_test.go`、`update_integration_test.go`（假 GitHub、假執行檔）。
5. 08d 擴充：manifest v2、`plugin_data`、啟用停用、host API、應用市場、外掛詳情、附件模型與策略與分組、上傳強化。測試：`store_integration_test.go`、`plugin_detail_integration_test.go`、`media_policies_integration_test.go`。
6. 08e 驗收：真實伺服器走完改網址樣式 → 舊網址 301 → 開維護 → 看日誌 → 備份與還原 → 匯入 WXR → 從市場安裝外掛並改設定 → 切換佇列後端不掉工作。
