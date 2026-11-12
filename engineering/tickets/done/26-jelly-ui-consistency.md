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
- 勾選框：`<jelly-checkbox size="sm" name value checked disabled>文字</jelly-checkbox>`，讀寫規則同開關。
- 單選：`<jelly-radio-group size="sm" label>` 包 `<jelly-radio size="sm" name value checked>`。上游 1.1.0 的單選群組**沒有** `value` 屬性（`observedAttributes` 只有 `label`、`direction`、`size`）；選取狀態在 `<jelly-radio>` 的 `checked` 屬性，同一組靠每個單選上相同的 `name`（上游以 `jelly-radio[name="…"]` 找同組，點選時會移除同組其他單選的 `checked`），`change` 事件由被選到的單選發出。讀取目前值：找同組帶 `checked` 屬性的單選讀它的 `value`；寫入：對目標單選 `setAttribute('checked','')`、同組其他單選 `removeAttribute('checked')`。
- 尺寸：後台的勾選框與單選一律 `size="sm"`（28px，與開關軌道等高；預設 32px 太大）；`--jelly-checkbox-size` 等尺寸變數實測無效。開關與分段選擇用預設大小，不加 `size`。
- 分段選擇：沿用工作單 12：讀 `getAttribute('value')`、寫 `setAttribute('value', v)`；段標籤只能是純文字。
- 「勾選卡片」（例：邀請使用者的角色、匯出格式）保留卡片版面，卡片內的單選指示改用 `<jelly-radio>`，點整張卡片等同點選。
- 對話框：`<jelly-dialog label>` 包內容；打開 `setAttribute('open','')`，關閉 `removeAttribute('open')`；監聽 `close` 事件做清理與焦點歸還。對話框內的表單保留原本的 `<form>`，送出邏輯不變。可以在 `layout.html` 的 `window.Jelly` 加一個共用的 `Jelly.dialog(el)`／`Jelly.closeDialog(el)` 輔助函式，給各頁一致的開關與焦點歸還，但不得改變 `Jelly.confirm` 與 `Jelly.toast` 的既有簽名。
- 提示：`window.Jelly.toast(message, { kind })`，`kind` 為 `success`、`error`、`info`、`warning`；訊息文字走頁面的 `T` 或 `{{.T.*}}` i18n 鍵。
- 主題設定（`themes.html` 依 `theme.json` 欄位產生的勾選框）與系統頁（`system-admin.js`）由腳本產生的 HTML 也遵守以上寫法。

### 資料與相容

- 所有替換只動前端模板、腳本與 CSS，不改 API、資料表、遷移與 Go 程式。送出的 JSON 欄位名稱、值的型別與表單欄位名稱不變；布林仍是布林。
- 預設值、停用狀態、驗證訊息、`aria-*` 標籤與 i18n 文案保留。
- 表單重設：`form.reset()` 不會還原 Jelly 勾選框、單選與開關。送出成功後要重設的表單，Jelly 控制項要由腳本設回該表單初次顯示時的狀態（例：新增 API 串接後權限全部不勾、邀請角色回到「編輯」）。
- 對話框點背景：main 上點背景會關閉的對話框（折扣碼批次產生、選單的新增與設定側頁、留言回覆／編輯、使用者對話框、個人帳號身分確認）維持點背景關閉；main 上點背景不會關閉的（外掛上傳、主題客製化、系統頁 `modal()` 表單、API 串接對話框含一次性金鑰、編輯器的還原／匯出／快捷鍵）點背景不關，Esc 與 ✕ 照常可關（鎖住的除外）。任何對話框都不因為在對話框內按下滑鼠、拖到背景才放開而關閉（例如拖曳選取金鑰或文字時超出邊界）。
- 忙碌時換篩選：分段與開關在請求進行中不停用；請求結束（不論成功、失敗或沒有更新任何資料）後，畫面上選取的篩選與清單內容必須一致，期間換過篩選就依新篩選重新載入。
- 搜尋時的期間分段：折扣碼有搜尋字時，點目前選取的期間段等同離開搜尋（清空搜尋框、依該期間重新載入），與 main 點任一段都會離開搜尋的行為相同。
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
| 巢狀對話框按一次 Esc 只關最上層，由 `layout.html` 的 `Jelly.dialog` 共用堆疊處理，並把焦點還給打開它的元素 | 照上游行為：每個開著的對話框各自在 document 聽 Esc，確認框疊在設定對話框上時一次全關，未儲存的輸入跟著消失 |
| 備用碼對話框鎖住（`locked`），只能按「我已保存備用碼」關閉 | 可隨意關閉：使用者可能沒存到備用碼就關掉，之後無法再看到 |
| 通知中心的「未讀」做成獨立開關，可與分類分段同時套用 | 照設計板併進同一組分段：未讀與分類變成互斥，篩選結果的意思會改變 |
| 附件頁的「複製網址」「刪除」改用 `data-*` 屬性加事件委派，一併修好 | 只換掉 `alert()`：行內 `onclick` 的引號把屬性截斷，兩個鈕按了沒有反應，換了通知也看不到 |
| 點背景是否關閉對話框沿用各對話框在 main 的行為；拖曳超出邊界一律不關 | 全部改成點背景就關（上游預設）：一次性金鑰、自動儲存還原提示、主題與系統頁表單會因誤點或拖曳選字而關閉，金鑰要重新輪替、未存的輸入消失；全部都不關：原本點背景可關的對話框行為改變 |

