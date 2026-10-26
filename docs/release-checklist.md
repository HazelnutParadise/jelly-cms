# 發版檢查清單

每次打版本標籤前，依序完成下列項目。任何一項未完成就不發版。

## 1. 自帶套件更新到最新版

| 套件 | 目錄 | 重建腳本 |
|---|---|---|
| Noto Sans TC／Bricolage Grotesque 字型 | `web/admin/assets/fonts/` | `scripts/vendor/fonts.sh` |
| Pele | `web/admin/assets/pele/` | `scripts/vendor/pele.sh` |

- 列出 `web/` 下所有自帶的第三方套件：每個套件目錄都有 `VERSION`（目前版本、來源 commit 或 tag、建置日期）與 `LICENSE`。
- 逐一查上游最新版本（GitHub Releases、npm 或上游網站），以 `scripts/vendor/<套件>.sh` 重新取得或重新建置到最新版，更新 `VERSION`。
- 上游有破壞性變更時先在本機跑過受影響的頁面再決定是否升版；不升版要在 `VERSION` 寫明原因與追蹤的 issue。
- 字型也算套件：woff2 子集與授權檔一起更新。

## 2. 零 CDN 檢查

- 搜尋 `web/`、`design/` 與 `cmd/` 下的 HTML 與模板，不得出現 `cdn.`、`unpkg.com`、`jsdelivr.net`、`fonts.googleapis.com`、`fonts.gstatic.com` 或其他外部 script、link、font 來源。
- 封鎖所有外部網域後，後台每一頁與預設主題每一頁都要完整可用（Playwright 以 `route.abort()` 擋外部請求跑一遍）。

## 3. 驗收

- `gofmt -l .` 空、`go vet ./...`、`JELLY_TEST_POSTGRES=1 go test -race ./... -count=1`、Windows 與 macOS 交叉編譯通過。
- 後台與前台截圖對照 `design/project/` 的板，線上畫布與 repo 同版。
- STATUS.md 的驗收狀態與實際相符。

## 4. 打版本

- 更新版本號與更新說明，列出本次升級的自帶套件與版本。
- 打 tag 後由 CI 產生各平台執行檔與映像檔。
