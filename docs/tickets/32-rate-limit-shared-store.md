# 32 — 限流計數器跟著站台的快取後端走：多實例共用、記憶體快取不再殘留

Epic：遷移與營運
User Story：站長在後台把快取切到 Redis 之後，機器 API 的限流在所有實例共用同一份計數，上限不會因為多開幾台而變成好幾倍；沒有 Redis 的單機站，記憶體計數器不會隨時間越吃越多。
Status：待實作。
Blocked by：27（限流，已合併）。

## 背景與決定

現況（行號以 main `0dc56d9` 為準）：

- `internal/server/integration_ratelimit.go:62-74` 的 `newIntegrationRateLimiterFromEnv` 固定 `cache.Open(memory)`，不看站台設定；每個 `RegisterRoutes` 一個，程序內有效。多實例部署時每台各算各的。
- 計數用 `Get`／`Set` 讀改寫加 `mutex`（`:124,138`），值存 `<視窗起點>:<計數>`，沒有用 `Incr`。原因是 memory store 的過期判斷用真實時間，測試注入的時鐘撥到下一分鐘時舊值還在，所以把視窗起點寫進值裡比對。
- `internal/cache/memory.go` 的過期項目只在被讀到（`live`）或 `Stats` 時刪除，沒有背景清掃。任何「寫了一次之後不再讀」的 TTL 鍵都會留到下次 `Stats`，不只限流，頁面與物件快取也一樣。
- `internal/cache/redis.go:99-107` 的 `Incr` 是 Lua：`INCR` 後只在 `n == 1` 時 `PEXPIRE`，之後不刷新 TTL，和 memory 的 `Incr`（`memory.go:93-107`，只在新建時設 `expires`）同一語意：固定視窗、從第一次計數起算。
- 站台的快取後端由 `cache_driver`／`cache_url_enc` 設定或 `JELLY_CACHE_URL` 決定（`infra_api.go:32-37,95-113`），`infraRuntime.activeCache`（`:160-178`）在設定變更時重開 store；它建立在 `registerInfraRoutes`（`:848-851`）裡，限流拿不到。
- `internal/cache/cache.go:21-22,54-55` 的註解還寫 Redis 是 stub 回 `ErrNotImplemented`，已過時；`ErrNotImplemented` 沒有呼叫端。
- CI 有 Redis 服務（`ci.yml:34-42`，`JELLY_TEST_REDIS_URL=redis://127.0.0.1:56379`），`internal/cache/redis_integration_test.go` 用它但沒有斷言 TTL。
- 決定：限流改用站台設定的快取後端（與頁面快取同一個 store，`ratelimit` 命名空間），後端切換時跟著切；計數改用 `Incr`，鍵帶視窗起點，TTL 為視窗剩餘秒數加一秒，兩種 store 都是固定視窗且鍵會自然到期。
- 決定：memory store 加背景清掃，解決所有命名空間的殘留，不只限流。
- 不改：三個計數器的語意、上限、環境變數、429 信封與標頭（工單 27 的測試契約全部維持）。

## 規則

### R1 計數器存放

- 新增 `internal/server/cache_runtime.go`：一個程序內的 `cacheRuntime`（取代 `infraRuntime` 的快取部分），提供 `Store(ctx) cache.Store`，依 `cacheConfig(ctx)` 的簽章快取已開的 store，簽章變更時重開並關舊的；`registerInfraRoutes` 與限流都向它拿。`infraRuntime.activeCache` 改為委派。
- 限流的 `counter` 改成：`key = "<scope>:<bucket>:<視窗起點 unix>"`，`n, err := store.Incr(ctx, "ratelimit", key, ttl)`，`ttl = 視窗剩餘秒數 + 1`。不再有 `mutex`、`Get`／`Set` 與值內的視窗戳。時鐘仍可注入（`integrationRateLimitNow`），視窗起點由注入時鐘算，所以撥到下一分鐘就換鍵、計數從零開始。
- store 回錯誤（Redis 斷線）時 fail-open：記一筆 `syslog` 警告（每分鐘最多一筆，避免洗版）並放行，標頭不送。

### R2 記憶體清掃