## 實作順序（槽位）

1. 26-A 設計板：新增 `.cb.j`、`.rd.j`，把所有後台板的開關、分段、勾選、單選、對話框改成 Jelly 樣子並重新產生；`design/README.md`。
2. 26-B 測試：擴充 `internal/assetcheck/adoption_test.go`（禁用清單與必要元件）、`internal/server/jelly_ui_integration_test.go`（各頁輸出），以及 `scripts/acceptance/jelly-ui.spec.mjs`（逐頁鍵盤操作、送出內容不變、對話框行為、通知取代 alert、亮暗與 390px 不溢出）。
3. 26-C 實作：依〈逐頁清單〉替換，更新 `COMPONENTS.md` 與 `components.html`。要通過：26-B 的全部測試與既有測試。
4. 26-D 驗收：主代理以真實伺服器跑瀏覽器腳本並逐頁截圖對照設計板。

## 測試另外釘下的約定

以下名稱與格式由 26-B 的測試釘死，工作單本文沒有寫到的部分以這裡為準。測試在現況（main `fc6ab71`）下失敗是預期的，失敗訊息會指出它找到的舊控制項。

### 測試與執行方式

- 不需資料庫：`internal/assetcheck/consistency_test.go`。`TestAdminHasNoHandMadeSwitches`、`TestAdminRendersNoNativeCheckboxOrRadio`、`TestAdminUsesNoNativeAlertConfirmOrPrompt`、`TestAdminDialogsAreJellyDialogs`、`TestAdminSegmentedChoicesUseNoPageOwnClasses`、`TestAdminPagesUseTheJellyElementsTheyNeed`、`TestAdminJellyChoiceControlsAreSmallAndNamed`、`TestComponentsGuideDescribesTheBoundary` 在替換後要通過；`TestAdminNavigationTabsStayNavigation` 現在就通過，替換後仍要通過。
- 需要資料庫：`internal/server/jelly_ui_consistency_integration_test.go`（`JELLY_TEST_POSTGRES=1`）。`TestAdminPagesRenderOnlyJellySelectionControls`、`TestAdminPagesRenderTheirChoicesAsJellyControls` 要通過；守門測試 `TestCouponCreatePayloadStaysTheSame`、`TestCategoryFlagsStayTheSame`、`TestProductShippingFlagsStayTheSame`、`TestUserInviteRolePayloadStaysTheSame`、`TestThemeLayoutCheckboxesStayTheSame` 現在就通過，替換後仍要通過。
- 瀏覽器：`JELLY_ONLY=consistency node scripts/acceptance/jelly-ui.spec.mjs`（其餘環境變數同檔頭）。不設 `JELLY_ONLY` 時這一組也會跑。它會建立並刪除一篇草稿文章、選單、折扣碼、分類與商品；邀請、主題設定、主題啟用、OAuth 儲存、批次產生與匯出下載的請求在瀏覽器內攔截回應，不寫進資料庫。

### 掃描範圍與允許清單

- 靜態掃描讀 `web/admin/*.html`、`web/admin/assets/*.js`、`web/admin/assets/*.css`（不遞迴，所以 `jelly-ui/`、`codemirror/`、`pele/`、`fonts/` 不讀）。CSS 只套用類名規則。
- 不掃描、也不受本單規則約束的模板（不使用後台版型、不載入 Jelly UI）：`login.html`、`install.html`、`account.html`、`account_layout.html`、`account_recover.html`、`password_reset.html`、`invite.html`。
- 原生勾選框／單選與原生對話框的允許清單都是空的。⌘K 面板是 `layout.html` 的 `<div class="palette-overlay" id="palette" hidden>` 內含 `<div class="palette" role="dialog" aria-modal="true">`，不是 `.modal-overlay`、`<dialog>` 或 `showModal()`，不會被規則抓到；`TestAdminNavigationTabsStayNavigation` 確認它原樣保留。內容裡的待辦清單 `<input type="checkbox" disabled>` 由 `internal/markup` 在執行時產生，不在模板裡；瀏覽器檢查排除 `#editorPreview` 之內的元素。
- 瀏覽器只數 light DOM（`document.querySelectorAll`）：`<jelly-checkbox>` 的 shadow root 內有它自己的原生 checkbox，不算。注意 Playwright 的 CSS 選擇器會穿透 shadow root，所以 `page.locator('input[type=checkbox]')` 會數到 Jelly 內部的那一個；檢查用的是頁面內的 `querySelectorAll`。瀏覽器檢查比「可見的為 0」更嚴：隱藏的原生 checkbox／radio 也算（失敗訊息標出哪些可見）。

### 不得再出現的寫法（模板、腳本字串與伺服器輸出）

