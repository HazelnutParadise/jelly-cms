# 03 — 管理者可以安全授權編輯者與作者使用後台

Epic：帳號與存取
User Story：登入後只可操作自己被授權的資料與功能。
Blocked by：01 的可用資料庫與登入流程。
Status：in_progress（HTTP 權限、金鑰保存與單次登入撤銷已分別透過 PR #2、#3、#4 合併；跨站請求保護已透過 PR #8 合併；登入返回網址已透過 PR #9 合併，登入嘗試限制已透過 PR #10 合併，正在驗證 OAuth 授權與身分連結）

## 流程與驗收

- [ ] 匿名不能讀取或修改後台商品、訂單、內容、媒體或設定。
- [ ] 管理員能管理系統設定、OAuth、金流、主題、外掛與帳號。
- [ ] 編輯者能管理內容與媒體，但不能讀取或修改敏感系統設定。
- [ ] 作者只可管理自己的文章與媒體，無法藉由 ID 或請求欄位更改他人資料。
- [ ] 新文章作者來自登入身分，不能固定為使用者 1。
- [ ] 更新輸入不接受 ID、Author、Role、CreatedAt 等伺服器管理欄位注入。
- [ ] Session 與 JWT 沒有可預測的內建秘密，重啟後有效性符合明確生命週期。
- [x] Cookie 寫入請求有跨站請求保護，未授權回 401/403 而非 HTML 成功回應。
- [x] 登出、失效登入、錯誤密碼、外部重新導向、暴力嘗試有可預期結果。
- [ ] OAuth 關閉時不顯示且不可使用，只能在驗證身分後安全建立／連結帳號。
- [ ] 使用者介面清楚顯示允許的操作，API 仍獨立檢查權限。

## 錯誤與邊界

未登入 401、已登入但無權限 403、不可見資源 404。DB 故障不可視為身分不存在而重新建帳號。空值、未知角色、刪除帳號、降權後舊token與重複OAuth回呼都需要測試。禁止紀錄秘密或把設定秘密回傳一般內容作者。

## 根因依據

商品與訂單路由只建立 /api/admin group，無認證中間件；一般管理路由只掛 RequireAuth。Post AuthorID 固定 1，更新直接 Bind 到資料庫物件。認證使用固定預設 Session secret，需以真實負面流程驗證修正。

## 分段實作與驗收

每段先由測試代理建立失敗案例，主代理審查後交另一代理實作。段落通過不代表本工作單完成。

1. HTTP 角色與文章所有權：沿用 RegisterRoutes、RegisterAdminRoutes 與 RegisterEcommerceRoutes。匿名 API 一律回 JSON 401，不依賴 Accept 標頭。系統設定、商店、主題與外掛限管理員；內容與媒體依角色授權。作者只管理自己的文章，頁面交由編輯者或管理員管理。未知角色拒絕，使用資料庫目前角色判斷已刪除或降權帳號。ID、作者、時間與巢狀 User 資料不能經內容輸入改寫。以真實註冊路由、登入憑證與獨立資料庫測試，並在請求後重新讀取資料證明沒有越權修改。
2. 認證生命週期：移除固定預設秘密，涵蓋首次啟動、重啟、讀寫失敗、過期與登出。限制重新導向目的地，補上登入限流與 Cookie 寫入請求保護。不得只清除瀏覽器 Cookie 就宣稱所有憑證已撤銷。
3. OAuth 與敏感設定：關閉的提供者不得進入授權流程，不能只憑相同信箱直接登入既有帳號。區分使用者不存在與資料庫故障，驗證重複回呼與安全帳號連結；後台秘密不回傳內容作者。
4. 媒體所有權與帳號管理：媒體需要可持久化的擁有者，涵蓋同名檔案、替換、刪除與歷史檔案；既有檔案不得任意歸給目前使用者。帳號建立、角色變更與刪除須有管理員流程及對稱的權限檢查。
5. 介面與整合：以管理員、編輯者、作者分別實際操作後台，顯示各自允許的選項；直接呼叫 API 仍須獨立拒絕越權操作。繁中與英文、桌面與行動版皆需驗收。

第一段測試檔為 `internal/server/access_control_integration_test.go`。後續各段另拆測試與實作檔案，避免同時修改認證核心。所有段落與原始驗收項目均完成後才可結案。


## 登入秘密保存與重啟驗收

