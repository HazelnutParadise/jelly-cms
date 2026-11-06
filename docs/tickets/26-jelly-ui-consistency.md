# 26 — 站長在後台遇到的每一種選擇、切換、提示與對話框，都只有一種長相與手感

User Story：站長在後台任何一頁按開關、選分段、勾選項目、選單選項、看到通知或打開對話框時，同一種控制項長得一樣、按起來的果凍手感一樣，不會這頁是 Jelly UI、下一頁又是自家方塊或瀏覽器原生樣式。

Blocked by：12 零 CDN 與 Jelly UI（已合併，Jelly UI 1.1.0 釘 `1b775385` 自帶於 `web/admin/assets/jelly-ui/`）。
設計稿：`design/project/` 中凡是畫了開關、分段選擇、勾選框、單選、對話框的後台板（清單見〈設計板〉）。`design/README.md` 的〈Jelly UI 元件對照〉改寫為本工作單的界線表。
來源：使用者回報「Jelly UI 融入得不好」。2026-11-06 以真實伺服器在 1440 寬、亮／暗截圖檢視 15 個後台頁，並在後台實際渲染 Jelly UI 全部可用元件比對後寫成本單。

## 現況與問題（2026-11-06，main `fc6ab71`）

工作單 12 只把四個點換成 Jelly UI：設定頁的開關、文章列表的狀態篩選、通知、確認框。其餘頁面仍用自家或原生控制項，同一種控制項在後台有兩到三種長相：

- **開關**：設定頁是 Jelly 膠囊（46×26、圓形拇指）；分類、選單、折扣碼是自家 `.toggle-switch`（對角方角、方形拇指）；文章與頁面編輯器另有自寫的 `role="switch"` 按鈕。
- **分段選擇**：文章列表與設定頁「環境」是 Jelly 膠囊；留言、版本、回收桶、使用者、分類、折扣碼、商品、訂單、三個編輯器的狀態、訂單明細、選單「加入項目」是十多種各自命名的自家方塊（`.cm-seg`、`.rv-seg`、`.tr-seg`、`.role-tabs`、`.tx-seg`、`.sa-seg`、`.segmented`、`.mn-tabs`）。
- **勾選框與單選**：全部是瀏覽器原生（`accent-color` 覆盆子紅），28 個勾選框、20 個單選。
- **提示**：22 個原生 `alert()`（附件、外掛、設定、主題），其中附件與設定的文字是寫死的英文。
- **對話框**：確認框是 Jelly；其餘是 5 個原生 `<dialog>`（編輯器 3、API 串接 2）與 8 組自製 `.modal-overlay`（留言、折扣碼、選單、外掛、個人資料、主題、使用者 3），以及 `system-admin.js` 用 `showModal()` 開的對話框。

實際渲染 Jelly UI 全部元件後確認的上游事實：

- 開關、勾選框、單選、分段選擇、對話框、通知在亮暗兩種模式都已經接上 `layout.html` 的 token，開的狀態是薄荷色（與自家開關相同），手感明顯。
- `jelly-button`、`jelly-input`、`jelly-select`、`jelly-badge`、`jelly-chip` 是薄荷色圓潤膠囊；`--jelly-<元件>-radius` 設成對角單角（`0 10px 0 10px`）**沒有效果**（實測左右兩組完全相同），無法接上本專案的對角單角形狀語言。只有 `--jelly-dialog-radius` 與 `--jelly-toast-radius` 有效（工作單 12 已設定）。

## 界線（本工作單的核心決定）

依「控制項的種類」劃線，同一類在整個後台只有一種實作：

| 類別 | 控制項 | 用什麼 | 理由 |
|---|---|---|---|
| 選擇與切換 | 開關、分段選擇、勾選框、單選群組 | Jelly UI：`<jelly-switch>`、`<jelly-segmented>`／`<jelly-segment>`、`<jelly-checkbox>`、`<jelly-radio-group>`／`<jelly-radio>` | 果凍手感最有感的地方；亮暗已對好；形狀是膠囊或圓形，與周圍無衝突 |
| 回饋與覆蓋層 | 通知、確認、一般對話框 | Jelly UI：`window.Jelly.toast()`、`window.Jelly.confirm()`、`<jelly-dialog>` | 已有對角單角圓角；統一 Esc、點背景、焦點鎖定與歸還 |
| 輸入與動作 | 文字欄位、多行文字、下拉選單、按鈕、徽章、卡片、表格、側欄、⌘K 面板 | 維持自家（底線欄位、對角單角按鈕、覆盆子紅主要動作） | Jelly 版本是薄荷色圓潤膠囊、圓角不可改，會讓後台不像 Jelly CMS |
| 導覽 | 會換網址或切換整頁區塊的分頁列：設定頁分頁列、個人資料分頁、附件策略頁 `.sys-tabs` | 維持自家分頁 | 分頁是導覽，分段選擇是選值，兩者語意不同 |