- 類名或 CSS 選擇器：`toggle-switch`、`modal-overlay`、`cm-seg`、`rv-seg`、`tr-seg`、`role-tabs`、`tx-seg`、`sa-seg`、`mn-tabs`（整個名稱比對，前後不是英數、`-`、`_`），以及作為類名的 `segmented`（`.segmented` 選擇器、`class="… segmented …"`、`className`、`classList.*('segmented')`）。`jelly-segmented` 不受影響。
- `{{define "toggle"}}`、`{{template "toggle"`；`role="switch"`（含 `setAttribute('role','switch')`）。
- 原生勾選框／單選：`type="checkbox"`、`type='radio'`、`type=checkbox`、JS 字串裡的 `type=\"checkbox\"`、`input.type = 'radio'`、`setAttribute('type','checkbox')`，以及 `input[type="checkbox"]` 選擇器（`layout.html` 的 `accent-color` 規則因此必須移除）。`el.type === 'checkbox'` 這類比較與 `theme.json` 欄位描述 `{type: 'checkbox'}` 不算。
- `alert(`、`confirm(`、`prompt(`（前面是 `.` 的方法呼叫如 `Jelly.confirm(` 不算）；JS 註解裡寫這幾個字也會被抓到。
- `<dialog`、`createElement('dialog')`、`.showModal(`。`<jelly-dialog>` 也有 `showModal()`，但只在升級後存在，所以一律用 `setAttribute('open','')` 打開。
- `.checked`、`.indeterminate`（讀或寫，含一般物件的鍵，例如 `system-admin.js` 保存表單狀態用的 `field.checked` 要改名）與 `:checked`。Go 模板的 `{{.checked}}` 不算。讀用 `hasAttribute('checked')`，寫用 `toggleAttribute('checked', v)`；`:checked` 改成 `[checked]`。
- 〈不做〉列出的 Jelly 元件：`jelly-button`、`jelly-input`、`jelly-textarea`、`jelly-select`、`jelly-badge`、`jelly-chip`、`jelly-tabs`、`jelly-card`、`jelly-pagination`、`jelly-tooltip`、`jelly-menu`、`jelly-drawer`。

### 各來源必須用到的元件

「用到」指模板或腳本字串裡的 `<jelly-…` 開始標籤，或帶引號的標籤名（`createElement('jelly-checkbox')`、`node("jelly-checkbox")`）。伺服器輸出測試另以 `/admin` 儀表板為基準，頁面的次數要多於儀表板（版型的 `Jelly.confirm` 本身就會建立 `jelly-dialog`）。

| 來源 | 必須用到 |
|---|---|
| `taxonomy.html` | `jelly-switch`、`jelly-segmented` |
| `menus.html`、`coupons.html` | `jelly-switch`、`jelly-segmented`、`jelly-checkbox`、`jelly-dialog` |
| `post_editor.html` | `jelly-switch`、`jelly-segmented`、`jelly-checkbox` |
| `page_editor.html` | `jelly-switch`、`jelly-segmented` |
| `product_editor.html`、`order_detail.html` | `jelly-segmented`、`jelly-checkbox` |
| `products.html`、`orders.html` | `jelly-segmented` |
| `comments.html` | `jelly-segmented`、`jelly-checkbox`、`jelly-dialog` |
| `revisions.html` | `jelly-segmented`、`jelly-switch` 或 `jelly-checkbox`（「只顯示變更」） |
| `trash.html` | `jelly-segmented`、`jelly-checkbox` |
| `users.html` | `jelly-segmented`、`jelly-checkbox`、`jelly-radio-group`、`jelly-radio`、`jelly-dialog` |
| `media.html` | `Jelly.toast(` |
| `plugins.html` | `Jelly.toast(`、`jelly-dialog` |
| `settings.html` | `Jelly.toast(` 或 `SA.toast(` |
| `themes.html` | `jelly-checkbox`、`Jelly.toast(`、`jelly-dialog` |
| `profile.html` | `jelly-checkbox` 或 `jelly-switch`、`jelly-dialog` |
| `editor.html` | `jelly-dialog`、`jelly-radio-group`、`jelly-radio`、`jelly-checkbox` |
| `integrations.html`、`integration_detail.html` | `jelly-dialog` |
| `assets/integrations.js` | `jelly-checkbox`（權限）、`jelly-radio-group`、`jelly-radio`（輪替方式） |
| `assets/system-admin.js` | `jelly-checkbox`（`.sys-check`）、`jelly-dialog` |

瀏覽器另外要求頁面載入後 DOM 裡有：`/admin/settings/auth` 的 `jelly-switch`、`/admin/logs` 的 `jelly-checkbox`（即時尾巴）、`/admin/integrations` 的 `jelly-checkbox`（權限）、`/admin/account?tab=notifications` 的 `jelly-checkbox` 或 `jelly-switch`。

### 文件與保留的導覽

- `web/admin/COMPONENTS.md` 必須出現：「選擇與切換」「回饋與覆蓋層」「輸入與動作」「導覽」四類，`<jelly-switch`、`<jelly-segmented`、`<jelly-checkbox`、`<jelly-radio-group`、`<jelly-radio`、`<jelly-dialog`、`Jelly.toast`、`Jelly.confirm`，以及 `size="sm"`、`toggleAttribute('checked'`、`hasAttribute('checked')`、`setAttribute('open'` 這幾個寫法；不得再有 `{{template "toggle"`、`toggle-switch`、`modal-overlay`。`components.html` 是範本庫（只有 `{{define}}`），測試只要求它不再定義 `toggle`、不再有 `modal-overlay`（`modal` 範本要改成 `<jelly-dialog>` 或移除）。
- 導覽維持自家（現在通過、替換後仍要通過）：`settings.html` 的 `<nav class="tabs">` 內 `{{range .Tabs}}<a class="tab…`；`profile.html` 的 `<nav class="profile-tabs">` 與六個 `<a href="/admin/account?tab=…">`；`system-admin.js` 的兩處 `class="sys-tabs" role="tablist"`（轉址頁與附件策略頁）與 `role="tab"` 按鈕；`layout.html` 的 ⌘K 面板原樣保留。應用市場的篩選是下拉選單，不在本單範圍。

### 保留的 id、name、值與預設