此段屬認證生命週期，完成後仍須接續登出撤銷、請求保護與 OAuth。

流程：程序啟動 → 取得環境指定或本機保存的秘密 → 完整驗證 → 初始化 Session/JWT → 開放 HTTP。安裝完成後只重新載入依賴資料庫的 OAuth 設定，不重新建立登入秘密。

`Init() error` 是初始化入口，失敗須傳回主程式並在開放 HTTP 前停止。既有明確設定的 `SESSION_SECRET` 與 `JWT_SECRET` 保留優先權，未指定的值使用各自獨立產生、至少 32 位元組的安全亂數。秘密不再從固定字串或另一種憑證的秘密推導。未指定的秘密存於 `data/auth-secrets.json`，檔案只允許擁有者讀寫，重啟後沿用。兩個環境值都明確提供時不要求本機秘密檔。

| 操作 | 失敗或邊界 | 預期結果與責任 | 驗證方式 |
|---|---|---|---|
| 首次取得秘密 | 檔案不存在、空環境值 | 認證層產生秘密；完成寫入才可使用 | 獨立暫存目錄、真實 Init 與 JWT/Session |
| 重新讀取 | 空檔、JSON 損壞、缺欄位、秘密過短、非一般檔案 | 回傳錯誤，不覆寫原檔、不退回預設值 | 保留前後檔案內容並比對 |
| 保存 | 目錄不可用、寫入或同步失敗 | 不發布半份秘密，不啟動 HTTP，不紀錄秘密內容 | 檔案系統失敗及程序退出驗證 |
| 同時首次啟動 | 兩個程序競爭 | 原子發布完整檔案，敗方讀回勝方，不覆寫既有秘密 | 子程序使用同一路徑，交叉驗證 JWT |
| 重啟 | 相同資料目錄 | 已簽發且未過期的 JWT 與 Session 仍有效 | 新子程序讀回並驗證兩種憑證 |
| 不同網站 | 不同資料目錄、環境值皆空 | 憑證不能跨網站使用 | 兩個隔離目錄交叉拒絕 |
| 更新安裝 | 既有版本使用固定預設秘密 | 舊預設憑證失效，重新登入後使用新秘密 | 舊預設簽章負面測試 |
| 安裝完成 | 資料庫剛建立 | 重新載入 OAuth，不改變登入秘密 | 真實 HTTP 安裝及後續登入測試 |

測試入口優先使用公開 Init、GenerateJWT、ValidateJWT、SetUserSession、GetUserIDFromSession，跨重啟情境使用子程序，不能只比對全域變數。資料庫保持隔離。儲存格式使用 `version: 1`、`session_secret` 與 `jwt_secret`，兩個秘密欄位使用 base64，解碼後各至少 32 位元組；讀取時驗證版本與欄位，不把環境秘密寫入檔案。備份須包含秘密檔，刪除秘密檔會使既有憑證失效。此段不新增資料庫欄位，也不變更正式環境。


## 單次登入撤銷

流程：本機或 OAuth 登入成功 → 產生本次登入專屬的安全隨機 ID → 同一次登入的 JWT 與 Session 共用該 ID → 每次 HTTP 認證查詢撤銷紀錄 → 登出將該 ID 的雜湊寫入資料庫 → 提交成功後清除 Cookie。登出只撤銷本次登入，不登出同一帳號的其他裝置；單獨呈現 JWT 或 Session 也必須撤銷同次登入的另一種憑證。

沿用 `GenerateJWT`、`SetUserSession` 的公開簽名，單獨呼叫時各產生獨立 ID。兩個登入處理器共用內部簽發流程，確保成對憑證取得相同 ID。JWT 的 `jti` 與 Session 的 `login_id` 使用至少 32 位元組亂數的 base64url 字串。`ValidateJWT` 仍可作無資料庫的簽章／內容驗證，HTTP 認證另要求合法 ID、有效期限與未撤銷；`GetUserIDFromSession` 的低階解碼不取代 HTTP 授權。缺少 ID 的舊憑證需重新登入。Session 簽章端與 Cookie 的最長有效期統一為 7 天。