判斷一組按鈕該用分段選擇還是分頁：**在卡片或清單內選一個值、篩選清單、切換卡片內的來源**，用 `<jelly-segmented>`；**換網址或切換整頁的主要區塊**，用自家分頁。

## 完成條件

- [ ] 站長在後台任何頁面看到的開關都是 `<jelly-switch>`：`.toggle-switch` 的 CSS、模板與 `components.html` 的 `toggle` 範本全部移除；文章與頁面編輯器的自寫 `role="switch"` 按鈕改為 `<jelly-switch>`。
- [ ] 站長在後台選值或篩選時，都是 `<jelly-segmented>`：〈逐頁清單〉列出的每一組都換掉，各頁自己的分段 CSS 類（`.cm-seg`、`.rv-seg`、`.tr-seg`、`.role-tabs`、`.tx-seg`、`.sa-seg`、`.segmented`、`.mn-tabs`）不再出現於模板與腳本。
- [ ] 站長在後台勾選或單選時，都是 `<jelly-checkbox>` 或 `<jelly-radio-group>`：後台模板與腳本產生的 HTML 不再有 `<input type="checkbox">` 與 `<input type="radio">`（例外見〈不做〉）；表格的全選與逐列勾選也換。
- [ ] 站長操作後看到的回饋都是 Jelly 通知：後台不再有原生 `alert()`；原本寫死英文的訊息改成 i18n 鍵，繁中與 English 都有。成功用 `kind: 'success'`，失敗用 `kind: 'error'`。
- [ ] 站長打開的每個對話框都是 `<jelly-dialog>`：5 個原生 `<dialog>`、8 組 `.modal-overlay` 與 `system-admin.js` 的 `showModal()` 全部改用 `<jelly-dialog>`；Esc、點背景、右上 ✕ 都能關閉；關閉後焦點回到打開它的按鈕；對話框裡的表單照常送出與驗證。`⌘K` 指令面板不在此列。
- [ ] 所有替換前後，送到伺服器的請求本文、查詢參數與儲存結果完全相同（既有後端測試照樣通過；瀏覽器驗收逐頁比對）。
- [ ] 鍵盤操作不退步：開關空白鍵切換、勾選框空白鍵切換、單選群組方向鍵移動、分段選擇方向鍵移動、對話框 Esc 關閉；焦點環可見。
- [ ] 亮色、暗色、繁中、English、390px、1280px 下，替換後的頁面沒有整頁橫向溢出；手機上的長分段選擇只在自己的列內捲動（沿用工作單 12 文章列表的做法）。
- [ ] `web/admin/COMPONENTS.md` 與 `components.html` 展示頁改寫成〈界線〉的規則與 Jelly 元件用法；`design/README.md` 的〈Jelly UI 元件對照〉改為〈界線〉表。
- [ ] 設計板依〈設計板〉更新並重新產生；線上畫布在合併後同步。

## 逐頁清單

實作者以這份清單為起點，但要自己再搜一次，清單漏掉的同類控制項也要換。