原本在原生控制項、自製遮罩或原生 `<dialog>` 上的 `id` 移到取代它的 Jelly 元素上（頁面腳本與 `scripts/acceptance/mermaid-editor.spec.mjs` 依賴這些 id）。伺服器輸出測試逐一檢查：

- 開關 `<jelly-switch>`：`/admin/taxonomy` 的 `#fHide`（`name="hide_from_list"`）、`#fNoCascade`（`no_cascade`）、`#fPHidden`（`pcat_hidden`），三個預設不勾；`/admin/menus` 的 `#iNewTab`、`#iLoginOnly`（不勾）；`/admin/coupons` 的 `#fStack`、`#fMembers`（不勾）、`#fEnabled`（`checked`）；文章編輯器的 `#optComments`（`checked`）、`#optPinned`（不勾）；頁面編輯器的 `#optComments`（`checked`）。
- 勾選框 `<jelly-checkbox size="sm">`：`#fShip`、`#fCold`（商品，預設由腳本設定）、`#rRestock`（訂單退款，不勾）、留言的 `#selectAll`（不勾）與 `#sheetApprove`（`checked`）、回收桶的 `#selectAll`、使用者的 `#selectAllUsers`、編輯器匯出的 `#exportFrontmatter`（不勾）。`/admin/account` 的 `#quietEnabled` 可以是勾選框或開關；修訂頁的 `#collapse` 可以是開關或勾選框，預設 `checked`。
- 分段 `<jelly-segmented>`（不加 `size`），段的 `value` 依序為：折扣碼 `name="kind"` 預設 `value="percent"`：`percent,fixed,free_shipping,bogo`；`name="scope"` 預設 `all`：`all,category,product`；文章與頁面編輯器 `name="status"` 預設 `draft`：`draft,published`；商品編輯器 `name="status"`：`draft,active,archived`；訂單退款 `name="mode"` 預設 `full`：`full,partial`；留言 `#statusSeg` 預設 `pending`：`all,pending,approved,spam,trash`；回收桶 `#kindSeg` 預設 `all`：`all,post,page,product,comment`；修訂 `#modeSeg` 預設 `inline`：`inline,only,split`；分類頁的分段：`categories,tags,products`；選單「加入項目」：`page,category,post,tag,shop,custom`；使用者 `#roleTabs` 只要求存在（值不釘）。
- 單選卡：`#inviteForm` 內一個 `<jelly-radio-group size="sm" label="…">`，`<jelly-radio name="role" size="sm">` 依序 `editor,author,admin`，只有 `editor` 帶 `checked`。`#editorExport` 內一個 `<jelly-radio-group size="sm" label="…">`，同一個 name 的 `<jelly-radio size="sm">` 依序 `jmd,md,html,zip,pdf`，只有 `jmd` 帶 `checked`；每張格式卡保留 `data-format="<格式>"`，點卡片等同點選。群組元素不得有 `value` 屬性。
- 對話框 `<jelly-dialog>`（伺服器輸出不得帶 `open`）：`#batchSheet`（內含 `form#bf`）、留言 `#sheet`（內含 `form#sheetForm`）、`#uploadModal`（內含 `form#uploadForm`）、`#identityConfirm`、`#userDialog`、編輯器 `#editorShortcuts`、`#editorExport`、`#editorRestore`、串接頁 `#integrationDialog`。表單一律放在對話框裡面（打開時對話框會被移到 `<body>`）。選單設定與主題客製化由腳本建立，id 不釘，但表單 `#msName`、`#msLoc`、`[data-del]`、`[data-cancel]`、`#customizeForm` 保留。
- 開啟的一般對話框要有可存取名稱（`label` 屬性或第一個標題），瀏覽器讀 shadow 內 `[role="dialog"]` 的 `aria-label`。

### 尺寸與單選寫法

- 後台每個 `<jelly-checkbox>`、`<jelly-radio-group>`、`<jelly-radio>` 開始標籤都寫 `size="sm"`；腳本建立的要 `setAttribute('size','sm')`（同一檔案裡找得到這個呼叫即可）。元素連上文件後 jelly.js 會把屬性改寫成正式值 `size="small"`，瀏覽器檢查接受 `sm` 或 `small`。`<jelly-switch>`、`<jelly-segmented>` 不得有 `size`。
- 每個 `<jelly-radio>` 都要有 `name`。選取狀態只看單選本身的 `checked` 屬性：瀏覽器檢查同一個 name 的單選最多一個帶 `checked`，shadow 內 `[role="radio"]` 的 `aria-checked` 與屬性一致，而且帶 `checked` 的單選在 canvas 中心的像素與未選的不同（選取狀態真的畫出來）。

### 瀏覽器檢查依賴的選擇器與操作