撤銷資料使用新的 `core.RevokedLogin` 與 `revoked_logins` 表，由既有 `data.MigrateDB` 加入相容性新增，不修改或回填 users。欄位為 `id`（登入 ID 的 SHA-256 十六進位雜湊，主鍵）與 `expires_at`（索引）；不保存原始 Cookie、JWT 或登入 ID。登出交易刪除已到期紀錄並冪等新增目前 ID，保留至少完整 7 天。資料庫備份自然涵蓋撤銷紀錄。回退舊版會失去撤銷檢查，因此不能以回退程式宣稱憑證仍被撤銷。

| 操作 | 失敗或邊界 | 預期結果與責任 | 驗證方式 |
|---|---|---|---|
| 簽發登入 | 同帳號同秒登入兩次 | 兩組不同登入 ID，單獨登出互不影響 | 真實登入 HTTP 及獨立 cookie jar |
| 重新登入 | 帳密正確但舊 Session Cookie 損壞 | 建立新的有效登入，不被舊 Cookie 卡住 | 真實登入帶損壞 Cookie |
| 登出 | JWT、Session 或兩者 | 成對憑證同時撤銷，重放回 401 | 留存兩種憑證，分開重放 |
| 重複登出 | 同一組憑證重複或並行提交 | 200、結果一致，主鍵去重，不影響其他登入 | 真實 PostgreSQL 並行 POST |
| 登出寫入 | 資料庫寫入失敗 | 503，不回報成功，不先清除 Cookie，允許重試 | 獨立資料庫撤銷表權限／SQL 失敗 |
| 認證查詢 | 撤銷表不可用、DB 故障 | 拒絕存取，不退回另一種憑證 | 保護 API 的負面 HTTP 測試 |
| 認證選擇 | Authorization 有誤或 JWT Cookie 已失效，另有有效 Session | 明確提供的憑證失敗即拒絕，不偷偷換身分 | header/cookie 組合測試 |
| 重啟 | 撤銷後由新子程序開啟 DB 與 auth | 舊憑證仍拒絕，新登入有效 | 新程序透過 HTTP 處理器重放，另驗 DB 重連 |
| 舊版憑證 | 簽章有效但無登入 ID | HTTP 認證拒絕，重新登入取得新憑證 | 已知測試秘密簽章的舊格式 |
| 瀏覽器登出 | 離線或非成功回應 | 留在原頁顯示可重試錯誤，不導向登入頁假裝成功 | 實際瀏覽器失敗與成功操作 |

驗收接縫使用真實 `/auth/login`、`/auth/logout`、`/api/auth/me` 與現有獨立 PostgreSQL fixture。新增測試先重現漏洞再實作。一般憑證解碼測試保持無資料庫；HTTP 認證與撤銷測試使用真實資料庫。未登入登出可以冪等回 200；沒有登入 Cookie 且沒有有效登入 ID 時不送出登入 Cookie 的 Set-Cookie，不能新增未驗證憑證的撤銷資料。已驗證登入的撤銷失敗不可回 200。

Session 另保存 `login_expires_at`（int64 Unix 秒），簽發時固定為七天後；HTTP 認證拒絕缺少、型別錯誤、零或已到期的值。OAuth 入口等流程重新儲存 Cookie 時不得延長此時間，避免舊 Cookie 重簽後跨過撤銷紀錄的保留期限。空的 JWT Cookie 仍屬明確提供憑證，不能退回 Session。

桌面側欄與手機頂部提供可見的登出按鈕，共用處理中狀態；請求未完成時停用兩個入口，失敗後恢復重試。非成功回應與網路錯誤顯示繁中／英文的可見提示，只有成功才前往登入頁。此次只補齊手機登出入口，整體手機導覽留待介面整合段落驗收。

跨站請求保護、登入限流、重新導向與 OAuth 身分連結各有獨立驗收，不以本段測試取代。

## Cookie 寫入請求保護

流程：瀏覽器送出寫入 → 既有權限檢查 → 確認同源證據 → 執行寫入。登入與安裝尚未有登入身分，仍須在處理帳密、連線或安裝前檢查。GET、HEAD、OPTIONS 保持讀取行為；OAuth 的 GET 與付款通知沿用各自的 state／簽章驗證，分別在後續段落驗收。

