# 15 — 更新系統或切換主題後自動取得新版前端資源

Epic：前端資源與更新
User Story：站長更新 Jelly CMS、切換或重新載入主題後，重新開啟頁面的訪客會取得對應的 JavaScript、CSS 與字型，沒有變動的資源繼續使用快取，不必手動設定版本號。
Blocked by：05、09、11、12 已整合 main。
Status：已實作，驗收證據與交付結果見 STATUS.md 與本工作單 PR。

## 範圍與契約

- 自帶模板以 `assetURL` 函式取得資源網址，格式為原路徑加 `v=<SHA-256>`，保留原有查詢參數與片段。
- 啟動、主題啟用、重新載入與升級時建立不可變資源快照，保存實際回應的位元組與 hash。一般頁面請求只查表。
- CSS 的本地 `url()` 與 `@import` 相依資源先產生 hash 網址，再計算實際 CSS 回應的 hash。循環匯入回明確錯誤。
- 含 CSS 跳脫字元的引用保留原樣，例如 `url("font\2e woff2")`。這類引用由未版本化網址重新確認快取，字型變動不會連帶改變父 CSS 的 hash；自帶套件與預設主題的引用不使用這種寫法。
- 主題、模板快取與資源快照同時切換。建置失敗維持原主題，並回報錯誤。
- 正確 hash 的資源可長期快取；未帶 hash 的既有網址仍可使用，但每次重用前須重新確認。錯誤或過期 hash 回 404 與 `no-store`，不以舊 hash 快取新內容。
- 資源以內容 ETag 處理條件請求，不以修改時間判斷內容是否相同。GET、HEAD 與 304 一致。
- HTML 使用 `no-cache`，已有 `no-store` 的帳號、付款等頁面保留較嚴格的設定。包含登入、安裝、後台、公開頁、錯誤、維護、預覽與 HTML 匯出。
- Mermaid 延遲載入從編輯器模板取得 hash 網址。不掃描或改寫文章內容、程式碼範例或任意 JavaScript 字串。
- 第三方主題可使用 `assetURL`；尚未採用的固定網址以重新確認快取的方式相容。
- 離線 HTML／ZIP 保持資源內嵌。外部網址、媒體附件、API、金流與資料庫不變。
- 已開頁不強制刷新。更新後才請求的舊 hash 可能回 404，重新整理可取得新版引用；不保存無上限的歷史資源快照。

## 骨架與分工

1. `internal/assets`：`Build(mounts map[string]string) (*Snapshot, error)`、`(*Snapshot).URL(raw string) string`、`(*Snapshot).ServeHTTP(http.ResponseWriter, *http.Request)`，負責快照、CSS 相依、內容 hash、安全路徑與 HTTP 條件請求。
2. `internal/theme`：`AssetURL(raw string) string`、`AssetSnapshot() *assets.Snapshot`，在啟用時建立並原子發布快照，渲染捕捉相同世代的 resolver。保留既有直接呼叫 `ContentAssets` 的介面。
3. `internal/server`、`web/`、`cmd/jellypreview`：接線模板函式與資源 handler，補齊 HTML 標頭與 Mermaid 延遲載入。
4. 測試代理只修改測試；實作代理不得修改測試。主代理審查完整差異並獨立重跑驗證。

## 驗收

- 相同內容、不同修改時間得到相同網址；不同內容、相同修改時間得到不同網址。
- 修改一般本地引用的字型後，字型與引用它的 CSS 網址都改變；含 CSS 跳脫字元的引用依前述例外處理。舊快照不因磁碟寫入而改變。
- 舊 ETag、未來修改時間、偽造 hash、目錄穿越、編碼路徑與越界符號連結不會取得錯誤快取內容或任意檔案。
- 並行啟用與渲染不混用主題與 hash。失敗不破壞目前可用主題。
- 真實 HTTP 與瀏覽器驗證登入、安裝、公開頁、後台、編輯器、Mermaid 與數學字型的 hash 引用與快取行為。
- 更新檔案並重啟、主題切換及重新載入後，HTML 引用改變且實際下載對應內容。未變動資源網址保持相同。
- 文章程式碼範例、離線匯出、外部資源限制與原有互動不退步。
- 主代理執行專用 PostgreSQL／Redis 完整競態測試、NATS、格式、vet、原生與跨平台編譯，並完成必要 CI 後合併與清理分支。

## 設計與交付

此工作單只變更資源網址與 HTTP 快取行為，不變更畫面、配色、間距或操作流程，沒有對應設計板差異。沒有新增依賴、資料遷移或正式部署。