- 分類：`#newBtn` → `#fName` → `#form jelly-switch[name="hide_from_list"]` 聚焦按空白鍵 → `#saveBtn`。
- 折扣碼：`#newBtn` → `#cf jelly-segmented[name="kind"]` 內 `button[role="radio"][aria-checked="true"]` 聚焦後按右方向鍵，值變成 `fixed`，`#fFixed` 要出現（頁面要聽分段的 `change`）→ `#fCode`、`#fFixed`=120 → `#fStack` 聚焦按空白鍵 → `#saveBtn`。
- 使用者邀請：`#inviteForm jelly-radio-group`、`#inviteForm jelly-radio[name="role"][value="editor"]` 聚焦後按下方向鍵，`author` 帶 `checked`、`editor` 不帶 → `#inviteEmail` → `#sendInvite`。
- 商品：`jelly-checkbox#fShip`（新商品預設勾）、`jelly-checkbox#fCold`（預設不勾）、`jelly-segmented[name="status"]`；`#fTitle`、`#fPrice` → `#fShip` 聚焦按空白鍵 → `#saveBtn`。
- 主題客製化：按鈕名稱 `客製化|Customize` → 開著的 `jelly-dialog` 內 `#customizeForm`，`jelly-checkbox[name="layout_sidebar"]`、`[name="layout_footer"]`，送出鈕 `#customizeForm button[type="submit"]`。
- 折扣碼批次：`#batchOpen` 開啟，Esc、shadow 內的 `button.close`（右上 ✕）各關一次，焦點回到 `#batchOpen`；`#bPrefix`、`#bSize`、`#batchBtn` 送出後 `#batchOut` 出現且對話框仍開著。
- 新增選單：`#newMenuBtn` 開啟，點背景（視窗座標 4,400）關閉、焦點回到 `#newMenuBtn`；在 `#msName` 按 Enter 送出，送出後對話框關閉。
- 選單設定巢狀確認：`#menuList button[data-id]` → `#treeMount #menuSettings` → 對話框內 `[data-del]` 開 `Jelly.confirm` → Esc 只關確認框，外層對話框保持開著、未儲存的名稱還在、焦點回到 `[data-del]`、沒有 DELETE；再按一次 Esc 才關外層，焦點回到 `#menuSettings`。
- 選單設定對話框內的表單保留 `#msName`、`[data-del]`、`[data-cancel]`；既有檢查「menus: Escape cancels the nested Jelly confirmation and preserves the menu editor」以 `jelly-dialog[open]:not([data-jelly-confirm]) form:has(#msName)` 找這個表單（主代理已把它從 `.modal-overlay.open form.mn-sheet-form` 改過來，斷言不變）。
- 編輯器匯出：`[data-action="export"]` 開啟的 `jelly-dialog` id 必須是 `editorExport`；`jelly-radio[value="jmd"]` 聚焦後按右方向鍵，選到 `md`；點 `[data-format="html"]`、`[data-format="md"]`；`jelly-checkbox#exportFrontmatter` 聚焦按空白鍵；`#exportDownload`；Esc 關閉後焦點回到匯出按鈕。
- 通知：媒體庫 `#mediaGrid .media-item button` 第一顆（複製網址）、主題卡片 `.theme-card` 內名稱 `啟用|Activate` 的按鈕、設定頁 `#googleEnabled`、`#googleClientID`、`#googleClientSecret`、`#googleSaveBtn`。

### 送出內容（main `fc6ab71` 實測，替換後必須相同）

欄位集合與值的型別逐一比對（多一個或少一個欄位都失敗）：

- `POST /api/admin/coupons`：`label` string、`kind` string、`value` number、`buy_qty` number、`get_qty` number、`scope` string、`code` string、`min_subtotal` number、`usage_limit` null、`per_user_limit` null、`starts_at` null、`ends_at` null、`bound_email` string、`stackable_with_free_shipping` boolean、`members_only` boolean、`status` string、`targets` array。操作後的值：`kind:"fixed"`、`value:12000`（輸入 120）、`scope:"all"`、`stackable_with_free_shipping:true`、`members_only:false`、`status:"active"`、`targets:[]`。
- `POST /api/admin/categories`：`name`、`slug`、`color`、`description`、`cover` string，`parent_id` null，`hide_from_list`、`no_cascade` boolean；按空白鍵後 `hide_from_list:true`、`no_cascade:false`。商品分類 `POST /api/admin/product-categories` 送 `{name, slug, description, parent_id, cover, hidden}`，`hidden` 是 boolean（伺服器守門測試）。
- `POST /api/admin/products`：`title`、`slug`、`description`、`price`、`compare_at_price`、`status`、`seo_title`、`seo_description`、`seo_index`、`seo_ai` string，`low_stock_threshold` number，`requires_shipping`、`cold_chain` boolean，`images`、`categories`、`variants` array；取消「需要運送」後 `requires_shipping:false`、`cold_chain:false`、`status:"draft"`。
- `POST /api/admin/users/invite`：只有 `email`、`role`、`note` 三個 string；選作者後 `role:"author"`、`note:""`。
- `POST /api/admin/themes/<name>/settings`：`colors`、`layout`、`custom` 都是 JSON 字串。`layout` 裡勾選的版面欄位是布林 `true`，**未勾選的欄位不出現**（原本靠 `FormData` 省略未勾的 checkbox；`<jelly-checkbox>` 未勾時 `setFormValue(null)`，行為相同）。測試以攔截的設定 `{"sidebar":false,"footer":true}` 起始，勾 Sidebar、取消 Footer 後期望 `layout.sidebar === true` 且沒有 `footer` 鍵。
- `POST /api/admin/coupons/generate`：`prefix` string、`size` number、`template` object；`template.usage_limit` 與 `per_user_limit` 為 1，其餘來自折扣碼表單。
- `POST /api/admin/menus`：`{name, location}` 兩個 string。
- 編輯器匯出：`GET /api/admin/posts/<id>/export?format=md&frontmatter=1`（查詢字串完全相同、順序相同）。

### 通知