| 頁面 | 開關 | 分段選擇 | 勾選框／單選 | 提示 | 對話框 |
|---|---|---|---|---|---|
| `taxonomy.html` | `.tx-toggle` ×3 | `.tx-seg`（分類／標籤／商品分類） | 有 | | |
| `menus.html` | `.mn-toggle` ×2 | `.mn-tabs`（加入項目的來源） | 有 | | `.modal-overlay` |
| `coupons.html` | `.sa-toggle` ×3 | `.sa-seg`（進行中／排程／已結束）、`.segmented` ×2（類型、適用） | 有 | | `.modal-overlay` |
| `post_editor.html`、`page_editor.html` | `role="switch"` ×3 | `.segmented`（狀態） | 有 | | |
| `product_editor.html` | | `.segmented`（狀態） | 有（運送：需要運送、冷藏） | | |
| `order_detail.html` | | `.segmented` | 有 | | |
| `products.html`、`orders.html` | | `.sa-seg` | | | |
| `comments.html` | | `.cm-seg`（狀態篩選） | 有 | | `.modal-overlay` |
| `revisions.html` | | `.rv-seg`（檢視模式） | 有 | | |
| `trash.html` | | `.tr-seg`（類型篩選） | 有 | | |
| `users.html` | | `.role-tabs`（依角色篩選） | 有（表格勾選、邀請的角色單選卡） | | `.modal-overlay` ×3 |
| `media.html` | | | | `alert()` ×5（英文寫死） | |
| `plugins.html` | | | | `alert()` ×3 | `.modal-overlay` |
| `settings.html` | | | | `alert()` ×6（英文寫死） | |
| `themes.html` | | | 有（主題設定欄位產生的勾選框） | `alert()` ×8 | `.modal-overlay` |
| `profile.html` | | | 有 | | `.modal-overlay` |
| `editor.html` | | | | | `<dialog>` ×3 |
| `integrations.html`、`integration_detail.html`、`assets/integrations.js` | | | | | `<dialog>` |
| `assets/system-admin.js` | | | 有（`.sys-check`，含「秘密欄位清空」與「即時尾巴」） | | `showModal()` |
| `assets/shop-admin.js`、其他 `assets/*.js` | 依實際搜尋 | 依實際搜尋 | 依實際搜尋 | | |
| `layout.html` | | | 移除 `input[type="checkbox"], input[type="radio"]` 的 `accent-color` 規則（已無對象時） | | 自製 `.modal-overlay` 中屬於一般對話框的換掉；⌘K 面板不換 |

## 契約

### 元件寫法（沿用工作單 12 已驗證的上游行為）

- 開關：`<jelly-switch name label checked disabled>`；讀 `el.hasAttribute('checked')`，寫 `el.toggleAttribute('checked', v)`；**不得**對開關、勾選框、單選讀寫 `.checked` 屬性（元素升級前指定會被實例屬性遮住，工作單 12〈上游事實〉第 6 點）。
- 勾選框：`<jelly-checkbox name value checked disabled>文字</jelly-checkbox>`，讀寫規則同開關。
- 單選群組：`<jelly-radio-group name label value>` 包 `<jelly-radio value>`；讀寫一律用 `value` 屬性（`getAttribute('value')`／`setAttribute('value', v)`）。實作前從上游原始碼確認單選群組的選取與 `change` 事件行為，寫進本檔〈測試另外釘下的約定〉。
- 分段選擇：沿用工作單 12：讀 `getAttribute('value')`、寫 `setAttribute('value', v)`；段標籤只能是純文字。
- 「勾選卡片」（例：邀請使用者的角色、匯出格式）保留卡片版面，卡片內的單選指示改用 `<jelly-radio>`，點整張卡片等同點選。
- 對話框：`<jelly-dialog label>` 包內容；打開 `setAttribute('open','')`，關閉 `removeAttribute('open')`；監聽 `close` 事件做清理與焦點歸還。對話框內的表單保留原本的 `<form>`，送出邏輯不變。可以在 `layout.html` 的 `window.Jelly` 加一個共用的 `Jelly.dialog(el)`／`Jelly.closeDialog(el)` 輔助函式，給各頁一致的開關與焦點歸還，但不得改變 `Jelly.confirm` 與 `Jelly.toast` 的既有簽名。
- 提示：`window.Jelly.toast(message, { kind })`，`kind` 為 `success`、`error`、`info`、`warning`；訊息文字走頁面的 `T` 或 `{{.T.*}}` i18n 鍵。
- 主題設定（`themes.html` 依 `theme.json` 欄位產生的勾選框）與系統頁（`system-admin.js`）由腳本產生的 HTML 也遵守以上寫法。

### 資料與相容

- 所有替換只動前端模板、腳本與 CSS，不改 API、資料表、遷移與 Go 程式。送出的 JSON 欄位名稱、值的型別與表單欄位名稱不變；布林仍是布林。
- 預設值、停用狀態、驗證訊息、`aria-*` 標籤與 i18n 文案保留。
- 移除不再使用的 CSS（各頁的分段與開關類、`.toggle-switch`、`.modal-overlay` 相關）與 `components.html` 的 `toggle` 範本；其他頁面若仍有引用則一併換掉，不留死碼。

## 不做

