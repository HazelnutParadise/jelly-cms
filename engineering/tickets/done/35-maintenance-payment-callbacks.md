# 35 — 維護模式不擋金流回呼

Epic：遷移與營運
User Story：站長開維護模式期間，顧客在維護前已送出的付款仍會由金流服務通知網站，訂單照常轉為已付款，不會因為重試用盡而掉單。
Blocked by：無。

## 背景與決定

- `internal/server/maintenance.go` 的 `maintenanceBypass` 放行登入、後台、`/api/v1`、`/api/auth`、安裝與上傳檔案，但不含金流回呼。金流回呼路由只有三條，都在 `internal/server/handlers_payment.go:35-37`：`POST /api/payment/:gateway/notify`、`POST /api/payment/:gateway/callback`、`GET /api/payment/:gateway/return`。
- 維護中金流服務的通知會收到 503。金流服務會重試，重試用盡就掉單；付款已經發生，網站卻沒有紀錄。
- 決定：`/api/payment` 整個子樹加入放行清單。這個前綴底下只有上述三條路由，回呼本身有簽章驗證與去重，放行不會擴大匿名可做的事。後台的金流測試在 `/api/admin/shop/payment/*`，本來就放行。
- 還原備份時也會開維護模式。`notify` 與 `callback` 是 POST，經過 `systemWriteGateMiddleware` 取得共用寫入租約；還原持有獨占租約，所以還原中送達的通知會等還原完成才寫入，不會寫進正在被取代的資料。`return` 只讀資料再轉址，不寫入。
- `return` 是顧客瀏覽器付款後被導回的網址。放行後它照常處理並轉址到訂單頁；訂單頁仍受維護模式限制，顧客看到維護頁，付款結果已寫入。
- 不改：維護模式對其他匿名路徑的 503 行為、`Retry-After`、維護頁內容。

## 驗收

| 情境 | 預期結果 |
|---|---|
| 維護中，金流服務 `POST /api/payment/sandbox/notify` 帶正確簽章 | 不是 503；訂單轉為 paid，與維護關閉時相同 |
| 維護中，`POST /api/payment/sandbox/callback` | 不是 503，走同一個 `HandlePaymentCallback` |
| 維護中，`GET /api/payment/sandbox/return` | 不是 503 |
| 維護中，匿名 `GET /api/comments?post_id=1` | 仍是 503（既有測試不變） |
| 維護中，`POST /api/payment/sandbox/notify` 簽章錯誤 | 與維護關閉時相同的拒絕結果，不是 503 |

## 格子與檔案

| 格子 | 檔案 |
|---|---|
| 放行清單 | `internal/server/maintenance.go` |
| 測試 | `internal/server/maintenance_payment_integration_test.go`（新） |
| 文件 | `docs/system-operations.md` 維護模式段、工單的驗收紀錄 |

## 驗收紀錄

交付時記下的驗證結果與裁決，依時間由新到舊。

- 工單 35：維護模式放行金流回呼。`maintenanceBypass` 加入 `/api/payment` 子樹，維護中 `notify`／`callback` 照常把訂單轉為已付款、簽章錯誤仍回 400，`return` 不再 503；其他匿名 API 維持 503。還原備份時通知經共用寫入租約等待還原完成才寫入。驗證：`internal/server/maintenance_payment_integration_test.go` 先於實作且在原程式上 4 條失敗；主代理重跑 `JELLY_TEST_POSTGRES=1 go test ./internal/server -run 'TestMaintenance|TestNotify'` 通過。文件：`docs/system-operations.md`。沒有部署。