- 原本 `alert()` 的流程改用 `Jelly.toast` 後，瀏覽器檢查要求：出現新的 `jelly-toaster .toast`，而且整個流程沒有任何 `dialog` 事件。種類由通知裡的 `.sr-tone` 文字判斷（`Success` 對應 `kind:'success'`、`Error` 對應 `kind:'error'`）：主題啟用成功是 success、失敗（攔截回 500）是 error；主題設定儲存是 success；OAuth 儲存失敗是 error；媒體複製網址在剪貼簿寫入成功時是 success。
- 以 `?lang=zh-TW` 打開媒體庫與設定頁時，複製網址與 OAuth 儲存失敗的通知文字必須含中文字（原本寫死的英文改成 i18n 鍵）。測試不釘鍵名與文案。
- 媒體庫的「複製網址」按鈕現在完全沒有作用（`onclick="copyUrl(${JSON.stringify(url)})"` 把雙引號放進雙引號屬性，屬性被截斷；刪除鈕同樣壞掉），點了既不提示也不複製。通知檢查要求按下後出現通知，所以實作時要一併修好這兩顆按鈕的事件綁定（只動 `media.html`）。

### 上游 Jelly UI 1.1.0（`1b775385`）查證：`jelly-checkbox`、`jelly-radio-group`／`jelly-radio`、`jelly-dialog`

原始碼 `src/components/{checkbox,radio,radio-group,dialog}/index.ts`、`src/anchor/modal.ts`、`src/utilities/size.ts`；自帶的 `jelly.js` 與原始碼一致（`jelly-radio-group` 的 `observedAttributes` 實測為 `["label","direction","size"]`）。下列行為另以真實元件在後台頁面實測（標「實測」）。

`<jelly-checkbox>`

- 屬性：`checked`、`indeterminate`、`disabled`、`label`、`value`（預設 `"on"`）、`name`、`size`、`variant`；`checked`／`indeterminate`／`value` 的 accessor 只在升級後存在，升級前指定會變成實例屬性（同開關）。沒有 `disabled` accessor：`el.disabled = true` 沒有作用，停用一律 `toggleAttribute('disabled', v)`。
- 結構：shadow 裡是一個視覺隱藏的原生 `<input type="checkbox">` 包在 `<label>` 內，文字由預設 slot 提供；`label` 屬性變成內部 input 的 `aria-label`。主機的 `focus()` 轉給內部 input，主機本身不在 tab 順序裡。
- 鍵盤：空白鍵切換（原生行為，實測）。點主機任何地方（方塊或文字）都會切換。
- 事件：只在使用者操作時，主機發出 `change`（`CustomEvent`，`bubbles`、`composed`）；程式改屬性不發。內部原生 `change` 不會穿出 shadow。
- 表單：form-associated；勾選時 `setFormValue(value)`，未勾 `setFormValue(null)`，所以 `FormData` 未勾時沒有這個鍵、勾選時是 `value`（預設 `"on"`，實測）。沒有 `formResetCallback`：`form.reset()` 不會還原 Jelly 勾選框與單選（例如 `users.html` 邀請送出後的 `form.reset()`），要自己重設屬性。
- `indeterminate` 顯示橫線、內部 input 回報 mixed；使用者一點就清掉（可用於表格全選的部分選取）。
- 尺寸：`size="sm"`（連上文件後改寫成 `small`）為 28px，預設 32px；尺寸由元件 shadow 樣式的 `:host([size=…])` 規則設定，主代理實測從外部設 `--jelly-checkbox-size` 無效。

`<jelly-radio-group>`／`<jelly-radio>`

- `<jelly-radio-group>` 只有 `label`（顯示為 legend 並成為群組的可存取名稱）、`direction`（`horizontal` 預設、`vertical`）、`size`（傳給沒有自訂 size 的子單選）；shadow 內 `.items` 帶 `role="radiogroup"`。**沒有 `name`、`value`，也不發 `change`**；`<jelly-radio-group value="a">` 不會選取任何單選。
- `<jelly-radio>` 屬性：`checked`、`disabled`、`label`、`value`（預設 `"on"`）、`name`、`size`、`variant`。同一組是「同一個 root（文件或 shadow root）裡 `jelly-radio[name="…"]` 的全部」，與外層的 `<jelly-radio-group>` 無關，所以同一頁不同用途的單選要用不同的 name。
- 選取：點單選的任何地方（控制點或文字）或按空白鍵、Enter，會選取它並移除同組其他單選的 `checked`，再由被選的單選發出 `change`（`bubbles`、`composed`；再點一次已選的也會發）。程式設 `checked` 屬性**不會**清掉同組其他單選，也不發事件：寫值時要對目標 `setAttribute('checked','')`、其他 `removeAttribute('checked')`。讀值：同組帶 `checked` 的單選的 `value`。
- 鍵盤：←→（RTL 反向）與 ↑↓ 移動並選取，到底會繞回；Home、End；roving tabindex（被選的單選可 tab 到，沒有選取時是第一個未停用的）。實測 ↓ 從 editor 選到 author 並清掉 editor。
- 表單：form-associated；被選時 `setFormValue(value)`，`FormData` 得到 `name=value`（實測）。沒有 `formResetCallback`。
- 畫面：選取狀態畫在 canvas 上（薄荷色填滿），內部控制點 `aria-checked` 同步；實測選與未選的中心像素不同。

`<jelly-dialog>`