`internal/cache/memory.go`：新增背景 goroutine，每 30 秒掃一次並刪除已過期項目；`Close` 停掉它；`Stats` 的計數在清掃後正確。另提供 `sweepNow()`（測試用）。單一項目的存取路徑不變。

### R3 Redis 契約

`internal/cache/redis_integration_test.go` 補：`Incr` 第一次設 TTL（`PTTL` 在 `ttl-100ms` 到 `ttl` 之間）、第二次不刷新（`PTTL` 單調遞減）、鍵到期後 `Incr` 回 1。同一組斷言用在 memory store（`cache_test.go`），證明兩者語意一致。

### R4 設定與文件

- 後台「快取與佇列」頁的快取區塊加一句說明：「機器 API 限流計數器也存在這裡；Redis 時多實例共用」（繁中與英文 locale）。
- README 限流段補：單機預設記憶體，多實例請把快取切到 Redis；切換後端時進行中的視窗計數重新開始（可接受）。
- `internal/cache/cache.go` 的過時註解改正，`ErrNotImplemented` 刪除。
- `STATUS.md`；工單 27 開頭加一行指向本工單。

## 程式（格子）

1. `internal/cache/memory.go`：R2；`internal/cache/cache_test.go`、`redis_integration_test.go`：R3。
2. `internal/server/cache_runtime.go`（新檔）與 `infra_api.go` 的委派。
3. `internal/server/integration_ratelimit.go`：`counter` 改 `Incr` 與鍵格式；建構子改收 `func(ctx) cache.Store`；fail-open 的節流日誌。
4. `internal/server/integration_routes.go`：建 limiter 時傳入 `cacheRuntime.Store`。
5. 文件與 locale：R4。

## 驗收（測試契約）

- 工單 27 的 `api_v1_contract_integration_test.go` 全部維持通過，不改任何斷言（時鐘注入的「下一分鐘歸零」測試也要過）。
- 新測試 `internal/server/rate_limit_store_integration_test.go`：
  - 以 `JELLY_CACHE_URL=<JELLY_TEST_REDIS_URL>` 啟動 harness（沒設 Redis 環境變數時 skip），`JELLY_API_RATE_LIMIT_PER_INTEGRATION=3`：兩個獨立的 `echo` 實例（模擬兩台）共用同一個 Redis，對同一個串接各打 2 次，第 4 次不論打哪一台都 429；Redis 內 `ratelimit` 命名空間的鍵 `PTTL` 介於 1 到 61 秒。
  - 記憶體後端：同樣兩個 `echo` 實例各算各的（各 3 次才 429），證明文件說法成立。
  - 後端切換：harness 以記憶體啟動並打到 429，改設定為 Redis 後下一次請求不是 429（新 store、新計數），再切回記憶體亦然。
  - Redis 斷線（把 URL 指到沒監聽的埠）：請求回 200、沒有 `RateLimit-*` 標頭、`system_logs` 一分鐘內只有一筆限流警告。
- `internal/cache` 測試：memory store 寫入 1,000 個 TTL 1 秒的鍵且不再讀取，2 秒後 `sweepNow()` 讓項目數為 0（現況會留 1,000 個直到 `Stats`）；`Close` 後 goroutine 結束（用 `goleak` 或計數器驗證）。
- 既有 `infra_integration_test.go`（快取狀態、測試連線、清除）全部維持通過。

## 不在本工單

- 每個串接自訂上限、滑動視窗。
- 頁面快取與物件快取本身的行為。
- NATS 或其他後端的計數器。

## 決策紀錄

- 跟著站台快取後端而不是另設一個「限流後端」設定：站長只決定一次「有沒有 Redis」；限流是快取的使用者，不是另一個子系統。
- 鍵帶視窗起點而不是值帶視窗起點：`Incr` 在兩種 store 都是原子的，不需要 `mutex`；鍵到期由 store 處理；代價是每個視窗一個新鍵，所以要有 R2 的清掃。
- memory store 加背景清掃而不是只在限流清：殘留是 memory store 的通病，頁面快取一樣會留；修在一個地方。
- 斷線 fail-open 而不是 fail-closed：限流是保護，不是授權；Redis 掛掉時寧可暫時沒有限流，也不要整個機器 API 回 503。
- 切換後端時計數重新開始：一分鐘的視窗，重算一次沒有實害；為了跨後端搬計數寫程式不划算。