- 不採用 `jelly-button`、`jelly-input`、`jelly-textarea`、`jelly-select`、`jelly-badge`、`jelly-chip`、`jelly-tabs`、`jelly-card`、`jelly-pagination`、`jelly-tooltip`、`jelly-menu`、`jelly-drawer`。
- 不換導覽分頁（設定頁分頁列、個人資料分頁、附件策略頁 `.sys-tabs`）與 ⌘K 指令面板。
- 不換內容裡的勾選框：編輯器預覽、文章內容渲染出的待辦清單 `<input type="checkbox" disabled>` 屬於使用者內容，不是後台控制項。
- 不換登入頁、安裝頁、帳號復原頁與前台主題（工作單 12 已決定只有後台 `layout.html` 載入 Jelly UI）。
- 不改上游 Jelly UI 的建置與版本。

## 設計板

依〈界線〉更新所有後台板，產生的板改 `design/gen/` 腳本後重新產生，手寫板直接改：

- 開關一律畫成 `.tg.j`（已有）；分段選擇一律畫成 `.seg.j`（已有）；勾選框與單選新增 `.cb.j`、`.rd.j` 兩個樣式（膠囊圓角勾選方塊、圓形單選，開的狀態薄荷色，與實際元件一致），加進 `gen.py` 共用 CSS 與手寫板。
- 對話框一律畫成 `.jd`（已有）；板上原本畫的自製對話框改畫成 `.jd`。
- 板上的導覽分頁、按鈕、欄位、下拉選單不改。
- `design/README.md`〈Jelly UI 元件對照〉改為〈界線〉表加每一類在板上的樣子；畫布索引與尺寸照工作單 #38 的量測規則維持一致（板高變動時更新 `$preview` 與 `canvas.json`，沒有重疊）。

## 決策紀錄

| 決定 | 被否決的選項與理由 |
|---|---|
| 依控制項種類劃線：選擇與切換、回饋與覆蓋層用 Jelly；輸入、按鈕、卡片、導覽維持自家 | 全面改用 Jelly 所有元件：按鈕、欄位、徽章會變成薄荷色圓潤膠囊且圓角不可改，後台不再像 Jelly CMS；退回自家只留確認框與通知：失去 issue #18 要的果凍手感 |
| 分段選擇與分頁的分界：選值／篩選／卡片內切換用分段，換網址或整頁區塊用分頁 | 一律用分段：設定頁那排是導覽，做成分段會讓人以為是選一個值；一律用 `jelly-tabs`：上游是薄荷色膠囊樣式，與自家分頁列衝突 |
| 勾選框與單選也換成 Jelly | 留原生：原生 `accent-color` 是覆盆子紅方塊，和旁邊 Jelly 開關的薄荷膠囊並排時最不協調（截圖已見） |
| 原生 `alert()` 全部換成 Jelly 通知並補 i18n | 留 `alert()`：會擋住整個頁面、樣式是瀏覽器的，且附件與設定頁寫死英文 |
| 所有一般對話框統一 `<jelly-dialog>` | 留原生 `<dialog>` 與自製遮罩：三種關閉行為、焦點處理與樣式並存 |

## 實作順序（槽位）

1. 26-A 設計板：新增 `.cb.j`、`.rd.j`，把所有後台板的開關、分段、勾選、單選、對話框改成 Jelly 樣子並重新產生；`design/README.md`。
2. 26-B 測試：擴充 `internal/assetcheck/adoption_test.go`（禁用清單與必要元件）、`internal/server/jelly_ui_integration_test.go`（各頁輸出），以及 `scripts/acceptance/jelly-ui.spec.mjs`（逐頁鍵盤操作、送出內容不變、對話框行為、通知取代 alert、亮暗與 390px 不溢出）。
3. 26-C 實作：依〈逐頁清單〉替換，更新 `COMPONENTS.md` 與 `components.html`。要通過：26-B 的全部測試與既有測試。
4. 26-D 驗收：主代理以真實伺服器跑瀏覽器腳本並逐頁截圖對照設計板。

## 測試另外釘下的約定

（由 26-B 測試代理補上。）

## 實作者的判斷邊界

可以自己決定：
- 共用輔助函式（例如 `Jelly.dialog`）的內部寫法與放置位置，只要不改既有 `Jelly.confirm`、`Jelly.toast` 的簽名。
- 新增 i18n 鍵的名稱與 English 文案。
- 移除死碼的範圍（只限本單替換造成的死碼）。
- 手機版分段選擇的內距與捲動細節，沿用工作單 12 文章列表的做法。

必須回報、不得自行決定：
- 某個控制項無法用 Jelly 元件表達原本的行為（例如三態勾選、需要 HTML 標籤的分段文字）。
- 需要改 API、資料格式、Go 程式或遷移。
- 需要採用〈不做〉列出的 Jelly 元件，或改變〈界線〉。
- 測試要求互相矛盾或做不到（回報，不改測試）。