- 屬性：`open`、`label`（優先於第一個 `h1`／`h2`／`h3`／`[data-dialog-title]` 成為 `aria-label`，標題文字在打開當下讀取）。事件 `open`、`close`（`bubbles`、`composed`）。另有 `showModal()` 與 `open` accessor，但只在升級後存在。
- 打開（加上 `open`）：記下當時的 `document.activeElement`，把元素移到 `<body>`（原位置留註解，關閉時移回），鎖捲動，把 `<body>` 下其他元素設成 `inert`，focus 到對話框本體（`tabindex=-1`，不是第一個欄位；要 focus 欄位得自己做）。因為會被移到 `<body>`：表單要放在對話框裡面；依賴祖先的 CSS（例如 `users.html` 的 `.accounts-page .modal-overlay …`）打開後不再套用。
- 關閉：Esc（`document` 上的 `keydown`，且事件未被 `preventDefault`）、點背景（`.wrap` 本身）、shadow 內右上 `button.close`（`aria-label="Close"`，英文寫死；`layout.html` 只替 `Jelly.confirm` 在地化，一般對話框要比照）都會把 `open` 收起來：先跑 150ms 退場動畫再移除屬性；直接 `removeAttribute('open')` 則立即關閉。兩種方式都會把焦點還給打開時記下的元素並發出 `close`（實測 Esc、✕、點背景三種都關閉且焦點回到開啟按鈕）。
- **巢狀對話框的 Esc**：每個開著的對話框都在 `document` 上各掛一個 Esc 監聽，先打開的先收到。實測一般對話框裡再開 `Jelly.confirm`，按一次 Esc 兩個都會關。瀏覽器檢查要求只關最上層（選單設定裡的刪除確認），實作要處理（例如在共用的 `Jelly.dialog` 只讓最上層的對話框處理 Esc），不能改上游。
- 沒有 Enter 處理、沒有確認／取消概念；表單照原本的 `<form>` 送出。對話框開著時，外面的通知看得到但點不到（`inert`）。

### 既有測試與既有缺陷（實作者不得改測試，由主代理處理）

- `scripts/acceptance/jelly-ui.spec.mjs` 既有的「menus: Escape cancels the nested Jelly confirmation and preserves the menu editor」用 `.modal-overlay.open form.mn-sheet-form` 找選單設定，替換後一定失敗；本組的巢狀確認檢查涵蓋同一行為。`scripts/acceptance/mermaid-editor.spec.mjs` 依賴 `#editorExport`、`[data-format=html]`、`[data-format=pdf]`、`#editorExport [data-action=close]`，所以這些 id 與屬性要保留。
- 商品「新增」時送 `requires_shipping:false` 會被存成 `true`（GORM 以欄位預設值取代零值；`core.Product` 的註解說用 `Select(...)` 可以避免，實際上不行），在本單之前就存在，屬於 Go 程式，不在本單範圍。守門測試因此只在「更新」路徑釘 `false`；瀏覽器檢查只比對送出的 JSON，不讀回儲存值。
- 主題客製化的 `color_text` 欄位因名稱以 `_text` 結尾而被當成文字輸入框略過，`colors` 裡沒有 `text`；這是既有行為，測試不依賴它，也不要求修。

### 〈資料與相容〉四條的瀏覽器檢查

- 表單重設（`consistency: the integration create form resets …`）：只用 `#integrationCreate` 內的 `input[name="name"]`、`jelly-checkbox[name="permission"]` 與送出鈕 `#integrationCreate button[data-mutation]`。A 建立成功後，按「我已複製，關閉」（名稱 `我已複製，關閉|I have copied it, close`）關掉 `jelly-dialog#integrationDialog`，再數 `jelly-checkbox[name="permission"][checked]` 必須為 0。B 不以回應判斷，而是 `waitForRequest` 等 5 秒：main 在一項權限都沒勾時**不送出請求**，而是在 `#integrationCreate .integration-error` 顯示「請至少選擇一項權限」（`/至少選擇一項權限|at least one permission/`）。所以「什麼都不勾」在 main 上有兩種可觀察結果，檢查兩種都接受：若送出了請求，`permissions` 必須是 `[]`；若沒有請求，錯誤文字必須是一勾權限訊息。A、B 建立的串接在結束時以 `DELETE /api/admin/integrations/:id` 清除。
- 拖曳超出邊界（`consistency: the coupon batch sheet ignores …`）：面板位置取自 `jelly-dialog` 的 shadow `[part="dialog"]` 的 `getBoundingClientRect()`，開啟後等 400 ms（150 ms 動畫結束）再量。背景點擊座標的算法：依序取面板左外 24 px、右外 24 px、上外 24 px、下外 24 px、(8, 8)、(視窗寬−8, 視窗高−8)，取第一個落在視窗內且不在面板內的點。拖曳以 `page.mouse.down()` 按在 `#bPrefix` 的中心，`mouse.move(…, { steps: 10 })` 移到背景點後 `mouse.up()`，等 400 ms，`jelly-dialog#batchSheet[open]` 必須仍有 1 個；之後單純 `mouse.click` 同一背景點，對話框要關閉，焦點回到 `#batchOpen`。
- main 上點背景不關的對話框（`consistency: dialogs that do not close …`）：每段獨立執行，失敗全部收集後才報告。(a) 建立串接後的一次性金鑰：`jelly-dialog#integrationDialog` 內的 `#integrationSecret`，文字必須以 `jly_` 開頭，點背景後文字不變。(b) `客製化|Customize` 按鈕開啟 `jelly-dialog#customizeModal`，`#customizeForm` 內第一個可見的 `input[type="text"]` 先 `fill` 標記字串，點背景後值不變，Esc 後焦點回到客製化按鈕。(c) 在 `/admin/posts/new` 載入前，把草稿寫進 `localStorage`，鍵為 `jelly.editor.<使用者>.draft.0`，`<使用者>` 與 `editor.js` 相同（`window.JellyEditorUser` 的 `ID`、`id`、`Username`、`username`，都沒有則 `self`）；重新載入後等 `jelly-dialog#editorRestore[open]`，測完移除該鍵。(d) `/admin/settings/crawlers` 的 `[data-action="add-ai-rule"]` 開啟 `jelly-dialog.sys-dialog`，`input[name="user_agent"]` 填入的值點背景後必須仍在，Esc 後焦點回到開啟鈕。四段都要求「點背景後仍開著」，Esc 關閉的斷言也在同一段。
- 忙碌時換篩選（`consistency: changing the notification filter …`）：`page.route` 攔 `GET /api/account/notifications?…`，回固定一筆未讀「留言」通知（id 9000001）與計數，並記下每次請求的 `category` 與 `Date.now()`；攔 `POST /api/account/notifications/9000001/read`，延遲 1.5 秒後回 500，失敗時間取自這個處理函式。點 `#notificationList [data-mark-read]` 後立刻點 `#notificationFilters` 內名稱符合 `訂單|Orders` 的 `button[role="radio"]`；`jelly-segment[value="orders"]` 的主機本身不可見，不能直接點。失敗後 5 秒內必須有 `category=orders` 的清單請求，且 `#notificationFilters` 的 `value` 為 `orders`。固定的 500 只列入允許的頁面問題。
- 搜尋時的期間分段（`consistency: clicking the selected period segment …`）：`#q` 填入 `JUIQ<時間戳>`，等 `GET /api/admin/coupons?q=…` 回應；再點 `#segs button[role="radio"][aria-checked="true"]`（目前期間，預設 `active`）。必須送出一個不帶 `q`、`status` 等於目前期間的 `GET /api/admin/coupons`，而且 `#q` 的值為空。

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