採用同源來源標頭與 AJAX 自訂標頭，不新增 token 儲存、資料表或依賴。依據為 [OWASP 的來源檢查與 AJAX 防護方法](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)。Go 的 [CrossOriginProtection](https://go.dev/src/net/http/csrf.go) 可作來源判斷對照；本專案另明確拒絕缺少來源證據與互相矛盾的標頭，並涵蓋無 Cookie 的頂層登出。

共用實作置於 `internal/auth/request_protection.go`。`ProtectBrowserWrites(echo.HandlerFunc) echo.HandlerFunc` 保護登入與兩個安裝 POST。`ProtectCookieWrites(echo.HandlerFunc) echo.HandlerFunc` 在 RequireAuth、RequireAdmin、RequireRole 完成身分／角色檢查後包住後續處理器，僅在請求帶有 `jwt_token` 或 `jelly-cms-session` Cookie（包含空值）時要求來源證據。無 Cookie 的 Bearer API 仍由原有認證驗證，不能只憑字首宣稱身分有效。登入與安裝不得使用 Cookie 缺少的豁免。

登出使用獨立的 `ProtectLogoutWrites(echo.HandlerFunc) echo.HandlerFunc`。請求帶有登入 Cookie，或任何 Origin、Referer、Sec-Fetch-Site 時，都執行完整來源檢查。只有 Cookie 與來源標頭皆無時才保留無來源 API 相容性；HandleLogout 在沒有登入 Cookie 且未驗證到任何登入 ID 時，冪等回 200，但不送出登入 Cookie 的 Set-Cookie。合法純 Bearer 登出仍撤銷其登入 ID。這樣可避免跨站頂層 POST 不帶 Lax Cookie，卻藉回應刪除 Cookie 的登出攻擊；[Cookie 規格](https://httpwg.org/http-extensions/draft-ietf-httpbis-rfc6265bis.html#section-5.6.7.2) 允許頂層導覽回應設定 Cookie，因此不能只檢查請求 Cookie。

來源判斷採負面證據優先：拒絕重複、多值、格式錯誤的 Origin、Referer、Sec-Fetch-Site。`same-site`、`cross-site` 及未知 metadata 值拒絕；`none` 單獨不足以放行。已提供 Origin 時必須是 HTTP(S) 的完整同源網址，拒絕 null、帳密、路徑、查詢及片段；不得因 Origin 失敗改讀 Referer 或自訂標頭。Referer 存在時也須同源，網址的路徑與查詢不參與來源比較。矛盾的來源證據拒絕。

有效證據為同源 Origin、同源 Referer 或 `Sec-Fetch-Site: same-origin`。三者皆無有效證據時，只接受單一 `X-Jelly-CSRF: 1`；這個非簡單請求標頭依賴瀏覽器的跨來源預檢與本服務不開放 CORS 的條件。固定值不當成秘密。明確的外站或不合法來源不得用此標頭覆蓋。既有瀏覽器請求會帶來源證據，不另修改每頁 fetch 或新增前端 token 狀態；來源標頭被移除的自訂客戶端須明確提供此標頭。

目標來源優先採明確的 `APP_URL`，僅接受 HTTP(S) scheme、host 與可選的根路徑 `/`，不得有帳密、查詢或片段。未設定時使用實際 TLS 狀態與 Request.Host，不採用 X-Forwarded-*、Forwarded、X-URL-Scheme。正規化大小寫、預設連接埠與等價 IPv6 寫法，保留不同 scheme、主機或有效連接埠的區別。IPv4 與 IPv4-mapped IPv6 是不同網址主機，不得合併為同源。反向代理終止 HTTPS 時，須以 APP_URL 指定公開來源；錯誤設定回 JSON 503，不能退回放行。一般來源拒絕回 JSON 403，不洩漏帳密、Cookie 或來源設定。

| 操作 | 失敗或邊界 | 預期結果 | 驗證 |
|---|---|---|---|
| Cookie 寫入 | 外站、兄弟網域、null、缺少證據、矛盾或重複來源 | 403，資料與 Cookie 不變 | 真實路由、資料庫前後回查 |
| Cookie 寫入 | 同源 Origin／Referer／metadata，或來源皆缺但有正確自訂標頭 | 依原有權限完成寫入 | 同一 API 正負對照 |
| 目標來源 | TLS、非預設連接埠、IPv6、APP_URL、偽造轉送標頭 | 正確比較來源，設定無效時 503 | 獨立 HTTP fixture |
| Bearer API | 合法／不合法 token，沒有登入 Cookie | 保留原有成功／401，偽字首不能授權 | 不加來源證據的 HTTP 請求 |
| 混合登入 | Bearer 加 Cookie | 仍須來源檢查，不能藉 Bearer 豁免 Cookie 操作 | 登出與受保護寫入 |
| 無 Cookie 登出 | 外站來源，或完全無憑證／來源 | 外站回 403；完全無憑證冪等 200 且不送清除 Cookie | 原始 HTTP 與跨站頂層表單 |
| 登入與安裝 | 無 Cookie 的外站表單 | 403，不簽發登入、不測試 DB、不寫入安裝 | 完整註冊路由與暫存資料夾 |
| 付款通知 | 外部 POST callback | 不套 Cookie 保護，保留獨立驗簽；不能豁免 create | 失敗通知無訂單變更 |
| 瀏覽器 | 登入、發文、登出、另一來源發送寫入 | 合法流程可用，外站寫入拒絕 | 實際 Chrome 與 360px／1280px |

測試新增於 `internal/server/browser_write_protection_test.go`。沿用真實 RegisterRoutes、隔離 PostgreSQL 與暫存資料夾，不以測試專用 production 開關或假路由取代。新負向測試直接建立原始 HTTP 請求，不使用自動加來源的 fixture；既有合法流程 fixture 只補齊來源證據，保留原斷言。來源檢查不改內容格式，multipart、JSON 與表單皆須保持。

## 安全登入重新導向

流程：使用者開啟登入頁 → 伺服器確認站內返回網址 → 本機或 OAuth 登入 → 只前往已確認的站內網址。未提供或無效的目的地回到 `/admin`。保留合法站內網址的路徑、查詢與片段，不要求使用者重新挑選原本要開啟的頁面。

共用 `auth.SafeRedirectURL(raw string) string` 處理目的地。只接受以單一 `/` 開頭的根相對網址，禁止絕對網址、`//`、反斜線、帳密、scheme、host、opaque URL、控制字元與錯誤的百分比編碼。解析後的路徑也必須以單一 `/` 開頭且不能含反斜線或控制字元；不得只檢查原字串。無效值回 `/admin`，不讓重新導向阻止帳密正確的使用者登入。

`HandleLocalLogin` 的 HTML 302、`HandleAuth` 儲存的 `oauth_redirect`、`HandleCallback` 取出的舊 Session 目的地都使用同一個函式，不能因為值來自 Cookie 就略過檢查。本機登入的 JSON 回應保持原有形狀。OAuth 成功回呼的身分驗證與帳號連結另由後續段落負責，此段不改建帳號政策。

`GET /login` 改以現有 html/template 呈現 `web/admin/login.html`，將經共用函式確認的值以 `RedirectURL` 提供模板。前端使用伺服器提供的值，移除自行信任 URLSearchParams 中 redirect 的導航分支。OAuth 按鈕仍可傳送返回網址，由伺服器在入口與回呼兩端確認。不得手動拼接可執行 JavaScript 字串，需保留模板的內容逸出。登入表單與安裝完成提示保持可用，不改樣式、語言選擇或 OAuth 啟用政策。

| 操作 | 失敗或邊界 | 預期結果與責任 | 驗證 |
|---|---|---|---|
| 本機登入 | 空值、絕對網址、協定相對網址、反斜線、控制字元、編碼後的危險路徑 | 帳密正確仍登入，HTML 回 `/admin` | 真實註冊路由與 PostgreSQL |
| 返回原頁 | `/admin/posts?search=hello#section`、一般站內頁面 | 保留合法目的地 | HTML 302 與真實瀏覽器 |
| OAuth 入口 | 惡意目的地 | Session 只保存 `/admin` | 公開 HandleAuth 與真實 Session 編解碼 |
| OAuth 回呼 | 舊版已簽章 Session 保存不合法網址 | 登入完成後回 `/admin` | Goth 公開 provider/session 測試替身，不連外 |
| 登入畫面 | redirect 含引號或結束 script 的字串 | 不執行輸入內容，不導向站外 | html/template 與 Chrome 操作 |
| 既有失敗 | 錯誤密碼、來源拒絕、離線 | 不導向成功頁或站外，不額外簽發登入 | HTTP 與畫面負向驗證 |

Go 回歸測試新增在 `internal/server/auth_redirect_integration_test.go`，使用既有 `newAccessHarness` 與真實 RegisterRoutes，不以測試專用 production 開關取代。OAuth 僅可透過 Goth 本來提供的 provider/session 接縫提供固定回應；不得改 production 以繞過 state 或連外交換授權。測試先重現，再由不同代理實作。瀏覽器腳本置於暫存目錄，以本機另一連接埠模擬站外目的地，禁止真正傳送帳密到外部網站。測試草稿、帳號與程序的清理僅限本次建立的內容。


## 登入嘗試限制

流程：POST /auth/login → 既有瀏覽器來源檢查 → 同一連線來源的嘗試次數檢查 → 既有帳密驗證。每個來源在第一次嘗試開始的固定 60 秒內最多 30 次；成功、錯誤帳密與不合法輸入均計數。第 31 次起回 JSON 429，附正整數秒數的 Retry-After，錯誤訊息也顯示等待秒數，不查詢帳號、不比對密碼、不簽發或清除登入 Cookie。拒絕不延長原本的到期時間，到期後可重新嘗試。前端沿用既有登入錯誤提示，不能導向成功頁。

共用入口 `ProtectLoginAttempts(next echo.HandlerFunc) echo.HandlerFunc` 置於新的認證檔案；RegisterRoutes 註冊一次，位於 ProtectBrowserWrites 內層。每次建立路由有獨立的限制狀態，不使用 Init 重設，也不在每次請求重建。來源取實際 Request.RemoteAddr 的 IP，忽略 X-Forwarded-For、X-Real-IP、Forwarded 與 Echo 的預設 RealIP；連接埠變動、IPv6 等價寫法與 IPv4-mapped IPv6 不得重建額度。無效或缺少連線位址使用單一共用來源，不能用任意字串增加記憶體紀錄。

狀態只保留在此程序記憶體，最多 4096 個來源，鎖內檢查與增加次數以支援並行。過期紀錄隨請求清理，不建立背景程序。容量滿且仍有有效紀錄時拒絕新來源，不淘汰仍有效的紀錄以恢復額度；已存在的來源可依剩餘次數操作。依到期順序移除過期來源，避免每個拒絕請求掃描整張表。可重用標準函式庫，不新增依賴、資料表、環境選項或永久帳號鎖定。

這是單程序的來源限制，重新啟動會重設。反向代理後會以代理連線位址共用額度，多程序與分散來源攻擊須由入口代理提供額外限制；此段不信任未設定信任範圍的轉送標頭。未新增全站帳號鎖定，以避免第三方連續嘗試就鎖住管理員帳號。此取捨依 [OWASP 登入限制及帳號鎖定的拒絕服務風險](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html#login-throttling)。

| 操作 | 失敗或邊界 | 預期結果 | 驗證 |
|---|---|---|---|
| 同一來源登入 | 30 次以內與第 31 次、輪替帳號與變更連接埠 | 額度內保留原結果，超量 429、無登入 Cookie | 真實 RegisterRoutes 與隔離 PostgreSQL |
| 偽造來源 | XFF、X-Real-IP、Forwarded、IPv6 等價或 mapped IPv4 | 仍使用同一實際連線來源的額度 | 原始 HTTP 請求 |
| 到期與重試 | 期間內多次被拒絕、正好到期 | Retry-After 合法，不延長等待；到期恢復 | Go testing/synctest 的公開路由測試，不實際等待一分鐘 |
| 並行與容量 | 同來源同時超量、4096 個有效來源、新來源 | 恰好 30 次進入下游；滿容量拒絕新來源且不重設舊來源 | 無 DB 的不合法 JSON 路由回應與競態測試 |
| 隔離 | 不同連線來源、新路由實例、首頁與已登入 API | 不共用錯誤額度，不影響其他功能 | 真實 HTTP 正負對照 |
| 瀏覽器 | 連續錯誤密碼後送出正確密碼 | 429 留在登入頁顯示錯誤，期限後可登入與登出 | Chrome 桌面與手機，僅專用驗收帳號 |

測試先新增 `internal/auth/login_attempts_test.go` 與 `internal/server/login_attempts_integration_test.go`。前者從公開 RegisterRoutes 送不合法 JSON，使用標準 testing/synctest 驗證時間、容量、來源與並行，不使用測試專用 production 開關。後者使用 newAccessHarness、真實帳密與 DB 回查；每個獨立測試重建路由。既有測試若超過額度，須先回報其語意與必要調整，不可削弱斷言。瀏覽器腳本只寫暫存目錄，由主代理執行，測試代理不得操作服務或瀏覽器。


## OAuth 提供者與 state 輸入

本段已由主代理重現，代理修正後完成全套 PostgreSQL 競態測試、go vet、Windows amd64 與 Linux 386 編譯，對抗性審查沒有已確認缺陷，待 CI 交付。既有絕對登入期限測試改用有效提供者觸發重簽，原有期限與身分不變及過期回 401 的斷言完整保留，另驗未知提供者 400 且不重簽。真實 Google／GitHub 授權仍未驗證。

流程：開啟 /auth/:provider → 以路徑選定提供者 → 產生本次 state → 保存既有 Goth Session → 提供者回呼 → 嚴格比對簽章 Session 中的原始 state → 取得相同提供者的使用者 → 原有登入流程。此段修正提供者與 state 的輸入處理，接續的一次性交易與帳號識別仍是本工作單的必要驗收。

入口與回呼只接受 google、github，且必須已在 Goth 註冊。清除使用者輸入的 provider、:provider，傳給 Goth 的 provider 只能是路徑值。入口忽略所有外部 state，每次以 crypto/rand 產生至少 32 位元組的 base64url 亂數；亂數失敗回一般 503，不 panic 或送出授權導向。沿用 HandleAuth、HandleCallback 公開簽名，不新增資料模型、依賴或設定。

回呼先以 url.ParseQuery 完整解析，拒絕不合法編碼或分隔、多個或空白 state。透過 Goth 公開 Store 與 Provider/Session 接縫取得簽章的原始授權網址；原網址也必須只有一個非空白 state。兩個 state 須完全相同，不能接受人工簽章但原始 state 空白的舊 Cookie。任何失敗在 FetchUser、Authorize 與帳號 DB 操作之前回一般 400，不簽發登入 Cookie。提供者取得使用者後，user.Provider 與路徑不同回 400，不進入帳號操作。有效 state 通過後的提供者錯誤回一般 502。錯誤訊息不得暴露原始 Cookie、state、provider token 或 provider 錯誤內容。

入口儲存返回網址時必須檢查 Session 讀取與 Save 的錯誤，失敗回一般 503 並停止授權。不改既有登入 Cookie 的七天期限、返回網址檢查、成功 JSON 形狀、預設角色或本機登入。已有登入 Session 經 OAuth 入口重簽仍不得延長 login_expires_at。

| 操作 | 失敗或邊界 | 預期結果 | 驗證 |
|---|---|---|---|
| 選擇提供者 | 查詢 provider、:provider 重複或衝突 | 始終採路徑提供者，未知或未註冊值 400 | 公開 HandleAuth、HandleCallback 與計數替身 |
| state 產生 | 指定值、多值、兩次獨立入口、亂數失敗 | 不採輸入值，兩次不同，失敗 503 且不導向 | 真實 HTTP 與公開 crypto/rand.Reader 接縫 |
| state 比對 | 缺少、空白、重複、不同、不合法編碼、損壞 Cookie、原始 state 空白 | 400，FetchUser/Authorize/DB 次數不增加，無登入 Cookie | 真實 RegisterRoutes、隔離 PostgreSQL 或無 DB 的公開處理器 |
| 正常回呼 | 正確簽章 Session 與唯一相同 state | 原有登入與站內返回網址保持可用 | 既有返回網址回歸與獨立成功對照 |
| 提供者資料 | user.Provider 與路徑不符、provider 錯誤 | 一般錯誤，不建立或登入帳號，不洩漏 provider 秘密 | 計數替身及 DB 前後回查 |
| Session 保存 | 損壞既有 Session、超出 Cookie 可保存長度 | 503，不開始授權，不忽略 Save 失敗 | 公開 Session API 與真實 HandleAuth |

新增測試置於 internal/auth/oauth_input_test.go 與 internal/server/oauth_input_integration_test.go。測試與實作由不同代理負責，主代理先重現再驗收。既有 redirect 測試保持原斷言，不用測試專用 production 開關。後續須完成 state 短效與伺服器端原子消耗、OAuth Cookie HttpOnly/期限/機密性、PKCE、提供者關閉即時檢查，以及穩定 provider+subject 與安全帳號連結；不能以此段通過宣稱 OAuth 完成。

依據：[RFC 9700 的不同回呼網址與 state 防護](https://www.rfc-editor.org/rfc/rfc9700.html#section-2.1)、[GitHub 的 state 與 PKCE 契約](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps)。