## 驗收紀錄

交付時記下的驗證結果與裁決，依時間由新到舊。

- 工作單 26 後續兩項：新增或複製商品時把「需要運送」設成 false、「低庫存門檻」設成 0，原本會存成 true 與 5。根因是 GORM 在 Create 時把帶 `default` 標籤的零值換成預設值，`Select`／`Omit` 也擋不住；改為在同一交易內 Create 後明確寫回使用者要的值，資料表與 API 不變，`core.Product` 的錯誤註解一併更正。`webmcp-edges.spec.mjs` 改以 `checked` 屬性讀寫 WebMCP 的 Jelly 開關。驗證：新增的五個商品零值整合測試先在舊程式重現失敗、修正後通過；主代理重跑 `JELLY_TEST_POSTGRES=1 go test ./... -count=1`：server 套件（783 秒）等 25 個套件通過，`internal/plugin` 的一個外掛停止測試偶發失敗（本次沒有改動該套件，單獨重跑 5 次與整個套件重跑都通過，已記入 AGENTS.md Follow-ups）；gofmt、go vet 通過；WebMCP 瀏覽器驗收 35/35。沒有部署。
- 工作單 26：後台的選擇、切換、對話框與通知全部統一用 Jelly UI，輸入欄位、按鈕、卡片、表格與導覽分頁維持自家樣式（界線見 工單 26）。自家開關、原生勾選框與單選、八種頁面自製分段、側頁遮罩與原生 `<dialog>` 都換成 `jelly-switch`、`jelly-checkbox`、`jelly-radio`、`jelly-segmented`、`jelly-dialog`；勾選框與單選一律 `size="sm"`；`alert()` 全部改成 Jelly 通知並補繁中與英文文案。`layout.html` 新增 `Jelly.dialog`：巢狀時按一次 Esc 只關最上層、關閉後焦點回到打開它的元素、✕ 依語言標示，備用碼對話框鎖到使用者確認已保存。附件頁原本按了沒反應的「複製網址」「刪除」一併修好。送出給伺服器的資料格式不變，由折扣碼、分類旗標、商品運送、邀請角色、主題版面五個守門測試鎖住。驗證：主代理重跑 `go test ./internal/assetcheck/`、gofmt、go vet；全新安裝的隔離站跑瀏覽器驗收，一致性 18/18、後台 24/24、Mermaid 編輯器 7/7 通過；獨立對抗式審查找到的四個缺陷先寫成失敗測試再修好：新增 API 串接後權限勾選沒清掉、從對話框內拖到背景放開會關閉（main 上點背景不關的對話框維持不關，用 `dismissOnBackdrop: false`）、通知中心請求失敗時換過的篩選不重新載入、折扣碼搜尋時點目前期間段沒反應；設定、個人帳號、版本、新增商品與文章頁在繁中／英文、1280／390px、亮／暗共 56 張截圖沒有整頁橫向溢出、沒有原生勾選框或頁面錯誤；實作端另拍其餘 18 頁共 144 張，主代理抽查並與設計板對照。設計板先改（勾選框與單選新增 `.cb.j`、`.rd.j`），通知中心的「未讀」畫成獨立開關以符合可與分類並用的實際篩選。已知後續記在 `AGENTS.md` 的 Follow-ups：新增商品時「需要運送」為 false 仍存成 true、WebMCP 瀏覽器測試仍把開關當原生勾選框。沒有部署。
